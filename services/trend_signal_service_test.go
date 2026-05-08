package services

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"prophet-trader/interfaces"
)

// makeBars builds L bars with the given closes; H = close*1.01, L = close*0.99.
// Bars are spaced one day apart starting from a fixed reference time.
func makeBars(closes []float64) []*interfaces.Bar {
	start := time.Date(2025, 1, 2, 16, 0, 0, 0, time.UTC)
	bars := make([]*interfaces.Bar, len(closes))
	for i, c := range closes {
		bars[i] = &interfaces.Bar{
			Symbol:    "TEST",
			Timestamp: start.AddDate(0, 0, i),
			Open:      c,
			Close:     c,
			High:      c * 1.01,
			Low:       c * 0.99,
			Volume:    1000,
		}
	}
	return bars
}

func TestDonchianHigh_ExcludesLastBar(t *testing.T) {
	closes := make([]float64, 200)
	for i := range closes {
		closes[i] = 100.0
	}
	closes[len(closes)-1] = 999.0 // last bar = all-time high

	got := donchianHigh(closes, 100)
	if got >= closes[len(closes)-1] {
		t.Fatalf("donchianHigh included the last bar: got %v, last_close=%v", got, closes[len(closes)-1])
	}
	if got != 100.0 {
		t.Fatalf("donchianHigh = %v, want 100.0 (the constant prefix)", got)
	}
}

func TestDonchianLow_ExcludesLastBar(t *testing.T) {
	closes := make([]float64, 200)
	for i := range closes {
		closes[i] = 100.0
	}
	closes[len(closes)-1] = 1.0 // last bar = all-time low

	got := donchianLow(closes, 50)
	if got <= closes[len(closes)-1] {
		t.Fatalf("donchianLow included the last bar: got %v, last_close=%v", got, closes[len(closes)-1])
	}
	if got != 100.0 {
		t.Fatalf("donchianLow = %v, want 100.0", got)
	}
}

func TestDonchianHigh_WindowSize(t *testing.T) {
	// Place a known high inside the Donchian-100 window to confirm window
	// extent is exactly 100 bars ending one bar before the last.
	L := 250
	closes := make([]float64, L)
	for i := range closes {
		closes[i] = 100.0
	}
	// L=250, n=100 → window [L-n-1, L-2] = [149, 248]
	// Set closes[149] (first index in window) and closes[248] (last index in window).
	closes[149] = 200.0
	closes[248] = 150.0
	closes[L-1] = 9999.0 // last bar (excluded)

	got := donchianHigh(closes, 100)
	if got != 200.0 {
		t.Fatalf("donchianHigh = %v, want 200.0 (high inside window)", got)
	}

	// Ensure closes[148] (just before window) is NOT included.
	closes2 := make([]float64, L)
	for i := range closes2 {
		closes2[i] = 100.0
	}
	closes2[148] = 500.0 // just before window
	got2 := donchianHigh(closes2, 100)
	if got2 != 100.0 {
		t.Fatalf("donchianHigh = %v, want 100.0 (out-of-window value should be ignored)", got2)
	}
}

func TestSMA200_StableUpTrend(t *testing.T) {
	// closes[i] = 100 + i, L = 250
	// Window: closes[49..248] = 149, 150, ..., 348 (200 values)
	// Sum = (149+348)*200/2 = 49700
	// Mean = 49700/200 = 248.5
	closes := make([]float64, 250)
	for i := range closes {
		closes[i] = 100.0 + float64(i)
	}
	got := sma(closes, 200)
	want := 248.5
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("sma200 = %v, want %v", got, want)
	}
}

