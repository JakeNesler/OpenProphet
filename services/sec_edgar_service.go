package services

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/net/html/charset"
)

const regulatoryRefreshInterval = 30 * time.Second
const regulatoryHalfLifeHours = 24.0

type regulatoryEntry struct {
	Entry     DecayEntry
	EventDesc string
}

// SECEdgarService polls EDGAR and GlobeNewswire for regulatory events.
type SECEdgarService struct {
	httpClient    *http.Client
	universe      *PennyUniverseService
	operatorEmail string
	mu            sync.RWMutex
	entries       map[string]regulatoryEntry // keyed by ticker; keeps highest-score entry
	logger        *logrus.Logger
}

// NewSECEdgarService creates the service.
func NewSECEdgarService(universe *PennyUniverseService, httpClient *http.Client, operatorEmail string) *SECEdgarService {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	logger := logrus.New()
	logger.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})
	return &SECEdgarService{
		httpClient:    httpClient,
		universe:      universe,
		operatorEmail: operatorEmail,
		entries:       make(map[string]regulatoryEntry),
		logger:        logger,
	}
}

// Start runs the polling loop until ctx is cancelled.
func (s *SECEdgarService) Start(ctx context.Context) {
	s.poll()
	ticker := time.NewTicker(regulatoryRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.poll()
		}
	}
}

// GetRegulatoryScore returns the current decayed regulatory score and event description.
func (s *SECEdgarService) GetRegulatoryScore(ticker string) (float64, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.entries[ticker]
	if !ok {
		return 0, ""
	}
	return e.Entry.EffectiveScore(), e.EventDesc
}

func (s *SECEdgarService) poll() {
	tickers := tickerSet(s.universe.GetTickers())
	fb1, tot1 := s.pollEdgar(tickers)
	fb2, tot2 := s.pollGlobeNewswire(tickers)
	total := tot1 + tot2
	fallbacks := fb1 + fb2
	if total > 0 && float64(fallbacks)/float64(total) > 0.50 {
		s.logger.WithField("pct", fmt.Sprintf("%.0f%%", float64(fallbacks)/float64(total)*100)).
			Error("decay anchor fallback rate — EDGAR feed format may have changed")
	}
}

// atomFeed is a minimal ATOM feed parser.
type atomFeed struct {
	XMLName xml.Name    `xml:"feed"`
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	Title   string `xml:"title"`
	Updated string `xml:"updated"`
	Summary string `xml:"summary"`
}

// rssFeed is a minimal RSS 2.0 feed parser.
type rssFeed struct {
	XMLName xml.Name   `xml:"rss"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Items []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate"`
}

func (s *SECEdgarService) fetchAtom(url string) ([]atomEntry, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", fmt.Sprintf("PennyProphet Trading Bot %s", s.operatorEmail))
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	var feed atomFeed
	dec := xml.NewDecoder(resp.Body)
	dec.CharsetReader = charset.NewReaderLabel
	if err := dec.Decode(&feed); err != nil {
		return nil, fmt.Errorf("atom parse: %w", err)
	}
	return feed.Entries, nil
}

func (s *SECEdgarService) fetchRSS(url string) ([]rssItem, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", fmt.Sprintf("PennyProphet Trading Bot %s", s.operatorEmail))
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	var feed rssFeed
	dec := xml.NewDecoder(resp.Body)
	dec.CharsetReader = charset.NewReaderLabel
	if err := dec.Decode(&feed); err != nil {
		return nil, fmt.Errorf("rss parse: %w", err)
	}
	return feed.Channel.Items, nil
}

// edgarDateLayouts is the ordered list of timestamp formats accepted on
// EDGAR feed entries. Most-likely formats are tried first. Includes
// no-seconds RFC1123 variants (e.g. "Fri, 08 May 2026 17:12 GMT") that
// EDGAR has been observed to emit in 2026.
var edgarDateLayouts = []string{
	time.RFC3339,
	time.RFC3339Nano,
	time.RFC1123Z,
	time.RFC1123,
	"Mon, 02 Jan 2006 15:04:05 MST",
	"Mon, 02 Jan 2006 15:04:05 -0700",
	"Mon, 02 Jan 2006 15:04 MST",
	"Mon, 02 Jan 2006 15:04 -0700",
}

