# PennyProphet — Earnings Exclusion Design Spec

**Date:** 2026-05-08
**Status:** Approved
**Applies to:** `services/penny_universe_service.go`, `cmd/bot/main.go`, new file `services/penny_earnings_service.go`

---

## Purpose

Penny stocks routinely gap 30–80% on earnings. PennyProphet currently has no awareness of upcoming earnings dates, so the universe filter can hand the aggregator candidates that are about to report — exposing new entries to large overnight gap risk that the bracket-stop logic cannot defend against.

This spec adds a daily-refreshed earnings calendar service that the universe filter consults to drop tickers reporting within the next 3 trading days from new-entry consideration. Existing positions and exit logic are not affected.

Scope: **entry-only**. Auto-flatten of held positions before earnings is explicitly out of scope and may be a follow-up.

---

## 1. Architecture Overview

A new service `EarningsCalendarService` in `services/penny_earnings_service.go` owns:

- Daily refresh of upcoming earnings entries from FMP `/api/v3/earning_calendar`.
- Daily refresh of a multi-day Alpaca trading-day calendar (separate from the single-day calendar maintained by `PennyUniverseService`).
- A single public predicate `IsExcluded(ticker, now) bool` consumed by `PennyUniverseService.filter()`.

`PennyUniverseService` takes the new service via a small interface and calls `IsExcluded` on each universe item during filtering.

```
        ┌──────────────────────────┐         ┌──────────────────────────┐
FMP ──► │ EarningsCalendarService  │ ◄────── │   PennyUniverseService   │
Alpaca ►│ • daily ET-day refresh   │         │   .filter()              │
        │ • IsExcluded(t, now)     │         │   skips when IsExcluded  │
        │ • WaitForFirstRefresh    │         │                          │
        └──────────────────────────┘         └──────────────────────────┘
```

---

## 2. Constants

All package-level constants in `services/penny_earnings_service.go`:

| Constant | Value | Purpose |
|---|---|---|
| `earningsExclusionDays` | `3` | Trading days before effective earnings date to exclude. |
| `staleThreshold` | `36 * time.Hour` | Threshold past which cached data is considered stale. |
| `staleWarnInterval` | `4 * time.Hour` | Minimum time between stale-data warning logs. |
| `earningsFetchHorizonDays` | `10` | Calendar days ahead to fetch earnings. |
| `calendarFetchHorizonDays` | `14` | Calendar days ahead to fetch Alpaca trading-day calendar. |
| `FirstRefreshWaitTimeout` | `5 * time.Second` | Exported (consumed by `cmd/bot/main.go`). Max wait for first refresh during startup before falling back to fail-open. |
| `refreshCheckInterval` | `1 * time.Hour` | Wake-up interval for the refresh loop (it actually refreshes only once per ET calendar day). |

The exclusion window is fixed at 3 trading days, not configurable via `config/config.go`. Add to `config/config.go` only if the user later requests tunability.

---

## 3. Data Model

### 3.1 `earningsEntry` (private)

```go
type earningsEntry struct {
    Ticker string
    Date   time.Time // company-local report date, parsed in nyLoc
    Time   string    // "bmo" | "amc" | "" (other values normalized to "")
}
```

### 3.2 FMP response shape

`GET https://financialmodelingprep.com/api/v3/earning_calendar?from=YYYY-MM-DD&to=YYYY-MM-DD&apikey=...`

Returns an array of objects. Only three fields are read; all others are ignored:

```json
[
  {"symbol": "ABCD", "date": "2026-05-12", "time": "amc"},
  {"symbol": "EFGH", "date": "2026-05-13", "time": "bmo"},
  ...
]
```

### 3.3 Trading-day calendar

Stored as `[]AlpacaCalendarEntry` (existing type from `penny_universe_service.go`). The earnings service fetches `start=todayET` and `end=todayET+calendarFetchHorizonDays` once per ET-day refresh. This is independent of the single-day calendar maintained by `PennyUniverseService`.

### 3.4 In-memory cache (private fields on `EarningsCalendarService`)

