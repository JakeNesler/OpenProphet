package services

import (
	"fmt"
	"testing"
	"time"

	"prophet-trader/models"
)

// stubIVStore satisfies the harvestIVStore interface for testing.
type stubIVStore struct {
	saved  []*models.DBHarvestIVSnapshot
	stored []*models.DBHarvestIVSnapshot
}

func (s *stubIVStore) SaveHarvestIVSnapshot(snap *models.DBHarvestIVSnapshot) error {
	s.saved = append(s.saved, snap)
	return nil
}
func (s *stubIVStore) GetHarvestIVSnapshots(underlying string, start, end time.Time) ([]*models.DBHarvestIVSnapshot, error) {
	var out []*models.DBHarvestIVSnapshot
	for _, sn := range s.stored {
		// Matches storage semantics: start <= date < end (end is exclusive)
		if sn.Underlying == underlying && !sn.Date.Before(start) && sn.Date.Before(end) {
			out = append(out, sn)
		}
	}
	return out, nil
}

func makeSnaps(underlying string, ivValues []float64, startDate time.Time) []*models.DBHarvestIVSnapshot {
	snaps := make([]*models.DBHarvestIVSnapshot, len(ivValues))
	for i, iv := range ivValues {
		snaps[i] = &models.DBHarvestIVSnapshot{
			Underlying: underlying,
			Date:       startDate.AddDate(0, 0, i),
			ATMIV:      iv,
		}
	}
	return snaps
}

func TestCalcIVR_FullRange(t *testing.T) {
	// low=0.10, high=0.30, current=0.20 → IVR = (0.20-0.10)/(0.30-0.10)*100 = 50
	ivr := calcIVR(0.20, 0.10, 0.30)
	if ivr != 50.0 {
		t.Errorf("expected IVR=50.0, got %.2f", ivr)
	}
}

func TestCalcIVR_AtLow(t *testing.T) {
	ivr := calcIVR(0.10, 0.10, 0.30)
	if ivr != 0.0 {
		t.Errorf("expected IVR=0.0, got %.2f", ivr)
	}
}

func TestCalcIVR_AtHigh(t *testing.T) {
	ivr := calcIVR(0.30, 0.10, 0.30)
	if ivr != 100.0 {
		t.Errorf("expected IVR=100.0, got %.2f", ivr)
	}
}

func TestCalcIVR_ZeroRange(t *testing.T) {
	// If high == low, return 50 (neutral) rather than dividing by zero
	ivr := calcIVR(0.15, 0.15, 0.15)
	if ivr != 50.0 {
		t.Errorf("expected IVR=50.0 on zero range, got %.2f", ivr)
	}
}

func TestGetIVRData_SufficientHistory(t *testing.T) {
	store := &stubIVStore{}
	// 100 days of history with a known range
	start := time.Now().AddDate(0, 0, -100)
	store.stored = makeSnaps("SPY", func() []float64 {
		vals := make([]float64, 100)
		for i := range vals {
			vals[i] = 0.10 + float64(i)*0.002 // 0.10 to 0.298
		}
		return vals
	}(), start)

	svc := NewHarvestIVRService(store)
	data, err := svc.GetIVRData("SPY", 0.20)
	if err != nil {
		t.Fatalf("GetIVRData failed: %v", err)
	}
	if data.IVR < 0 || data.IVR > 100 {
		t.Errorf("IVR out of range: %.2f", data.IVR)
	}
	if data.DaysOfHistory != 100 {
		t.Errorf("expected 100 days, got %d", data.DaysOfHistory)
	}
}

func TestGetIVRData_NoHistory(t *testing.T) {
	store := &stubIVStore{}
	svc := NewHarvestIVRService(store)
	data, err := svc.GetIVRData("TLT", 0.15)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data.DaysOfHistory != 0 {
		t.Errorf("expected 0 days, got %d", data.DaysOfHistory)
	}
	// With no history, IVR is unknown — signal this with IVR = -1
	if data.IVR != -1 {
		t.Errorf("expected IVR=-1 (unknown), got %.2f", data.IVR)
	}
}

// ── IV Percentile tests ─────────────────────────────────────────

func TestCalcIVPercentile_EmptyHistory(t *testing.T) {
	p := calcIVPercentile(0.20, nil)
	if p != -1 {
		t.Errorf("expected -1 on empty history, got %.2f", p)
	}
}

func TestCalcIVPercentile_AllSame(t *testing.T) {
	p := calcIVPercentile(0.18, []float64{0.18, 0.18, 0.18})
	if p != 50.0 {
		t.Errorf("expected 50 when all values identical, got %.2f", p)
	}
}

func TestCalcIVPercentile_AtMin(t *testing.T) {
	// All historical values >= current; current equals min → 1 of N ≤ current → 1/5*100=20.
	// But more natural: percentile of 0.10 against [0.10, 0.20, 0.30, 0.40, 0.50] = 20%.
	p := calcIVPercentile(0.10, []float64{0.10, 0.20, 0.30, 0.40, 0.50})
	if p != 20.0 {
		t.Errorf("expected 20.0 when current equals min of 5 values, got %.2f", p)
	}
}

func TestCalcIVPercentile_BelowMin(t *testing.T) {
	// current below entire history → 0%.
	p := calcIVPercentile(0.05, []float64{0.10, 0.20, 0.30, 0.40, 0.50})
	if p != 0.0 {
		t.Errorf("expected 0.0 when current below all history, got %.2f", p)
	}
}

func TestCalcIVPercentile_AtMax(t *testing.T) {
	p := calcIVPercentile(0.50, []float64{0.10, 0.20, 0.30, 0.40, 0.50})
	if p != 100.0 {
		t.Errorf("expected 100.0 when current equals max, got %.2f", p)
	}
}

