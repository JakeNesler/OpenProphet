# PennyProphet Earnings Exclusion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an `EarningsCalendarService` that drops tickers reporting within the next 3 trading days from `PennyUniverseService.filter()`, preventing new entries into positions exposed to overnight earnings-gap risk.

**Architecture:** New standalone service in `services/penny_earnings_service.go` owning a daily-refreshed map of upcoming earnings entries plus a 14-day Alpaca trading-day calendar. The universe filter consults the service via a small `EarningsCalendarChecker` interface. Fail-open semantics on FMP/Alpaca outage; entry-only scope (existing positions are not auto-flattened).

**Tech Stack:** Go 1.21+, `net/http`, `net/http/httptest`, `sirupsen/logrus`, FMP `/api/v3/earning_calendar` and Alpaca `/v2/calendar` endpoints.

**Spec:** `docs/superpowers/specs/2026-05-08-pennyprophet-earnings-exclusion-design.md`

---

## File Structure

| File | Action | Responsibility |
|---|---|---|
| `services/penny_earnings_service.go` | Create | `EarningsCalendarService` struct, public API, refresh loop |
| `services/penny_earnings_service_test.go` | Create | Pure-function unit tests + `httptest`-based refresh tests |
| `services/penny_universe_service.go` | Modify | Add `EarningsCalendarChecker` interface; change `NewPennyUniverseService` signature; change `filter()` return |
| `services/penny_universe_service_test.go` | Modify | Update existing call sites; add exclusion test cases |
| `cmd/bot/main.go` | Modify | Construct earnings service, wait for first refresh, pass to universe |

Test commands run from repo root:
- All earnings-service tests: `go test ./services -run TestEarnings -v`
- All universe-service tests: `go test ./services -run TestPennyUniverse -v`
- Build verification: `go build ./...`

---

## Task 1: Create skeleton with types, constants, and constructor

**Files:**
- Create: `services/penny_earnings_service.go`

- [ ] **Step 1: Create the skeleton file**

```go
package services

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	earningsExclusionDays    = 3
	staleThreshold           = 36 * time.Hour
	staleWarnInterval        = 4 * time.Hour
	earningsFetchHorizonDays = 10
	calendarFetchHorizonDays = 14
	refreshCheckInterval     = 1 * time.Hour

	// FirstRefreshWaitTimeout is the maximum time cmd/bot/main.go waits for
	// the first earnings refresh before proceeding in fail-open mode.
	FirstRefreshWaitTimeout = 5 * time.Second
)

type earningsEntry struct {
	Ticker string
	Date   time.Time
	Time   string // "bmo" | "amc" | "" (other values normalized to "")
}

type EarningsCalendarService struct {
	httpClient        *http.Client
	fmpAPIKey         string
	fmpBaseURL        string
	alpacaAPIKey      string
	alpacaSecretKey   string
	alpacaBaseURL     string
	mu                sync.RWMutex
	entries           map[string]earningsEntry
	calendar          []AlpacaCalendarEntry
	lastRefreshETDate string
	lastRefresh       time.Time
	lastWarnTime      time.Time
	firstRefreshDone  chan struct{}
	firstRefreshOnce  sync.Once
	logger            *logrus.Logger
}

func NewEarningsCalendarService(
	fmpAPIKey, alpacaAPIKey, alpacaSecretKey, alpacaBaseURL string,
	httpClient *http.Client,
) *EarningsCalendarService {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	if alpacaBaseURL == "" {
		alpacaBaseURL = "https://paper-api.alpaca.markets"
	}
	logger := logrus.New()
	logger.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})
	return &EarningsCalendarService{
		httpClient:       httpClient,
		fmpAPIKey:        fmpAPIKey,
		fmpBaseURL:       "https://financialmodelingprep.com",
		alpacaAPIKey:     alpacaAPIKey,
		alpacaSecretKey:  alpacaSecretKey,
		alpacaBaseURL:    alpacaBaseURL,
		entries:          make(map[string]earningsEntry),
		firstRefreshDone: make(chan struct{}),
		logger:           logger,
	}
}

// Start, IsExcluded, WaitForFirstRefresh, refresh implemented in subsequent tasks.
func (s *EarningsCalendarService) Start(ctx context.Context)                          {}
func (s *EarningsCalendarService) IsExcluded(ticker string, now time.Time) bool       { return false }
func (s *EarningsCalendarService) WaitForFirstRefresh(timeout time.Duration) bool     { return false }
```

- [ ] **Step 2: Verify the package compiles**

Run: `go build ./services`
Expected: no output (success). The unused-imports/unused-vars checker should be happy because every imported package is referenced and every field exists.

- [ ] **Step 3: Commit**

```bash
git add services/penny_earnings_service.go
git commit -m "feat(penny): add EarningsCalendarService skeleton with types and constants"
```

---

## Task 2: tradingDayDistance helper (pure function)

**Files:**
- Modify: `services/penny_earnings_service.go`
- Create: `services/penny_earnings_service_test.go`

- [ ] **Step 1: Write the failing test**

Create `services/penny_earnings_service_test.go`:

```go
package services

import (
	"testing"
	"time"
)

// monFriCalendar returns a list of AlpacaCalendarEntry for Mon-Fri across the given week range,
// skipping weekends. Dates are formatted as "YYYY-MM-DD".
func monFriCalendar(start time.Time, days int) []AlpacaCalendarEntry {
	out := []AlpacaCalendarEntry{}
	d := start
	for i := 0; i < days; i++ {
		wd := d.Weekday()
		if wd != time.Saturday && wd != time.Sunday {
			out = append(out, AlpacaCalendarEntry{Date: d.Format("2006-01-02")})
		}
		d = d.AddDate(0, 0, 1)
	}
	return out
}

func TestEarningsTradingDayDistance_SameDay(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	mon := time.Date(2026, 5, 4, 0, 0, 0, 0, loc) // Monday
	cal := monFriCalendar(mon, 10)
	got := tradingDayDistance(mon, mon, cal)
	if got != 0 {
		t.Errorf("expected 0 for same day, got %d", got)
	}
}

func TestEarningsTradingDayDistance_NextTradingDay(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	mon := time.Date(2026, 5, 4, 0, 0, 0, 0, loc)
	tue := time.Date(2026, 5, 5, 0, 0, 0, 0, loc)
	cal := monFriCalendar(mon, 10)
	got := tradingDayDistance(mon, tue, cal)
	if got != 1 {
		t.Errorf("expected 1 trading day Mon→Tue, got %d", got)
	}
}

func TestEarningsTradingDayDistance_AcrossWeekend(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	fri := time.Date(2026, 5, 8, 0, 0, 0, 0, loc)  // Friday
	nextMon := time.Date(2026, 5, 11, 0, 0, 0, 0, loc) // following Monday
	cal := monFriCalendar(fri, 7)
	got := tradingDayDistance(fri, nextMon, cal)
	if got != 1 {
		t.Errorf("expected 1 trading day Fri→Mon (skipping weekend), got %d", got)
	}
}

func TestEarningsTradingDayDistance_FullWeek(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	mon := time.Date(2026, 5, 4, 0, 0, 0, 0, loc)
	fri := time.Date(2026, 5, 8, 0, 0, 0, 0, loc)
	cal := monFriCalendar(mon, 10)
	got := tradingDayDistance(mon, fri, cal)
	if got != 4 {
		t.Errorf("expected 4 trading days Mon→Fri, got %d", got)
	}
}

func TestEarningsTradingDayDistance_EffectiveBeforeNow(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	mon := time.Date(2026, 5, 4, 0, 0, 0, 0, loc)
	prevFri := time.Date(2026, 5, 1, 0, 0, 0, 0, loc)
	cal := monFriCalendar(prevFri, 10)
	got := tradingDayDistance(mon, prevFri, cal)
	if got != -1 {
		t.Errorf("expected -1 sentinel when effective is before now, got %d", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./services -run TestEarningsTradingDayDistance -v`
