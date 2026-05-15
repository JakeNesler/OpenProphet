package services

import (
	"fmt"
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
		t.Errorf("expected %s, got %s", expected2.Format("2006-01-02"), f2.Format("2006-01-02"))
	}
}

// ── Circuit breaker tests ───────────────────────────────────────────

func TestGetState_CircuitBreakerActivates(t *testing.T) {
	store := &stubHarvestStore{pnl: -5001.0} // -5.001% of 100k
	svc := NewHarvestService(store)
	state, err := svc.GetState(100000.0)
	if err != nil {
		t.Fatalf("GetState failed: %v", err)
	}
	if !state.CircuitBreakerActive {
		t.Errorf("expected circuit breaker active at -5.001%% P&L")
	}
}

func TestGetState_CircuitBreakerInactive(t *testing.T) {
	store := &stubHarvestStore{pnl: -4999.0} // -4.999% of 100k
	svc := NewHarvestService(store)
	state, err := svc.GetState(100000.0)
	if err != nil {
		t.Fatalf("GetState failed: %v", err)
	}
	if state.CircuitBreakerActive {
		t.Errorf("expected circuit breaker inactive at -4.999%% P&L")
	}
}

func TestGetState_ZeroPortfolioValue_NoCircuitBreaker(t *testing.T) {
	store := &stubHarvestStore{pnl: -10000.0}
	svc := NewHarvestService(store)
	state, err := svc.GetState(0)
	if err != nil {
		t.Fatalf("GetState failed: %v", err)
	}
	if state.CircuitBreakerActive {
		t.Errorf("expected no circuit breaker when portfolioValue=0")
	}
}

// ── Harvest state aggregation tests ────────────────────────────────

type stubHarvestStore struct {
	condors    []*models.DBHarvestCondor
	pnl        float64
	condorErr  error
	pnlErr     error
}

func (s *stubHarvestStore) ListOpenHarvestCondors() ([]*models.DBHarvestCondor, error) {
	return s.condors, s.condorErr
}
func (s *stubHarvestStore) GetHarvestClosedPnL(start, end time.Time) (float64, error) {
	return s.pnl, s.pnlErr
}
func (s *stubHarvestStore) SaveHarvestCondor(c *models.DBHarvestCondor) error { return nil }
func (s *stubHarvestStore) UpdateHarvestCondor(condorID string, updates map[string]interface{}) error {
	return nil
}
func (s *stubHarvestStore) GetHarvestCondorByID(condorID string) (*models.DBHarvestCondor, error) {
	return nil, nil
}

func TestGetState_CondorStoreError(t *testing.T) {
	store := &stubHarvestStore{condorErr: fmt.Errorf("DB down")}
	svc := NewHarvestService(store)
	_, err := svc.GetState(100000.0)
	if err == nil {
		t.Fatal("expected error from condor store, got nil")
	}
}

func TestGetState_PnLStoreError(t *testing.T) {
	store := &stubHarvestStore{pnlErr: fmt.Errorf("DB down")}
	svc := NewHarvestService(store)
	_, err := svc.GetState(100000.0)
	if err == nil {
		t.Fatal("expected error from PnL store, got nil")
	}
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

// ── OptionsExposureProvider implementation tests ────────────────────

func TestBucketExposure_NoOpenCondors_ReturnsNil(t *testing.T) {
	svc := NewHarvestService(&stubHarvestStore{})
	got := svc.BucketExposureDollars()
	if got != nil {
		t.Errorf("expected nil exposure with no condors, got %v", got)
	}
}

func TestBucketExposure_SingleSPYCondor_ContributesIndexBeta(t *testing.T) {
	// 5 contracts × $400 short put strike × 100 shares × 0.30 delta proxy = $60,000
	store := &stubHarvestStore{
		condors: []*models.DBHarvestCondor{
			{Underlying: "SPY", ShortPutStrike: 400, Contracts: 5},
		},
	}
	svc := NewHarvestService(store)
	got := svc.BucketExposureDollars()
	want := 60000.0
	if got["INDEX_BETA"] != want {
		t.Errorf("INDEX_BETA: want $%.2f, got $%.2f", want, got["INDEX_BETA"])
	}
}

func TestBucketExposure_MultipleCondorsSum(t *testing.T) {
	// SPY: 5 × $400 × 100 × 0.30 = $60,000
	// QQQ: 2 × $350 × 100 × 0.30 = $21,000
	// Total INDEX_BETA = $81,000
	store := &stubHarvestStore{
		condors: []*models.DBHarvestCondor{
			{Underlying: "SPY", ShortPutStrike: 400, Contracts: 5},
			{Underlying: "QQQ", ShortPutStrike: 350, Contracts: 2},
		},
	}
	svc := NewHarvestService(store)
	got := svc.BucketExposureDollars()
	want := 81000.0
	if got["INDEX_BETA"] != want {
		t.Errorf("INDEX_BETA: want $%.2f, got $%.2f", want, got["INDEX_BETA"])
	}
}

func TestBucketExposure_NonIndexUnderlying_Skipped(t *testing.T) {
	// Harvest is index-only by design. An AAPL condor (configuration drift)
	// must not contribute to INDEX_BETA — silently misattributing equity
	// exposure to the index bucket would defeat the cap.
	store := &stubHarvestStore{
		condors: []*models.DBHarvestCondor{
			{Underlying: "AAPL", ShortPutStrike: 180, Contracts: 3},
		},
	}
	svc := NewHarvestService(store)
	got := svc.BucketExposureDollars()
	if got != nil {
		t.Errorf("expected nil exposure for non-index underlying, got %v", got)
	}
}

func TestBucketExposure_StoreError_ReturnsNil(t *testing.T) {
	// Soft-fail: TradeGuard already fails closed on its own account fetch
	// errors. Compounding here on a transient DB hiccup would over-block.
	store := &stubHarvestStore{condorErr: fmt.Errorf("DB down")}
	svc := NewHarvestService(store)
	got := svc.BucketExposureDollars()
	if got != nil {
		t.Errorf("expected nil exposure on store error, got %v", got)
	}
}

func TestBucketExposure_ConfigurableDeltaProxy(t *testing.T) {
	// Override default 0.30 with 0.50: 5 × $400 × 100 × 0.50 = $100,000
	store := &stubHarvestStore{
		condors: []*models.DBHarvestCondor{
			{Underlying: "SPY", ShortPutStrike: 400, Contracts: 5},
		},
	}
	svc := NewHarvestService(store)
	svc.SetShortPutDeltaProxy(0.50)
	got := svc.BucketExposureDollars()
	want := 100000.0
	if got["INDEX_BETA"] != want {
		t.Errorf("INDEX_BETA: want $%.2f, got $%.2f", want, got["INDEX_BETA"])
	}
}
