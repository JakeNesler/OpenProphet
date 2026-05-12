package services

import (
	"context"
	"fmt"
	"math"
	"sync/atomic"
	"testing"
	"time"

	"prophet-trader/interfaces"
)

// ── pure helpers ─────────────────────────────────────────────────

func TestCalcChangePctOverBars_Empty(t *testing.T) {
	if _, ok := calcChangePctOverBars(nil); ok {
		t.Error("expected ok=false for nil")
	}
	if _, ok := calcChangePctOverBars([]float64{}); ok {
		t.Error("expected ok=false for empty")
	}
}

func TestCalcChangePctOverBars_SingleCloseInsufficient(t *testing.T) {
	if _, ok := calcChangePctOverBars([]float64{100}); ok {
		t.Error("expected ok=false with one close")
	}
}

func TestCalcChangePctOverBars_FirstZero(t *testing.T) {
	if _, ok := calcChangePctOverBars([]float64{0, 100}); ok {
		t.Error("expected ok=false when first close is zero")
	}
}

func TestCalcChangePctOverBars_Typical(t *testing.T) {
	got, ok := calcChangePctOverBars([]float64{100, 105})
	if !ok {
		t.Fatal("expected ok=true")
	}
	if math.Abs(got-5.0) > 1e-9 {
		t.Errorf("expected +5.0, got %f", got)
	}
}

func TestCalcChangePctOverBars_MultiBarUsesFirstAndLast(t *testing.T) {
	got, ok := calcChangePctOverBars([]float64{100, 99, 101, 110})
	if !ok {
		t.Fatal("expected ok=true")
	}
	if math.Abs(got-10.0) > 1e-9 {
		t.Errorf("expected +10.0 (100 -> 110), got %f", got)
	}
}

func TestCalcRelativeStrength5d_EqualReturns(t *testing.T) {
	got, ok := calcRelativeStrength5d([]float64{100, 105}, []float64{200, 210}) // both +5%
	if !ok {
		t.Fatal("expected ok=true")
	}
	if math.Abs(got) > 1e-9 {
		t.Errorf("expected 0 for equal returns, got %f", got)
	}
}

func TestCalcRelativeStrength5d_SymbolOutperforms(t *testing.T) {
	got, ok := calcRelativeStrength5d([]float64{100, 105}, []float64{200, 204}) // +5% vs +2%
	if !ok {
		t.Fatal("expected ok=true")
	}
	if math.Abs(got-3.0) > 1e-9 {
		t.Errorf("expected +3.0, got %f", got)
	}
}

func TestCalcRelativeStrength5d_SymbolUnderperforms(t *testing.T) {
	got, ok := calcRelativeStrength5d([]float64{100, 99}, []float64{200, 206}) // -1% vs +3%
	if !ok {
		t.Fatal("expected ok=true")
	}
	if math.Abs(got-(-4.0)) > 1e-9 {
		t.Errorf("expected -4.0, got %f", got)
	}
}

func TestCalcRelativeStrength5d_EmptyEitherSide(t *testing.T) {
	if _, ok := calcRelativeStrength5d(nil, []float64{200, 210}); ok {
		t.Error("expected ok=false with empty symbol closes")
	}
	if _, ok := calcRelativeStrength5d([]float64{100, 105}, nil); ok {
		t.Error("expected ok=false with empty etf closes")
	}
}

// ── enrichment integration ────────────────────────────────────────

type stubSectorData struct {
	bars   map[string][]*interfaces.Bar
	failOn map[string]bool
	calls  int32
}

func (s *stubSectorData) GetHistoricalBars(ctx context.Context, symbol string, start, end time.Time, timeframe string) ([]*interfaces.Bar, error) {
	atomic.AddInt32(&s.calls, 1)
	if s.failOn[symbol] {
		return nil, fmt.Errorf("simulated failure for %s", symbol)
	}
	return s.bars[symbol], nil
}

