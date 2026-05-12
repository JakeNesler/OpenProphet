package services

import (
	"testing"
	"time"

	alpacaMarket "github.com/alpacahq/alpaca-trade-api-go/v3/marketdata"
	"github.com/sirupsen/logrus"
)

func newTestLogger() *logrus.Logger {
	return logrus.New()
}

func TestPennyScreenerService_ComputeEntry_HighVolume(t *testing.T) {
	svc := &PennyScreenerService{
		scores: make(map[string]TechnicalEntry),
		logger: newTestLogger(),
	}
	snap := &alpacaMarket.Snapshot{
		DailyBar: &alpacaMarket.Bar{
			Open: 5.5, High: 6.0, Low: 5.0, Close: 5.9,
			Volume: 500_000,
		},
		PrevDailyBar: &alpacaMarket.Bar{
			Open: 5.0, High: 5.2, Low: 4.8, Close: 5.0,
			Volume: 100_000,
		},
	}
	// RVOL = 3.0 hits the volume-score ceiling (min(3/3, 1) × 20 = 20).
	// gapPct = 10% → gapScore=10; distFromHigh=(6.0-5.9)/6.0≈0.0167 ≤ 0.02 → breakoutScore=10.
	// total = 20 + 10 + 10 = 40.
	intra := intradayContext{rvol: 3.0}
	beforeCall := time.Now()
	entry := svc.computeEntry("TEST", snap, intra, beforeCall)
	afterCall := time.Now()
	if entry.Entry.BaseScore != 40.0 {
		t.Errorf("expected score=40.0 for high-volume entry, got %f", entry.Entry.BaseScore)
	}
	if entry.RVOL != 3.0 {
		t.Errorf("expected RVOL=3.0, got %f", entry.RVOL)
	}
	// Legacy field still computed from daily ratio.
	if entry.VolumeRatio != 5.0 {
		t.Errorf("expected legacy volumeRatio=5.0, got %f", entry.VolumeRatio)
	}
	if entry.Entry.HalfLifeHrs != 2.0 {
		t.Errorf("expected HalfLifeHrs=2.0, got %f", entry.Entry.HalfLifeHrs)
	}
	if entry.Entry.EventTime.Before(beforeCall) || entry.Entry.EventTime.After(afterCall) {
		t.Errorf("expected EventTime within call window, got %v", entry.Entry.EventTime)
	}
}

func TestPennyScreenerService_ComputeEntry_LowRVOLScalesDownVolumeScore(t *testing.T) {
	svc := &PennyScreenerService{scores: make(map[string]TechnicalEntry), logger: newTestLogger()}
	snap := &alpacaMarket.Snapshot{
		DailyBar:     &alpacaMarket.Bar{Open: 5.5, High: 6.0, Low: 5.0, Close: 5.9, Volume: 100_000},
		PrevDailyBar: &alpacaMarket.Bar{Open: 5.0, High: 5.2, Low: 4.8, Close: 5.0, Volume: 100_000},
	}
	// RVOL = 0.6 → volScore = 0.6/3 × 20 = 4.0
	// gapPct = 10% → 10; breakout (6-5.9)/6=0.0167 ≤ 0.02 → 10. Total = 24.
	intra := intradayContext{rvol: 0.6}
	entry := svc.computeEntry("TEST", snap, intra, time.Now())
	if entry.Entry.BaseScore != 24.0 {
		t.Errorf("expected score=24.0 (low RVOL), got %f", entry.Entry.BaseScore)
	}
}

func TestPennyScreenerService_ComputeEntry_PopulatesORBFields(t *testing.T) {
	svc := &PennyScreenerService{scores: make(map[string]TechnicalEntry), logger: newTestLogger()}
	snap := &alpacaMarket.Snapshot{
		DailyBar:     &alpacaMarket.Bar{Open: 5.5, High: 6.0, Low: 5.0, Close: 5.30, Volume: 100_000},
		PrevDailyBar: &alpacaMarket.Bar{Open: 5.0, High: 5.2, Low: 4.8, Close: 5.0, Volume: 100_000},
	}
	intra := intradayContext{rvol: 1.0, orbHigh: 5.20, orbLow: 4.85, orbOK: true}
	entry := svc.computeEntry("TEST", snap, intra, time.Now())
	if entry.ORBHigh != 5.20 || entry.ORBLow != 4.85 {
		t.Errorf("expected OR (5.20, 4.85), got (%f, %f)", entry.ORBHigh, entry.ORBLow)
	}
	// Close=5.30 > orHigh=5.20 → above_or_high
	if entry.ORBStatus != "above_or_high" {
		t.Errorf("expected ORBStatus=above_or_high, got %s", entry.ORBStatus)
	}
}

func TestPennyScreenerService_ComputeEntry_ORBAwaitingWhenNotCaptured(t *testing.T) {
	svc := &PennyScreenerService{scores: make(map[string]TechnicalEntry), logger: newTestLogger()}
	snap := &alpacaMarket.Snapshot{
		DailyBar:     &alpacaMarket.Bar{Open: 5.5, High: 6.0, Low: 5.0, Close: 5.30, Volume: 100_000},
		PrevDailyBar: &alpacaMarket.Bar{Open: 5.0, High: 5.2, Low: 4.8, Close: 5.0, Volume: 100_000},
	}
	intra := intradayContext{rvol: 1.0} // orbOK=false, bounds=0
	entry := svc.computeEntry("TEST", snap, intra, time.Now())
	if entry.ORBStatus != "awaiting" {
		t.Errorf("expected ORBStatus=awaiting when OR not captured, got %s", entry.ORBStatus)
	}
}

