package services

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"prophet-trader/interfaces"
)

// ── pure helpers ─────────────────────────────────────────────────

func TestCalcORB_Empty(t *testing.T) {
	_, _, ok := calcORB(nil)
	if ok {
		t.Error("expected ok=false for nil bars")
	}
	_, _, ok = calcORB([]*interfaces.Bar{})
	if ok {
		t.Error("expected ok=false for empty slice")
	}
}

func TestCalcORB_SingleBar(t *testing.T) {
	bars := []*interfaces.Bar{{High: 5.10, Low: 4.90}}
	h, l, ok := calcORB(bars)
	if !ok {
		t.Fatal("expected ok=true for single bar")
	}
	if h != 5.10 || l != 4.90 {
		t.Errorf("expected (5.10, 4.90), got (%f, %f)", h, l)
	}
}

func TestCalcORB_MultiBarHighLow(t *testing.T) {
	bars := []*interfaces.Bar{
		{High: 5.05, Low: 4.95},
		{High: 5.20, Low: 4.85}, // widest bar
		{High: 5.15, Low: 5.00},
	}
	h, l, ok := calcORB(bars)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if h != 5.20 {
		t.Errorf("expected high=5.20, got %f", h)
	}
	if l != 4.85 {
		t.Errorf("expected low=4.85, got %f", l)
	}
}

func TestClassifyORBStatus_AboveHigh(t *testing.T) {
	if got := classifyORBStatus(5.25, 5.20, 4.85); got != "above_or_high" {
		t.Errorf("expected above_or_high, got %s", got)
	}
}

func TestClassifyORBStatus_BelowLow(t *testing.T) {
	if got := classifyORBStatus(4.80, 5.20, 4.85); got != "below_or_low" {
		t.Errorf("expected below_or_low, got %s", got)
	}
}

func TestClassifyORBStatus_InsideRange(t *testing.T) {
	if got := classifyORBStatus(5.00, 5.20, 4.85); got != "inside_or" {
		t.Errorf("expected inside_or, got %s", got)
	}
}

func TestClassifyORBStatus_AtBoundaryHigh(t *testing.T) {
	// Exactly at the OR high → inside (strict greater-than for break).
	if got := classifyORBStatus(5.20, 5.20, 4.85); got != "inside_or" {
		t.Errorf("expected inside_or at boundary, got %s", got)
	}
}

func TestClassifyORBStatus_ZeroBoundsAwaiting(t *testing.T) {
	if got := classifyORBStatus(5.00, 0, 0); got != "awaiting" {
		t.Errorf("expected awaiting with zero bounds, got %s", got)
	}
}

// ── PennyIntradayCache — GetORB ───────────────────────────────────

type stubPennyData struct {
	bars      map[string][]*interfaces.Bar
	dailyBars map[string][]*interfaces.Bar
	failOn    map[string]bool
	calls     int32
}

func (s *stubPennyData) GetHistoricalBars(ctx context.Context, symbol string, start, end time.Time, timeframe string) ([]*interfaces.Bar, error) {
	atomic.AddInt32(&s.calls, 1)
	if s.failOn[symbol] {
		return nil, fmt.Errorf("simulated failure for %s", symbol)
	}
	if timeframe == "1Day" {
		return s.dailyBars[symbol], nil
	}
	return s.bars[symbol], nil
}

func mkBar(high, low float64, vol int64) *interfaces.Bar {
	return &interfaces.Bar{High: high, Low: low, Close: (high + low) / 2, Volume: vol}
}

// 10:00 ET = 14:00 UTC during EDT. 9:30 ET = 13:30 UTC.
func etTime(year int, month time.Month, day, hour, minute int) time.Time {
	loc, _ := time.LoadLocation("America/New_York")
	return time.Date(year, month, day, hour, minute, 0, 0, loc)
}

func TestPennyIntradayCache_GetORB_BeforeWindow(t *testing.T) {
	// 9:40 ET — inside the 9:30-9:45 OR window, should return ok=false (not done capturing yet)
	now := etTime(2026, 6, 15, 9, 40)
	ds := &stubPennyData{bars: map[string][]*interfaces.Bar{"AAA": {mkBar(5.10, 4.90, 1000)}}}
	cache := NewPennyIntradayCache(ds)

	_, _, ok := cache.GetORB(context.Background(), "AAA", now)
	if ok {
		t.Error("expected ok=false before 9:45 ET")
	}
}

