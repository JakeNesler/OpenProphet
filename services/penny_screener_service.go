package services

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	alpacaMarket "github.com/alpacahq/alpaca-trade-api-go/v3/marketdata"
	"github.com/sirupsen/logrus"
)

const technicalRefreshInterval = 60 * time.Second

// TechnicalEntry holds computed technical signal data for one symbol.
type TechnicalEntry struct {
	Entry       DecayEntry // BaseScore=computed score, EventTime=last meaningful change, HalfLifeHrs=2.0
	VolumeRatio float64
	GapPct      float64
	Context     string
}

// updateAnchor applies the meaningful-change rule: anchor resets only on first observation,
// prior-zero-to-positive, or >10% relative change.
func updateAnchor(newScore, priorBase float64, priorAnchor time.Time, hasPrior bool) (base float64, anchor time.Time) {
	if !hasPrior || priorBase == 0 {
		return newScore, time.Now()
	}
	relChange := math.Abs(newScore-priorBase) / priorBase
	if relChange > 0.10 {
		return newScore, time.Now()
	}
	return priorBase, priorAnchor
}

// PennyScreenerService computes technical signals via Alpaca market data.
type PennyScreenerService struct {
	client   *alpacaMarket.Client
	universe *PennyUniverseService
	mu       sync.RWMutex
	scores   map[string]TechnicalEntry
	logger   *logrus.Logger
}

// NewPennyScreenerService creates the service.
func NewPennyScreenerService(apiKey, secretKey string, universe *PennyUniverseService) *PennyScreenerService {
	client := alpacaMarket.NewClient(alpacaMarket.ClientOpts{
		APIKey:    apiKey,
		APISecret: secretKey,
		Feed:      alpacaMarket.IEX,
	})
	logger := logrus.New()
	logger.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})
	return &PennyScreenerService{
		client:   client,
		universe: universe,
		scores:   make(map[string]TechnicalEntry),
		logger:   logger,
	}
}

// Start runs the screener loop until ctx is cancelled.
func (s *PennyScreenerService) Start(ctx context.Context) {
	s.scan()
	ticker := time.NewTicker(technicalRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.scan()
		}
	}
}

// GetTechnicalScore returns the current technical score and context for a ticker.
// Decay is applied via DecayEntry.EffectiveScore using a 2-hour half-life.
func (s *PennyScreenerService) GetTechnicalScore(ticker string) (float64, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.scores[ticker]
	if !ok {
		return 0, ""
	}
	return e.Entry.EffectiveScore(), e.Context
}

func (s *PennyScreenerService) scan() {
	tickers := s.universe.GetTickers()
	if len(tickers) == 0 {
		return
	}
	// Process in chunks of 100 to stay within Alpaca batch limits.
	for i := 0; i < len(tickers); i += 100 {
		end := i + 100
		if end > len(tickers) {
			end = len(tickers)
		}
		s.scanChunk(tickers[i:end])
	}
	s.logger.WithField("symbols", len(tickers)).Info("PennyScreenerService: scan complete")
}

func (s *PennyScreenerService) scanChunk(tickers []string) {
	snapshots, err := s.client.GetSnapshots(tickers, alpacaMarket.GetSnapshotRequest{
		Feed: alpacaMarket.IEX,
	})
	if err != nil {
		s.logger.WithError(err).Warn("PennyScreenerService: GetSnapshots failed")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for ticker, snap := range snapshots {
		entry := s.computeEntry(ticker, snap)
		s.scores[ticker] = entry
	}
}

func (s *PennyScreenerService) computeEntry(ticker string, snap *alpacaMarket.Snapshot) TechnicalEntry {
	if snap == nil || snap.DailyBar == nil || snap.PrevDailyBar == nil {
		prior, hasPrior := s.scores[ticker]
		if hasPrior {
			return prior
		}
		return TechnicalEntry{Entry: DecayEntry{HalfLifeHrs: 2.0, EventTime: time.Now()}}
	}

	var volumeRatio float64
	if snap.PrevDailyBar.Volume > 0 {
		volumeRatio = float64(snap.DailyBar.Volume) / float64(snap.PrevDailyBar.Volume)
	}
	var gapPct float64
	if snap.PrevDailyBar.Close > 0 {
		gapPct = (snap.DailyBar.Open - snap.PrevDailyBar.Close) / snap.PrevDailyBar.Close * 100
	}
	var breakoutBonus float64
	if snap.DailyBar.High > 0 {
		if (snap.DailyBar.High-snap.DailyBar.Close)/snap.DailyBar.High <= 0.02 {
			breakoutBonus = 1.0
		}
	}

	total := math.Min(volumeRatio/5.0, 1.0)*20.0 +
		math.Min(math.Abs(gapPct)/5.0, 1.0)*10.0 +
		breakoutBonus*10.0
	signalSummary := fmt.Sprintf("vol_ratio=%.1fx gap=%.1f%% breakout_near=%v", volumeRatio, gapPct, breakoutBonus > 0)

	prior, hasPrior := s.scores[ticker]
	var priorBase float64
	var priorAnchor time.Time
	if hasPrior {
		priorBase = prior.Entry.BaseScore
		priorAnchor = prior.Entry.EventTime
	}
	base, anchor := updateAnchor(total, priorBase, priorAnchor, hasPrior)

	return TechnicalEntry{
		Entry:       DecayEntry{BaseScore: base, EventTime: anchor, HalfLifeHrs: 2.0},
		VolumeRatio: volumeRatio,
		GapPct:      gapPct,
		Context:     signalSummary,
	}
}