```go
mu                  sync.RWMutex
entries             map[string]earningsEntry  // ticker -> earliest upcoming entry
calendar            []AlpacaCalendarEntry     // sorted ascending by date
lastRefreshETDate   string                    // "2006-01-02" in nyLoc
lastRefresh         time.Time                 // wall-clock of last successful refresh
lastWarnTime        time.Time                 // shared throttle for stale + empty-calendar warns
firstRefreshDone    chan struct{}             // closed once after first successful refresh
firstRefreshOnce    sync.Once
```

---

## 4. `EarningsCalendarService` API

### 4.1 Constructor

```go
func NewEarningsCalendarService(
    fmpAPIKey, alpacaAPIKey, alpacaSecretKey, alpacaBaseURL string,
    httpClient *http.Client,
) *EarningsCalendarService
```

Mirrors `NewPennyUniverseService` parameter style. If `httpClient` is nil, defaults to `&http.Client{Timeout: 20 * time.Second}`. If `alpacaBaseURL` is empty, defaults to `"https://paper-api.alpaca.markets"`. The struct holds its own `fmpBaseURL = "https://financialmodelingprep.com"` (overridable in tests via direct field assignment, matching the existing pattern in `penny_universe_service_test.go:42`). Initializes `firstRefreshDone = make(chan struct{})`.

### 4.2 `Start(ctx context.Context)`

Calls `s.refresh()` once synchronously, then enters a loop driven by a `time.NewTicker(refreshCheckInterval)`. On each tick:

```go
todayET := time.Now().In(nyLoc).Format("2006-01-02")
if todayET != s.getLastRefreshETDate() {
    s.refresh()
}
```

Exits cleanly on `ctx.Done()`. Wakes hourly but actual refresh fires only on ET-day rollover, ensuring at most one refresh per ET calendar day and a consistent post-overnight pickup.

### 4.3 `IsExcluded(ticker string, now time.Time) bool`

Pure predicate. Steps:

