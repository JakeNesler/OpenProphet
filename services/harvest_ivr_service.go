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
	IVPercentile  float64 // -1 means insufficient history; 0..100 otherwise
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
	// tomorrow is the exclusive upper bound: GetHarvestIVSnapshots uses date < end,
	// so this captures the full calendar day without risking a 23h59m gap.
	tomorrow := today.Add(24 * time.Hour)
	existing, err := s.store.GetHarvestIVSnapshots(underlying, today, tomorrow)
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
	snaps, err := s.fetchTrailingYearSnaps(underlying)
	if err != nil {
		return nil, err
	}
	return computeIVRData(underlying, currentIV, snaps), nil
}

// GetIVRDataLatest is the no-args read path used by the LLM-side MCP tool.
// It reuses the most recent stored ATM IV snapshot as "current" so callers
// (Prophet's get_iv_rank) don't have to fetch the live chain first. Returns
// IVR=-1, IVPercentile=-1 when there is no history for the symbol yet.
//
// Data freshness is bounded by the daily collection cadence in
// startHarvestIVCollection (6h). For IV-rank purposes that is acceptable —
// IV rank is a slow-moving metric.
func (s *HarvestIVRService) GetIVRDataLatest(underlying string) (*IVRData, error) {
	snaps, err := s.fetchTrailingYearSnaps(underlying)
	if err != nil {
		return nil, err
	}
	if len(snaps) == 0 {
		return computeIVRData(underlying, 0, snaps), nil
	}
	latest := snaps[0]
	for _, sn := range snaps[1:] {
		if sn.Date.After(latest.Date) {
			latest = sn
		}
	}
	return computeIVRData(underlying, latest.ATMIV, snaps), nil
}

func (s *HarvestIVRService) fetchTrailingYearSnaps(underlying string) ([]*models.DBHarvestIVSnapshot, error) {
	end := time.Now().UTC()
	start := end.AddDate(-1, 0, 0) // up to 52 weeks back
	snaps, err := s.store.GetHarvestIVSnapshots(underlying, start, end)
	if err != nil {
		return nil, fmt.Errorf("fetching IV snapshots: %w", err)
	}
	return snaps, nil
}

// computeIVRData builds IVRData from a current IV value and a snapshot slice.
// Pure function; no I/O.
func computeIVRData(underlying string, currentIV float64, snaps []*models.DBHarvestIVSnapshot) *IVRData {
	data := &IVRData{
		Underlying:    underlying,
		CurrentIV:     currentIV,
		DaysOfHistory: len(snaps),
	}
	if len(snaps) == 0 {
		data.IVR = -1
		data.IVPercentile = -1
		return data
	}
	history := make([]float64, len(snaps))
	low, high := snaps[0].ATMIV, snaps[0].ATMIV
	for i, sn := range snaps {
		history[i] = sn.ATMIV
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
	data.IVPercentile = calcIVPercentile(currentIV, history)
	return data
}

// calcIVPercentile returns the percentage of values in `history` that are
// less than or equal to `current`. Returns -1 for empty history; 50 when all
// values are equal (neutral). Range: [0, 100].
func calcIVPercentile(current float64, history []float64) float64 {
	if len(history) == 0 {
		return -1
	}
	allSame := true
	for _, h := range history[1:] {
		if h != history[0] {
			allSame = false
			break
		}
	}
	if allSame {
		return 50.0
	}
	count := 0
	for _, h := range history {
		if h <= current {
			count++
		}
	}
	return float64(count) / float64(len(history)) * 100.0
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
