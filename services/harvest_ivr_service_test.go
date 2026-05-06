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