func (s *stubSectorData) GetLatestBar(ctx context.Context, symbol string) (*interfaces.Bar, error) {
	return nil, fmt.Errorf("unused")
}

func (s *stubSectorData) GetLatestQuote(ctx context.Context, symbol string) (*interfaces.Quote, error) {
	return nil, fmt.Errorf("unused")
}

func (s *stubSectorData) GetLatestTrade(ctx context.Context, symbol string) (*interfaces.Trade, error) {
	return nil, fmt.Errorf("unused")
}

func (s *stubSectorData) StreamBars(ctx context.Context, symbols []string) (<-chan *interfaces.Bar, error) {
	return nil, fmt.Errorf("unused")
}

func dailyBars(closes []float64) []*interfaces.Bar {
	bars := make([]*interfaces.Bar, len(closes))
	for i, c := range closes {
		bars[i] = &interfaces.Bar{Close: c, Volume: 1_000_000}
	}
	return bars
}

func TestEnrichAnalysisWithSector_EquityWithMappedETF(t *testing.T) {
	now := time.Date(2026, 6, 15, 14, 0, 0, 0, time.UTC)
	ds := &stubSectorData{bars: map[string][]*interfaces.Bar{
		"NVDA": dailyBars([]float64{480, 482, 485, 490, 495, 500}), // +4.17% 5d
		"SMH":  dailyBars([]float64{240, 241, 242, 243, 244, 245}), // +2.08% 5d
	}}
	cache := newSectorContextCache(ds)
	analysis := &StockAnalysis{Symbol: "NVDA"}
	enrichAnalysisWithSector(context.Background(), analysis, cache, "NVDA", now)
	if analysis.Sector == nil {
		t.Fatal("expected Sector summary populated for NVDA")
	}
	if analysis.Sector.ETF != "SMH" {
		t.Errorf("expected ETF=SMH, got %s", analysis.Sector.ETF)
	}
	// Relative strength: 4.17 - 2.08 ≈ 2.09 (symbol outperforming).
	if analysis.Sector.RelativeStrength5d < 1.5 || analysis.Sector.RelativeStrength5d > 2.5 {
		t.Errorf("expected RS5d ≈ 2.09, got %f", analysis.Sector.RelativeStrength5d)
	}
}

func TestEnrichAnalysisWithSector_UnmappedSymbolLeavesNil(t *testing.T) {
	now := time.Date(2026, 6, 15, 14, 0, 0, 0, time.UTC)
	ds := &stubSectorData{bars: map[string][]*interfaces.Bar{}}
	cache := newSectorContextCache(ds)
	analysis := &StockAnalysis{Symbol: "SPY"} // no sector mapping
	enrichAnalysisWithSector(context.Background(), analysis, cache, "SPY", now)
	if analysis.Sector != nil {
		t.Errorf("expected Sector nil for unmapped SPY, got %+v", analysis.Sector)
	}
}

func TestEnrichAnalysisWithSector_FetchErrorLeavesNil(t *testing.T) {
	now := time.Date(2026, 6, 15, 14, 0, 0, 0, time.UTC)
	ds := &stubSectorData{failOn: map[string]bool{"NVDA": true, "SMH": true}}
	cache := newSectorContextCache(ds)
	analysis := &StockAnalysis{Symbol: "NVDA"}
	enrichAnalysisWithSector(context.Background(), analysis, cache, "NVDA", now)
	if analysis.Sector != nil {
		t.Error("expected Sector nil on fetch failure (soft-fail)")
	}
}

func TestEnrichAnalysisWithSector_NilCacheNoOp(t *testing.T) {
	analysis := &StockAnalysis{Symbol: "NVDA"}
	enrichAnalysisWithSector(context.Background(), analysis, nil, "NVDA", time.Now())
	if analysis.Sector != nil {
		t.Error("expected Sector nil with nil cache")
	}
}

