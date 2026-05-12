# PennyProphet MAX Filter (Shadow Mode) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a shadow-mode 21-session MAX filter for PennyProphet that logs but does not yet enforce a "lottery already paid out" filter on penny stock candidates, so that the enforce decision can be made from four weeks of real data.

**Architecture:** A new `PennyMaxFilterService` maintains a per-ticker daily-refreshed cache of MAX values (max close-to-close return over the prior 21 sessions). The aggregator's existing `GetCandidates` path calls `GetMax` for each candidate and emits a structured log line; an env var (`PENNY_MAX_FILTER_MODE`) toggles between shadow (log-only, default) and enforce (skip candidates with MAX >= 20%). Pattern mirrors the existing dilution filter in `services/penny_signal_aggregator.go`.

**Tech Stack:** Go 1.21+, `github.com/alpacahq/alpaca-trade-api-go/v3/marketdata`, `github.com/sirupsen/logrus`, standard library testing (no testify in this codebase).

**Spec:** `docs/superpowers/specs/2026-05-12-pennyprophet-max-filter-design.md`

---

## File Map

| File | Action | Responsibility |
|---|---|---|
| `services/alpaca_data.go` | Modify | Add `GetMultiBars` wrapper method on `AlpacaDataService` |
| `services/penny_max_filter.go` | Create | New `PennyMaxFilterService` — cache, refresh, public API |
| `services/penny_max_filter_test.go` | Create | Unit tests for MAX math, refresh, and public API using a fake fetcher |
| `services/penny_signal_aggregator.go` | Modify | Add `maxFilter` field, extend constructor, add hook in `GetCandidates`, update lock-ordering comment |
| `services/penny_signal_aggregator_test.go` | Modify | Update `aggregatorForTest` signature; add shadow/enforce/nil behavior tests |
| `cmd/bot/main.go` | Modify | Construct, wire, and start `PennyMaxFilterService` |
| `TRADING_RULES_PENNY.md` | Modify | Add "MAX Filter (Shadow)" section |
| `data/agent-config.json` | Modify | Mirror rule text into `strategies[].customRules` for `penny-momentum` |

---

### Task 1: Add `GetMultiBars` wrapper to AlpacaDataService

**Files:**
- Modify: `services/alpaca_data.go`

This is a thin pass-through to the SDK's existing `client.GetMultiBars`. We skip a unit test on the wrapper itself because there is no behavior to test beyond argument forwarding to a third-party SDK; the MAX filter's tests exercise the contract via a narrow fake interface. The wrapper exists so `PennyMaxFilterService` can depend on `*AlpacaDataService` like `TrendSignalService` does, rather than holding its own raw `marketdata.Client`.

- [ ] **Step 1: Add the new method below `GetHistoricalBars`**

Open `services/alpaca_data.go` and add this method directly after the existing `GetHistoricalBars` function (after line 80):

```go
// GetMultiBars retrieves historical bar data for multiple symbols in a single request.
// Returns a map keyed by symbol. Missing or errored symbols are simply absent from the map.
func (s *AlpacaDataService) GetMultiBars(ctx context.Context, symbols []string, start, end time.Time, timeframe string) (map[string][]*interfaces.Bar, error) {
	s.logger.WithFields(logrus.Fields{
		"symbols_count": len(symbols),
		"start":         start,
		"end":           end,
		"timeframe":     timeframe,
	}).Info("Fetching multi-symbol historical bars")

	tf := s.parseTimeframe(timeframe)

	req := marketdata.GetBarsRequest{
		TimeFrame:  tf,
		Start:      start,
		End:        end,
		Feed:       marketdata.IEX,
		PageLimit:  10000,
		Adjustment: marketdata.All,
	}

	resp, err := s.client.GetMultiBars(symbols, req)
	if err != nil {
		s.logger.WithError(err).Error("Failed to fetch multi-symbol historical bars")
		return nil, fmt.Errorf("failed to get multi bars: %w", err)
	}

	out := make(map[string][]*interfaces.Bar, len(resp))
	for symbol, bars := range resp {
		converted := make([]*interfaces.Bar, 0, len(bars))
		for _, bar := range bars {
			converted = append(converted, &interfaces.Bar{
				Symbol:    symbol,
				Timestamp: bar.Timestamp,
				Open:      bar.Open,
				High:      bar.High,
				Low:       bar.Low,
				Close:     bar.Close,
				Volume:    int64(bar.Volume),
				VWAP:      bar.VWAP,
			})
		}
		out[symbol] = converted
	}

	s.logger.WithField("symbols_returned", len(out)).Info("Fetched multi-symbol historical bars")
	return out, nil
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./services/...`
Expected: clean build, no errors.

