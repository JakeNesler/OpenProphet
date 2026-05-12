package services

import (
	"context"
	"fmt"
	"math"
	"time"

	"prophet-trader/interfaces"
)

// RealizedVolService computes trailing annualized realized volatility from
// daily closes. Used by Harvest's premium-selling edge filter (block condor
// entries when implied <= realized — no premium-selling edge to capture).
//
// Pulled into its own service rather than tacked onto HarvestIVRService so
// the IVR service can remain context-free (the IV snapshot store is
// synchronous; this fetches via DataService which needs a context).

const tradingDaysPerYear = 252

// rvDataSource is the narrow subset of interfaces.DataService used by
// RealizedVolService — only historical daily bars are needed.
type rvDataSource interface {
	GetHistoricalBars(ctx context.Context, symbol string, start, end time.Time, timeframe string) ([]*interfaces.Bar, error)
}

type RealizedVolService struct {
	data rvDataSource
}

// NewRealizedVolService constructs the service over the given data source.
// In production the source is *AlpacaDataService.
func NewRealizedVolService(data rvDataSource) *RealizedVolService {
	return &RealizedVolService{data: data}
}

// GetAnnualizedRealizedVol fetches the trailing lookbackDays of daily closes
// for `symbol` and returns the annualized stddev of log returns.
//
// Returns 0 (no error) when the data source returns < 2 closes — there are
// not enough returns to compute stddev. Callers must treat 0 as "no signal"
// rather than "low volatility" (Harvest's preflight gate already does).
func (s *RealizedVolService) GetAnnualizedRealizedVol(ctx context.Context, symbol string, lookbackDays int) (float64, error) {
	if lookbackDays < 2 {
		lookbackDays = 2
	}
	// Calendar days = trading days × ~1.45 to leave buffer for weekends /
	// holidays; we trim to the trailing N closes after the fetch returns.
	end := time.Now().UTC()
	start := end.AddDate(0, 0, -int(math.Ceil(float64(lookbackDays)*2.0)))
	bars, err := s.data.GetHistoricalBars(ctx, symbol, start, end, "1Day")
	if err != nil {
		return 0, fmt.Errorf("realized vol fetch for %s: %w", symbol, err)
	}
	if len(bars) > lookbackDays+1 {
		bars = bars[len(bars)-(lookbackDays+1):] // keep N+1 closes → N returns
	}
	closes := make([]float64, len(bars))
	for i, b := range bars {
		closes[i] = b.Close
	}
	return calcAnnualizedRealizedVol(closes), nil
}

// calcAnnualizedRealizedVol returns annualized realized volatility (e.g.,
// 0.18 = 18%) from a slice of daily closes.
//
// Formula: stddev(log returns) × sqrt(252). With N+1 closes we get N log
// returns; need N >= 2 for a defined stddev. Returns 0 below that threshold,
// and 0 when all returns are identical (zero variance).
func calcAnnualizedRealizedVol(closes []float64) float64 {
	if len(closes) < 3 {
		return 0
	}
	returns := make([]float64, 0, len(closes)-1)
	for i := 1; i < len(closes); i++ {
		if closes[i-1] <= 0 || closes[i] <= 0 {
			continue
		}
		returns = append(returns, math.Log(closes[i]/closes[i-1]))
	}
	if len(returns) < 2 {
		return 0
	}
	var sum float64
	for _, r := range returns {
		sum += r
	}
	mean := sum / float64(len(returns))
	var sq float64
	for _, r := range returns {
		d := r - mean
		sq += d * d
	}
	// Sample stddev (n-1).
	variance := sq / float64(len(returns)-1)
	if variance <= 0 {
		return 0
	}
	stddev := math.Sqrt(variance)
	return stddev * math.Sqrt(float64(tradingDaysPerYear))
}
