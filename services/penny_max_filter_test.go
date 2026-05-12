package services

import (
	"context"
	"prophet-trader/interfaces"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

// barsAsc returns synthetic daily bars ascending in time. closes[i] becomes
// the close of bar i; other OHLC fields are filled with the close.
func barsAsc(closes []float64) []*interfaces.Bar {
	out := make([]*interfaces.Bar, len(closes))
	base := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	for i, c := range closes {
		out[i] = &interfaces.Bar{
			Timestamp: base.AddDate(0, 0, i),
			Open:      c, High: c, Low: c, Close: c, Volume: 1_000_000,
		}
	}
	return out
}

func TestComputeMaxFromBars_Standard(t *testing.T) {
	// 22 closes, day-12 has a +25% jump (100 → 125), all other day-on-day
	// changes are +1%.
	closes := []float64{100}
	for i := 1; i < 22; i++ {
		next := closes[len(closes)-1] * 1.01
		if i == 12 {
			next = closes[len(closes)-1] * 1.25
		}
		closes = append(closes, next)
	}
	entry, ok := computeMaxFromBars(barsAsc(closes))
	if !ok {
		t.Fatal("expected ok=true")
	}
	if entry.BarsUsed != 21 {
		t.Errorf("BarsUsed = %d, want 21", entry.BarsUsed)
	}
	if entry.Value < 0.249 || entry.Value > 0.251 {
		t.Errorf("Value = %f, want ~0.25", entry.Value)
	}
	wantDay := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC).AddDate(0, 0, 12)
	if !entry.BestDay.Equal(wantDay) {
		t.Errorf("BestDay = %v, want %v", entry.BestDay, wantDay)
	}
}

func TestComputeMaxFromBars_ShortHistory(t *testing.T) {
	// Only 10 bars → 9 returns available, BarsUsed = 9.
	closes := []float64{100, 101, 102, 103, 104, 105, 110, 111, 112, 113}
	entry, ok := computeMaxFromBars(barsAsc(closes))
	if !ok {
		t.Fatal("expected ok=true")
	}
	if entry.BarsUsed != 9 {
		t.Errorf("BarsUsed = %d, want 9", entry.BarsUsed)
	}
	// Biggest day-on-day jump is 105 → 110 = +4.762%.
	if entry.Value < 0.047 || entry.Value > 0.048 {
		t.Errorf("Value = %f, want ~0.0476", entry.Value)
	}
}

func TestComputeMaxFromBars_InsufficientHistory(t *testing.T) {
	// 1 bar = 0 returns → ok=false.
	_, ok := computeMaxFromBars(barsAsc([]float64{100}))
	if ok {
		t.Error("expected ok=false for 1 bar")
	}
	_, ok = computeMaxFromBars(nil)
	if ok {
		t.Error("expected ok=false for nil bars")
	}
}

func TestComputeMaxFromBars_SingleRip(t *testing.T) {
	closes := make([]float64, 22)
	for i := range closes {
		closes[i] = 100
	}
	closes[15] = 140 // +40% pop, then flat
	closes[16] = 100 // faded back the next day
	for i := 17; i < 22; i++ {
		closes[i] = 100
	}
	entry, ok := computeMaxFromBars(barsAsc(closes))
	if !ok {
		t.Fatal("expected ok=true")
	}
	if entry.Value < 0.399 || entry.Value > 0.401 {
		t.Errorf("Value = %f, want ~0.40", entry.Value)
	}
	wantDay := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC).AddDate(0, 0, 15)
	if !entry.BestDay.Equal(wantDay) {
		t.Errorf("BestDay = %v, want %v", entry.BestDay, wantDay)
	}
}

func TestComputeMaxFromBars_NegativeOnly(t *testing.T) {
	// All returns negative; MAX is the least-negative.
	closes := []float64{100, 95, 92, 90, 89, 88}
	entry, ok := computeMaxFromBars(barsAsc(closes))
	if !ok {
		t.Fatal("expected ok=true")
	}
	// Smallest drop is 89 → 88 = -1.124%. MAX is -0.01124.
	if entry.Value > -0.011 || entry.Value < -0.012 {
		t.Errorf("Value = %f, want ~-0.0112", entry.Value)
	}
}

