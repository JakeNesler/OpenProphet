package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

const socialRefreshInterval = 30 * time.Second
const stockTwitsRefreshInterval = 2 * time.Minute
const socialHalfLifeHours = 4.0

var tickerRegex = regexp.MustCompile(`\$([A-Z]{2,5})\b`)

type mentionBaseline struct {
	buckets    [336]int
	total      int
	firstSeen  time.Time
	lastBucket int
}

func (b *mentionBaseline) advance(now time.Time, newCount int) {
	currentBucket := int(now.Unix()/1800) % 336
	if currentBucket != b.lastBucket {
		passed := (currentBucket - b.lastBucket + 336) % 336
		if passed >= 336 {
			b.total = 0
			for i := range b.buckets {
				b.buckets[i] = 0
			}
		} else {
			// Zero from lastBucket (inclusive) through currentBucket (inclusive)
			// so stale data in lastBucket and any skipped slots are cleared.
			for i := 0; i <= passed; i++ {
				idx := (b.lastBucket + i) % 336
				b.total -= b.buckets[idx]
				b.buckets[idx] = 0
			}
		}
		b.lastBucket = currentBucket
	}
	b.buckets[currentBucket] += newCount
	b.total += newCount
}

func (b *mentionBaseline) baselinePer30min() float64 {
	avg := float64(b.total) / 336.0
	if avg < 0.5 {
		return 0.5
	}
	return avg
}

type socialEntry struct {
	Entry      DecayEntry
	MentionPts float64
	Context    string
}

// SocialSignalService polls Reddit and StockTwits for social signals.
type SocialSignalService struct {
	httpClient *http.Client
	universe   *PennyUniverseService
	mu         sync.RWMutex
	entries    map[string]socialEntry
	baselines  map[string]*mentionBaseline
	logger     *logrus.Logger
}

// NewSocialSignalService creates the service.
func NewSocialSignalService(universe *PennyUniverseService, httpClient *http.Client) *SocialSignalService {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	logger := logrus.New()
	logger.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})
	return &SocialSignalService{
		httpClient: httpClient,
		universe:   universe,
		entries:    make(map[string]socialEntry),
		baselines:  make(map[string]*mentionBaseline),
		logger:     logger,
	}
}

// Start runs both Reddit and StockTwits loops until ctx is cancelled.
func (s *SocialSignalService) Start(ctx context.Context) {
	go s.runReddit(ctx)
	go s.runStockTwits(ctx)
	<-ctx.Done()
}

// GetSocialScore returns the current decayed social score and context for a ticker.
func (s *SocialSignalService) GetSocialScore(ticker string) (float64, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.entries[ticker]
	if !ok {
		return 0, ""
	}
	return e.Entry.EffectiveScore(), e.Context
}

func (s *SocialSignalService) runReddit(ctx context.Context) {
	s.pollReddit()
	ticker := time.NewTicker(socialRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.pollReddit()
		}
	}
}

func (s *SocialSignalService) runStockTwits(ctx context.Context) {
	ticker := time.NewTicker(stockTwitsRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.pollStockTwitsForTopMentioned()
		}
	}
}

type redditListing struct {
	Data struct {
		Children []struct {
			Data struct {
				Title    string `json:"title"`
				Selftext string `json:"selftext"`
			} `json:"data"`
		} `json:"children"`
	} `json:"data"`
}

