package services

import (
	"context"
	"prophet-trader/interfaces"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// MaxEntry is the cached MAX value for one ticker.
// Exported because PennySignalAggregator (same package, but the
// aggregator's hook returns it through a value-by-value copy that is
// cleaner to read with exported field names).
type MaxEntry struct {
	Value      float64   // e.g. 0.32 for a +32% best day
	BestDay    time.Time // session that produced the max
	BarsUsed   int       // number of daily returns used (typically 21)
	ComputedAt time.Time
}

// MultiBarsFetcher is the narrow interface PennyMaxFilterService depends on,
// allowing tests to substitute a fake. Satisfied by *AlpacaDataService.
type MultiBarsFetcher interface {
	GetMultiBars(ctx context.Context, symbols []string, start, end time.Time, timeframe string) (map[string][]*interfaces.Bar, error)
}

// PennyMaxFilterService maintains a daily-refreshed cache of 21-session MAX
// values for every ticker in the penny universe.
//
// Lock ordering: PennyMaxFilterService.mu is a leaf lock. It is never
// acquired while another services-package lock is held.
type PennyMaxFilterService struct {
	universe *PennyUniverseService
	bars     MultiBarsFetcher
	mu       sync.RWMutex
	cache    map[string]MaxEntry
	nowFunc  func() time.Time
	logger   *logrus.Logger
}

// NewPennyMaxFilterService constructs the service.
func NewPennyMaxFilterService(universe *PennyUniverseService, bars MultiBarsFetcher) *PennyMaxFilterService {
	logger := logrus.New()
	logger.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})
	return &PennyMaxFilterService{
		universe: universe,
		bars:     bars,
		cache:    make(map[string]MaxEntry),
		nowFunc:  time.Now,
		logger:   logger,
	}
}

// refresh fetches 30 calendar days of daily bars for the entire universe
// and recomputes MAX values. On fetcher error, the existing cache is
// preserved (stale but readable).
func (s *PennyMaxFilterService) refresh(ctx context.Context) {
	tickers := s.universe.GetTickers()
	if len(tickers) == 0 {
		s.logger.Info("PennyMaxFilterService: empty universe, skipping refresh")
		return
	}

	now := s.nowFunc()
	start := now.AddDate(0, 0, -30)

	// Chunk to stay within Alpaca's per-request symbol cap.
	const chunkSize = 100
	combined := make(map[string][]*interfaces.Bar)
	for i := 0; i < len(tickers); i += chunkSize {
		end := i + chunkSize
		if end > len(tickers) {
			end = len(tickers)
		}
		resp, err := s.bars.GetMultiBars(ctx, tickers[i:end], start, now, "1Day")
		if err != nil {
			s.logger.WithError(err).Warn("PennyMaxFilterService: GetMultiBars failed; preserving prior cache")
			return
		}
		for k, v := range resp {
			combined[k] = v
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for ticker, bars := range combined {
		entry, ok := computeMaxFromBars(bars)
		if !ok {
			continue
		}
		entry.ComputedAt = now
		s.cache[ticker] = entry
	}
	s.logger.WithField("entries", len(s.cache)).Info("PennyMaxFilterService: refresh complete")
}

// GetMax returns the cached MAX entry for ticker.
// ok=false when: the ticker has no cache entry (universe miss, first
// refresh not yet succeeded, or fewer than 2 daily bars available).
// ok=true with BarsUsed < 21 indicates a low-confidence value (newly
// listed ticker); the caller should still log it and downstream
// analysis can filter by BarsUsed.
func (s *PennyMaxFilterService) GetMax(ticker string) (MaxEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.cache[ticker]
	return e, ok
}

// nextRefreshTime returns the next 07:00 America/New_York instant strictly
// after now. Falls back to UTC if the tz database is unavailable.
func nextRefreshTime(now time.Time) time.Time {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		loc = time.UTC
	}
	et := now.In(loc)
	candidate := time.Date(et.Year(), et.Month(), et.Day(), 7, 0, 0, 0, loc)
	if !candidate.After(et) {
		candidate = candidate.AddDate(0, 0, 1)
	}
	return candidate
}

// computeMaxFromBars returns the MAX entry computed from ascending-time
// daily bars. ok=false when fewer than 2 bars are available (zero returns).
// Uses the most recent 22 bars to produce up to 21 close-to-close returns.
func computeMaxFromBars(bars []*interfaces.Bar) (MaxEntry, bool) {
	if len(bars) < 2 {
		return MaxEntry{}, false
	}
	// Trim to the most recent 22 bars (yields 21 returns).
	start := 0
	if len(bars) > 22 {
		start = len(bars) - 22
	}
	window := bars[start:]

	var maxVal float64
	var maxDay time.Time
	first := true
	for i := 1; i < len(window); i++ {
		prev := window[i-1].Close
		curr := window[i].Close
		if prev <= 0 {
			continue
		}
		r := (curr / prev) - 1
		if first || r > maxVal {
			maxVal = r
			maxDay = window[i].Timestamp
			first = false
		}
	}
	if first {
		// No usable returns (e.g., all prev closes were zero).
		return MaxEntry{}, false
	}
	return MaxEntry{
		Value:    maxVal,
		BestDay:  maxDay,
		BarsUsed: len(window) - 1,
	}, true
}
