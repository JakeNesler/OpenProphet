package services

import (
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
		if sn.Underlying == underlying && !sn.Date.Before(start) && !sn.Date.After(end) {
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