func TestPennyScreenerService_ComputeEntry_NilSnapshot(t *testing.T) {
	svc := &PennyScreenerService{scores: make(map[string]TechnicalEntry), logger: newTestLogger()}
	entry := svc.computeEntry("TEST", nil, intradayContext{}, time.Now())
	if entry.Entry.BaseScore != 0 {
		t.Errorf("expected 0 for nil snapshot, got %f", entry.Entry.BaseScore)
	}
}

func TestPennyScreenerService_ComputeEntry_PartialNil(t *testing.T) {
	svc := &PennyScreenerService{scores: make(map[string]TechnicalEntry), logger: newTestLogger()}
	snap := &alpacaMarket.Snapshot{
		DailyBar: &alpacaMarket.Bar{Open: 5.5, High: 6.0, Low: 5.0, Close: 5.9, Volume: 100_000},
		// PrevDailyBar intentionally nil
	}
	entry := svc.computeEntry("TEST", snap, intradayContext{}, time.Now())
	if entry.Entry.BaseScore != 0 {
		t.Errorf("expected 0 score for partial-nil snapshot, got %f", entry.Entry.BaseScore)
	}
}

func TestPennyScreenerService_GetTechnicalScore_Decay(t *testing.T) {
	svc := &PennyScreenerService{scores: make(map[string]TechnicalEntry), logger: newTestLogger()}
	// 2-hour-old entry with 2h half-life → score should be ~half
	svc.scores["STALE"] = TechnicalEntry{
		Entry: DecayEntry{BaseScore: 40.0, EventTime: time.Now().Add(-2 * time.Hour), HalfLifeHrs: 2.0},
	}
	got, _ := svc.GetTechnicalScore("STALE")
	if got < 18 || got > 22 {
		t.Errorf("expected ~20 at half-life, got %f", got)
	}
}

func TestUpdateAnchor_FirstObservation(t *testing.T) {
	before := time.Now()
	base, anchor := updateAnchor(30.0, 0, time.Time{}, false)
	after := time.Now()
	if base != 30.0 {
		t.Errorf("first obs: expected base=30.0, got %f", base)
	}
	if anchor.Before(before) || anchor.After(after) {
		t.Errorf("first obs: anchor should be ~now, got %v", anchor)
	}
}

func TestUpdateAnchor_PriorZero_NewPositive(t *testing.T) {
	before := time.Now()
	base, anchor := updateAnchor(25.0, 0, time.Now().Add(-1*time.Hour), true)
	after := time.Now()
	if base != 25.0 {
		t.Errorf("prior zero: expected base=25.0, got %f", base)
	}
	if anchor.Before(before) || anchor.After(after) {
		t.Error("prior zero: anchor should reset to now")
	}
}

func TestUpdateAnchor_SmallChange_PreservesAnchor(t *testing.T) {
	oldAnchor := time.Now().Add(-2 * time.Hour)
	// 5% change (below 10% threshold)
	base, anchor := updateAnchor(31.5, 30.0, oldAnchor, true)
	if base != 30.0 {
		t.Errorf("small change: expected base preserved at 30.0, got %f", base)
	}
	if !anchor.Equal(oldAnchor) {
		t.Errorf("small change: expected anchor preserved at %v, got %v", oldAnchor, anchor)
	}
}

func TestUpdateAnchor_LargeChange_UpdatesAnchor(t *testing.T) {
	oldAnchor := time.Now().Add(-2 * time.Hour)
	// 20% change (above 10% threshold)
	before := time.Now()
	base, anchor := updateAnchor(24.0, 20.0, oldAnchor, true)
	after := time.Now()
	if base != 24.0 {
		t.Errorf("large change: expected base=24.0, got %f", base)
	}
	if anchor.Before(before) || anchor.After(after) {
		t.Errorf("large change: expected anchor ~now, got %v", anchor)
	}
}

func TestUpdateAnchor_ExactlyTenPercent_PreservesAnchor(t *testing.T) {
	// 30.0 * 1.10 = 33.0 → |33-30|/30 = 0.10, NOT > 0.10 → preserve
	oldAnchor := time.Now().Add(-1 * time.Hour)
	base, anchor := updateAnchor(33.0, 30.0, oldAnchor, true)
	if base != 30.0 {
		t.Errorf("exactly 10%%: expected base preserved, got %f", base)
	}
	if !anchor.Equal(oldAnchor) {
		t.Error("exactly 10%: expected anchor preserved (not strictly > 10%)")
	}
}

func TestPennyScreenerService_ComputeEntry_NilSnapshot_PreservesPrior(t *testing.T) {
	svc := &PennyScreenerService{scores: make(map[string]TechnicalEntry), logger: newTestLogger()}
	anchor := time.Now().Add(-1 * time.Hour)
	svc.scores["TEST"] = TechnicalEntry{
		Entry: DecayEntry{BaseScore: 30.0, EventTime: anchor, HalfLifeHrs: 2.0},
	}
	entry := svc.computeEntry("TEST", nil, intradayContext{}, time.Now())
	if entry.Entry.BaseScore != 30.0 {
		t.Errorf("nil snapshot with prior: expected BaseScore=30.0 preserved, got %f", entry.Entry.BaseScore)
	}
	if !entry.Entry.EventTime.Equal(anchor) {
		t.Errorf("nil snapshot with prior: expected EventTime preserved, got %v", entry.Entry.EventTime)
	}
}
