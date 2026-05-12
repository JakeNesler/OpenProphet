package services

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"prophet-trader/interfaces"
)

// IntradaySignalService computes the per-symbol intraday context blob
// injected into Prophet's market-hours beats. Aggressively cached (60s TTL)
// so a steady cadence of beats doesn't multiply Alpaca fetches. All compute
// is concurrent across symbols to keep the per-beat budget under ~500ms.

const (
	intradayCacheTTL  = 60 * time.Second
	sectorCacheTTL    = 6 * time.Hour
	intradayBarsTF    = "5Min"
	dailyBarsTF       = "1Day"
	atrLookback       = 20 // 20 trading days
)

// sectorETFMap maps a symbol to its primary sector ETF for context.
// SPY/QQQ have no sector — they are the broad references themselves.
var sectorETFMap = map[string]string{
	"NVDA": "SMH",
	"AMD":  "SMH",
	"TSLA": "XLY",
	"MSTR": "XLK",
}

// intradayDataSource is the narrow subset of interfaces.DataService used by
// IntradaySignalService — only historical bars are needed. Keeping the
// interface small lets tests stub it without implementing the full
// DataService surface.
type intradayDataSource interface {
	GetHistoricalBars(ctx context.Context, symbol string, start, end time.Time, timeframe string) ([]*interfaces.Bar, error)
}

// IntradaySignal is one symbol's snapshot.
type IntradaySignal struct {
	Symbol          string  `json:"symbol"`
	Price           float64 `json:"price"`
	DayChangePct    float64 `json:"day_change_pct"`
	VWAP            float64 `json:"vwap"`
	DistFromVWAPPct float64 `json:"dist_from_vwap_pct"`
	RVOL            float64 `json:"rvol"`
	SessionHigh     float64 `json:"session_high"`
	SessionLow      float64 `json:"session_low"`
	RangeOverATR    float64 `json:"range_over_atr"`
	SectorETF       string  `json:"sector_etf,omitempty"`
	SectorChangePct float64 `json:"sector_change_pct,omitempty"`
	Note            string  `json:"note,omitempty"`
}

// IntradaySignalSet is the full response.
type IntradaySignalSet struct {
	GeneratedAt time.Time        `json:"generated_at"`
	Signals     []IntradaySignal `json:"signals"`
	Errors      []string         `json:"errors,omitempty"`
}

type cachedSymbol struct {
	signal    IntradaySignal
	cachedAt  time.Time
}

type cachedSector struct {
	pctChange float64
	cachedAt  time.Time
}

// IntradaySignalService caches per-symbol snapshots and sector ETF readings.
type IntradaySignalService struct {
	data intradayDataSource

	mu          sync.RWMutex
	symbolCache map[string]cachedSymbol
	sectorCache map[string]cachedSector
}

// NewIntradaySignalService constructs a service over the given data source.
// In production the source is *AlpacaDataService (which satisfies the
// intradayDataSource interface implicitly via GetHistoricalBars).
func NewIntradaySignalService(data intradayDataSource) *IntradaySignalService {
	return &IntradaySignalService{
		data:        data,
		symbolCache: make(map[string]cachedSymbol),
		sectorCache: make(map[string]cachedSector),
	}
}

// GetSignals returns a snapshot for each requested symbol. Never returns
// nil; per-symbol failures populate IntradaySignalSet.Errors but other
// symbols remain populated. Fetches are concurrent across symbols.
func (s *IntradaySignalService) GetSignals(ctx context.Context, symbols []string, now time.Time) *IntradaySignalSet {
	set := &IntradaySignalSet{
		GeneratedAt: now.UTC(),
		Signals:     make([]IntradaySignal, 0, len(symbols)),
	}

	var (
		wg sync.WaitGroup
		mu sync.Mutex
	)

	for _, sym := range symbols {
		wg.Add(1)
		go func(symbol string) {
			defer wg.Done()
			sig, err := s.getOrComputeSymbol(ctx, symbol, now)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				set.Errors = append(set.Errors, fmt.Sprintf("%s: %v", symbol, err))
				// Still emit a placeholder so the LLM sees the symbol was attempted.
				set.Signals = append(set.Signals, IntradaySignal{Symbol: symbol, Note: "fetch failed"})
				return
			}
			set.Signals = append(set.Signals, sig)
		}(sym)
	}
	wg.Wait()

	// Stable order: match the requested symbol order so the LLM-side
	// renderer has a deterministic layout.
	order := make(map[string]int, len(symbols))
	for i, sym := range symbols {
		order[sym] = i
	}
	sort.SliceStable(set.Signals, func(i, j int) bool {
		return order[set.Signals[i].Symbol] < order[set.Signals[j].Symbol]
	})

	return set
}

