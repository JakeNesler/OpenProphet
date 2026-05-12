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
	VolumeRatio float64    // legacy field, kept for log / dashboard compatibility
	GapPct      float64
	Context     string

	// Replaces VolumeRatio as the score driver. Time-of-day-adjusted relative
	// volume vs trailing 20-day daily-volume average. 0 means "no signal yet".
	RVOL float64

	// 9:30-9:45 ET opening range. ORBStatus is one of "awaiting",
	// "inside_or", "above_or_high", "below_or_low".
	ORBHigh   float64
	ORBLow    float64
	ORBStatus string
}

// intradayContext is gathered outside the screener mutex so HTTP fetches
// don't block the screener's per-ticker compute loop.
type intradayContext struct {
	rvol     float64
	orbHigh  float64
	orbLow   float64
	orbOK    bool
}

// updateAnchor applies the meaningful-change rule: anchor resets only on first observation,
// prior-zero-to-positive, or >10% relative change.
func updateAnchor(newScore, priorBase float64, priorAnchor time.Time, hasPrior bool) (base float64, anchor time.Time) {
	if !hasPrior || (priorBase == 0 && newScore > 0) {
		return newScore, time.Now()
	}
	if priorBase == 0 {
		return priorBase, priorAnchor
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
	intraday *PennyIntradayCache // optional; nil disables RVOL+ORB enrichment
	mu       sync.RWMutex
	scores   map[string]TechnicalEntry
	logger   *logrus.Logger
}

// NewPennyScreenerService creates the service. intraday may be nil; when nil
// the screener falls back to behavior without RVOL or ORB enrichment. Tests
// can construct PennyScreenerService directly to bypass this constructor.
func NewPennyScreenerService(apiKey, secretKey string, universe *PennyUniverseService, intraday *PennyIntradayCache) *PennyScreenerService {
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
		intraday: intraday,
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

// GetTechnicalEntry returns the full computed entry for `ticker` (or the zero
// value if the symbol is not tracked). Callers (notably the signal aggregator)
// use this to read RVOL and ORB fields alongside the decayed score.
func (s *PennyScreenerService) GetTechnicalEntry(ticker string) (TechnicalEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.scores[ticker]
	return e, ok
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

	// Gather intraday context BEFORE acquiring the scores mutex — the cache
	// may issue HTTP fetches and we don't want to block the lock on those.
	now := time.Now()
	intraday := make(map[string]intradayContext, len(snapshots))
	if s.intraday != nil {
		ctx := context.Background()
		for ticker, snap := range snapshots {
			if snap == nil || snap.DailyBar == nil {
				continue
			}
			intraday[ticker] = s.gatherIntradayContext(ctx, ticker, snap, now)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for ticker, snap := range snapshots {
		entry := s.computeEntry(ticker, snap, intraday[ticker], now)
		s.scores[ticker] = entry
	}
}

// gatherIntradayContext fetches RVOL and ORB for one ticker via the cache.
// Caller must NOT hold s.mu when calling — the cache may issue HTTP fetches.
func (s *PennyScreenerService) gatherIntradayContext(ctx context.Context, ticker string, snap *alpacaMarket.Snapshot, now time.Time) intradayContext {
	out := intradayContext{}
	if snap == nil || snap.DailyBar == nil {
		return out
	}
	avgVol, _ := s.intraday.GetAvgDailyVolume20d(ctx, ticker, now)
	out.rvol = calcRVOL(int64(snap.DailyBar.Volume), avgVol, fractionOfSessionElapsed(now))
	if h, l, ok := s.intraday.GetORB(ctx, ticker, now); ok {
		out.orbHigh = h
		out.orbLow = l
		out.orbOK = true
	}
	return out
}

func (s *PennyScreenerService) computeEntry(ticker string, snap *alpacaMarket.Snapshot, intra intradayContext, now time.Time) TechnicalEntry {
	if snap == nil || snap.DailyBar == nil || snap.PrevDailyBar == nil {
		// On a transient data outage, preserve the prior signal so decay continues from the existing anchor.
		prior, hasPrior := s.scores[ticker]
		if hasPrior {
			return prior
		}
		return TechnicalEntry{Entry: DecayEntry{HalfLifeHrs: 2.0, EventTime: now}}
	}

	// volumeRatio retained as a legacy log/dashboard signal. Score driver is now RVOL.
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

	// Volume score now keyed off time-of-day-adjusted RVOL. 3.0x = max points
	// (tighter cap than the old 5.0x daily-ratio which was easy to hit late
	// in the session as cumulative volume mechanically passed prior-day).
	volScore := math.Min(intra.rvol/3.0, 1.0) * 20.0

	total := volScore +
		math.Min(math.Abs(gapPct)/5.0, 1.0)*10.0 +
		breakoutBonus*10.0

	// ORB classification — context only, does NOT contribute to score.
	orbStatus := classifyORBStatus(snap.DailyBar.Close, intra.orbHigh, intra.orbLow)

	signalSummary := fmt.Sprintf("rvol=%.2fx gap=%.1f%% breakout_near=%v orb=%s", intra.rvol, gapPct, breakoutBonus > 0, orbStatus)

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
		RVOL:        intra.rvol,
		ORBHigh:     intra.orbHigh,
		ORBLow:      intra.orbLow,
		ORBStatus:   orbStatus,
	}
}
