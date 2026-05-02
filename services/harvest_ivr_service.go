package services

import (
	"fmt"
	"math"
	"time"

	"prophet-trader/models"
)

// harvestIVStore is the subset of storage used by the IVR service.
type harvestIVStore interface {
	SaveHarvestIVSnapshot(snap *models.DBHarvestIVSnapshot) error
	GetHarvestIVSnapshots(underlying string, start, end time.Time) ([]*models.DBHarvestIVSnapshot, error)
}

// IVRData contains the result of an IVR calculation.
type IVRData struct {
	Underlying    string
	CurrentIV     float64
	Low52Wk       float64
	High52Wk      float64
	IVR           float64 // -1 means insufficient history
	DaysOfHistory int
}

// HarvestIVRService collects and calculates IV rank for Harvest underlyings.
type HarvestIVRService struct {
	store harvestIVStore
}

// NewHarvestIVRService creates a new IVR service.
func NewHarvestIVRService(store harvestIVStore) *HarvestIVRService {
	return &HarvestIVRService{store: store}
}

// RecordDailyIV stores today's ATM IV for the given underlying if not already stored today.
func (s *HarvestIVRService) RecordDailyIV(underlying string, atmIV float64) error {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	existing, err := s.store.GetHarvestIVSnapshots(underlying, today, today.Add(23*time.Hour+59*time.Minute))
	if err != nil {
		return fmt.Errorf("checking existing snapshot: %w", err)
	}
	if len(existing) > 0 {
		return nil // already recorded today
	}
	return s.store.SaveHarvestIVSnapshot(&models.DBHarvestIVSnapshot{
		Underlying: underlying,
		Date:       today,
		ATMIV:      atmIV,
	})
}

// GetIVRData returns the IVR for an underlying given its current ATM IV.
// currentIV should be the ATM implied volatility from live quotes.
func (s *HarvestIVRService) GetIVRData(underlying string, currentIV float64) (*IVRData, error) {
	end := time.Now().UTC()
	start := end.AddDate(-1, 0, 0) // up to 52 weeks back
	snaps, err := s.store.GetHarvestIVSnapshots(underlying, start, end)
	if err != nil {
		return nil, fmt.Errorf("fetching IV snapshots: %w", err)
	}

	data := &IVRData{
		Underlying:    underlying,
		CurrentIV:     currentIV,
		DaysOfHistory: len(snaps),
	}

	if len(snaps) == 0 {
		data.IVR = -1
		return data, nil
	}

	low, high := snaps[0].ATMIV, snaps[0].ATMIV
	for _, sn := range snaps[1:] {
		if sn.ATMIV < low {
			low = sn.ATMIV
		}
		if sn.ATMIV > high {
			high = sn.ATMIV
		}
	}
	data.Low52Wk = low
	data.High52Wk = high
	data.IVR = calcIVR(currentIV, low, high)
	return data, nil
}

// calcIVR computes (current - low) / (high - low) * 100.
// Returns 50 when high == low to avoid division by zero.
func calcIVR(current, low, high float64) float64 {
	if high == low {
		return 50.0
	}
	ivr := (current - low) / (high - low) * 100.0
	// Round to avoid floating-point precision issues
	ivr = math.Round(ivr*1e10) / 1e10
	if ivr < 0 {
		return 0.0
	}
	if ivr > 100 {
		return 100.0
	}
	return ivr
}