Expected: FAIL — `undefined: tradingDayDistance`.

- [ ] **Step 3: Implement the helper**

Add to `services/penny_earnings_service.go` (above the `EarningsCalendarService` struct or at end of file):

```go
// tradingDayDistance returns the number of trading days from nowDate (exclusive)
// to effective (inclusive). Both arguments are compared by date in their stored
// location. Returns -1 if effective is strictly before nowDate.
func tradingDayDistance(nowDate, effective time.Time, calendar []AlpacaCalendarEntry) int {
	nowYMD := nowDate.Format("2006-01-02")
	effYMD := effective.Format("2006-01-02")
	if effYMD < nowYMD {
		return -1
	}
	if effYMD == nowYMD {
		return 0
	}
	count := 0
	for _, e := range calendar {
		if e.Date > nowYMD && e.Date <= effYMD {
			count++
		}
	}
	return count
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./services -run TestEarningsTradingDayDistance -v`
Expected: all five PASS.

- [ ] **Step 5: Commit**

```bash
git add services/penny_earnings_service.go services/penny_earnings_service_test.go
git commit -m "feat(penny): add tradingDayDistance helper for earnings exclusion"
```

---

## Task 3: effectiveDate helper (pure function)

**Files:**
- Modify: `services/penny_earnings_service.go`
- Modify: `services/penny_earnings_service_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `services/penny_earnings_service_test.go`:

```go
func TestEarningsEffectiveDate_BMO_returnsCalendarDate(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	mon := time.Date(2026, 5, 4, 0, 0, 0, 0, loc)
	cal := monFriCalendar(mon, 10)
	entry := earningsEntry{Ticker: "AAA", Date: mon, Time: "bmo"}
	s := &EarningsCalendarService{}
	got := s.effectiveDate(entry, cal)
	if got.Format("2006-01-02") != mon.Format("2006-01-02") {
		t.Errorf("BMO Mon: expected %s, got %s", mon.Format("2006-01-02"), got.Format("2006-01-02"))
	}
}

func TestEarningsEffectiveDate_AMC_returnsNextTradingDay(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	mon := time.Date(2026, 5, 4, 0, 0, 0, 0, loc)
	tue := time.Date(2026, 5, 5, 0, 0, 0, 0, loc)
	cal := monFriCalendar(mon, 10)
	entry := earningsEntry{Ticker: "AAA", Date: mon, Time: "amc"}
	s := &EarningsCalendarService{}
	got := s.effectiveDate(entry, cal)
	if got.Format("2006-01-02") != tue.Format("2006-01-02") {
		t.Errorf("AMC Mon: expected %s, got %s", tue.Format("2006-01-02"), got.Format("2006-01-02"))
	}
}

func TestEarningsEffectiveDate_AMCFriday_returnsNextMonday(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	fri := time.Date(2026, 5, 8, 0, 0, 0, 0, loc)
	mon := time.Date(2026, 5, 11, 0, 0, 0, 0, loc)
	cal := monFriCalendar(fri, 7)
	entry := earningsEntry{Ticker: "AAA", Date: fri, Time: "amc"}
	s := &EarningsCalendarService{}
	got := s.effectiveDate(entry, cal)
	if got.Format("2006-01-02") != mon.Format("2006-01-02") {
		t.Errorf("AMC Fri: expected next Mon %s, got %s", mon.Format("2006-01-02"), got.Format("2006-01-02"))
	}
}

func TestEarningsEffectiveDate_EmptyTime_treatedAsBMO(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	mon := time.Date(2026, 5, 4, 0, 0, 0, 0, loc)
	cal := monFriCalendar(mon, 10)
	entry := earningsEntry{Ticker: "AAA", Date: mon, Time: ""}
	s := &EarningsCalendarService{}
	got := s.effectiveDate(entry, cal)
	if got.Format("2006-01-02") != mon.Format("2006-01-02") {
		t.Errorf("empty time: expected BMO behavior, got %s", got.Format("2006-01-02"))
	}
}

func TestEarningsEffectiveDate_EmptyCalendar_returnsEntryDateUnchanged(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	mon := time.Date(2026, 5, 4, 0, 0, 0, 0, loc)
	entry := earningsEntry{Ticker: "AAA", Date: mon, Time: "amc"}
	s := &EarningsCalendarService{}
	got := s.effectiveDate(entry, nil)
	if got.Format("2006-01-02") != mon.Format("2006-01-02") {
		t.Errorf("empty calendar: expected entry.Date unchanged, got %s", got.Format("2006-01-02"))
	}
}