func TestCalcIVPercentile_Median(t *testing.T) {
	// current=0.30 vs [0.10, 0.20, 0.30, 0.40, 0.50]: 3 of 5 ≤ 0.30 → 60%.
	p := calcIVPercentile(0.30, []float64{0.10, 0.20, 0.30, 0.40, 0.50})
	if p != 60.0 {
		t.Errorf("expected 60.0 (3/5 ≤ 0.30), got %.2f", p)
	}
}

func TestGetIVRData_PopulatesIVPercentile(t *testing.T) {
	store := &stubIVStore{}
	start := time.Now().AddDate(0, 0, -10)
	store.stored = makeSnaps("SPY", []float64{0.10, 0.12, 0.15, 0.18, 0.20, 0.22, 0.25, 0.28, 0.30, 0.32}, start)

	svc := NewHarvestIVRService(store)
	data, err := svc.GetIVRData("SPY", 0.18)
	if err != nil {
		t.Fatalf("GetIVRData failed: %v", err)
	}
	// IVR by min/max: (0.18-0.10)/(0.32-0.10)*100 ≈ 36.36
	if data.IVR < 36.0 || data.IVR > 37.0 {
		t.Errorf("expected IVR≈36.36, got %.4f", data.IVR)
	}
	// Percentile: 4 of 10 ≤ 0.18 → 40%
	if data.IVPercentile != 40.0 {
		t.Errorf("expected IVPercentile=40.0, got %.2f", data.IVPercentile)
	}
}

func TestGetIVRData_IVPercentileMinusOneOnEmptyHistory(t *testing.T) {
	store := &stubIVStore{}
	svc := NewHarvestIVRService(store)
	data, err := svc.GetIVRData("XYZ", 0.20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data.IVPercentile != -1 {
		t.Errorf("expected IVPercentile=-1 on no history, got %.2f", data.IVPercentile)
	}
}

// ── GetIVRDataLatest — uses most recent stored snapshot as current ────

func TestGetIVRDataLatest_UsesMostRecentSnapshot(t *testing.T) {
	store := &stubIVStore{}
	start := time.Now().AddDate(0, 0, -5)
	// Five days: 0.10, 0.15, 0.20, 0.25, 0.30. Latest (i=4) is 0.30.
	store.stored = makeSnaps("SPY", []float64{0.10, 0.15, 0.20, 0.25, 0.30}, start)

	svc := NewHarvestIVRService(store)
	data, err := svc.GetIVRDataLatest("SPY")
	if err != nil {
		t.Fatalf("GetIVRDataLatest failed: %v", err)
	}
	if data.CurrentIV != 0.30 {
		t.Errorf("expected CurrentIV=0.30 (latest snapshot), got %f", data.CurrentIV)
	}
	if data.IVR != 100.0 {
		t.Errorf("expected IVR=100.0 (current at top of range), got %f", data.IVR)
	}
	if data.IVPercentile != 100.0 {
		t.Errorf("expected IVPercentile=100.0 (current at top), got %f", data.IVPercentile)
	}
	if data.DaysOfHistory != 5 {
		t.Errorf("expected 5 days, got %d", data.DaysOfHistory)
	}
}

func TestGetIVRDataLatest_NoHistoryReturnsSentinels(t *testing.T) {
	store := &stubIVStore{}
	svc := NewHarvestIVRService(store)
	data, err := svc.GetIVRDataLatest("XYZ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data.DaysOfHistory != 0 {
		t.Errorf("expected 0 days, got %d", data.DaysOfHistory)
	}
	if data.IVR != -1 || data.IVPercentile != -1 {
		t.Errorf("expected IVR=-1 IVPercentile=-1, got IVR=%f IVPercentile=%f", data.IVR, data.IVPercentile)
	}
}

func TestRecordDailyIV_FirstCall_WritesSnapshot(t *testing.T) {
	store := &stubIVStore{}
	svc := NewHarvestIVRService(store)
	err := svc.RecordDailyIV("SPY", 0.185)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(store.saved) != 1 {
		t.Errorf("expected 1 saved snapshot, got %d", len(store.saved))
	}
	if store.saved[0].ATMIV != 0.185 {
		t.Errorf("expected ATMIV=0.185, got %f", store.saved[0].ATMIV)
	}
}

func TestRecordDailyIV_Idempotent(t *testing.T) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	store := &stubIVStore{
		// Pre-populate stored so the service sees an existing snapshot for today
		stored: []*models.DBHarvestIVSnapshot{
			{Underlying: "SPY", Date: today, ATMIV: 0.185},
		},
	}
	svc := NewHarvestIVRService(store)
	err := svc.RecordDailyIV("SPY", 0.200) // different IV, same day
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	// Should NOT have written a second snapshot since today already has one
	if len(store.saved) != 0 {
		t.Errorf("expected no new snapshot written (idempotent), got %d", len(store.saved))
	}
}

func TestRecordDailyIV_StoreError_Propagates(t *testing.T) {
	store := &stubGetErrorIVStore{}
	svc := NewHarvestIVRService(store)
	err := svc.RecordDailyIV("SPY", 0.185)
	if err == nil {
		t.Fatal("expected error from store, got nil")
	}
}

// stubGetErrorIVStore returns an error on GetHarvestIVSnapshots.
type stubGetErrorIVStore struct{}

func (s *stubGetErrorIVStore) SaveHarvestIVSnapshot(snap *models.DBHarvestIVSnapshot) error {
	return nil
}
func (s *stubGetErrorIVStore) GetHarvestIVSnapshots(underlying string, start, end time.Time) ([]*models.DBHarvestIVSnapshot, error) {
	return nil, fmt.Errorf("DB unavailable")
}