func TestWilderATR_NotEqualToSimpleATR(t *testing.T) {
	// Construct a series where Wilder ATR and a simple SMA-of-TR diverge
	// visibly. With H=L=C, TR[i] = |close[i] - close[i-1]|.
	//
	// closes:    0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 109, 110
	// TR by i:   _, 1, 1, 1, 1, 1, 1, 1, 1, 1, 100, 1
	//
	// Wilder ATR with n=10:
	//   seed   = mean(TR[1..10]) = (1*9 + 100) / 10 = 109/10 = 10.9
	//   ATR[11] = (10.9 * 9 + 1) / 10 = 99.1/10 = 9.91
	//
	// Simple SMA-of-TR over the most recent 10 (TR[2..11]):
	//   = (1*8 + 100 + 1) / 10 = 109/10 = 10.9
	//
	// The two formulations differ. Our function must match Wilder (9.91).
	closes := []float64{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 109, 110}
	highs := append([]float64{}, closes...)
	lows := append([]float64{}, closes...)

	got := wilderATR(highs, lows, closes, 10)
	wantWilder := 9.91
	if math.Abs(got-wantWilder) > 1e-9 {
		t.Fatalf("wilderATR = %v, want %v (Wilder smoothing)", got, wantWilder)
	}
	if math.Abs(got-10.9) < 1e-3 {
		t.Fatalf("wilderATR returned %v ~= 10.9, which is the simple SMA-of-TR result. Implementation must use Wilder recursion, not SMA.", got)
	}
}

func TestWilderATR_ConstantTRSeries(t *testing.T) {
	// When TR is constant, Wilder ATR equals that constant. Sanity check.
	// closes step by 2 each bar, H=L=C → TR = 2 always.
	L := 30
	closes := make([]float64, L)
	for i := range closes {
		closes[i] = 100.0 + 2.0*float64(i)
	}
	highs := append([]float64{}, closes...)
	lows := append([]float64{}, closes...)

	got := wilderATR(highs, lows, closes, 20)
	want := 2.0
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("wilderATR on constant-TR series = %v, want %v", got, want)
	}
}

// fakeBarFetcher implements BarFetcher for tests.
type fakeBarFetcher struct {
	bars []*interfaces.Bar
	err  error
}

func (f *fakeBarFetcher) GetHistoricalBars(ctx context.Context, symbol string, start, end time.Time, timeframe string) ([]*interfaces.Bar, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.bars, nil
}

func TestGetSignal_InsufficientHistory(t *testing.T) {
	closes := make([]float64, 100) // < minBarsRequired (250)
	for i := range closes {
		closes[i] = 100.0
	}
	svc := NewTrendSignalService(&fakeBarFetcher{bars: makeBars(closes)})

	_, err := svc.GetSignal(context.Background(), "TEST")
	if !errors.Is(err, ErrInsufficientHistory) {
		t.Fatalf("expected ErrInsufficientHistory, got %v", err)
	}
}

func TestGetSignal_FullCompute(t *testing.T) {
	// Build 260 bars with a clear uptrend; verify the full signal payload.
	L := 260
	closes := make([]float64, L)
	for i := range closes {
		closes[i] = 100.0 + float64(i)
	}
	bars := makeBars(closes)
	svc := NewTrendSignalService(&fakeBarFetcher{bars: bars})

	sig, err := svc.GetSignal(context.Background(), "TEST")
	if err != nil {
		t.Fatalf("GetSignal failed: %v", err)
	}
	if sig.Ticker != "TEST" {
		t.Errorf("Ticker = %v, want TEST", sig.Ticker)
	}
	if sig.BarsCount != L {
		t.Errorf("BarsCount = %v, want %v", sig.BarsCount, L)
	}
	if sig.LastClose != 100.0+float64(L-1) {
		t.Errorf("LastClose = %v, want %v", sig.LastClose, 100.0+float64(L-1))
	}
	// Donchian-100 high: window is closes[L-101..L-2] = [159..258], so values
	// 100+159=259 through 100+258=358. Max = 358.
	wantD100High := 100.0 + float64(L-2)
	if math.Abs(sig.Donchian100High-wantD100High) > 1e-9 {
		t.Errorf("Donchian100High = %v, want %v", sig.Donchian100High, wantD100High)
	}
	if sig.SignalVersion != "v1" {
		t.Errorf("SignalVersion = %v, want v1", sig.SignalVersion)
	}
}