func TestPennyIntradayCache_GetORB_AfterWindowFetchesAndCaches(t *testing.T) {
	now := etTime(2026, 6, 15, 10, 0)
	ds := &stubPennyData{bars: map[string][]*interfaces.Bar{
		"AAA": {mkBar(5.10, 4.95, 1000), mkBar(5.20, 4.85, 1500), mkBar(5.15, 5.00, 900)},
	}}
	cache := NewPennyIntradayCache(ds)

	h, l, ok := cache.GetORB(context.Background(), "AAA", now)
	if !ok {
		t.Fatal("expected ok=true after 9:45 ET")
	}
	if h != 5.20 || l != 4.85 {
		t.Errorf("expected (5.20, 4.85), got (%f, %f)", h, l)
	}
	if atomic.LoadInt32(&ds.calls) != 1 {
		t.Errorf("expected 1 HTTP call, got %d", atomic.LoadInt32(&ds.calls))
	}

	// Second call same day — cached.
	_, _, ok = cache.GetORB(context.Background(), "AAA", now.Add(2*time.Hour))
	if !ok {
		t.Fatal("expected ok=true on cached lookup")
	}
	if atomic.LoadInt32(&ds.calls) != 1 {
		t.Errorf("expected still 1 HTTP call after cache hit, got %d", atomic.LoadInt32(&ds.calls))
	}
}

func TestPennyIntradayCache_GetORB_FetchErrorReturnsNotOK(t *testing.T) {
	now := etTime(2026, 6, 15, 10, 0)
	ds := &stubPennyData{failOn: map[string]bool{"BAD": true}}
	cache := NewPennyIntradayCache(ds)

	_, _, ok := cache.GetORB(context.Background(), "BAD", now)
	if ok {
		t.Error("expected ok=false on fetch error (degrade silently)")
	}
}

// ── PennyIntradayCache — GetAvgDailyVolume20d ────────────────────

func TestPennyIntradayCache_GetAvgDailyVolume20d_KnownFixture(t *testing.T) {
	now := etTime(2026, 6, 15, 10, 0)
	// 5 daily bars with volumes 1000, 2000, 3000, 4000, 5000 → avg = 3000.
	bars := []*interfaces.Bar{
		mkBar(1, 1, 1000), mkBar(1, 1, 2000), mkBar(1, 1, 3000), mkBar(1, 1, 4000), mkBar(1, 1, 5000),
	}
	ds := &stubPennyData{dailyBars: map[string][]*interfaces.Bar{"AAA": bars}}
	cache := NewPennyIntradayCache(ds)

	avg, err := cache.GetAvgDailyVolume20d(context.Background(), "AAA", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if avg != 3000 {
		t.Errorf("expected avg=3000, got %d", avg)
	}
}

func TestPennyIntradayCache_GetAvgDailyVolume20d_EmptyReturnsZero(t *testing.T) {
	now := etTime(2026, 6, 15, 10, 0)
	ds := &stubPennyData{dailyBars: map[string][]*interfaces.Bar{}}
	cache := NewPennyIntradayCache(ds)

	avg, err := cache.GetAvgDailyVolume20d(context.Background(), "AAA", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if avg != 0 {
		t.Errorf("expected 0 on empty data, got %d", avg)
	}
}

func TestPennyIntradayCache_GetAvgDailyVolume20d_CachedSameDay(t *testing.T) {
	now := etTime(2026, 6, 15, 10, 0)
	ds := &stubPennyData{dailyBars: map[string][]*interfaces.Bar{"AAA": {mkBar(1, 1, 1000)}}}
	cache := NewPennyIntradayCache(ds)

	_, _ = cache.GetAvgDailyVolume20d(context.Background(), "AAA", now)
	calls1 := atomic.LoadInt32(&ds.calls)
	if calls1 != 1 {
		t.Fatalf("expected 1 HTTP call, got %d", calls1)
	}
	_, _ = cache.GetAvgDailyVolume20d(context.Background(), "AAA", now.Add(3*time.Hour))
	calls2 := atomic.LoadInt32(&ds.calls)
	if calls2 != 1 {
		t.Errorf("expected still 1 HTTP call after same-day cache hit, got %d", calls2)
	}
}
