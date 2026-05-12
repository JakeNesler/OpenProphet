package services

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"prophet-trader/interfaces"
)

// ── calcAnnualizedRealizedVol ──────────────────────────────────────

func TestCalcAnnualizedRealizedVol_Empty(t *testing.T) {
	if got := calcAnnualizedRealizedVol(nil); got != 0 {
		t.Errorf("expected 0 for nil, got %f", got)
	}
	if got := calcAnnualizedRealizedVol([]float64{}); got != 0 {
		t.Errorf("expected 0 for empty, got %f", got)
	}
}

func TestCalcAnnualizedRealizedVol_SingleClose(t *testing.T) {
	if got := calcAnnualizedRealizedVol([]float64{100}); got != 0 {
		t.Errorf("expected 0 for single close, got %f", got)
	}
}

func TestCalcAnnualizedRealizedVol_TwoCloses(t *testing.T) {
	// Two closes → one log return → stddev of a single sample is 0.
	if got := calcAnnualizedRealizedVol([]float64{100, 101}); got != 0 {
		t.Errorf("expected 0 with only one return sample, got %f", got)
	}
}

func TestCalcAnnualizedRealizedVol_AllEqualClosesZeroVol(t *testing.T) {
	closes := []float64{100, 100, 100, 100, 100}
	if got := calcAnnualizedRealizedVol(closes); got != 0 {
		t.Errorf("expected 0 for constant closes, got %f", got)
	}
}

func TestCalcAnnualizedRealizedVol_AlternatingOnePercent(t *testing.T) {
	// Closes that produce alternating +1% / -1% log returns. Log returns:
	// ln(101/100)=0.00995, ln(100/101)=-0.00995, ... All abs values equal.
	// stddev across N samples = 0.00995. Annualized = 0.00995 * sqrt(252)
	// ≈ 0.1579 (~15.79%).
	closes := []float64{100, 101, 100, 101, 100, 101, 100, 101, 100, 101, 100}
	got := calcAnnualizedRealizedVol(closes)
	want := 0.00995 * math.Sqrt(252)
	if math.Abs(got-want) > 0.01 {
		t.Errorf("expected ≈ %.4f, got %.4f", want, got)
	}
}

func TestCalcAnnualizedRealizedVol_KnownFixture(t *testing.T) {
	// Five closes producing log returns of approximately
	// [0.04879, -0.04652, 0.04652, -0.04879] — stddev ≈ 0.0541.
	// Annualized ≈ 0.0541 * sqrt(252) ≈ 0.8585.
	// (Just verifying the formula doesn't go off the rails on larger moves.)
	closes := []float64{100, 105, 100, 105, 100}
	got := calcAnnualizedRealizedVol(closes)
	if got <= 0 {
		t.Errorf("expected positive vol for known oscillation, got %f", got)
	}
	if got < 0.5 || got > 1.2 {
		t.Errorf("vol way outside expected range: got %f", got)
	}
}

// ── RealizedVolService.GetAnnualizedRealizedVol ────────────────────

type stubRVDataService struct {
	bars   map[string][]*interfaces.Bar
	failOn map[string]bool
}

func (s *stubRVDataService) GetHistoricalBars(ctx context.Context, symbol string, start, end time.Time, timeframe string) ([]*interfaces.Bar, error) {
	if s.failOn[symbol] {
		return nil, fmt.Errorf("simulated failure for %s", symbol)
	}
	return s.bars[symbol], nil
}

func barsFromCloses(closes []float64) []*interfaces.Bar {
	bars := make([]*interfaces.Bar, len(closes))
	for i, c := range closes {
		bars[i] = &interfaces.Bar{Close: c}
	}
	return bars
}

func TestRealizedVolService_HappyPath(t *testing.T) {
	closes := []float64{100, 101, 100, 101, 100, 101, 100, 101, 100, 101, 100}
	ds := &stubRVDataService{bars: map[string][]*interfaces.Bar{"SPY": barsFromCloses(closes)}}
	svc := NewRealizedVolService(ds)

	rv, err := svc.GetAnnualizedRealizedVol(context.Background(), "SPY", 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rv <= 0 {
		t.Errorf("expected positive annualized vol, got %f", rv)
	}
}

func TestRealizedVolService_FetchErrorPropagates(t *testing.T) {
	ds := &stubRVDataService{failOn: map[string]bool{"BAD": true}}
	svc := NewRealizedVolService(ds)

	_, err := svc.GetAnnualizedRealizedVol(context.Background(), "BAD", 20)
	if err == nil {
		t.Fatal("expected error to propagate, got nil")
	}
}

func TestRealizedVolService_InsufficientBarsReturnsZero(t *testing.T) {
	// Only one bar means zero usable log returns.
	ds := &stubRVDataService{bars: map[string][]*interfaces.Bar{"THIN": barsFromCloses([]float64{100})}}
	svc := NewRealizedVolService(ds)

	rv, err := svc.GetAnnualizedRealizedVol(context.Background(), "THIN", 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rv != 0 {
		t.Errorf("expected 0 on insufficient bars, got %f", rv)
	}
}