func (s *SocialSignalService) pollReddit() {
	subreddits := []string{"pennystocks", "RobinHoodPennyStocks"}
	tickers := tickerSet(s.universe.GetTickers())
	now := time.Now()
	counts := make(map[string]int)

	for _, sub := range subreddits {
		url := fmt.Sprintf("https://www.reddit.com/r/%s/new.json?limit=100", sub)
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "ProphetBot/1.0 (contact: trading@example.com)")
		resp, err := s.httpClient.Do(req)
		if err != nil {
			s.logger.WithError(err).Warnf("SocialSignalService: Reddit r/%s failed", sub)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			s.logger.WithField("status", resp.StatusCode).Warnf("SocialSignalService: Reddit r/%s non-200", sub)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var listing redditListing
		if err := json.Unmarshal(body, &listing); err != nil {
			continue
		}
		for _, child := range listing.Data.Children {
			combined := strings.ToUpper(child.Data.Title + " " + child.Data.Selftext)
			for _, m := range tickerRegex.FindAllStringSubmatch(combined, -1) {
				if len(m) >= 2 && tickers[m[1]] {
					counts[m[1]]++
				}
			}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.recomputeRedditScores(now, counts)
}

func (s *SocialSignalService) recomputeRedditScores(now time.Time, counts map[string]int) {
	for ticker, count := range counts {
		bl, ok := s.baselines[ticker]
		if !ok {
			bl = &mentionBaseline{firstSeen: now, lastBucket: int(now.Unix()/1800) % 336}
			s.baselines[ticker] = bl
		}
		bl.advance(now, count)

		var mentionVelocityPts float64
		if time.Since(bl.firstSeen) >= 72*time.Hour {
			velocity := math.Min(float64(count)/bl.baselinePer30min(), 2.0)
			mentionVelocityPts = velocity * 5.0
		}

		var sentimentPts float64
		if existing, ok := s.entries[ticker]; ok {
			sentimentPts = existing.Entry.BaseScore - existing.MentionPts
			if sentimentPts < 0 {
				sentimentPts = 0
			}
		}

		score := math.Min(mentionVelocityPts+sentimentPts, 20.0)
		ctx := fmt.Sprintf("mentions=%d", count)
		if time.Since(bl.firstSeen) >= 72*time.Hour {
			ctx = fmt.Sprintf("mentions=%d velocity=%.1fx", count, float64(count)/bl.baselinePer30min())
		}
		s.entries[ticker] = socialEntry{
			Entry:      DecayEntry{BaseScore: score, EventTime: now, HalfLifeHrs: socialHalfLifeHours},
			MentionPts: mentionVelocityPts,
			Context:    ctx,
		}
	}

	// Evict entries for tickers with no current mentions
	for ticker := range s.entries {
		if _, ok := counts[ticker]; !ok {
			delete(s.entries, ticker)
		}
	}

	// Universe-exit cleanup: remove baselines for tickers no longer in universe
	universeTickers := tickerSet(s.universe.GetTickers())
	for ticker := range s.baselines {
		if !universeTickers[ticker] {
			delete(s.baselines, ticker)
		}
	}
}

func (s *SocialSignalService) pollStockTwitsForTopMentioned() {
	s.mu.RLock()
	type kv struct {
		ticker string
		score  float64
	}
	var ranked []kv
	for t, e := range s.entries {
		decayed := e.Entry.EffectiveScore()
		ranked = append(ranked, kv{t, decayed})
	}
	s.mu.RUnlock()

	sort.Slice(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
	limit := 5
	if len(ranked) < limit {
		limit = len(ranked)
	}
	for i := 0; i < limit; i++ {
		s.fetchStockTwits(ranked[i].ticker)
	}
}

type stResponse struct {
	Messages []struct {
		Entities struct {
			Sentiment *struct {
				Basic string `json:"basic"`
			} `json:"sentiment"`
		} `json:"entities"`
	} `json:"messages"`
}

func (s *SocialSignalService) fetchStockTwits(ticker string) {
	url := fmt.Sprintf("https://api.stocktwits.com/api/2/streams/symbol/%s.json", ticker)
	resp, err := s.httpClient.Get(url)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	body, _ := io.ReadAll(resp.Body)
	var st stResponse
	if err := json.Unmarshal(body, &st); err != nil {
		return
	}
	bullish, bearish := 0, 0
	for _, m := range st.Messages {
		if m.Entities.Sentiment == nil {
			continue
		}
		switch m.Entities.Sentiment.Basic {
		case "Bullish":
			bullish++
		case "Bearish":
			bearish++
		}
	}
	total := bullish + bearish
	if total == 0 {
		return
	}
	ratio := float64(bullish) / float64(total)
	var sentimentPts float64
	if ratio > 0.65 {
		sentimentPts = 10.0
	} else if ratio > 0.55 {
		sentimentPts = 5.0
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	existing := s.entries[ticker]
	// Always recalculate from the clean mention base (MentionPts), never from BaseScore,
	// so sentiment cannot compound across successive StockTwits polls.
	newScore := min64(existing.MentionPts+sentimentPts, 20.0)
	signalCtx := fmt.Sprintf("%s st_bullish=%.0f%%", existing.Context, ratio*100)
	s.entries[ticker] = socialEntry{
		Entry:      DecayEntry{BaseScore: newScore, EventTime: existing.Entry.EventTime, HalfLifeHrs: socialHalfLifeHours},
		MentionPts: existing.MentionPts,
		Context:    signalCtx,
	}
}

func min64(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