- [ ] **Step 3: Commit**

```bash
git add services/alpaca_data.go
git commit -m "feat(alpaca): add GetMultiBars wrapper for multi-symbol bar fetches"
```

---

### Task 2: Create `PennyMaxFilterService` skeleton with types and constructor

**Files:**
- Create: `services/penny_max_filter.go`

Lay down the types, the narrow fetcher interface (for testability), and the constructor. No behavior yet — subsequent tasks add `refresh`, `GetMax`, and the loop.

- [ ] **Step 1: Create the file with skeleton**

Create `services/penny_max_filter.go`:

```go
package services

import (
	"context"
	"prophet-trader/interfaces"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// MaxEntry is the cached MAX value for one ticker.
// Exported because PennySignalAggregator (same package, but the
// aggregator's hook returns it through a value-by-value copy that is
// cleaner to read with exported field names).
type MaxEntry struct {
	Value      float64   // e.g. 0.32 for a +32% best day
	BestDay    time.Time // session that produced the max
	BarsUsed   int       // number of daily returns used (typically 21)
	ComputedAt time.Time
}

// MultiBarsFetcher is the narrow interface PennyMaxFilterService depends on,
// allowing tests to substitute a fake. Satisfied by *AlpacaDataService.
type MultiBarsFetcher interface {
	GetMultiBars(ctx context.Context, symbols []string, start, end time.Time, timeframe string) (map[string][]*interfaces.Bar, error)
}

// PennyMaxFilterService maintains a daily-refreshed cache of 21-session MAX
// values for every ticker in the penny universe.
//
// Lock ordering: PennyMaxFilterService.mu is a leaf lock. It is never
// acquired while another services-package lock is held.
type PennyMaxFilterService struct {
	universe *PennyUniverseService
	bars     MultiBarsFetcher
	mu       sync.RWMutex
	cache    map[string]MaxEntry
	nowFunc  func() time.Time
	logger   *logrus.Logger
}

// NewPennyMaxFilterService constructs the service.
func NewPennyMaxFilterService(universe *PennyUniverseService, bars MultiBarsFetcher) *PennyMaxFilterService {
	logger := logrus.New()
	logger.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})
	return &PennyMaxFilterService{
		universe: universe,
		bars:     bars,
		cache:    make(map[string]MaxEntry),
		nowFunc:  time.Now,
		logger:   logger,
	}
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./services/...`
Expected: clean build.

- [ ] **Step 3: Commit**

```bash
git add services/penny_max_filter.go
git commit -m "feat(penny): scaffold PennyMaxFilterService with types and constructor"
```

---

### Task 3: TDD MAX computation as a pure function

**Files:**
- Modify: `services/penny_max_filter.go`
- Create: `services/penny_max_filter_test.go`

Extract MAX computation as a package-private pure function `computeMaxFromBars`. Test it with table-driven cases covering normal, short-history, single-rip, and negative-only inputs. This is the math kernel — the part most likely to have an off-by-one or wrong-window error, so the unit tests focus here.

- [ ] **Step 1: Write the failing test file**

Create `services/penny_max_filter_test.go`:

```go
package services

import (
	"prophet-trader/interfaces"
	"testing"
	"time"
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./services/ -run TestComputeMaxFromBars -v`
Expected: tests fail to compile — `undefined: computeMaxFromBars`.

- [ ] **Step 3: Implement `computeMaxFromBars`**

Append to `services/penny_max_filter.go`:

```go
// computeMaxFromBars returns the MAX entry computed from ascending-time
// daily bars. ok=false when fewer than 2 bars are available (zero returns).
// Uses the most recent 22 bars to produce up to 21 close-to-close returns.
func computeMaxFromBars(bars []*interfaces.Bar) (MaxEntry, bool) {
	if len(bars) < 2 {
		return MaxEntry{}, false
	}
	// Trim to the most recent 22 bars (yields 21 returns).
	start := 0
	if len(bars) > 22 {
		start = len(bars) - 22
	}
	window := bars[start:]

	var maxVal float64
	var maxDay time.Time
	first := true
	for i := 1; i < len(window); i++ {
		prev := window[i-1].Close
		curr := window[i].Close
		if prev <= 0 {
			continue
		}
		r := (curr / prev) - 1
		if first || r > maxVal {
			maxVal = r
			maxDay = window[i].Timestamp
			first = false
		}
	}
	if first {
		// No usable returns (e.g., all prev closes were zero).
		return MaxEntry{}, false
	}
	return MaxEntry{
		Value:    maxVal,
		BestDay:  maxDay,
		BarsUsed: len(window) - 1,
	}, true
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./services/ -run TestComputeMaxFromBars -v`
Expected: all five tests PASS.

