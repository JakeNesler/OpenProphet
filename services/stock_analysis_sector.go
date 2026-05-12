package services

import (
	"context"
	"fmt"
	"sync"
	"time"

	"prophet-trader/interfaces"
)

// Sector / cross-asset enrichment for AnalyzeStocks responses (brief §4.6).
// Two surfaces:
//   - SectorSummary: for equities mapped via sectorETFMap (NVDA→SMH, etc.).
//     Carries the sector ETF's day-change and 5-day relative strength.
//   - CrossAssetSummary: for TrendProphet's cross-asset universe (TLT, GLD,
//     USO, DBC, UUP, EEM). Carries DXY (via UUP), 10y-rate (via IEF, inverse
//     to yield), and HYG 5-day moves.

const (
	sectorContextTTL          = 1 * time.Hour
	relativeStrengthDays      = 5  // 5-day window per brief
	macroBarsCalendarLookback = 14 // calendar days back to fetch ~5 trading bars + buffer
)

// trendUniverseSet mirrors TREND_UNIVERSE in agent/preflight.js. Symbols in
// this set get CrossAssetSummary enrichment; symbols in sectorETFMap get
// SectorSummary enrichment; symbols in neither get neither (and that's OK).
var trendUniverseSet = map[string]struct{}{
	"TLT": {}, "GLD": {}, "USO": {}, "DBC": {}, "UUP": {}, "EEM": {},
}

// SectorSummary captures the sector context for an equity symbol.
type SectorSummary struct {
	ETF                string  `json:"etf"`
	ChangePctDay       float64 `json:"change_pct_day"`
	RelativeStrength5d float64 `json:"relative_strength_5d"`
}

// CrossAssetSummary captures three cross-asset 5-day moves used by
// TrendProphet to interpret a directional macro trade.
//
// Sign conventions (documented for LLM-side reading):
//   DXYChangePct5d  > 0 → dollar bid
//   RateProxyPct5d  > 0 → rates FALLING (IEF rises when yields fall)
//                  < 0 → rates RISING
//   HYGChangePct5d  > 0 → credit appetite up (risk-on); < 0 → risk-off
type CrossAssetSummary struct {
	DXYChangePct5d float64 `json:"dxy_change_pct_5d"`
	RateProxyPct5d float64 `json:"rate_proxy_pct_5d"`
	HYGChangePct5d float64 `json:"hyg_change_pct_5d"`
}

// sectorDataLike is the narrow subset of interfaces.DataService used by the
// sector context cache — only historical daily bars are needed.
type sectorDataLike interface {
	GetHistoricalBars(ctx context.Context, symbol string, start, end time.Time, timeframe string) ([]*interfaces.Bar, error)
}

// sectorContextCache caches per-symbol trailing-5d closes (used for both
// the equity symbol and any referenced ETF). 1h TTL — daily bars turn over
// once per session.
type sectorContextCache struct {
	data sectorDataLike

	mu    sync.Mutex
	cache map[string]sectorCacheEntry // key = symbol, value = closes + cachedAt
}

type sectorCacheEntry struct {
	closes   []float64
	cachedAt time.Time
}

func newSectorContextCache(data sectorDataLike) *sectorContextCache {
	return &sectorContextCache{
		data:  data,
		cache: make(map[string]sectorCacheEntry),
	}
}

// trailingCloses returns the last `days` daily closes for `symbol`, hitting
// the data source only when the cached value is older than sectorCacheTTL.
func (c *sectorContextCache) trailingCloses(ctx context.Context, symbol string, now time.Time, days int) ([]float64, error) {
	c.mu.Lock()
	if e, ok := c.cache[symbol]; ok && now.Sub(e.cachedAt) < sectorContextTTL {
		closes := e.closes
		c.mu.Unlock()
		return closes, nil
	}
	c.mu.Unlock()

	end := now
	start := end.AddDate(0, 0, -macroBarsCalendarLookback)
	bars, err := c.data.GetHistoricalBars(ctx, symbol, start, end, "1Day")
	if err != nil {
		return nil, fmt.Errorf("sector context fetch %s: %w", symbol, err)
	}
	if len(bars) == 0 {
		return nil, fmt.Errorf("no daily bars for %s", symbol)
	}
	if len(bars) > days+1 {
		bars = bars[len(bars)-(days+1):]
	}
	closes := make([]float64, len(bars))
	for i, b := range bars {
		closes[i] = b.Close
	}

	c.mu.Lock()
	c.cache[symbol] = sectorCacheEntry{closes: closes, cachedAt: now}
	c.mu.Unlock()
	return closes, nil
}