// fakeMultiBarsFetcher returns canned bar data per symbol.
type fakeMultiBarsFetcher struct {
	mu       sync.Mutex
	response map[string][]*interfaces.Bar
	calls    int
	err      error
}

func (f *fakeMultiBarsFetcher) GetMultiBars(ctx context.Context, symbols []string, start, end time.Time, timeframe string) (map[string][]*interfaces.Bar, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	out := make(map[string][]*interfaces.Bar)
	for _, s := range symbols {
		if bars, ok := f.response[s]; ok {
			out[s] = bars
		}
	}
	return out, nil
}

func universeForTest(tickers []string) *PennyUniverseService {
	u := &PennyUniverseService{logger: logrus.New()}
	u.universe = make([]UniverseSymbol, len(tickers))
	for i, t := range tickers {
		u.universe[i] = UniverseSymbol{Ticker: t, Price: 5.0}
	}
	return u
}

func TestRefresh_PopulatesCache(t *testing.T) {
	closes := []float64{100, 101, 102, 103, 104, 105, 130, 131, 132}
	fetcher := &fakeMultiBarsFetcher{
		response: map[string][]*interfaces.Bar{
			"AAAA": barsAsc(closes),
			"BBBB": barsAsc([]float64{50, 50.5, 51}),
		},
	}
	universe := universeForTest([]string{"AAAA", "BBBB", "CCCC"})
	svc := NewPennyMaxFilterService(universe, fetcher)

	frozen := time.Date(2026, 5, 12, 12, 0, 0, 0, time.UTC)
	svc.nowFunc = func() time.Time { return frozen }

	svc.refresh(context.Background())

	if fetcher.calls != 1 {
		t.Errorf("expected 1 fetcher call, got %d", fetcher.calls)
	}

	entryA, okA := svc.GetMax("AAAA")
	if !okA {
		t.Fatal("expected AAAA to be cached")
	}
	if entryA.Value < 0.237 || entryA.Value > 0.239 {
		t.Errorf("AAAA Value = %f, want ~0.238", entryA.Value)
	}
	if !entryA.ComputedAt.Equal(frozen) {
		t.Errorf("AAAA ComputedAt = %v, want %v", entryA.ComputedAt, frozen)
	}

	entryB, okB := svc.GetMax("BBBB")
	if !okB {
		t.Fatal("expected BBBB to be cached")
	}
	if entryB.BarsUsed != 2 {
		t.Errorf("BBBB BarsUsed = %d, want 2", entryB.BarsUsed)
	}

	_, okC := svc.GetMax("CCCC")
	if okC {
		t.Error("expected CCCC to be ok=false (no bars returned by fetcher)")
	}
}

func TestRefresh_FetcherError_PreservesPriorCache(t *testing.T) {
	fetcher := &fakeMultiBarsFetcher{
		response: map[string][]*interfaces.Bar{
			"AAAA": barsAsc([]float64{100, 110}),
		},
	}
	universe := universeForTest([]string{"AAAA"})
	svc := NewPennyMaxFilterService(universe, fetcher)
	svc.refresh(context.Background())

	if _, ok := svc.GetMax("AAAA"); !ok {
		t.Fatal("setup: expected AAAA cached after first refresh")
	}

	// Second refresh errors out — prior cache must remain readable.
	fetcher.response = nil
	fetcher.err = errFakeOutage
	svc.refresh(context.Background())

	if _, ok := svc.GetMax("AAAA"); !ok {
		t.Error("cache wiped on fetcher error; expected stale entry preserved")
	}
}

var errFakeOutage = &fakeOutageErr{}

type fakeOutageErr struct{}

func (e *fakeOutageErr) Error() string { return "fake outage" }