func TestEnrichAnalysisWithCrossAsset_TrendUniverseSymbol(t *testing.T) {
	now := time.Date(2026, 6, 15, 14, 0, 0, 0, time.UTC)
	ds := &stubSectorData{bars: map[string][]*interfaces.Bar{
		"UUP": dailyBars([]float64{29, 29, 29, 29, 29, 30}),    // +3.45%
		"IEF": dailyBars([]float64{95, 95, 95, 95, 95, 94}),    // -1.05%
		"HYG": dailyBars([]float64{78, 78, 78, 78, 78, 79}),    // +1.28%
	}}
	cache := newSectorContextCache(ds)
	analysis := &StockAnalysis{Symbol: "TLT"}
	enrichAnalysisWithCrossAsset(context.Background(), analysis, cache, "TLT", now)
	if analysis.CrossAsset == nil {
		t.Fatal("expected CrossAsset populated for TLT")
	}
	if analysis.CrossAsset.DXYChangePct5d < 3 || analysis.CrossAsset.DXYChangePct5d > 4 {
		t.Errorf("expected DXYChangePct5d ≈ 3.45, got %f", analysis.CrossAsset.DXYChangePct5d)
	}
	if analysis.CrossAsset.RateProxyPct5d > 0 {
		t.Errorf("expected RateProxyPct5d negative (rates rising), got %f", analysis.CrossAsset.RateProxyPct5d)
	}
	if analysis.CrossAsset.HYGChangePct5d < 1 || analysis.CrossAsset.HYGChangePct5d > 2 {
		t.Errorf("expected HYGChangePct5d ≈ 1.28, got %f", analysis.CrossAsset.HYGChangePct5d)
	}
}

func TestEnrichAnalysisWithCrossAsset_NonTrendSymbolLeavesNil(t *testing.T) {
	now := time.Date(2026, 6, 15, 14, 0, 0, 0, time.UTC)
	ds := &stubSectorData{bars: map[string][]*interfaces.Bar{}}
	cache := newSectorContextCache(ds)
	analysis := &StockAnalysis{Symbol: "NVDA"} // not in Trend universe
	enrichAnalysisWithCrossAsset(context.Background(), analysis, cache, "NVDA", now)
	if analysis.CrossAsset != nil {
		t.Errorf("expected CrossAsset nil for non-Trend NVDA, got %+v", analysis.CrossAsset)
	}
}

func TestSectorContextCache_CachesETFFetches(t *testing.T) {
	now := time.Date(2026, 6, 15, 14, 0, 0, 0, time.UTC)
	ds := &stubSectorData{bars: map[string][]*interfaces.Bar{
		"NVDA": dailyBars([]float64{480, 485, 490, 495, 500, 505}),
		"SMH":  dailyBars([]float64{240, 242, 244, 246, 248, 250}),
	}}
	cache := newSectorContextCache(ds)

	// First enrichment — should hit DS for both NVDA and SMH.
	analysis1 := &StockAnalysis{Symbol: "NVDA"}
	enrichAnalysisWithSector(context.Background(), analysis1, cache, "NVDA", now)
	callsAfterFirst := atomic.LoadInt32(&ds.calls)
	if callsAfterFirst < 2 {
		t.Fatalf("expected at least 2 fetches (NVDA + SMH), got %d", callsAfterFirst)
	}

	// Second enrichment of AMD (also maps to SMH) within cache TTL — SMH
	// reused, only AMD fetched.
	ds.bars["AMD"] = dailyBars([]float64{140, 142, 144, 146, 148, 150})
	analysis2 := &StockAnalysis{Symbol: "AMD"}
	enrichAnalysisWithSector(context.Background(), analysis2, cache, "AMD", now.Add(30*time.Minute))
	callsAfterSecond := atomic.LoadInt32(&ds.calls)
	// First call did NVDA+SMH (≥2). Second should add only AMD (1 more).
	if callsAfterSecond-callsAfterFirst != 1 {
		t.Errorf("expected exactly 1 new fetch (AMD only — SMH cached), got %d new", callsAfterSecond-callsAfterFirst)
	}
}