- [ ] **Step 5: Commit**

```bash
git add services/penny_max_filter.go services/penny_max_filter_test.go
git commit -m "feat(penny): add MAX computation kernel with unit tests"
```

---

### Task 4: TDD `refresh()` with a fake fetcher

**Files:**
- Modify: `services/penny_max_filter.go`
- Modify: `services/penny_max_filter_test.go`

Implement universe-wide refresh that fetches multi-bars and populates the cache. Use a fake `MultiBarsFetcher` so the test runs without an Alpaca client.

- [ ] **Step 1: Write the failing test**

The existing imports in `services/penny_max_filter_test.go` (from Task 3) need to grow. Replace the import block at the top of the file with:

```go
import (
	"context"
	"prophet-trader/interfaces"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)
```

Then append the test infrastructure and tests to the same file:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./services/ -run TestRefresh -v`
Expected: compile failure on `svc.refresh` (method not defined) and `svc.GetMax` (method not defined).

- [ ] **Step 3: Implement `refresh` and `GetMax`**

Append to `services/penny_max_filter.go`:

```go
// refresh fetches 30 calendar days of daily bars for the entire universe
// and recomputes MAX values. On fetcher error, the existing cache is
// preserved (stale but readable).
func (s *PennyMaxFilterService) refresh(ctx context.Context) {
	tickers := s.universe.GetTickers()
	if len(tickers) == 0 {
		s.logger.Info("PennyMaxFilterService: empty universe, skipping refresh")
		return
	}

	now := s.nowFunc()
	start := now.AddDate(0, 0, -30)

	// Chunk to stay within Alpaca's per-request symbol cap.
	const chunkSize = 100
	combined := make(map[string][]*interfaces.Bar)
	for i := 0; i < len(tickers); i += chunkSize {
		end := i + chunkSize
		if end > len(tickers) {
			end = len(tickers)
		}
		resp, err := s.bars.GetMultiBars(ctx, tickers[i:end], start, now, "1Day")
		if err != nil {
			s.logger.WithError(err).Warn("PennyMaxFilterService: GetMultiBars failed; preserving prior cache")
			return
		}
		for k, v := range resp {
			combined[k] = v
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for ticker, bars := range combined {
		entry, ok := computeMaxFromBars(bars)
		if !ok {
			continue
		}
		entry.ComputedAt = now
		s.cache[ticker] = entry
	}
	s.logger.WithField("entries", len(s.cache)).Info("PennyMaxFilterService: refresh complete")
}

// GetMax returns the cached MAX entry for ticker.
// ok=false when: the ticker has no cache entry (universe miss, first
// refresh not yet succeeded, or fewer than 2 daily bars available).
// ok=true with BarsUsed < 21 indicates a low-confidence value (newly
// listed ticker); the caller should still log it and downstream
// analysis can filter by BarsUsed.
func (s *PennyMaxFilterService) GetMax(ticker string) (MaxEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.cache[ticker]
	return e, ok
}
```

You may also need to verify `PennyUniverseService.GetTickers()` exists. Check with `grep -n "func.*GetTickers" services/penny_universe_service.go`. If it does not exist (only `GetUniverse` returning `[]UniverseSymbol` does), inline the ticker extraction:

```go
universe := s.universe.GetUniverse()
tickers := make([]string, len(universe))
for i, u := range universe {
	tickers[i] = u.Ticker
}
```

Use whichever form actually compiles. (`PennyScreenerService.scan` at `services/penny_screener_service.go:94` already calls `s.universe.GetTickers()`, so the method does exist — use it.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./services/ -run TestRefresh -v`
Expected: both tests PASS.

- [ ] **Step 5: Commit**

```bash
git add services/penny_max_filter.go services/penny_max_filter_test.go
git commit -m "feat(penny): implement refresh and GetMax with fake-fetcher tests"
```

---

### Task 5: TDD `nextRefreshTime` scheduling helper

**Files:**
- Modify: `services/penny_max_filter.go`
- Modify: `services/penny_max_filter_test.go`

The daily refresh fires at 07:00 ET. Extract the "compute next fire time" math as a pure function so we can test it deterministically.

- [ ] **Step 1: Write the failing test**

Append to `services/penny_max_filter_test.go`:

```go
func TestNextRefreshTime(t *testing.T) {
	et, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load ET: %v", err)
	}

	cases := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{
			name: "before 07:00 ET fires same day",
			now:  time.Date(2026, 5, 12, 6, 30, 0, 0, et),
			want: time.Date(2026, 5, 12, 7, 0, 0, 0, et),
		},
		{
			name: "exactly 07:00 ET fires next day",
			now:  time.Date(2026, 5, 12, 7, 0, 0, 0, et),
			want: time.Date(2026, 5, 13, 7, 0, 0, 0, et),
		},
		{
			name: "after 07:00 ET fires next day",
			now:  time.Date(2026, 5, 12, 14, 0, 0, 0, et),
			want: time.Date(2026, 5, 13, 7, 0, 0, 0, et),
		},
		{
			name: "UTC input is normalized to ET",
			now:  time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC), // 06:00 ET (DST)
			want: time.Date(2026, 5, 12, 7, 0, 0, 0, et),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := nextRefreshTime(tc.now)
			if !got.Equal(tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./services/ -run TestNextRefreshTime -v`
Expected: compile failure — `undefined: nextRefreshTime`.

- [ ] **Step 3: Implement `nextRefreshTime`**

Append to `services/penny_max_filter.go`:

```go
// nextRefreshTime returns the next 07:00 America/New_York instant strictly
// after now. Falls back to UTC if the tz database is unavailable.
func nextRefreshTime(now time.Time) time.Time {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		loc = time.UTC
	}
	et := now.In(loc)
	candidate := time.Date(et.Year(), et.Month(), et.Day(), 7, 0, 0, 0, loc)
	if !candidate.After(et) {
		candidate = candidate.AddDate(0, 0, 1)
	}
	return candidate
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./services/ -run TestNextRefreshTime -v`
Expected: all four sub-tests PASS.

- [ ] **Step 5: Commit**

```bash
git add services/penny_max_filter.go services/penny_max_filter_test.go
git commit -m "feat(penny): add nextRefreshTime scheduling helper"
```

---

### Task 6: Implement `Start(ctx)` daily refresh loop

**Files:**
- Modify: `services/penny_max_filter.go`

Wire the pieces together. `Start(ctx)` does an immediate refresh, then sleeps until next 07:00 ET, refreshes, repeats. This is a thin loop on top of already-tested pieces; a separate behavior test for the loop itself would mostly test `time.After` and adds noise without catching real bugs.

- [ ] **Step 1: Append `Start` to the service**

Append to `services/penny_max_filter.go`:

```go
// Start runs the daily refresh loop until ctx is cancelled.
// Fires an immediate refresh on entry so the cache is populated as
// quickly as possible after agent startup, then schedules at 07:00 ET
// daily.
func (s *PennyMaxFilterService) Start(ctx context.Context) {
	s.refresh(ctx)
	for {
		next := nextRefreshTime(s.nowFunc())
		sleepDuration := next.Sub(s.nowFunc())
		select {
		case <-ctx.Done():
			return
		case <-time.After(sleepDuration):
			s.refresh(ctx)
		}
	}
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./services/...`
Expected: clean build.

- [ ] **Step 3: Run full service test suite**

Run: `go test ./services/ -run "TestComputeMaxFromBars|TestRefresh|TestNextRefreshTime" -v -race`
Expected: all tests PASS, no race warnings.

- [ ] **Step 4: Commit**

```bash
git add services/penny_max_filter.go
git commit -m "feat(penny): add Start loop for daily MAX refresh"
```

---

### Task 7: TDD aggregator integration (shadow / enforce / nil-safe)

**Files:**
- Modify: `services/penny_signal_aggregator.go`
- Modify: `services/penny_signal_aggregator_test.go`

This is where the filter actually gets wired into `GetCandidates`. Three test cases drive the behavior: shadow never suppresses, enforce suppresses above threshold, nil filter is a no-op.

- [ ] **Step 1: Update `aggregatorForTest` helper to take an optional MAX filter**

In `services/penny_signal_aggregator_test.go`, replace the existing `aggregatorForTest` function (lines 11-50) and add a builder for a pre-seeded MAX filter:

```go
// aggregatorForTest builds a PennySignalAggregator with pre-seeded sub-service state.
// maxFilter may be nil to test the nil-safe path.
func aggregatorForTest(techScore, regScore, socScore float64, tickers []string, maxFilter *PennyMaxFilterService) *PennySignalAggregator {
	universe := &PennyUniverseService{logger: logrus.New()}
	universe.universe = make([]UniverseSymbol, len(tickers))
	for i, t := range tickers {
		universe.universe[i] = UniverseSymbol{Ticker: t, Price: 5.0}
	}

	screener := &PennyScreenerService{
		scores: make(map[string]TechnicalEntry),
		logger: logrus.New(),
	}
	for _, t := range tickers {
		screener.scores[t] = TechnicalEntry{Entry: DecayEntry{BaseScore: techScore, EventTime: time.Now(), HalfLifeHrs: 2.0}}
	}

	edgar := &SECEdgarService{
		entries: make(map[string]regulatoryEntry),
		logger:  logrus.New(),
	}
	for _, t := range tickers {
		edgar.entries[t] = regulatoryEntry{
			Entry:     DecayEntry{BaseScore: regScore, EventTime: time.Now(), HalfLifeHrs: regulatoryHalfLifeHours},
			EventDesc: "test event",
		}
	}

	social := &SocialSignalService{
		entries: make(map[string]socialEntry),
		logger:  logrus.New(),
	}
	for _, t := range tickers {
		social.entries[t] = socialEntry{
			Entry:      DecayEntry{BaseScore: socScore, EventTime: time.Now(), HalfLifeHrs: socialHalfLifeHours},
			MentionPts: socScore,
			Context:    "test ctx",
		}
	}

	return NewPennySignalAggregator(universe, screener, edgar, social, maxFilter)
}

// maxFilterWithEntry returns a service whose cache contains one seeded entry.
func maxFilterWithEntry(ticker string, value float64) *PennyMaxFilterService {
	svc := &PennyMaxFilterService{
		cache:   make(map[string]MaxEntry),
		nowFunc: time.Now,
		logger:  logrus.New(),
	}
	svc.cache[ticker] = MaxEntry{
		Value:      value,
		BestDay:    time.Now().AddDate(0, 0, -3),
		BarsUsed:   21,
		ComputedAt: time.Now(),
	}
	return svc
}
```

All existing call sites in this test file must be updated to pass a fifth argument. Use the IDE's find-and-replace or run a search:

```bash
grep -n "aggregatorForTest(" services/penny_signal_aggregator_test.go
```

For every match, append `, nil` before the closing paren. There are existing tests like `aggregatorForTest(30.0, 20.0, 10.0, []string{"TICK"})` — these become `aggregatorForTest(30.0, 20.0, 10.0, []string{"TICK"}, nil)`.

- [ ] **Step 2: Add the three new behavior tests**

Append to `services/penny_signal_aggregator_test.go`:

`services/penny_signal_aggregator_test.go` does not currently import `os`. Add it to the existing import block at the top of the file before adding the test functions below. The final import block should read:

```go
import (
	"os"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)
```

Then add the test functions:

```go
func TestMaxFilter_ShadowNeverSuppresses(t *testing.T) {
	os.Setenv("PENNY_MAX_FILTER_MODE", "shadow")
	defer os.Unsetenv("PENNY_MAX_FILTER_MODE")

	maxF := maxFilterWithEntry("HIGH", 0.50) // 50% MAX, well above any threshold
	agg := aggregatorForTest(30.0, 20.0, 10.0, []string{"HIGH"}, maxF)
	agg.aggregate()
	out := agg.GetCandidates(0)
	if len(out) != 1 {
		t.Fatalf("shadow mode must not suppress: got %d candidates, want 1", len(out))
	}
}

func TestMaxFilter_EnforceSuppressesAbove20(t *testing.T) {
	os.Setenv("PENNY_MAX_FILTER_MODE", "enforce")
	defer os.Unsetenv("PENNY_MAX_FILTER_MODE")

	maxF := maxFilterWithEntry("HIGH", 0.30)
	agg := aggregatorForTest(30.0, 20.0, 10.0, []string{"HIGH"}, maxF)
	agg.aggregate()
	out := agg.GetCandidates(0)
	if len(out) != 0 {
		t.Errorf("enforce mode must suppress MAX=0.30: got %d candidates, want 0", len(out))
	}
}

func TestMaxFilter_EnforcePassesBelow20(t *testing.T) {
	os.Setenv("PENNY_MAX_FILTER_MODE", "enforce")
	defer os.Unsetenv("PENNY_MAX_FILTER_MODE")

	maxF := maxFilterWithEntry("LOW", 0.10)
	agg := aggregatorForTest(30.0, 20.0, 10.0, []string{"LOW"}, maxF)
	agg.aggregate()
	out := agg.GetCandidates(0)
	if len(out) != 1 {
		t.Errorf("enforce mode must pass MAX=0.10: got %d candidates, want 1", len(out))
	}
}

func TestMaxFilter_NilIsNoOp(t *testing.T) {
	os.Setenv("PENNY_MAX_FILTER_MODE", "enforce") // even in enforce, nil filter must not panic
	defer os.Unsetenv("PENNY_MAX_FILTER_MODE")

	agg := aggregatorForTest(30.0, 20.0, 10.0, []string{"ANY"}, nil)
	agg.aggregate()
	out := agg.GetCandidates(0)
	if len(out) != 1 {
		t.Errorf("nil filter must be no-op: got %d candidates, want 1", len(out))
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./services/ -run TestMaxFilter -v`
Expected: tests fail to compile because (a) `NewPennySignalAggregator` does not take a fifth parameter, (b) `maxMode` field and the env-var-reading logic do not exist, (c) the `GetCandidates` hook is missing. Multiple compile errors — that's the expected failing state.

- [ ] **Step 4: Update `PennySignalAggregator` struct, constructor, and GetCandidates**

In `services/penny_signal_aggregator.go`:

(a) Update the struct definition at line 48-58. Add `maxFilter` and `maxMode` fields:

```go
type PennySignalAggregator struct {
	universe     *PennyUniverseService
	screener     *PennyScreenerService
	edgar        *SECEdgarService
	social       *SocialSignalService
	maxFilter    *PennyMaxFilterService
	mu           sync.RWMutex
	candidates   map[string]CandidateScore
	blacklist    *BracketBlacklist
	logger       *logrus.Logger
	dilutionMode string // "shadow" (log only) or "enforce" (suppress); default "shadow"
	maxMode      string // "shadow" (log only) or "enforce" (suppress); default "shadow"
}
```

(b) Update `NewPennySignalAggregator` signature and body (around line 61-84):

```go
func NewPennySignalAggregator(
	universe *PennyUniverseService,
	screener *PennyScreenerService,
	edgar *SECEdgarService,
	social *SocialSignalService,
	maxFilter *PennyMaxFilterService,
) *PennySignalAggregator {
	logger := logrus.New()
	logger.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})

	dilutionMode := os.Getenv("PENNY_DILUTION_FILTER_MODE")
	if dilutionMode != "enforce" {
		dilutionMode = "shadow"
	}
	logger.WithField("mode", dilutionMode).Info("PennySignalAggregator: dilution filter mode")

	maxMode := os.Getenv("PENNY_MAX_FILTER_MODE")
	if maxMode != "enforce" {
		maxMode = "shadow"
	}
	logger.WithField("mode", maxMode).Info("PennySignalAggregator: max filter mode")

	return &PennySignalAggregator{
		universe:     universe,
		screener:     screener,
		edgar:        edgar,
		social:       social,
		maxFilter:    maxFilter,
		candidates:   make(map[string]CandidateScore),
		blacklist:    newBracketBlacklist(),
		logger:       logger,
		dilutionMode: dilutionMode,
		maxMode:      maxMode,
	}
}
```

(c) Update the lock-ordering comment block at lines 24-30. Replace it with:

```go
// Lock ordering: PennySignalAggregator.mu must always be acquired before
// BracketBlacklist.mu, before SECEdgarService.dilutionMu, and before
// PennyMaxFilterService.mu. GetCandidates holds a.mu.RLock while calling
// blacklist.IsBlacklisted (b.mu.RLock), edgar.IsDilutionBlocked (which may
// take dilutionMu.Lock during eviction), and maxFilter.GetMax (which takes
// maxFilter.mu.RLock for a single map lookup). No code path may acquire
// BracketBlacklist.mu, SECEdgarService.dilutionMu, or
// PennyMaxFilterService.mu before PennySignalAggregator.mu.
```

(d) Add the MAX hook inside `GetCandidates`. Find the existing dilution block (lines 149-161) and immediately after it (after the closing brace of the `if a.edgar != nil` block, and before `out = append(out, c)`), insert:

```go
if a.maxFilter != nil {
	if entry, ok := a.maxFilter.GetMax(c.Ticker); ok {
		a.logger.WithFields(logrus.Fields{
			"ticker":           c.Ticker,
			"composite":        c.CompositeScore,
			"max_21":           entry.Value,
			"max_best_day":     entry.BestDay.Format("2006-01-02"),
			"bars_used":        entry.BarsUsed,
			"would_skip_15pct": entry.Value >= 0.15,
			"would_skip_20pct": entry.Value >= 0.20,
			"would_skip_25pct": entry.Value >= 0.25,
			"computed_at":      entry.ComputedAt.Format(time.RFC3339),
			"mode":             a.maxMode,
		}).Info("max filter evaluation")
		if a.maxMode == "enforce" && entry.Value >= 0.20 {
			continue
		}
	}
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./services/ -run TestMaxFilter -v -race`
Expected: all four new tests PASS, no race warnings.

Also run the broader aggregator test suite to verify nothing regressed:

Run: `go test ./services/ -run TestAggregator -v`
Expected: all existing aggregator tests still PASS.

- [ ] **Step 6: Run the entire services package**

Run: `go test ./services/ -race`
Expected: full package passes.

- [ ] **Step 7: Commit**

```bash
git add services/penny_signal_aggregator.go services/penny_signal_aggregator_test.go
git commit -m "feat(penny): integrate MAX filter into aggregator GetCandidates (shadow default)"
```

---

### Task 8: Wire `PennyMaxFilterService` into `cmd/bot/main.go`

**Files:**
- Modify: `cmd/bot/main.go`

This is a compile-time integration. The new constructor signature added in Task 7 breaks the existing call site at `cmd/bot/main.go:171`; without this task, the binary will not build.

- [ ] **Step 1: Locate the current aggregator construction**

Open `cmd/bot/main.go` and find the block at lines 167-184. The relevant section is:

```go
pennyUniverseService := services.NewPennyUniverseService(...)
pennyScreenerService := services.NewPennyScreenerService(...)
secEdgarService := services.NewSECEdgarService(...)
socialSignalService := services.NewSocialSignalService(...)
pennyAggregator := services.NewPennySignalAggregator(pennyUniverseService, pennyScreenerService, secEdgarService, socialSignalService)
pennyController := controllers.NewPennyController(pennyAggregator)

// Wire dilution filter to operator-visible held-position logging.
secEdgarService.SetHeldTickersFn(positionManager.HeldPennyTickers)

// Start penny pipeline goroutines
go pennyUniverseService.Start(ctx)
go pennyScreenerService.Start(ctx)
go secEdgarService.Start(ctx)
go socialSignalService.Start(ctx)
go pennyAggregator.Start(ctx)
```

- [ ] **Step 2: Construct the MAX filter and update the aggregator call**

Replace the relevant lines with:

```go
pennyUniverseService := services.NewPennyUniverseService(cfg.FMPAPIKey, cfg.AlpacaAPIKey, cfg.AlpacaSecretKey, cfg.AlpacaBaseURL, earningsService, nil)
pennyScreenerService := services.NewPennyScreenerService(cfg.AlpacaAPIKey, cfg.AlpacaSecretKey, pennyUniverseService)
secEdgarService := services.NewSECEdgarService(pennyUniverseService, nil, cfg.OperatorEmail, earningsService)
socialSignalService := services.NewSocialSignalService(pennyUniverseService, nil)
pennyMaxFilter := services.NewPennyMaxFilterService(pennyUniverseService, dataService)
pennyAggregator := services.NewPennySignalAggregator(pennyUniverseService, pennyScreenerService, secEdgarService, socialSignalService, pennyMaxFilter)
pennyController := controllers.NewPennyController(pennyAggregator)

// Wire dilution filter to operator-visible held-position logging.
secEdgarService.SetHeldTickersFn(positionManager.HeldPennyTickers)

// Start penny pipeline goroutines
go pennyUniverseService.Start(ctx)
go pennyScreenerService.Start(ctx)
go secEdgarService.Start(ctx)
go socialSignalService.Start(ctx)
go pennyMaxFilter.Start(ctx)
go pennyAggregator.Start(ctx)
```

The `dataService` variable already exists at line 62 (an `*AlpacaDataService`), so no additional construction is needed.

- [ ] **Step 3: Verify the binary builds**

Run: `go build ./cmd/bot`
Expected: clean build, produces `bot.exe` (on Windows) or `bot`.

- [ ] **Step 4: Run the full test suite one more time**

Run: `go test ./... -race`
Expected: all tests pass.

- [ ] **Step 5: Commit**

```bash
git add cmd/bot/main.go
git commit -m "feat(penny): wire PennyMaxFilterService into main bot startup"
```

---

### Task 9: Update rules documentation and agent config mirror

**Files:**
- Modify: `TRADING_RULES_PENNY.md`
- Modify: `data/agent-config.json`

The rules file is a human-readable mirror; the JSON `customRules` field is the authoritative agent input (per the note at `TRADING_RULES_PENNY.md:3-8`). Both must change together.

- [ ] **Step 1: Add a new section to `TRADING_RULES_PENNY.md`**

Search the file for `Dilution Filter` (case-insensitive). If a section by that name exists, insert the new MAX Filter section immediately after that section's last line. If no Dilution Filter section exists in the file, insert the new section immediately before the last existing top-level (`## `) heading in the file. The heading style for the new section is `## MAX Filter (Shadow Mode)` exactly, matching the document's existing two-hash convention.

```markdown
## MAX Filter (Shadow Mode)

The agent's signal pipeline now logs a 21-session MAX value for every
candidate that reaches the post-dilution stage of `get_penny_candidates`.
MAX is the maximum close-to-close return over the prior 21 trading
sessions, sourced from Bali, Cakici & Whitelaw (2011), "Maxing Out:
Stocks as Lotteries and the Cross-Section of Expected Returns" (JFE).

**Current mode:** shadow. The agent's behavior is unchanged. The MAX
value, the best-day date, and pre-computed `would_skip_at_X` boolean
flags at 15%, 20%, and 25% thresholds are written to the operator log
on every candidate evaluation.

**This filter does not affect:**
- Composite score calculation
- Entry decisions (in shadow mode)
- Existing managed positions (ever — same "block ≠ exit" principle as
  the dilution filter)
- The agent's tool surface; this is operator-side telemetry only

**Future:** after a four-week shadow window, operator reviews the logs
against actual trade outcomes and either (a) promotes to enforce at a
validated threshold, or (b) removes the filter. The decision is
documented in a follow-up spec under `docs/superpowers/specs/`.

The shadow-vs-enforce toggle is the env var `PENNY_MAX_FILTER_MODE`.
The agent does not read this; only the operator sets it.
```

- [ ] **Step 2: Mirror the rule text into `data/agent-config.json`**

Open `data/agent-config.json`, find the entry where `id == "penny-momentum"`, and locate its `customRules` field. Append the MAX Filter section as a continuation of the existing rules string. The JSON value is a single multi-line string; embed the new section verbatim using `\n` newlines.

The block to append (as a Go-quoted string for clarity — when pasting into JSON, replace literal newlines with `\n`):

```
## MAX Filter (Shadow Mode)

The agent's signal pipeline now logs a 21-session MAX value for every
candidate that reaches the post-dilution stage of get_penny_candidates.
MAX is the maximum close-to-close return over the prior 21 trading sessions.

Current mode: shadow. The agent's behavior is unchanged. MAX is operator
telemetry only — the agent does not act on it and does not need to
mention it in trade rationale. Existing entry filters (composite >= 60,
blacklists, dilution) remain authoritative.

The shadow-vs-enforce toggle is the env var PENNY_MAX_FILTER_MODE,
set by the operator only.
```

After editing, verify the JSON still parses:

Run (PowerShell): `Get-Content data/agent-config.json | ConvertFrom-Json | Out-Null`
Or (Bash): `python -c "import json; json.load(open('data/agent-config.json'))"`
Expected: no error.

- [ ] **Step 3: Commit**

```bash
git add TRADING_RULES_PENNY.md data/agent-config.json
git commit -m "docs(penny): document MAX filter shadow mode in rules and config"
```

---

### Task 10: End-to-end smoke verification

**Files:**
- Read-only verification — no code changes

Final check that the assembled system actually emits the log lines we expect.

- [ ] **Step 1: Build and run the bot in paper-trading mode with a short-lived process**

Run: `go build -o bot.exe ./cmd/bot`
Expected: build success.

Run the bot with shadow filter mode explicitly set, for ~60 seconds, then Ctrl-C:

```bash
PENNY_MAX_FILTER_MODE=shadow ./bot.exe
```

(On PowerShell: `$env:PENNY_MAX_FILTER_MODE="shadow"; ./bot.exe`)

Watch the log stream. Within the first minute you should see:

1. At startup: `level=info msg="PennySignalAggregator: max filter mode" mode=shadow`
2. At first refresh completion: `level=info msg="PennyMaxFilterService: refresh complete" entries=<N>` where N > 0 (assuming Alpaca creds and the universe both populate)
3. At every aggregator cycle (every 10s) with a non-empty universe: one or more `level=info msg="max filter evaluation"` lines each containing `ticker=`, `max_21=`, `would_skip_20pct=`, `mode=shadow`.

- [ ] **Step 2: Verify the four-week validation procedure is ready**

Confirm with the operator (you) that:
- The log destination (file or stdout pipe) is being captured durably.
- The `activity_logs/` and `decisive_actions/` directories continue to capture trade outcomes.

This is a checkpoint, not a code change. If the log destination is ephemeral (stdout only with no capture), pause and decide on a durable log file before letting the four-week window run.

- [ ] **Step 3: (No commit — verification task)**

If everything looks correct, the implementation is complete and ready for the four-week shadow window. If log lines are missing or malformed, return to the failing task and re-investigate.

---

## Validation Procedure (Reference — Not a Coding Task)

Four weeks from the day shadow mode begins, run the validation procedure documented in the spec at `docs/superpowers/specs/2026-05-12-pennyprophet-max-filter-design.md` under "Four-week validation procedure". The result of that procedure is either:

- A follow-up implementation plan that promotes the filter to enforce mode (env var flip + threshold update if validation shifts it from 20%).
- A follow-up cleanup plan that removes the service entirely.

Either outcome requires a new spec document; the current spec terminates at the validation memo.