// enrichAnalysisWithSector populates analysis.Sector when `symbol` is mapped
// in sectorETFMap and both the symbol's and ETF's 5-day daily closes can be
// fetched. Silently no-ops on any failure (analysis is still returned to the
// LLM intact).
func enrichAnalysisWithSector(ctx context.Context, analysis *StockAnalysis, cache *sectorContextCache, symbol string, now time.Time) {
	if analysis == nil || cache == nil {
		return
	}
	etf, ok := sectorETFMap[symbol]
	if !ok || etf == "" {
		return
	}
	symbolCloses, err := cache.trailingCloses(ctx, symbol, now, relativeStrengthDays)
	if err != nil {
		return
	}
	etfCloses, err := cache.trailingCloses(ctx, etf, now, relativeStrengthDays)
	if err != nil {
		return
	}
	rs, rsOK := calcRelativeStrength5d(symbolCloses, etfCloses)
	if !rsOK {
		return
	}
	dayChange := 0.0
	if len(etfCloses) >= 2 {
		if pct, ok := calcChangePctOverBars(etfCloses[len(etfCloses)-2:]); ok {
			dayChange = pct
		}
	}
	analysis.Sector = &SectorSummary{
		ETF:                etf,
		ChangePctDay:       dayChange,
		RelativeStrength5d: rs,
	}
}

// enrichAnalysisWithCrossAsset populates analysis.CrossAsset when `symbol` is
// in the TrendProphet universe. Reads UUP/IEF/HYG 5-day moves; soft-fails on
// individual proxy errors so the block is populated as much as possible.
func enrichAnalysisWithCrossAsset(ctx context.Context, analysis *StockAnalysis, cache *sectorContextCache, symbol string, now time.Time) {
	if analysis == nil || cache == nil {
		return
	}
	if _, ok := trendUniverseSet[symbol]; !ok {
		return
	}
	summary := &CrossAssetSummary{}
	if closes, err := cache.trailingCloses(ctx, "UUP", now, relativeStrengthDays); err == nil {
		if pct, ok := calcChangePctOverBars(closes); ok {
			summary.DXYChangePct5d = pct
		}
	}
	if closes, err := cache.trailingCloses(ctx, "IEF", now, relativeStrengthDays); err == nil {
		if pct, ok := calcChangePctOverBars(closes); ok {
			summary.RateProxyPct5d = pct
		}
	}
	if closes, err := cache.trailingCloses(ctx, "HYG", now, relativeStrengthDays); err == nil {
		if pct, ok := calcChangePctOverBars(closes); ok {
			summary.HYGChangePct5d = pct
		}
	}
	analysis.CrossAsset = summary
}

// ── pure helpers ─────────────────────────────────────────────────

// calcChangePctOverBars returns the percent change from the first to the
// last close in the slice. Returns ok=false on empty/single-element input
// or when the first close is zero.
func calcChangePctOverBars(closes []float64) (float64, bool) {
	if len(closes) < 2 {
		return 0, false
	}
	first := closes[0]
	last := closes[len(closes)-1]
	if first == 0 {
		return 0, false
	}
	return (last - first) / first * 100.0, true
}

// calcRelativeStrength5d returns the symbol's 5-day % change minus the ETF's
// 5-day % change. Positive = symbol outperforming sector. Returns ok=false
// if either side has insufficient or invalid data.
func calcRelativeStrength5d(symbolCloses, etfCloses []float64) (float64, bool) {
	sym, ok := calcChangePctOverBars(symbolCloses)
	if !ok {
		return 0, false
	}
	etf, ok := calcChangePctOverBars(etfCloses)
	if !ok {
		return 0, false
	}
	return sym - etf, true
}
