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
	entry := svc.computeEntry("TEST", snap)
	// volumeRatio=5.0 → volScore=20; gapPct=10% → gapScore=10; distFromHigh=(6.0-5.9)/6.0≈0.0167 ≤ 0.02 → breakoutScore=10; total=40
	if entry.Entry.BaseScore != 40.0 {
		t.Errorf("expected score=40.0 for high-volume entry, got %f", entry.Entry.BaseScore)
	}
	if entry.VolumeRatio != 5.0 {
		t.Errorf("expected volumeRatio=5.0, got %f", entry.VolumeRatio)
	}
}

func TestPennyScreenerService_ComputeEntry_NilSnapshot(t *testing.T) {
	svc := &PennyScreenerService{scores: make(map[string]TechnicalEntry), logger: newTestLogger()}
	entry := svc.computeEntry("TEST", nil)
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
	entry := svc.computeEntry("TEST", snap)
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
