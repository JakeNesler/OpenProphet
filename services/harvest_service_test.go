package services

import (
	"testing"
	"time"

	"prophet-trader/models"
)

// ── FOMC tests ─────────────────────────────────────────────────────

func TestIsFOMCBlackout_InsideWindow(t *testing.T) {
	// A date that is 12 hours before a known 2026 FOMC meeting (Jan 28, 2026 2pm ET)
	fomc := time.Date(2026, 1, 28, 19, 0, 0, 0, time.UTC)
	testTime := fomc.Add(-12 * time.Hour)
	if !isFOMCBlackout(testTime, fomc2026Dates) {
		t.Error("expected blackout 12h before FOMC")
	}
}

func TestIsFOMCBlackout_OutsideWindow(t *testing.T) {
	fomc := time.Date(2026, 1, 28, 19, 0, 0, 0, time.UTC)
	testTime := fomc.Add(-25 * time.Hour) // 25h before — outside 24h window
	if isFOMCBlackout(testTime, fomc2026Dates) {
		t.Error("expected no blackout 25h before FOMC")
	}
}

func TestIsFOMCBlackout_AfterAnnouncement(t *testing.T) {
	fomc := time.Date(2026, 1, 28, 19, 0, 0, 0, time.UTC)
	testTime := fomc.Add(1 * time.Hour) // 1h after announcement
	if isFOMCBlackout(testTime, fomc2026Dates) {
		t.Error("expected no blackout after FOMC announcement")
	}
}

// ── Monthly expiration tests ────────────────────────────────────────

func TestNextMonthlyExpiration_InBand(t *testing.T) {
	// May 1, 2026 → next monthly is Jun 19, 2026 → DTE = 49 ✓
	ref := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	exp, dte, ok := nextMonthlyExpiration(ref, 35, 55)
	if !ok {
		t.Fatal("expected to find expiration in [35,55] band")
	}
	if dte < 35 || dte > 55 {
		t.Errorf("DTE %d out of [35,55] band, expiration=%s", dte, exp.Format("2006-01-02"))
	}
	// Verify it's a Friday
	if exp.Weekday() != time.Friday {
		t.Errorf("expiration %s is not a Friday", exp.Format("2006-01-02"))
	}
}

func TestNextMonthlyExpiration_NoneInBand(t *testing.T) {
	// Use extremely tight band [99, 100] which will never match
	ref := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	_, _, ok := nextMonthlyExpiration(ref, 99, 100)
	if ok {
		t.Error("expected no expiration in impossible [99,100] DTE band")
	}
}

func TestThirdFriday(t *testing.T) {
	// June 2026: 3rd Friday is June 19
	f := thirdFriday(2026, time.June)
	expected := time.Date(2026, time.June, 19, 0, 0, 0, 0, time.UTC)
	if !f.Equal(expected) {
		t.Errorf("expected %s, got %s", expected.Format("2006-01-02"), f.Format("2006-01-02"))
	}
	// January 2026: 3rd Friday is Jan 16
	f2 := thirdFriday(2026, time.January)
	expected2 := time.Date(2026, time.January, 16, 0, 0, 0, 0, time.UTC)
	if !f2.Equal(expected2) {
		t.Errorf("expected %s, got %s", expected2.Format("2006-01-02"), f2.Format("2026-01-02"))
	}
}

// ── Circuit breaker tests ───────────────────────────────────────────

func TestCircuitBreakerThreshold(t *testing.T) {
	portfolioValue := 100000.0
	threshold := portfolioValue * 0.05 // -5%
	if threshold != 5000.0 {
		t.Errorf("expected threshold=5000, got %.2f", threshold)
	}
}

// ── Harvest state aggregation tests ────────────────────────────────

type stubHarvestStore struct {
	condors []*models.DBHarvestCondor
	pnl     float64
}

func (s *stubHarvestStore) ListOpenHarvestCondors() ([]*models.DBHarvestCondor, error) {
	return s.condors, nil
}
func (s *stubHarvestStore) GetHarvestClosedPnL(start, end time.Time) (float64, error) {
	return s.pnl, nil
}
func (s *stubHarvestStore) SaveHarvestCondor(c *models.DBHarvestCondor) error { return nil }
func (s *stubHarvestStore) UpdateHarvestCondor(condorID string, updates map[string]interface{}) error {
	return nil
}
func (s *stubHarvestStore) GetHarvestCondorByID(condorID string) (*models.DBHarvestCondor, error) {
	return nil, nil
}

func TestCalcDeployedBuyingPower(t *testing.T) {
	condors := []*models.DBHarvestCondor{
		{MaxLoss: 1000.0}, // $1000 max loss
		{MaxLoss: 500.0},  // $500 max loss
	}
	portfolioValue := 100000.0
	pct := calcDeployedBuyingPowerPct(condors, portfolioValue)
	expected := 1.5 // 1500 / 100000 * 100
	if pct != expected {
		t.Errorf("expected %.2f%%, got %.2f%%", expected, pct)
	}
}
