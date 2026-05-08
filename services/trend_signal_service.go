package services

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"prophet-trader/interfaces"
)

// ErrInsufficientHistory is returned when a ticker has fewer bars than the
// minimum required to compute the trend signal. The agent's rules treat this
// as a skip condition.
var ErrInsufficientHistory = errors.New("insufficient bar history")

// BarFetcher is the narrow interface this service depends on. AlpacaDataService
// implements it; tests can substitute a fake.
type BarFetcher interface {
	GetHistoricalBars(ctx context.Context, symbol string, start, end time.Time, timeframe string) ([]*interfaces.Bar, error)
}

// TrendSignalService computes daily-bar trend signals (Donchian, SMA, Wilder
// ATR) for the TrendProphet agent. See docs/get-trend-signal-spec.md for the
// full contract.
type TrendSignalService struct {
	dataSvc BarFetcher
}

// TrendSignal is the JSON shape returned by GET /api/v1/trend/signal/:symbol.
type TrendSignal struct {
	Ticker          string  `json:"ticker"`
	AsOf            string  `json:"as_of"`
	BarsCount       int     `json:"bars_count"`
	LastClose       float64 `json:"last_close"`
	Donchian100High float64 `json:"donchian_100_high"`
	Donchian50Low   float64 `json:"donchian_50_low"`
	SMA200          float64 `json:"sma_200"`
	ATR20           float64 `json:"atr_20"`
	SignalVersion   string  `json:"signal_version"`
}

const (
	minBarsRequired    = 250
	barLookbackDays    = 400 // calendar days; covers ~260 trading days with margin
	donchianHighWindow = 100
	donchianLowWindow  = 50
	smaWindow          = 200
	atrWindow          = 20
	signalVersion      = "v1"
)

// NewTrendSignalService creates a TrendSignalService backed by the given
// bar-fetching data source.
func NewTrendSignalService(dataSvc BarFetcher) *TrendSignalService {
	return &TrendSignalService{dataSvc: dataSvc}
}

// GetSignal fetches daily bars for symbol and computes the full signal set.
// Returns ErrInsufficientHistory if fewer than minBarsRequired bars are
// available; the agent's rules treat this as a skip condition.
func (s *TrendSignalService) GetSignal(ctx context.Context, symbol string) (*TrendSignal, error) {
	end := time.Now()
	start := end.AddDate(0, 0, -barLookbackDays)
	bars, err := s.dataSvc.GetHistoricalBars(ctx, symbol, start, end, "1Day")
	if err != nil {
		return nil, fmt.Errorf("fetch bars for %s: %w", symbol, err)
	}
	if len(bars) < minBarsRequired {
		return nil, ErrInsufficientHistory
	}
	return ComputeSignal(symbol, bars), nil
}

// ComputeSignal is the pure-function form of signal computation. Exposed so
// tests can drive it with synthetic bars without going through Alpaca.
func ComputeSignal(symbol string, bars []*interfaces.Bar) *TrendSignal {
	L := len(bars)
	closes := make([]float64, L)
	highs := make([]float64, L)
	lows := make([]float64, L)
	for i, b := range bars {
		closes[i], highs[i], lows[i] = b.Close, b.High, b.Low
	}

	return &TrendSignal{
		Ticker:          symbol,
		AsOf:            bars[L-1].Timestamp.Format(time.RFC3339),
		BarsCount:       L,
		LastClose:       closes[L-1],
		Donchian100High: donchianHigh(closes, donchianHighWindow),
		Donchian50Low:   donchianLow(closes, donchianLowWindow),
		SMA200:          sma(closes, smaWindow),
		ATR20:           wilderATR(highs, lows, closes, atrWindow),
		SignalVersion:   signalVersion,
	}
}

// donchianHigh returns max(closes[L-n-1 .. L-2]) — n bars ending one bar
// before the most recent close. The last bar is excluded by design: the
// breakout signal compares the most recent close *against* this value, so
// the value must not include the bar being tested.
func donchianHigh(closes []float64, n int) float64 {
	L := len(closes)
	if L < n+2 {
		return 0
	}
	max := closes[L-n-1]
	for i := L - n; i <= L-2; i++ {
		if closes[i] > max {
			max = closes[i]
		}
	}
	return max
}

// donchianLow returns min(closes[L-n-1 .. L-2]). Same exclude-last-bar rule.
func donchianLow(closes []float64, n int) float64 {
	L := len(closes)
	if L < n+2 {
		return 0
	}
	min := closes[L-n-1]
	for i := L - n; i <= L-2; i++ {
		if closes[i] < min {
			min = closes[i]
		}
	}
	return min
}

// sma returns the simple mean of closes[L-n-1 .. L-2]. Same exclude-last-bar
// rule as the Donchian channels.
func sma(closes []float64, n int) float64 {
	L := len(closes)
	if L < n+2 {
		return 0
	}
	sum := 0.0
	for i := L - n - 1; i <= L-2; i++ {
		sum += closes[i]
	}
	return sum / float64(n)
}

// wilderATR computes the Wilder-smoothed Average True Range over the last n
// bars, returning ATR through the most recent bar (closes[L-1]).
//
// Wilder smoothing is NOT a simple moving average of TR. The seed value is
// the simple mean of TR[1..n]; each subsequent ATR uses the recursive form
// ATR[i] = (ATR[i-1]*(n-1) + TR[i]) / n. The two formulations give visibly
// different results on volatile series.
//
// True range: TR[i] = max(high[i]-low[i], |high[i]-close[i-1]|, |low[i]-close[i-1]|)
// TR[0] is undefined (no close[-1]); the seed window starts at i=1.
func wilderATR(highs, lows, closes []float64, n int) float64 {
	L := len(closes)
	if L < n+1 {
		return 0
	}
	tr := make([]float64, L)
	for i := 1; i < L; i++ {
		hl := highs[i] - lows[i]
		hc := math.Abs(highs[i] - closes[i-1])
		lc := math.Abs(lows[i] - closes[i-1])
		tr[i] = math.Max(hl, math.Max(hc, lc))
	}
	// Seed: simple mean of TR[1..n]
	sum := 0.0
	for i := 1; i <= n; i++ {
		sum += tr[i]
	}
	atr := sum / float64(n)
	// Wilder recursion: i = n+1 .. L-1
	for i := n + 1; i < L; i++ {
		atr = (atr*float64(n-1) + tr[i]) / float64(n)
	}
	return atr
}