// getOrComputeSymbol returns the cached signal if fresh, else recomputes it.
func (s *IntradaySignalService) getOrComputeSymbol(ctx context.Context, symbol string, now time.Time) (IntradaySignal, error) {
	s.mu.RLock()
	if c, ok := s.symbolCache[symbol]; ok && now.Sub(c.cachedAt) < intradayCacheTTL {
		s.mu.RUnlock()
		return c.signal, nil
	}
	s.mu.RUnlock()

	sig, err := s.computeSymbol(ctx, symbol, now)
	if err != nil {
		return IntradaySignal{}, err
	}
	s.mu.Lock()
	s.symbolCache[symbol] = cachedSymbol{signal: sig, cachedAt: now}
	s.mu.Unlock()
	return sig, nil
}

// computeSymbol does the actual data fetches + math for one symbol.
func (s *IntradaySignalService) computeSymbol(ctx context.Context, symbol string, now time.Time) (IntradaySignal, error) {
	// Today's intraday bars — 5-min interval over the session window.
	sessionStart := startOfSessionUTC(now)
	intraday, err := s.data.GetHistoricalBars(ctx, symbol, sessionStart, now, intradayBarsTF)
	if err != nil {
		return IntradaySignal{}, fmt.Errorf("intraday bars: %w", err)
	}

	// Daily bars — last 21 to compute ATR-20 + prior close + avg daily volume.
	dailyEnd := now
	dailyStart := dailyEnd.AddDate(0, 0, -45) // calendar days, well > 21 trading days
	daily, err := s.data.GetHistoricalBars(ctx, symbol, dailyStart, dailyEnd, dailyBarsTF)
	if err != nil {
		return IntradaySignal{}, fmt.Errorf("daily bars: %w", err)
	}

	sig := IntradaySignal{Symbol: symbol}

	if len(intraday) == 0 {
		sig.Note = "no intraday bars yet"
	} else {
		sig.VWAP = calcSessionVWAP(intraday)
		sig.SessionHigh = intraday[0].High
		sig.SessionLow = intraday[0].Low
		var cumVol int64
		for _, b := range intraday {
			if b.High > sig.SessionHigh {
				sig.SessionHigh = b.High
			}
			if b.Low < sig.SessionLow {
				sig.SessionLow = b.Low
			}
			cumVol += b.Volume
		}
		sig.Price = intraday[len(intraday)-1].Close
		if sig.VWAP > 0 {
			sig.DistFromVWAPPct = (sig.Price - sig.VWAP) / sig.VWAP * 100.0
		}

		if len(daily) > 0 {
			priorClose := daily[len(daily)-1].Close
			// If the most recent daily bar IS today's, prior close is the one before it.
			if isSameUTCDay(daily[len(daily)-1].Timestamp, now) && len(daily) > 1 {
				priorClose = daily[len(daily)-2].Close
			}
			sig.DayChangePct = calcDayChangePct(sig.Price, priorClose)
			atr := calcATR(trailingBars(daily, atrLookback+1))
			if atr > 0 {
				sig.RangeOverATR = calcRangeOverATR(sig.SessionHigh, sig.SessionLow, atr)
			}
			avgVol := avgDailyVolume(trailingBars(daily, atrLookback))
			sig.RVOL = calcRVOL(cumVol, avgVol, fractionOfSessionElapsed(now))
		}
	}

	// Sector ETF lookup (optional).
	if etf, ok := sectorETFMap[symbol]; ok && etf != "" {
		sig.SectorETF = etf
		if pct, err := s.sectorChange(ctx, etf, now); err == nil {
			sig.SectorChangePct = pct
		}
	}

	return sig, nil
}

// sectorChange returns today's % change for the sector ETF, cached for 6h
// since daily bar data turns over once per session.
func (s *IntradaySignalService) sectorChange(ctx context.Context, etf string, now time.Time) (float64, error) {
	s.mu.RLock()
	if c, ok := s.sectorCache[etf]; ok && now.Sub(c.cachedAt) < sectorCacheTTL {
		s.mu.RUnlock()
		return c.pctChange, nil
	}
	s.mu.RUnlock()

	end := now
	start := end.AddDate(0, 0, -7) // a week of daily bars is plenty
	bars, err := s.data.GetHistoricalBars(ctx, etf, start, end, dailyBarsTF)
	if err != nil || len(bars) < 2 {
		return 0, fmt.Errorf("sector etf %s: insufficient data", etf)
	}
	latest := bars[len(bars)-1]
	prior := bars[len(bars)-2]
	pct := calcDayChangePct(latest.Close, prior.Close)

	s.mu.Lock()
	s.sectorCache[etf] = cachedSector{pctChange: pct, cachedAt: now}
	s.mu.Unlock()
	return pct, nil
}