func tryParseEdgarDate(s string) (time.Time, bool) {
	for _, layout := range edgarDateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, false
		}
	}
	return time.Now(), true
}

func parseAtomDate(s string) (time.Time, bool) {
	return tryParseEdgarDate(s)
}

func parseRSSDate(s string) (time.Time, bool) {
	return tryParseEdgarDate(s)
}

func (s *SECEdgarService) pollEdgar(tickers map[string]bool) (fallbacks, total int) {
	const edgarURL = "https://www.sec.gov/cgi-bin/browse-edgar?action=getcurrent&type=8-K&dateb=&owner=include&count=40&search_text=&output=atom"
	entries, err := s.fetchAtom(edgarURL)
	if err != nil {
		s.logger.WithError(err).Warn("SECEdgarService: EDGAR poll failed")
		return 0, 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range entries {
		total++
		ticker := extractTickerFromTitle(entry.Title, tickers)
		if ticker == "" {
			continue
		}
		eventTime, isFallback := parseAtomDate(entry.Updated)
		if isFallback {
			fallbacks++
			s.logger.Warnf("decay anchor: skipping %s — unparseable timestamp %q", ticker, entry.Updated)
			continue
		}
		desc := fmt.Sprintf("8-K filed %s", eventTime.Format("15:04 ET"))
		s.upsertEntry(ticker, 40.0, eventTime, desc)
	}
	return fallbacks, total
}

func (s *SECEdgarService) pollGlobeNewswire(tickers map[string]bool) (fallbacks, total int) {
	const gnwURL = "https://www.globenewswire.com/RssFeed/country/US"
	items, err := s.fetchRSS(gnwURL)
	if err != nil {
		s.logger.WithError(err).Warn("SECEdgarService: GlobeNewswire poll failed")
		return 0, 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range items {
		eventTime, isFallback := parseRSSDate(item.PubDate)
		combined := strings.ToUpper(item.Title + " " + item.Description)
		for ticker := range tickers {
			if !strings.Contains(combined, ticker) {
				continue
			}
			total++
			if isFallback {
				fallbacks++
				s.logger.Warnf("decay anchor: skipping %s — unparseable timestamp %q", ticker, item.PubDate)
				continue
			}
			desc := fmt.Sprintf("PR wire mention %s", eventTime.Format("15:04 ET"))
			s.upsertEntry(ticker, 25.0, eventTime, desc)
		}
	}
	return fallbacks, total
}

// upsertEntry implements the max rule: replace only when new_base > existing decayed score.
// Caller must hold mu.Lock.
func (s *SECEdgarService) upsertEntry(ticker string, newBase float64, eventTime time.Time, desc string) {
	existing, ok := s.entries[ticker]
	if !ok {
		s.entries[ticker] = regulatoryEntry{
			Entry:     DecayEntry{BaseScore: newBase, EventTime: eventTime, HalfLifeHrs: regulatoryHalfLifeHours},
			EventDesc: desc,
		}
		return
	}
	if newBase > existing.Entry.EffectiveScore() {
		s.entries[ticker] = regulatoryEntry{
			Entry:     DecayEntry{BaseScore: newBase, EventTime: eventTime, HalfLifeHrs: regulatoryHalfLifeHours},
			EventDesc: desc,
		}
	}
}

// extractTickerFromTitle finds a universe ticker in an EDGAR entry title.
// EDGAR 8-K titles look like: "8-K - ACME CORP (0001234567) (Issuer)"
func extractTickerFromTitle(title string, tickers map[string]bool) string {
	upper := strings.ToUpper(title)
	for ticker := range tickers {
		if strings.Contains(upper, " "+ticker+" ") ||
			strings.Contains(upper, "("+ticker+")") ||
			strings.HasSuffix(upper, " "+ticker) ||
			strings.HasPrefix(upper, ticker+" ") {
			return ticker
		}
	}
	return ""
}

// tickerSet converts a slice of ticker strings into a set (map) for O(1) lookup.
func tickerSet(tickers []string) map[string]bool {
	set := make(map[string]bool, len(tickers))
	for _, t := range tickers {
		set[t] = true
	}
	return set
}