func TestEarningsEffectiveDate_DateBeforeCalendarStart_returnsFirstCalendarDay(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	dateBeforeCal := time.Date(2026, 5, 1, 0, 0, 0, 0, loc) // Friday
	monStart := time.Date(2026, 5, 4, 0, 0, 0, 0, loc)
	cal := monFriCalendar(monStart, 5)
	entry := earningsEntry{Ticker: "AAA", Date: dateBeforeCal, Time: "bmo"}
	s := &EarningsCalendarService{}
	got := s.effectiveDate(entry, cal)
	// First trading day in calendar on or after dateBeforeCal is Mon 2026-05-04
	if got.Format("2006-01-02") != monStart.Format("2006-01-02") {
		t.Errorf("expected %s (first cal day), got %s", monStart.Format("2006-01-02"), got.Format("2006-01-02"))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./services -run TestEarningsEffectiveDate -v`
Expected: FAIL — `s.effectiveDate undefined`.

- [ ] **Step 3: Implement effectiveDate**

Add to `services/penny_earnings_service.go`:

```go
// effectiveDate computes the trading day on which the post-earnings gap will manifest.
// For BMO/empty time: returns the first trading day on or after entry.Date.
// For AMC: returns the first trading day strictly after entry.Date.
// Returns entry.Date unchanged if calendar is empty or no qualifying day exists.
func (s *EarningsCalendarService) effectiveDate(entry earningsEntry, calendar []AlpacaCalendarEntry) time.Time {
	if len(calendar) == 0 {
		return entry.Date
	}
	entryYMD := entry.Date.Format("2006-01-02")
	loc := nyLoc
	if loc == nil {
		loc = time.UTC
	}
	for _, c := range calendar {
		if entry.Time == "amc" {
			if c.Date > entryYMD {
				if d, err := time.ParseInLocation("2006-01-02", c.Date, loc); err == nil {
					return d
				}
			}
		} else {
			if c.Date >= entryYMD {
				if d, err := time.ParseInLocation("2006-01-02", c.Date, loc); err == nil {
					return d
				}
			}
		}
	}
	return entry.Date
}
```

Note: This relies on the package-level `nyLoc` variable already declared in `services/penny_universe_service.go:229`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./services -run TestEarningsEffectiveDate -v`
Expected: all six PASS.

- [ ] **Step 5: Commit**

```bash
git add services/penny_earnings_service.go services/penny_earnings_service_test.go
git commit -m "feat(penny): add effectiveDate helper with BMO/AMC handling"
```

---

## Task 4: maybeWarn throttled-warn helper

**Files:**
- Modify: `services/penny_earnings_service.go`
- Modify: `services/penny_earnings_service_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `services/penny_earnings_service_test.go`:

```go
func TestEarningsMaybeWarn_FirstCallReturnsTrue(t *testing.T) {
	s := &EarningsCalendarService{logger: logrus.New()}
	if !s.maybeWarn(time.Now()) {
		t.Error("expected first maybeWarn call to return true")
	}
}

func TestEarningsMaybeWarn_SecondCallWithinIntervalReturnsFalse(t *testing.T) {
	now := time.Now()
	s := &EarningsCalendarService{logger: logrus.New(), lastWarnTime: now}
	if s.maybeWarn(now.Add(1 * time.Minute)) {
		t.Error("expected second maybeWarn within interval to return false")
	}
}

func TestEarningsMaybeWarn_SecondCallAfterIntervalReturnsTrue(t *testing.T) {
	now := time.Now()
	s := &EarningsCalendarService{logger: logrus.New(), lastWarnTime: now}
	if !s.maybeWarn(now.Add(staleWarnInterval + time.Second)) {
		t.Error("expected maybeWarn after interval to return true")
	}
}
```

You'll need to add `"github.com/sirupsen/logrus"` to the test file's imports.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./services -run TestEarningsMaybeWarn -v`
Expected: FAIL — `s.maybeWarn undefined`.

- [ ] **Step 3: Implement maybeWarn**

Add to `services/penny_earnings_service.go`:

```go
// maybeWarn returns true if a warning should be emitted right now (caller emits the log message)
// and updates lastWarnTime under write-lock. Returns false if the previous warn was within
// staleWarnInterval. The shared throttle covers all warn types (stale, empty calendar, etc.)
// to keep logs from flooding when multiple conditions co-occur.
func (s *EarningsCalendarService) maybeWarn(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.lastWarnTime.IsZero() && now.Sub(s.lastWarnTime) < staleWarnInterval {
		return false
	}
	s.lastWarnTime = now
	return true
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./services -run TestEarningsMaybeWarn -v`
Expected: all three PASS.

- [ ] **Step 5: Commit**

```bash
git add services/penny_earnings_service.go services/penny_earnings_service_test.go
git commit -m "feat(penny): add throttled maybeWarn helper for earnings service"
```

---

## Task 5: IsExcluded predicate

**Files:**
- Modify: `services/penny_earnings_service.go`
- Modify: `services/penny_earnings_service_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `services/penny_earnings_service_test.go`:

```go
// helper: build a service in a fresh, fully-populated state at a known "today"
func earningsServiceAt(today time.Time, entries map[string]earningsEntry, calendar []AlpacaCalendarEntry) *EarningsCalendarService {
	s := &EarningsCalendarService{
		entries:          entries,
		calendar:         calendar,
		lastRefresh:      today,
		firstRefreshDone: make(chan struct{}),
		logger:           logrus.New(),
	}
	return s
}

func TestEarningsIsExcluded_NeverRefreshed_returnsFalse(t *testing.T) {
	s := &EarningsCalendarService{
		entries:          map[string]earningsEntry{},
		firstRefreshDone: make(chan struct{}),
		logger:           logrus.New(),
	}
	if s.IsExcluded("AAA", time.Now()) {
		t.Error("expected false when lastRefresh is zero")
	}
}

func TestEarningsIsExcluded_UnknownTicker_returnsFalse(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	mon := time.Date(2026, 5, 4, 12, 0, 0, 0, loc)
	cal := monFriCalendar(time.Date(2026, 5, 4, 0, 0, 0, 0, loc), 10)
	s := earningsServiceAt(mon, map[string]earningsEntry{}, cal)
	if s.IsExcluded("AAA", mon) {
		t.Error("expected false for unknown ticker")
	}
}

func TestEarningsIsExcluded_PastEarnings_returnsFalse(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	mon := time.Date(2026, 5, 4, 12, 0, 0, 0, loc)
	prevFri := time.Date(2026, 5, 1, 0, 0, 0, 0, loc)
	cal := monFriCalendar(prevFri, 10)
	entries := map[string]earningsEntry{
		"AAA": {Ticker: "AAA", Date: prevFri, Time: "bmo"},
	}
	s := earningsServiceAt(mon, entries, cal)
	if s.IsExcluded("AAA", mon) {
		t.Error("expected false for past earnings")
	}
}

func TestEarningsIsExcluded_SameDayBMO_returnsTrue(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	mon := time.Date(2026, 5, 4, 12, 0, 0, 0, loc)
	cal := monFriCalendar(time.Date(2026, 5, 4, 0, 0, 0, 0, loc), 10)
	entries := map[string]earningsEntry{
		"AAA": {Ticker: "AAA", Date: time.Date(2026, 5, 4, 0, 0, 0, 0, loc), Time: "bmo"},
	}
	s := earningsServiceAt(mon, entries, cal)
	if !s.IsExcluded("AAA", mon) {
		t.Error("expected true for same-day BMO earnings (distance 0)")
	}
}

func TestEarningsIsExcluded_ThreeTradingDaysOut_returnsTrue(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	mon := time.Date(2026, 5, 4, 12, 0, 0, 0, loc)
	thu := time.Date(2026, 5, 7, 0, 0, 0, 0, loc) // 3 trading days from Mon
	cal := monFriCalendar(time.Date(2026, 5, 4, 0, 0, 0, 0, loc), 10)
	entries := map[string]earningsEntry{
		"AAA": {Ticker: "AAA", Date: thu, Time: "bmo"},
	}
	s := earningsServiceAt(mon, entries, cal)
	if !s.IsExcluded("AAA", mon) {
		t.Error("expected true for 3-trading-days-out BMO")
	}
}

func TestEarningsIsExcluded_FourTradingDaysOut_returnsFalse(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	mon := time.Date(2026, 5, 4, 12, 0, 0, 0, loc)
	fri := time.Date(2026, 5, 8, 0, 0, 0, 0, loc) // 4 trading days from Mon
	cal := monFriCalendar(time.Date(2026, 5, 4, 0, 0, 0, 0, loc), 10)
	entries := map[string]earningsEntry{
		"AAA": {Ticker: "AAA", Date: fri, Time: "bmo"},
	}
	s := earningsServiceAt(mon, entries, cal)
	if s.IsExcluded("AAA", mon) {
		t.Error("expected false for 4-trading-days-out BMO")
	}
}

func TestEarningsIsExcluded_EmptyCalendar_returnsFalseAndWarns(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	mon := time.Date(2026, 5, 4, 12, 0, 0, 0, loc)
	entries := map[string]earningsEntry{
		"AAA": {Ticker: "AAA", Date: mon, Time: "bmo"},
	}
	s := earningsServiceAt(mon, entries, nil)
	if s.IsExcluded("AAA", mon) {
		t.Error("expected false when calendar is empty (cannot compute distance)")
	}
	// lastWarnTime should now be set (warn was emitted)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.lastWarnTime.IsZero() {
		t.Error("expected maybeWarn to have fired (lastWarnTime should be non-zero)")
	}
}

func TestEarningsIsExcluded_StaleAndEmptyCalendar_SharesThrottle(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	mon := time.Date(2026, 5, 4, 12, 0, 0, 0, loc)
	entries := map[string]earningsEntry{
		"AAA": {Ticker: "AAA", Date: mon, Time: "bmo"},
	}
	s := earningsServiceAt(mon, entries, nil) // empty calendar
	s.lastRefresh = mon.Add(-48 * time.Hour) // also stale

	// First call: both stale and empty-calendar conditions true.
	// Expect IsExcluded → false (calendar empty), and exactly ONE warn fired (shared throttle).
	_ = s.IsExcluded("AAA", mon)
	s.mu.RLock()
	first := s.lastWarnTime
	s.mu.RUnlock()
	if first.IsZero() {
		t.Fatal("expected at least one warn to fire on first call")
	}

	// Second call within the throttle interval: lastWarnTime should NOT advance.
	_ = s.IsExcluded("AAA", mon.Add(1*time.Minute))
	s.mu.RLock()
	second := s.lastWarnTime
	s.mu.RUnlock()
	if !second.Equal(first) {
		t.Errorf("expected lastWarnTime unchanged within throttle interval, got %v vs %v", first, second)
	}
}

func TestEarningsIsExcluded_StaleData_stillApplies(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	mon := time.Date(2026, 5, 4, 12, 0, 0, 0, loc)
	cal := monFriCalendar(time.Date(2026, 5, 4, 0, 0, 0, 0, loc), 10)
	entries := map[string]earningsEntry{
		"AAA": {Ticker: "AAA", Date: time.Date(2026, 5, 4, 0, 0, 0, 0, loc), Time: "bmo"},
	}
	s := earningsServiceAt(mon, entries, cal)
	// Force stale: lastRefresh is 48h before "now"
	s.lastRefresh = mon.Add(-48 * time.Hour)
	if !s.IsExcluded("AAA", mon) {
		t.Error("expected stale-but-populated cache to still apply exclusion")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.lastWarnTime.IsZero() {
		t.Error("expected stale warn to have fired")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./services -run TestEarningsIsExcluded -v`
Expected: FAIL — current `IsExcluded` always returns false (so unknown-ticker, past, never-refreshed pass; same-day, three-days-out, stale fail).

- [ ] **Step 3: Replace the IsExcluded stub**

Replace the stub `IsExcluded` in `services/penny_earnings_service.go` with:

```go
// IsExcluded returns true if the ticker has an effective earnings date within the
// next earningsExclusionDays trading days. Fail-open semantics: returns false if
// the cache has never been populated or if required calendar data is missing.
func (s *EarningsCalendarService) IsExcluded(ticker string, now time.Time) bool {
	s.mu.RLock()
	entry, hasEntry := s.entries[ticker]
	calendar := s.calendar
	lastRefresh := s.lastRefresh
	s.mu.RUnlock()

	if lastRefresh.IsZero() {
		return false
	}

	if time.Since(lastRefresh) > staleThreshold {
		if s.maybeWarn(now) {
			s.logger.Warnf("earnings calendar is stale (last refresh > %s ago) — still applying cached exclusions", staleThreshold)
		}
	}

	if !hasEntry {
		return false
	}

	if len(calendar) == 0 {
		if s.maybeWarn(now) {
			s.logger.Warn("earnings calendar trading-day cache empty — exclusion temporarily disabled")
		}
		return false
	}

	loc := nyLoc
	if loc == nil {
		loc = time.UTC
	}
	nowET := now.In(loc)
	nowDate := time.Date(nowET.Year(), nowET.Month(), nowET.Day(), 0, 0, 0, 0, loc)
	effective := s.effectiveDate(entry, calendar)
	distance := tradingDayDistance(nowDate, effective, calendar)
	if distance < 0 {
		return false
	}
	return distance <= earningsExclusionDays
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./services -run TestEarningsIsExcluded -v`
Expected: all nine PASS.

- [ ] **Step 5: Commit**

```bash
git add services/penny_earnings_service.go services/penny_earnings_service_test.go
git commit -m "feat(penny): implement IsExcluded with stale and empty-calendar handling"
```

---

## Task 6: WaitForFirstRefresh

**Files:**
- Modify: `services/penny_earnings_service.go`
- Modify: `services/penny_earnings_service_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `services/penny_earnings_service_test.go`:

```go
func TestEarningsWaitForFirstRefresh_TimesOutWhenNotSignaled(t *testing.T) {
	s := &EarningsCalendarService{
		firstRefreshDone: make(chan struct{}),
		logger:           logrus.New(),
	}
	if s.WaitForFirstRefresh(50 * time.Millisecond) {
		t.Error("expected timeout when firstRefreshDone is never closed")
	}
}

func TestEarningsWaitForFirstRefresh_ReturnsTrueWhenChannelClosed(t *testing.T) {
	s := &EarningsCalendarService{
		firstRefreshDone: make(chan struct{}),
		logger:           logrus.New(),
	}
	close(s.firstRefreshDone)
	if !s.WaitForFirstRefresh(50 * time.Millisecond) {
		t.Error("expected true when firstRefreshDone is closed")
	}
}

func TestEarningsWaitForFirstRefresh_CompletesWhenSignaledMidWait(t *testing.T) {
	s := &EarningsCalendarService{
		firstRefreshDone: make(chan struct{}),
		logger:           logrus.New(),
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		s.firstRefreshOnce.Do(func() { close(s.firstRefreshDone) })
	}()
	if !s.WaitForFirstRefresh(200 * time.Millisecond) {
		t.Error("expected WaitForFirstRefresh to return true after mid-wait signal")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./services -run TestEarningsWaitForFirstRefresh -v`
Expected: FAIL — current stub returns false unconditionally so the closed-channel and mid-wait tests fail.

- [ ] **Step 3: Replace the stub**

Replace the WaitForFirstRefresh stub in `services/penny_earnings_service.go` with:

```go
// WaitForFirstRefresh blocks until the first successful refresh has signaled
// firstRefreshDone, or the timeout elapses. Returns true if the signal arrived first.
func (s *EarningsCalendarService) WaitForFirstRefresh(timeout time.Duration) bool {
	select {
	case <-s.firstRefreshDone:
		return true
	case <-time.After(timeout):
		return false
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./services -run TestEarningsWaitForFirstRefresh -v`
Expected: all three PASS.

- [ ] **Step 5: Commit**

```bash
git add services/penny_earnings_service.go services/penny_earnings_service_test.go
git commit -m "feat(penny): implement WaitForFirstRefresh with channel-based signal"
```

---

## Task 7: refresh() — FMP earnings fetch and parse

**Files:**
- Modify: `services/penny_earnings_service.go`
- Modify: `services/penny_earnings_service_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `services/penny_earnings_service_test.go` (note the new imports `encoding/json`, `net/http`, `net/http/httptest`, `strings`):

```go
// fmpEarningsTestServer returns an httptest.Server that responds to /api/v3/earning_calendar
// with the supplied JSON body and status code.
func fmpEarningsTestServer(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v3/earning_calendar") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
}

func TestEarningsRefresh_FMPSuccess_PopulatesEntries(t *testing.T) {
	body := `[
		{"symbol":"AAA","date":"2026-05-12","time":"bmo"},
		{"symbol":"BBB","date":"2026-05-13","time":"amc"}
	]`
	ts := fmpEarningsTestServer(t, body, 200)
	defer ts.Close()
	s := NewEarningsCalendarService("k", "", "", "", ts.Client())
	s.fmpBaseURL = ts.URL
	if _, _, err := s.refreshEarnings(time.Now()); err != nil {
		t.Fatalf("expected refreshEarnings success, got %v", err)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(s.entries))
	}
	if s.entries["AAA"].Time != "bmo" || s.entries["BBB"].Time != "amc" {
		t.Errorf("unexpected entries: %+v", s.entries)
	}
}

func TestEarningsRefresh_FMPNon200_KeepsPriorCache(t *testing.T) {
	ts := fmpEarningsTestServer(t, `internal error`, 500)
	defer ts.Close()
	s := NewEarningsCalendarService("k", "", "", "", ts.Client())
	s.fmpBaseURL = ts.URL
	prior := map[string]earningsEntry{"X": {Ticker: "X"}}
	s.entries = prior
	if _, _, err := s.refreshEarnings(time.Now()); err == nil {
		t.Error("expected refreshEarnings to return error on 500")
	}
	if _, ok := s.entries["X"]; !ok {
		t.Error("prior cache must be preserved on FMP failure")
	}
}

func TestEarningsRefresh_FMPMalformedJSON_KeepsPriorCache(t *testing.T) {
	ts := fmpEarningsTestServer(t, `{not json`, 200)
	defer ts.Close()
	s := NewEarningsCalendarService("k", "", "", "", ts.Client())
	s.fmpBaseURL = ts.URL
	prior := map[string]earningsEntry{"X": {Ticker: "X"}}
	s.entries = prior
	if _, _, err := s.refreshEarnings(time.Now()); err == nil {
		t.Error("expected refreshEarnings to return error on malformed JSON")
	}
	if _, ok := s.entries["X"]; !ok {
		t.Error("prior cache must be preserved on parse failure")
	}
}

func TestEarningsRefresh_DuplicateEntries_KeepsEarliest(t *testing.T) {
	body := `[
		{"symbol":"AAA","date":"2026-05-15","time":"amc"},
		{"symbol":"AAA","date":"2026-05-12","time":"bmo"},
		{"symbol":"AAA","date":"2026-05-20","time":"bmo"}
	]`
	ts := fmpEarningsTestServer(t, body, 200)
	defer ts.Close()
	s := NewEarningsCalendarService("k", "", "", "", ts.Client())
	s.fmpBaseURL = ts.URL
	if _, _, err := s.refreshEarnings(time.Now()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	got := s.entries["AAA"]
	if got.Date.Format("2006-01-02") != "2026-05-12" {
		t.Errorf("expected earliest 2026-05-12, got %s", got.Date.Format("2006-01-02"))
	}
}

func TestEarningsRefresh_PastDateEntries_Dropped(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, loc)
	body := `[
		{"symbol":"OLD","date":"2026-05-01","time":"bmo"},
		{"symbol":"NEW","date":"2026-05-12","time":"bmo"}
	]`
	ts := fmpEarningsTestServer(t, body, 200)
	defer ts.Close()
	s := NewEarningsCalendarService("k", "", "", "", ts.Client())
	s.fmpBaseURL = ts.URL
	if _, _, err := s.refreshEarnings(now); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.entries["OLD"]; ok {
		t.Error("past-dated entry should have been dropped")
	}
	if _, ok := s.entries["NEW"]; !ok {
		t.Error("future entry should be present")
	}
}

func TestEarningsRefresh_UnknownTimeNormalizedToEmpty(t *testing.T) {
	body := `[{"symbol":"AAA","date":"2026-05-12","time":"DURING-MARKET"}]`
	ts := fmpEarningsTestServer(t, body, 200)
	defer ts.Close()
	s := NewEarningsCalendarService("k", "", "", "", ts.Client())
	s.fmpBaseURL = ts.URL
	if _, _, err := s.refreshEarnings(time.Now()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.entries["AAA"].Time != "" {
		t.Errorf("unknown time should normalize to empty, got %q", s.entries["AAA"].Time)
	}
}

func TestEarningsRefresh_UppercaseTimeNormalizedToLowercase(t *testing.T) {
	body := `[{"symbol":"AAA","date":"2026-05-12","time":"BMO"}]`
	ts := fmpEarningsTestServer(t, body, 200)
	defer ts.Close()
	s := NewEarningsCalendarService("k", "", "", "", ts.Client())
	s.fmpBaseURL = ts.URL
	if _, _, err := s.refreshEarnings(time.Now()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.entries["AAA"].Time != "bmo" {
		t.Errorf("uppercase BMO should normalize to lowercase bmo, got %q", s.entries["AAA"].Time)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./services -run TestEarningsRefresh -v`
Expected: FAIL — `s.refreshEarnings undefined`.

- [ ] **Step 3: Implement refreshEarnings**

Add to `services/penny_earnings_service.go` (also add `"encoding/json"`, `"fmt"`, `"io"`, `"strings"` to the import block):

```go
type fmpEarningsItem struct {
	Symbol string `json:"symbol"`
	Date   string `json:"date"`
	Time   string `json:"time"`
}

// refreshEarnings fetches the FMP earnings calendar and replaces the entries map
// with the parsed result. The HTTP call and parse run outside the mutex; the lock
// is held only for the final swap. Returns the count of parsed and skipped entries
// for the caller to log; on failure returns an error and preserves the prior cache.
func (s *EarningsCalendarService) refreshEarnings(now time.Time) (parsed, skipped int, err error) {
	loc := nyLoc
	if loc == nil {
		loc = time.UTC
	}
	nowET := now.In(loc)
	from := nowET.Format("2006-01-02")
	to := nowET.AddDate(0, 0, earningsFetchHorizonDays).Format("2006-01-02")
	url := fmt.Sprintf("%s/api/v3/earning_calendar?from=%s&to=%s&apikey=%s",
		s.fmpBaseURL, from, to, s.fmpAPIKey)

	resp, err := s.httpClient.Get(url)
	if err != nil {
		s.logger.WithError(err).Warn("EarningsCalendarService: FMP earnings fetch failed")
		return 0, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		s.logger.WithField("status", resp.StatusCode).Warn("EarningsCalendarService: FMP earnings non-200")
		return 0, 0, fmt.Errorf("fmp earnings returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.logger.WithError(err).Warn("EarningsCalendarService: failed to read FMP earnings body")
		return 0, 0, err
	}
	var items []fmpEarningsItem
	if err := json.Unmarshal(body, &items); err != nil {
		s.logger.WithError(err).Warn("EarningsCalendarService: failed to parse FMP earnings JSON")
		return 0, 0, err
	}

	todayYMD := from
	parsedMap := make(map[string]earningsEntry)
	for _, it := range items {
		d, perr := time.ParseInLocation("2006-01-02", it.Date, loc)
		if perr != nil {
			skipped++
			continue
		}
		if it.Date < todayYMD {
			continue
		}
		t := strings.ToLower(strings.TrimSpace(it.Time))
		if t != "bmo" && t != "amc" {
			t = ""
		}
		entry := earningsEntry{Ticker: it.Symbol, Date: d, Time: t}
		if existing, ok := parsedMap[it.Symbol]; !ok || d.Before(existing.Date) {
			parsedMap[it.Symbol] = entry
		}
	}

	s.mu.Lock()
	s.entries = parsedMap
	s.mu.Unlock()

	return len(parsedMap), skipped, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./services -run TestEarningsRefresh -v`
Expected: all seven PASS.

- [ ] **Step 5: Commit**

```bash
git add services/penny_earnings_service.go services/penny_earnings_service_test.go
git commit -m "feat(penny): implement FMP earnings fetch with dedup and date filtering"
```

---

## Task 8: refresh() — Alpaca calendar fetch and orchestrating refresh()

**Files:**
- Modify: `services/penny_earnings_service.go`
- Modify: `services/penny_earnings_service_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `services/penny_earnings_service_test.go`:

```go
// dualEndpointServer returns one httptest.Server that handles BOTH the FMP earnings
// path and the Alpaca calendar path. The earnings response is supplied; the calendar
// response is auto-generated as Mon-Fri starting from "from" date in the request.
type dualEndpointConfig struct {
	earningsBody     string
	earningsStatus   int
	calendarBody     string // if non-empty, overrides auto-generation
	calendarStatus   int
	calendarFailHard bool // if true, 500 on calendar
}

func dualEndpointServer(t *testing.T, cfg dualEndpointConfig) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v3/earning_calendar"):
			w.WriteHeader(cfg.earningsStatus)
			w.Write([]byte(cfg.earningsBody))
		case strings.HasPrefix(r.URL.Path, "/v2/calendar"):
			if cfg.calendarFailHard {
				w.WriteHeader(500)
				w.Write([]byte(`internal`))
				return
			}
			if cfg.calendarBody != "" {
				w.WriteHeader(cfg.calendarStatus)
				w.Write([]byte(cfg.calendarBody))
				return
			}
			from := r.URL.Query().Get("start")
			loc, _ := time.LoadLocation("America/New_York")
			start, _ := time.ParseInLocation("2006-01-02", from, loc)
			cal := monFriCalendar(start, calendarFetchHorizonDays)
			b, _ := json.Marshal(cal)
			w.WriteHeader(200)
			w.Write(b)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestEarningsRefresh_FullSuccess_SignalsFirstRefresh(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 5, 4, 8, 0, 0, 0, loc)
	body := `[{"symbol":"AAA","date":"2026-05-12","time":"bmo"}]`
	ts := dualEndpointServer(t, dualEndpointConfig{
		earningsBody: body, earningsStatus: 200,
	})
	defer ts.Close()
	s := NewEarningsCalendarService("k", "alpaca-id", "alpaca-secret", ts.URL, ts.Client())
	s.fmpBaseURL = ts.URL

	s.refresh(now)
	select {
	case <-s.firstRefreshDone:
		// ok
	default:
		t.Error("expected firstRefreshDone to be closed after full success")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.entries) != 1 || len(s.calendar) == 0 {
		t.Errorf("expected entries+calendar populated, got entries=%d calendar=%d", len(s.entries), len(s.calendar))
	}
	if s.lastRefresh.IsZero() {
		t.Error("expected lastRefresh to be set")
	}
	if s.lastRefreshETDate != "2026-05-04" {
		t.Errorf("expected lastRefreshETDate=2026-05-04, got %q", s.lastRefreshETDate)
	}
}

func TestEarningsRefresh_FMPFails_DoesNotSignalFirstRefresh(t *testing.T) {
	now := time.Now()
	ts := dualEndpointServer(t, dualEndpointConfig{
		earningsBody: `nope`, earningsStatus: 500,
	})
	defer ts.Close()
	s := NewEarningsCalendarService("k", "id", "sec", ts.URL, ts.Client())
	s.fmpBaseURL = ts.URL
	s.refresh(now)
	select {
	case <-s.firstRefreshDone:
		t.Error("firstRefreshDone should NOT be closed when FMP fails")
	default:
		// ok
	}
}

func TestEarningsRefresh_AlpacaFails_DoesNotSignalButUpdatesEntries(t *testing.T) {
	body := `[{"symbol":"AAA","date":"2026-05-12","time":"bmo"}]`
	ts := dualEndpointServer(t, dualEndpointConfig{
		earningsBody: body, earningsStatus: 200,
		calendarFailHard: true,
	})
	defer ts.Close()
	s := NewEarningsCalendarService("k", "id", "sec", ts.URL, ts.Client())
	s.fmpBaseURL = ts.URL
	s.refresh(time.Now())
	select {
	case <-s.firstRefreshDone:
		t.Error("firstRefreshDone should NOT be closed when calendar fails")
	default:
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.entries) != 1 {
		t.Errorf("expected entries to update even when calendar fails, got %d", len(s.entries))
	}
	if len(s.calendar) != 0 {
		t.Errorf("expected calendar to remain empty when Alpaca fails, got %d", len(s.calendar))
	}
}

func TestEarningsRefresh_RunsHTTPOutsideLock(t *testing.T) {
	// Server holds the FMP request open for 200ms. While it's waiting, we should
	// be able to acquire RLock on the service. If HTTP work were inside the lock,
	// this read would block for the full 200ms.
	release := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(200)
		w.Write([]byte(`[]`))
	}))
	defer ts.Close()
	s := NewEarningsCalendarService("k", "id", "sec", ts.URL, ts.Client())
	s.fmpBaseURL = ts.URL

	done := make(chan struct{})
	go func() {
		_, _, _ = s.refreshEarnings(time.Now())
		close(done)
	}()

	// Give the goroutine a moment to start the HTTP call.
	time.Sleep(20 * time.Millisecond)

	// Try to acquire RLock with a tight deadline. If lock is free (HTTP outside lock), this succeeds quickly.
	gotLock := make(chan struct{})
	go func() {
		s.mu.RLock()
		s.mu.RUnlock()
		close(gotLock)
	}()
	select {
	case <-gotLock:
		// success — lock was free during the HTTP call
	case <-time.After(100 * time.Millisecond):
		t.Error("RLock blocked while HTTP call was in flight — HTTP work must run outside the lock")
	}
	close(release)
	<-done
}

func TestEarningsRefresh_RetainsPriorCalendarOnPartialFailure(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 5, 4, 8, 0, 0, 0, loc)

	// First call: full success populates calendar
	body := `[{"symbol":"AAA","date":"2026-05-12","time":"bmo"}]`
	good := dualEndpointServer(t, dualEndpointConfig{earningsBody: body, earningsStatus: 200})
	defer good.Close()
	s := NewEarningsCalendarService("k", "id", "sec", good.URL, good.Client())
	s.fmpBaseURL = good.URL
	s.refresh(now)
	priorCalLen := len(s.calendar)
	if priorCalLen == 0 {
		t.Fatal("setup: expected calendar populated after first refresh")
	}

	// Second call: only Alpaca fails. Calendar should be retained.
	bad := dualEndpointServer(t, dualEndpointConfig{
		earningsBody: body, earningsStatus: 200, calendarFailHard: true,
	})
	defer bad.Close()
	s.fmpBaseURL = bad.URL
	s.alpacaBaseURL = bad.URL
	s.refresh(now)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.calendar) != priorCalLen {
		t.Errorf("expected prior calendar (%d entries) to be retained, got %d", priorCalLen, len(s.calendar))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./services -run TestEarningsRefresh_Full -v && go test ./services -run TestEarningsRefresh_FMPFails -v && go test ./services -run TestEarningsRefresh_Alpaca -v && go test ./services -run TestEarningsRefresh_RetainsPrior -v`
Expected: FAIL — `s.refresh undefined` (or missing Alpaca path).

- [ ] **Step 3: Implement Alpaca calendar fetch and orchestrating `refresh`**

Add to `services/penny_earnings_service.go`:

```go
// refreshCalendar fetches the multi-day Alpaca trading-day calendar. Returns the
// first and last dates in the fetched calendar (for logging) on success.
func (s *EarningsCalendarService) refreshCalendar(now time.Time) (firstDate, lastDate string, err error) {
	loc := nyLoc
	if loc == nil {
		loc = time.UTC
	}
	nowET := now.In(loc)
	start := nowET.Format("2006-01-02")
	end := nowET.AddDate(0, 0, calendarFetchHorizonDays).Format("2006-01-02")
	url := fmt.Sprintf("%s/v2/calendar?start=%s&end=%s", s.alpacaBaseURL, start, end)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		s.logger.WithError(err).Warn("EarningsCalendarService: Alpaca calendar request build failed")
		return "", "", err
	}
	req.Header.Set("APCA-API-KEY-ID", s.alpacaAPIKey)
	req.Header.Set("APCA-API-SECRET-KEY", s.alpacaSecretKey)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		s.logger.WithError(err).Warn("EarningsCalendarService: Alpaca calendar fetch failed")
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		s.logger.WithField("status", resp.StatusCode).Warn("EarningsCalendarService: Alpaca calendar non-200")
		return "", "", fmt.Errorf("alpaca calendar returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}
	var entries []AlpacaCalendarEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		s.logger.WithError(err).Warn("EarningsCalendarService: failed to parse Alpaca calendar JSON")
		return "", "", err
	}
	if len(entries) == 0 {
		s.logger.Warn("EarningsCalendarService: Alpaca calendar returned 0 entries")
		return "", "", fmt.Errorf("alpaca calendar empty")
	}

	s.mu.Lock()
	s.calendar = entries
	s.mu.Unlock()
	return entries[0].Date, entries[len(entries)-1].Date, nil
}

// refresh runs both fetches and updates lastRefresh / lastRefreshETDate.
// Signals firstRefreshDone only when both fetches succeeded. FMP failure aborts;
// Alpaca failure leaves prior calendar in place but skips the firstRefreshDone signal.
// On success, emits a single combined info log.
func (s *EarningsCalendarService) refresh(now time.Time) {
	parsed, skipped, err := s.refreshEarnings(now)
	if err != nil {
		return
	}
	calFrom, calTo, calErr := s.refreshCalendar(now)

	loc := nyLoc
	if loc == nil {
		loc = time.UTC
	}
	todayET := now.In(loc).Format("2006-01-02")

	s.mu.Lock()
	s.lastRefresh = now
	s.lastRefreshETDate = todayET
	s.mu.Unlock()

	fields := logrus.Fields{
		"parsed":  parsed,
		"skipped": skipped,
	}
	if calErr == nil {
		fields["calendar_from"] = calFrom
		fields["calendar_to"] = calTo
		s.firstRefreshOnce.Do(func() { close(s.firstRefreshDone) })
	}
	s.logger.WithFields(fields).Info("EarningsCalendarService: refresh complete")
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./services -run TestEarningsRefresh -v`
Expected: all eleven PASS (the seven from Task 7 plus the four added here).

- [ ] **Step 5: Commit**

```bash
git add services/penny_earnings_service.go services/penny_earnings_service_test.go
git commit -m "feat(penny): add Alpaca calendar fetch and orchestrating refresh()"
```

---

## Task 9: shouldRefreshNow predicate + Start() loop

**Files:**
- Modify: `services/penny_earnings_service.go`
- Modify: `services/penny_earnings_service_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `services/penny_earnings_service_test.go`:

```go
func TestEarningsShouldRefreshNow_FirstRunReturnsTrue(t *testing.T) {
	if !shouldRefreshNow("2026-05-04", "") {
		t.Error("expected true on first run (lastRefreshETDate empty)")
	}
}

func TestEarningsShouldRefreshNow_SameDayReturnsFalse(t *testing.T) {
	if shouldRefreshNow("2026-05-04", "2026-05-04") {
		t.Error("expected false when same ET day")
	}
}

func TestEarningsShouldRefreshNow_NextDayReturnsTrue(t *testing.T) {
	if !shouldRefreshNow("2026-05-05", "2026-05-04") {
		t.Error("expected true on next ET day")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./services -run TestEarningsShouldRefreshNow -v`
Expected: FAIL — `shouldRefreshNow undefined`.

- [ ] **Step 3: Implement predicate and replace Start stub**

Add to `services/penny_earnings_service.go`:

```go
// shouldRefreshNow returns true if a refresh should fire because the ET calendar
// day has rolled over since the last successful refresh (or one has never run).
func shouldRefreshNow(todayETDate, lastRefreshETDate string) bool {
	return lastRefreshETDate != todayETDate
}
```

Replace the `Start` stub with:

```go
// Start runs an initial refresh, then wakes every refreshCheckInterval and runs
// another refresh when the ET calendar day has rolled over. Exits on ctx cancellation.
func (s *EarningsCalendarService) Start(ctx context.Context) {
	loc := nyLoc
	if loc == nil {
		loc = time.UTC
	}
	s.refresh(time.Now())
	ticker := time.NewTicker(refreshCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			todayET := now.In(loc).Format("2006-01-02")
			s.mu.RLock()
			last := s.lastRefreshETDate
			s.mu.RUnlock()
			if shouldRefreshNow(todayET, last) {
				s.refresh(now)
			}
		}
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./services -run TestEarningsShouldRefreshNow -v && go build ./...`
Expected: tests PASS, build succeeds.

- [ ] **Step 5: Commit**

```bash
git add services/penny_earnings_service.go services/penny_earnings_service_test.go
git commit -m "feat(penny): add Start loop with ET-day rollover refresh"
```

---

## Task 10: Add EarningsCalendarChecker interface to PennyUniverseService

**Files:**
- Modify: `services/penny_universe_service.go`
- Modify: `services/penny_universe_service_test.go`

- [ ] **Step 1: Add the interface and update the constructor signature**

Edit `services/penny_universe_service.go` — add the interface near the top of the file (after the `AlpacaCalendarEntry` declaration, around line 22):

```go
// EarningsCalendarChecker is the subset of EarningsCalendarService consumed by
// PennyUniverseService.filter(). Defined as an interface so tests can supply
// stubs and so the universe service does not depend on the earnings service's
// concrete refresh machinery.
type EarningsCalendarChecker interface {
	IsExcluded(ticker string, now time.Time) bool
}
```

In the `PennyUniverseService` struct (around line 103), add a new field after `logger`:

```go
	earnings EarningsCalendarChecker
```

Change `NewPennyUniverseService` signature to accept the new dependency. The current signature is:
```go
func NewPennyUniverseService(fmpAPIKey, alpacaAPIKey, alpacaSecretKey, alpacaBaseURL string, httpClient *http.Client) *PennyUniverseService {
```
Replace with:
```go
func NewPennyUniverseService(fmpAPIKey, alpacaAPIKey, alpacaSecretKey, alpacaBaseURL string, earnings EarningsCalendarChecker, httpClient *http.Client) *PennyUniverseService {
```
And in the function body, set the new field on the returned struct:
```go
	return &PennyUniverseService{
		httpClient:      httpClient,
		fmpAPIKey:       fmpAPIKey,
		fmpBaseURL:      "https://financialmodelingprep.com",
		alpacaAPIKey:    alpacaAPIKey,
		alpacaSecretKey: alpacaSecretKey,
		alpacaBaseURL:   alpacaBaseURL,
		earnings:        earnings,
		logger:          logger,
	}
```

- [ ] **Step 2: Update existing test call sites**

Edit `services/penny_universe_service_test.go`. Every existing call to `NewPennyUniverseService("...", "", "", "", nil)` needs an additional `nil` parameter for `earnings` inserted before `httpClient`:

- Line 20: `svc := NewPennyUniverseService("dummy", "", "", "", nil, nil)`
- Line 41: `svc := NewPennyUniverseService("testkey", "", "", "", nil, ts.Client())`
- Line 51: `svc := NewPennyUniverseService("key", "", "", "", nil, nil)`
- Line 63: `svc := NewPennyUniverseService("key", "", "", "", nil, nil)`

(If line numbers have shifted, find each occurrence and apply the same insertion of `nil` before the trailing argument.)

- [ ] **Step 3: Verify the package still compiles and existing tests pass**

Run: `go build ./services && go test ./services -run TestPennyUniverse -v`
Expected: build succeeds; all existing universe tests still pass (the `earnings` field is unused so far).

Also run: `go build ./...`
Expected: this will FAIL on `cmd/bot/main.go` because its `NewPennyUniverseService` call still uses the old signature. That is expected — Task 12 fixes it. The `services` package alone must compile.

- [ ] **Step 4: Commit**

```bash
git add services/penny_universe_service.go services/penny_universe_service_test.go
git commit -m "feat(penny): add EarningsCalendarChecker interface to universe service"
```

---

## Task 11: filter() return change and exclusion logic

**Files:**
- Modify: `services/penny_universe_service.go`
- Modify: `services/penny_universe_service_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `services/penny_universe_service_test.go`:

```go
type stubEarningsChecker struct {
	excluded map[string]bool
}

func (s *stubEarningsChecker) IsExcluded(ticker string, _ time.Time) bool {
	return s.excluded[ticker]
}

func TestUniverseFilter_NilChecker_DoesNotExclude(t *testing.T) {
	svc := NewPennyUniverseService("k", "", "", "", nil, nil)
	items := []fmpScreenerItem{
		{Symbol: "GOOD", CompanyName: "Good", MarketCap: 100_000_000, Price: 5.0, Volume: 100_000, ExchangeShortName: "NASDAQ"},
	}
	out, excluded := svc.filter(items)
	if len(out) != 1 || excluded != 0 {
		t.Errorf("nil checker: expected 1 included / 0 excluded, got %d / %d", len(out), excluded)
	}
}

func TestUniverseFilter_CheckerExcludesTicker(t *testing.T) {
	checker := &stubEarningsChecker{excluded: map[string]bool{"BAD": true}}
	svc := NewPennyUniverseService("k", "", "", "", checker, nil)
	items := []fmpScreenerItem{
		{Symbol: "GOOD", CompanyName: "Good", MarketCap: 100_000_000, Price: 5.0, Volume: 100_000, ExchangeShortName: "NASDAQ"},
		{Symbol: "BAD", CompanyName: "Bad", MarketCap: 100_000_000, Price: 5.0, Volume: 100_000, ExchangeShortName: "NASDAQ"},
	}
	out, excluded := svc.filter(items)
	if len(out) != 1 || out[0].Ticker != "GOOD" {
		t.Errorf("expected only GOOD to remain, got %+v", out)
	}
	if excluded != 1 {
		t.Errorf("expected excluded count 1, got %d", excluded)
	}
}

func TestUniverseFilter_CheckerExcludesNothing(t *testing.T) {
	checker := &stubEarningsChecker{excluded: map[string]bool{}}
	svc := NewPennyUniverseService("k", "", "", "", checker, nil)
	items := []fmpScreenerItem{
		{Symbol: "GOOD", CompanyName: "Good", MarketCap: 100_000_000, Price: 5.0, Volume: 100_000, ExchangeShortName: "NASDAQ"},
	}
	out, excluded := svc.filter(items)
	if len(out) != 1 || excluded != 0 {
		t.Errorf("expected 1 included / 0 excluded, got %d / %d", len(out), excluded)
	}
}
```

Also update existing tests that destructure the old single-return `filter()`:

- Line 21: `result := svc.filter(items)` → `result, _ := svc.filter(items)`
- Line 56: `result := svc.filter(items)` → `result, _ := svc.filter(items)`
- Line 68: `result := svc.filter(items)` → `result, _ := svc.filter(items)`

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./services -run TestUniverseFilter -v`
Expected: FAIL — `filter` still returns `[]UniverseSymbol` (single value), the new tests expect a two-value return.

- [ ] **Step 3: Update `filter()` signature and behavior**

Edit `services/penny_universe_service.go`. Change `filter` from:

```go
func (s *PennyUniverseService) filter(items []fmpScreenerItem) []UniverseSymbol {
	out := make([]UniverseSymbol, 0)
	for _, item := range items {
		// existing checks...
		out = append(out, UniverseSymbol{...})
	}
	return out
}
```

to:

```go
func (s *PennyUniverseService) filter(items []fmpScreenerItem) ([]UniverseSymbol, int) {
	out := make([]UniverseSymbol, 0)
	excludedForEarnings := 0
	for _, item := range items {
		if !allowedExchanges[item.ExchangeShortName] {
			continue
		}
		if item.Price < 2.0 || item.Price > 10.0 {
			continue
		}
		if item.MarketCap < 50_000_000 || item.MarketCap > 500_000_000 {
			continue
		}
		dollarVol := item.Volume * item.Price
		if dollarVol < 500_000 {
			continue
		}
		if s.earnings != nil && s.earnings.IsExcluded(item.Symbol, time.Now()) {
			excludedForEarnings++
			continue
		}
		out = append(out, UniverseSymbol{
			Ticker:       item.Symbol,
			Name:         item.CompanyName,
			Exchange:     item.ExchangeShortName,
			Price:        item.Price,
			MarketCapM:   item.MarketCap / 1_000_000,
			AvgDollarVol: dollarVol,
		})
	}
	return out, excludedForEarnings
}
```

In the same file, find the call site inside `refresh()` (around line 216) and update it:

```go
universe, excludedForEarnings := s.filter(items)
s.mu.Lock()
s.universe = universe
s.mu.Unlock()
s.logger.WithFields(logrus.Fields{
	"count":                 len(universe),
	"excluded_for_earnings": excludedForEarnings,
}).Info("PennyUniverseService: universe refreshed")
```

(Replace the existing single-field log call.)

You'll need to add `"github.com/sirupsen/logrus"` if not already imported; check the existing import block.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./services -run TestUniverseFilter -v && go test ./services -run TestPennyUniverse -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add services/penny_universe_service.go services/penny_universe_service_test.go
git commit -m "feat(penny): apply earnings exclusion in PennyUniverseService.filter()"
```

---

## Task 12: Wire EarningsCalendarService into cmd/bot/main.go

**Files:**
- Modify: `cmd/bot/main.go`

- [ ] **Step 1: Add the construction and wait block before the existing pennyUniverseService line**

Edit `cmd/bot/main.go`. Find the line (currently around 161):

```go
pennyUniverseService := services.NewPennyUniverseService(cfg.FMPAPIKey, cfg.AlpacaAPIKey, cfg.AlpacaSecretKey, cfg.AlpacaBaseURL, nil)
```

Replace with:

```go
earningsService := services.NewEarningsCalendarService(cfg.FMPAPIKey, cfg.AlpacaAPIKey, cfg.AlpacaSecretKey, cfg.AlpacaBaseURL, nil)
go earningsService.Start(ctx)
if !earningsService.WaitForFirstRefresh(services.FirstRefreshWaitTimeout) {
	logger.Warn("earnings calendar first refresh did not complete within timeout — universe will start in fail-open mode")
}

pennyUniverseService := services.NewPennyUniverseService(cfg.FMPAPIKey, cfg.AlpacaAPIKey, cfg.AlpacaSecretKey, cfg.AlpacaBaseURL, earningsService, nil)
```

- [ ] **Step 2: Build the binary**

Run: `go build ./...`
Expected: success — no compile errors anywhere in the module.

- [ ] **Step 3: Run the full test suite**

Run: `go test ./...`
Expected: all packages PASS.

- [ ] **Step 4: Smoke verification**

Skim `cmd/bot/main.go` to confirm:
1. `earningsService` is constructed before `pennyUniverseService`.
2. `go earningsService.Start(ctx)` is called.
3. `WaitForFirstRefresh` is called with `services.FirstRefreshWaitTimeout`.
4. `earningsService` is passed as the new parameter to `NewPennyUniverseService`.

No need to actually start the bot — the integration is verified by compilation and the universe-service tests.

- [ ] **Step 5: Commit**

```bash
git add cmd/bot/main.go
git commit -m "feat(penny): wire EarningsCalendarService into bot startup"
```

---

## Verification Checklist

After all tasks complete:

- [ ] `go build ./...` succeeds
- [ ] `go test ./...` passes
- [ ] `git log --oneline` shows 12 commits, one per task
- [ ] Spec checklist (re-read `docs/superpowers/specs/2026-05-08-pennyprophet-earnings-exclusion-design.md`):
  - [ ] §2 constants exist in `services/penny_earnings_service.go`
  - [ ] §3 `earningsEntry` struct exists
  - [ ] §4.1–4.6 public/private API methods all implemented
  - [ ] §5 `EarningsCalendarChecker` interface defined; constructor and `filter()` updated
  - [ ] §6 failure-mode matrix behaviors covered by tests
  - [ ] §7 wiring in `cmd/bot/main.go`
  - [ ] §8 test list complete
  - [ ] §9 logging messages present in code