1. Acquire `mu.RLock()`; capture pointers/copies of `entries`, `calendar`, `lastRefresh`. Release lock.
2. If `lastRefresh.IsZero()` → return `false` (never populated; fail open silently).
3. If `time.Since(lastRefresh) > staleThreshold` → emit a throttled warn log (gated by `lastWarnTime`, max once per `staleWarnInterval`) and continue applying cached data.
4. Look up `entries[ticker]`. If absent → return `false`.
5. Compute `effective := effectiveDate(entry, calendar)`.
   - If `calendar` is empty → emit throttled warn and return `false` (fail open; can't compute trading-day distance).
6. Convert `now` to ET; let `nowDate = nowET.Date()`.
7. If `effective` is before `nowDate` → return `false` (already happened).
8. Compute trading-day distance from `nowDate` to `effective`:
   - If `effective == nowDate` → distance = `0`
   - Else → distance = number of trading days in `calendar` strictly after `nowDate` and on or before `effective`
9. Return `distance <= earningsExclusionDays`.

**Worked examples** (all assuming a Mon–Fri trading-day calendar with no holidays):

| Today | Effective date | Distance | Excluded? |
|---|---|---|---|
| Mon | Mon (BMO same-day) | 0 | yes |
| Mon | Tue | 1 | yes |
| Mon | Thu | 3 | yes |
| Mon | Fri | 4 | no |
| Fri | next Mon | 1 | yes |
| Fri | next Wed | 3 | yes |

### 4.4 `WaitForFirstRefresh(timeout time.Duration) bool`

```go
func (s *EarningsCalendarService) WaitForFirstRefresh(timeout time.Duration) bool {
    select {
    case <-s.firstRefreshDone:
        return true
    case <-time.After(timeout):
        return false
    }
}
```

Returns true if the first successful refresh has completed. `refresh()` calls `s.firstRefreshOnce.Do(func() { close(s.firstRefreshDone) })` only on the first fully-successful refresh (FMP earnings *and* Alpaca calendar both succeeded).

### 4.5 `refresh()` (private)

Behavior:

1. Compute `todayET` and the fetch window dates.
2. Fetch FMP earnings calendar (`{fmpBaseURL}/api/v3/earning_calendar?from={today}&to={today+10d}&apikey={key}`):
   - HTTP error / non-200 / unparseable JSON → log `Warn` with status; **keep prior cache**; do not advance `lastRefreshETDate` and do not signal `firstRefreshDone`. Return.
3. Fetch Alpaca calendar (`{alpacaBaseURL}/v2/calendar?start={today}&end={today+14d}` with auth headers):
   - HTTP error / non-200 / unparseable / empty → log `Warn`; **keep prior calendar cache**; the earnings cache may still be updated below, but `firstRefreshDone` is *not* signaled. Note: if calendar fails repeatedly the service operates in the "earnings populated, calendar empty" partial-data state per §6.
4. Parse FMP entries:
   - Skip entries with malformed dates, increment a local `skipped` counter.
   - Normalize `time` field: lowercase before comparison; if the lowercased value is not `"bmo"` or `"amc"` → set to `""`.
   - Filter to entries where the entry date (in ET) is on or after `todayET` (drop already-past).
   - Build a fresh `map[string]earningsEntry` keyed by ticker, keeping the **earliest** upcoming `Date` per ticker (handles duplicate/amendment entries from FMP).
5. Acquire `mu.Lock()`. The FMP fetch succeeded (otherwise we returned at step 2), so always swap `entries`. Swap `calendar` only if the Alpaca fetch in step 3 also succeeded; otherwise leave the prior `calendar` in place. Set `lastRefresh = time.Now()` and `lastRefreshETDate = todayET` regardless. Release lock.
6. Log `Info`: `"earnings refresh: parsed N entries, skipped M malformed, calendar covers <date1>..<dateN>"`.
7. If both fetches succeeded, signal `firstRefreshDone` via the `sync.Once`.

**HTTP work happens outside the write lock.** The lock is acquired only for the final swap.

### 4.6 `effectiveDate(entry earningsEntry, calendar []AlpacaCalendarEntry) time.Time` (private)

- If `calendar` is empty → return `entry.Date` unchanged (caller will treat as partial-data).
- If `entry.Time == "amc"` → return the **first** trading day in `calendar` strictly after `entry.Date`.
- Else (`bmo` or `""`) → return the **first** trading day in `calendar` on or after `entry.Date`.
- If no qualifying trading day exists in `calendar` → return `entry.Date` unchanged.

All date comparisons use ET (`nyLoc`).

---

## 5. Universe Service Integration

### 5.1 New interface

In `services/penny_universe_service.go` (top-of-file, near `AlpacaCalendarEntry`):

```go
type EarningsCalendarChecker interface {
    IsExcluded(ticker string, now time.Time) bool
}
```

### 5.2 Constructor signature change

```go
func NewPennyUniverseService(
    fmpAPIKey, alpacaAPIKey, alpacaSecretKey, alpacaBaseURL string,
    earnings EarningsCalendarChecker,
    httpClient *http.Client,
) *PennyUniverseService
```

`earnings` may be `nil` — exclusion is then skipped. Nil-safety preserves backward-compatibility with struct-literal usage in `controllers/penny_controller_test.go:32`.

### 5.3 `filter()` change

Return signature changes from `[]UniverseSymbol` to `([]UniverseSymbol, int)` where the second value is `excludedForEarnings`. Inside the loop, after the existing dollar-volume check and before `out = append(...)`:

```go
if s.earnings != nil && s.earnings.IsExcluded(item.Symbol, time.Now()) {
    excludedForEarnings++
    continue
}
```

`refresh()` updates its log line to include `excluded_for_earnings: K` alongside the existing `count` field.

### 5.4 No other behavior changes

- Universe refresh cadence unchanged (5/30/60-min by phase).
- Aggregator path unchanged — it consumes the universe via `GetUniverse()` / `GetTickers()`.
- MCP layer (`mcp-server.js:1390`, `get_penny_universe`) unchanged — universe data shape is identical.

---

## 6. Failure-Mode Decision Matrix

`IsExcluded` behavior under each cache state:

| Earnings cache | Calendar cache | Returns | Side effect |
|---|---|---|---|
| Empty (`lastRefresh.IsZero()`) | — | `false` | None (silent fail-open) |
| Populated, fresh (<36h) | Empty | `false` | Throttled warn |
| Populated, fresh (<36h) | Populated | normal logic | None |
| Populated, stale (>36h) | Populated | normal logic | Throttled warn |
| Populated, stale (>36h) | Empty | `false` | Throttled warn |

All warns are gated by a single shared `lastWarnTime` to fire at most once per `staleWarnInterval`. A state that simultaneously triggers multiple warn types (e.g., stale + empty calendar) emits at most one warn per interval.

---

## 7. Wiring in `cmd/bot/main.go`

Insertion sequence (before existing `pennyUniverseService := services.NewPennyUniverseService(...)` line at `cmd/bot/main.go:161`):

```go
earningsService := services.NewEarningsCalendarService(
    cfg.FMPAPIKey, cfg.AlpacaAPIKey, cfg.AlpacaSecretKey, cfg.AlpacaBaseURL, nil,
)
go earningsService.Start(ctx)

if !earningsService.WaitForFirstRefresh(services.FirstRefreshWaitTimeout) {
    logger.Warn("earnings calendar first refresh did not complete within timeout — universe will start in fail-open mode")
}

pennyUniverseService := services.NewPennyUniverseService(
    cfg.FMPAPIKey, cfg.AlpacaAPIKey, cfg.AlpacaSecretKey, cfg.AlpacaBaseURL,
    earningsService, nil,
)
```

The wait is bounded; on miss, the universe still starts (fail-open).

---

## 8. Testing

### 8.1 New file: `services/penny_earnings_service_test.go`

**Pure unit tests (no I/O):**

- `effectiveDate_BMO_returnsCalendarDate`
- `effectiveDate_AMC_returnsNextTradingDay`
- `effectiveDate_AMCFriday_returnsMonday` (with stub calendar that includes Mon but not Sat/Sun)
- `effectiveDate_AMCBeforeHoliday_skipsHoliday`
- `effectiveDate_emptyTime_treatedAsBMO`
- `effectiveDate_unknownTime_treatedAsBMO`
- `effectiveDate_emptyCalendar_returnsEntryDateUnchanged`
- `IsExcluded_unknownTicker_returnsFalse`
- `IsExcluded_inWindow_returnsTrue` (effective date 0/1/2/3 trading days out)
- `IsExcluded_outsideWindow_returnsFalse` (4+ trading days out)
- `IsExcluded_pastEarnings_returnsFalse`
- `IsExcluded_neverRefreshed_returnsFalse`
- `IsExcluded_emptyCalendar_returnsFalse_andWarns`
- `IsExcluded_staleData_emitsWarn_andStillExcludes`
- `IsExcluded_staleData_warnsAtMostOncePerInterval`
- `IsExcluded_emptyCalendar_warnsAtMostOncePerInterval`
- `IsExcluded_staleAndEmptyCalendar_emitsAtMostOneWarnPerInterval` (shared throttle)

**HTTP tests using `httptest.Server`:**

- `refresh_success_populatesEntriesAndCalendar`
- `refresh_success_signalsFirstRefreshDone`
- `refresh_FMPNon200_keepsPriorCache_doesNotSignalFirstRefresh`
- `refresh_FMPMalformedJSON_keepsPriorCache`
- `refresh_AlpacaCalendarFails_keepsPriorCalendar_doesNotSignalFirstRefresh`
- `refresh_duplicateEntries_keepsEarliestUpcoming`
- `refresh_pastDateEntries_dropped`
- `refresh_unknownTimeField_normalizedToEmpty`
- `refresh_runsHTTPOutsideLock` (slow handler held open while concurrent reads succeed)
- `WaitForFirstRefresh_returnsTrueAfterFirstSuccess`
- `WaitForFirstRefresh_returnsFalseOnTimeout`
- `Start_refreshesOnETDayRollover` (advance a fake clock past midnight ET; verify second refresh fires)

**Stub helper (in test file):**

```go
type stubEarningsChecker struct {
    excluded map[string]bool
}
func (s *stubEarningsChecker) IsExcluded(ticker string, _ time.Time) bool {
    return s.excluded[ticker]
}
```

### 8.2 Modified: `services/penny_universe_service_test.go`

Two breaking changes need test updates:

1. Existing `NewPennyUniverseService(...)` calls need the new `earnings` parameter — pass `nil` in tests that don't exercise exclusion.
2. The `filter()` method's return signature changes from `[]UniverseSymbol` to `([]UniverseSymbol, int)` — every existing test that destructures `filter()` must be updated to consume the new return value (use `_` if not asserting on it).

**New cases:**

- `filter_excludesTickersWhenCheckerReturnsTrue`
- `filter_includesTickersWhenCheckerReturnsFalse`
- `filter_nilChecker_doesNotExclude`
- `filter_returnsExcludedForEarningsCount`

### 8.3 Other test files

- `controllers/penny_controller_test.go:32` uses `&services.PennyUniverseService{}` (struct literal). The new `earnings` field stays at the nil zero-value, exclusion correctly skipped. **No change required.**

### 8.4 No integration test for `cmd/bot/main.go` wiring

End-to-end test infrastructure for the bot binary is not established in this repo. Compilation + manual smoke-run is the verification.

---

## 9. Logging

| Location | Level | Format |
|---|---|---|
| `EarningsCalendarService.refresh()` success | `Info` | `"earnings refresh: parsed N entries, skipped M malformed, calendar covers <d1>..<dN>"` |
| `EarningsCalendarService.refresh()` FMP failure | `Warn` | `WithError(err).WithField("status", code).Warn("EarningsCalendarService: FMP earnings fetch failed")` |
| `EarningsCalendarService.refresh()` Alpaca failure | `Warn` | `WithError(err).WithField("status", code).Warn("EarningsCalendarService: Alpaca calendar fetch failed")` |
| `EarningsCalendarService.IsExcluded()` stale | `Warn` (throttled) | `"earnings calendar is stale (last refresh > 36h ago) — still applying cached exclusions"` |
| `EarningsCalendarService.IsExcluded()` calendar empty | `Warn` (throttled) | `"earnings calendar trading-day cache empty — exclusion temporarily disabled"` |
| `PennyUniverseService.refresh()` success | `Info` | existing log + `excluded_for_earnings: K` field |

---

## 10. Out of Scope

The following are explicitly deferred and are not part of this implementation:

- Auto-flatten of held positions before earnings.
- Configurable exclusion window via `config/config.go`.
- BMO/AMC-aware *intraday* trading allowance (e.g., "AMC reports → allow entry until 15:50 ET with auto-flatten by close"). Current design treats any earnings within the window as full-day exclusion.
- Persistence across bot restarts.
- Admin "refresh now" endpoint.
- Symbol-format normalization (e.g., `BRK.B` vs `BRK-B`).
- Metrics / tracing emission. Logging only.
- Concurrent-refresh guard. Single refresh loop only; if a future feature triggers ad-hoc refreshes, add a guard then.

---

## 11. Backwards Compatibility

- `NewPennyUniverseService` signature gains one parameter (the `earnings` checker). All call sites updated:
  - `cmd/bot/main.go:161` — wiring change per §7.
  - `services/penny_universe_service_test.go` — pass `nil` or stub.
- `controllers/penny_controller_test.go:32` uses a struct literal; no signature exposure, no change.
- MCP / JS layer unchanged.
- `config/config.go` unchanged (no new config fields).

---

## 12. Operational Notes

- This feature requires the FMP `/api/v3/earning_calendar` endpoint with multi-day date-range support. The existing skill `.claude/skills/earnings-calendar/scripts/fetch_earnings_fmp.py` already uses this endpoint, confirming availability on the current plan.
- Expected daily API call cost: 1 FMP earnings call + 1 Alpaca calendar call per ET-day. Negligible against the FMP starter plan's 300 RPM ceiling.
- Memory footprint: ~50KB upper bound (10-day earnings horizon × ~100 reports/day × ~50 bytes/entry).