// ── pure functions (tested directly) ───────────────────────────────

// calcSessionVWAP returns the volume-weighted average of the per-bar VWAPs.
// Zero-volume bars are skipped. Returns 0 when total volume is zero.
func calcSessionVWAP(bars []*interfaces.Bar) float64 {
	var totalVol float64
	var weighted float64
	for _, b := range bars {
		if b.Volume == 0 {
			continue
		}
		v := float64(b.Volume)
		weighted += b.VWAP * v
		totalVol += v
	}
	if totalVol == 0 {
		return 0
	}
	return weighted / totalVol
}

// calcRVOL returns today's cumulative volume normalized by the 20-day average
// daily volume, time-adjusted for the fraction of session elapsed. Returns 0
// when either input is zero.
func calcRVOL(todayCumVol int64, avgDailyVol int64, sessionElapsed float64) float64 {
	if avgDailyVol <= 0 || sessionElapsed <= 0 {
		return 0
	}
	expected := float64(avgDailyVol) * sessionElapsed
	if expected <= 0 {
		return 0
	}
	return float64(todayCumVol) / expected
}

// calcRangeOverATR returns (session high − session low) / ATR. Returns 0 when
// ATR is zero or negative.
func calcRangeOverATR(high, low, atr float64) float64 {
	if atr <= 0 {
		return 0
	}
	return (high - low) / atr
}

// calcDayChangePct returns (latest − priorClose) / priorClose × 100. Returns 0
// when priorClose is zero.
func calcDayChangePct(latest, priorClose float64) float64 {
	if priorClose == 0 {
		return 0
	}
	return (latest - priorClose) / priorClose * 100.0
}

// calcATR averages true-range over the given daily bars. Needs at least two
// bars to compute one TR; returns 0 with fewer bars.
func calcATR(bars []*interfaces.Bar) float64 {
	if len(bars) < 2 {
		return 0
	}
	var sum float64
	for i := 1; i < len(bars); i++ {
		prevClose := bars[i-1].Close
		tr := math.Max(bars[i].High-bars[i].Low, math.Max(
			math.Abs(bars[i].High-prevClose),
			math.Abs(bars[i].Low-prevClose),
		))
		sum += tr
	}
	return sum / float64(len(bars)-1)
}

// fractionOfSessionElapsed returns the fraction (0..1) of the US-equities
// regular session that has elapsed at `now`. Before 9:30 ET → 0; after
// 16:00 ET → 1; weekends → 0.
func fractionOfSessionElapsed(now time.Time) float64 {
	day := now.Weekday()
	if day == time.Saturday || day == time.Sunday {
		return 0
	}
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		return 0
	}
	et := now.In(loc)
	open := time.Date(et.Year(), et.Month(), et.Day(), 9, 30, 0, 0, loc)
	close := time.Date(et.Year(), et.Month(), et.Day(), 16, 0, 0, 0, loc)
	if !et.After(open) {
		return 0
	}
	if !et.Before(close) {
		return 1
	}
	elapsed := et.Sub(open).Seconds()
	total := close.Sub(open).Seconds()
	return elapsed / total
}

// ── helpers ────────────────────────────────────────────────────────

func trailingBars(bars []*interfaces.Bar, n int) []*interfaces.Bar {
	if len(bars) <= n {
		return bars
	}
	return bars[len(bars)-n:]
}

func avgDailyVolume(bars []*interfaces.Bar) int64 {
	if len(bars) == 0 {
		return 0
	}
	var sum int64
	for _, b := range bars {
		sum += b.Volume
	}
	return sum / int64(len(bars))
}

func startOfSessionUTC(now time.Time) time.Time {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		return now.Add(-7 * time.Hour) // approximate fallback
	}
	et := now.In(loc)
	open := time.Date(et.Year(), et.Month(), et.Day(), 9, 30, 0, 0, loc)
	return open.UTC()
}

func isSameUTCDay(a, b time.Time) bool {
	au := a.UTC()
	bu := b.UTC()
	return au.Year() == bu.Year() && au.YearDay() == bu.YearDay()
}
