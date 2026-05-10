# PennyProphet Dilution Filter — Design

**Date:** 2026-05-10
**Status:** Approved, ready for implementation plan
**Owner:** PennyProphet

## Motivation

Penny stocks are uniquely vulnerable to toxic financing events: overnight S-1
or S-3 filings for convertible notes, warrant re-pricings, at-the-market
offerings, or 8-K-announced PIPE deals that crush retail longs the next
morning. The signal pipeline can produce a textbook 90-point composite
(volume spike + positive PR + StockTwits buzz) on a ticker that filed an
S-3 take-down after-hours, and the agent will buy into a guaranteed
gap-down. No signal quality survives a dilution event.

The dilution filter is **capital protection, not alpha generation**. Its job
is to remove tickers from the candidate pool whenever a recent SEC filing
indicates active or imminent equity dilution. Existing positions are
informed but not auto-exited; the dominant-signal stop-loss rules remain
the exit authority.

## Scope

In scope:
- Detection of recent dilution filings on universe tickers via SEC EDGAR
  ATOM feeds (existing `SECEdgarService` infrastructure).
- A per-ticker block list inside `SECEdgarService`, queried by the
  `PennySignalAggregator` before returning candidates.
- A heuristic 8-K item-number scan (title + summary) layered on top of the
  existing 8-K poll.
- Shadow-mode rollout (logger-only) followed by enforcement after
  one trading day of clean operator review.

Out of scope (explicit non-goals):
- 8-K body fetching and full-document parsing (deferred to v2 if heuristic
  miss rate proves material).
- Auto-exit of existing managed positions on new dilution filings (handled
  by existing dominant-signal stop rules).
- Score-penalty mechanism for any form type (rejected during design — all
  forms produce hard blocks, only the window length differs).
- Form 4 insider-cluster signals (separate spec; this filter is dilution
  only).

## Decisions Locked During Brainstorming

These were resolved through user selection during the brainstorming pass.
Recorded here so future readers do not re-litigate them:

1. **All matched filings produce hard blocks.** No score-penalty
   mechanism. Bare S-3 / F-3 shelves are blocked for a longer window
   rather than scored down. Rejected the tiered penalty hybrid because
   the math (max raw composite = 100, penalty = 40, threshold = 60) made
   the penalty mathematically equivalent to a block in all but a single
   unreachable edge case.
2. **S-3/A is shelf, not take-down.** Amendments are typically
   SEC-required corrections, not market-side actions. The actual
   take-down event is the 424B prospectus supplement.
3. **Block ≠ exit.** A dilution block prevents new entries on a ticker
   but does not force-exit an existing managed position. The dominant-signal
   stop rules already cover gap-down protection.
4. **Architecture: extend `SECEdgarService`.** Don't create a separate
   service. The dilution data IS EDGAR data; keeping it together avoids
   duplicating polling infrastructure.
5. **Ship 8-K detection as a title/summary keyword heuristic.** Body
   fetching is deferred. Acknowledged false-negative cases are documented
   below, not silently ignored.

## Form-Type Coverage

| Form `type=` | Bucket | Window | Notes |
|---|---|---|---|
| `S-1` | takedown | 2 trading days | Catches S-1 and S-1/A |
| `S-3` | shelf | 5 trading days | Catches S-3 and S-3/A; both go in shelf bucket |
| `424` | takedown | 2 trading days | Catches 424B2/B3/B4/B5 prospectus supplements |
| `F-1` | takedown | 2 trading days | Foreign issuer initial registration |
| `F-3` | shelf | 5 trading days | Foreign issuer shelf |
| `8-K` (heuristic) | takedown | 2 trading days | Item-number + keyword pattern match on title+summary; piggybacks on existing 8-K poll |

Trading-day distance is computed using the existing `tradingDayDistance`
helper at `services/penny_earnings_service.go:169`. This is a package-level
function in the same `services` package, so `SECEdgarService` can call it
directly with no extraction needed. The helper requires an Alpaca calendar
(`[]AlpacaCalendarEntry`); see the "Calendar dependency" subsection under
Components for how `SECEdgarService` obtains it.

### 8-K Heuristic Patterns

Run on `strings.ToUpper(title + " " + summary)`:

```
ITEM 1.01    + (SECURITIES PURCHASE AGREEMENT, EQUITY PURCHASE,
                STANDBY EQUITY, ATM OFFERING, AT-THE-MARKET,
                REGISTERED DIRECT, SHELF TAKEDOWN)
ITEM 3.02    (always — 3.02 is "Unregistered Sales of Equity Securities")
ITEM 8.01    + (PUBLIC OFFERING, PRIVATE PLACEMENT, PIPE FINANCING,
                WARRANT, CONVERTIBLE NOTE)
PRICING OF   + PUBLIC OFFERING
COMMENCEMENT OF + ATM
```

Match → block, bucket=takedown, FormType=`8-K-{itemNo}` for log clarity.

**Acknowledged false negatives** (will not be caught day-1):
- 8-Ks where the issuer omitted the item number from the title and used
  no trigger keywords (e.g., a vague "Strategic Financing Update" title).
- These are a minority of dilution events. The S-1/S-3/424B/F-1/F-3
  registration-side blocks remain the primary defense.

## Components

### Data model — added to `services/sec_edgar_service.go`

```go
type dilutionEntry struct {
    Ticker    string
    FormType  string    // "S-1", "S-3", "424B5", "8-K-3.02", etc.
    FiledAt   time.Time // best-effort from feed timestamp
    Bucket    string    // "takedown" or "shelf"
    SourceURL string    // EDGAR filing URL (for log audit trail)
}

type SECEdgarService struct {
    // ...existing fields...
    dilutionMu     sync.RWMutex
    dilutionBlocks map[string]dilutionEntry  // keyed by ticker
}
```

**Replacement rule** (when a new filing lands on a ticker that already
has an entry): if the new filing's bucket is more conservative (takedown
> shelf), replace; if same bucket, replace (refreshes the window); if
new is shelf and existing is takedown, keep existing (don't downgrade).

**Eviction**: lazy on-read inside `IsDilutionBlocked`. If
`tradingDayDistance(entry.FiledAt, now, calendar)` exceeds the bucket's
window, the entry is removed and `false` returned. No separate sweeper
goroutine.

### Calendar dependency

The trading-day eviction logic and the existing earnings exclusion logic
both need the Alpaca trading calendar. Today, `EarningsCalendarService`
fetches and caches it (`calendar []AlpacaCalendarEntry`). To avoid a
second independent fetch from `SECEdgarService`, the constructor signature
changes:

```go
func NewSECEdgarService(
    universe *PennyUniverseService,
    httpClient *http.Client,
    operatorEmail string,
    earnings *EarningsCalendarService,  // NEW — for calendar access
) *SECEdgarService
```

`EarningsCalendarService` exposes a thin getter:

```go
func (s *EarningsCalendarService) Calendar() []AlpacaCalendarEntry
```

`SECEdgarService` calls `s.earnings.Calendar()` at eviction-check time.
This keeps a single source of truth for the calendar and avoids
duplicative HTTP load. Initialization order in `cmd/bot/main.go` becomes:
`EarningsCalendarService` first, then `SECEdgarService`.

### Clock injection

`SECEdgarService` does not currently support clock injection (no
`nowFunc` field exists). Add one as part of this work:

```go
type SECEdgarService struct {
    // ...existing fields...
    nowFunc func() time.Time  // defaults to time.Now in constructor
}
```

All `time.Now()` calls inside the service route through `s.nowFunc()`.
This is required for deterministic eviction tests (advance the clock past
window expiry, assert eviction). The constructor sets
`nowFunc: time.Now` by default; tests override.

### Public API

```go
// IsDilutionBlocked returns (true, reason) if ticker has an unexpired
// dilution block, where reason is a short human string for logging.
// Returns (false, "") otherwise.
func (s *SECEdgarService) IsDilutionBlocked(ticker string) (bool, string)
```

### Polling strategy

Two independent goroutines launched from `Start(ctx)`:

1. **Existing `pollLoop`** at `regulatoryRefreshInterval` (30s):
   - Continues to do the positive-signal 8-K poll (writes to `entries` map).
   - **Adds a second pass** on the same fetched 8-K entries, applying the
     heuristic patterns above, writing to `dilutionBlocks` on match.
   - Free piggyback — no extra HTTP.
2. **New `dilutionPollLoop`** at `dilutionRefreshInterval` (5 min):
   - Fans out 5 concurrent EDGAR fetches (S-1, S-3, 424, F-1, F-3) via
     goroutines + `sync.WaitGroup`.
   - Each fetch reuses the existing `fetchAtom` helper.
   - Per-form failures are logged and isolated; one form's failure does
     not block the others.

**Rate-limit posture**: 6 polls × every 5 min = 72 calls/hour from this
loop, well below EDGAR's informal politeness threshold and far below the
10 req/sec hard limit.

**Failure semantics**: a blanket EDGAR outage degrades us toward "no new
dilution blocks added" while existing blocks remain in place until their
windows expire. This is fail-closed for the capital-protection direction
(we keep blocking when uncertain, never start permitting when uncertain).

## Aggregator Integration

Inside `PennySignalAggregator.GetCandidates`, after the composite ≥
minScore filter and after the existing `blacklist.IsBlacklisted` check:

```go
if blocked, reason := a.edgar.IsDilutionBlocked(c.Ticker); blocked {
    a.logger.WithFields(logrus.Fields{
        "ticker": c.Ticker,
        "composite": c.CompositeScore,
        "reason": reason,
    }).Info("dilution block: candidate suppressed")
    continue
}
```

The candidate is dropped from the returned slice. The composite score is
not modified — `get_penny_signal_detail` on a blocked ticker still returns
the score breakdown for audit, but the ticker never appears in
`get_penny_candidates`.

### Lock ordering

Extends the existing rule documented in `penny_signal_aggregator.go:24-27`:

> `PennySignalAggregator.mu` → `BracketBlacklist.mu` → `SECEdgarService.dilutionMu`

`IsDilutionBlocked` only takes `dilutionMu.RLock` and never calls back
into the aggregator. No cycle is possible. The lock-ordering comment in
`penny_signal_aggregator.go` will be updated to include the new lock.

## Logging — Three Event Sites

1. **On block creation** (in `SECEdgarService` when a new dilution
   filing is detected):
   ```
   level=info msg="dilution block created" ticker=ABCD form=S-3 bucket=shelf
                 filed_at=2026-05-09T16:42:00Z expires_in_trading_days=5
                 source_url=https://www.sec.gov/...
   ```
   Heuristic-source matches additionally include `matched_pattern="ITEM 3.02"`
   and `source=heuristic` for later audit.

2. **On candidate suppression** (in aggregator when a would-be candidate
   is dropped):
   ```
   level=info msg="dilution block: candidate suppressed"
                 ticker=ABCD composite=72 reason="S-3 shelf filed 3 trading days ago"
   ```
   Operational telemetry only — does not write `log_decision`. The agent
   never saw this candidate; there is no decision to record.

3. **On block-during-managed-position** (NEW high-severity event): if a
   dilution block lands on a ticker the agent is currently holding,
   emit a HIGH-severity log line that the `agent-health-penny` skill
   surfaces in operator review. The agent does NOT auto-exit. The
   existing dominant-signal stop rules remain the exit authority.

## Behavior on Existing Held Positions

A dilution block is a **no-new-entries** rule, not an automatic
liquidation rule. When a new dilution filing is detected on a ticker
the agent already holds:

- The existing managed position is left untouched.
- Stop and target levels are not modified.
- No sell order is placed.
- A high-severity log entry is emitted (event site #3 above).
- Standard dominant-signal exit rules continue to apply normally.

Rationale:
- The dominant-signal stop (−7% to −10% depending on signal type) will
  trip on any real take-down gap-down, providing capital protection
  through the existing exit path.
- Adding a forced-exit path introduces race-condition complexity
  against the bracket order's stop and target legs (mirrors the
  social-exit cancel-then-market-sell choreography in
  `TRADING_RULES_PENNY.md:225-244`), which is non-trivial to test
  correctly and unnecessary given the existing stop coverage.
- "Log loudly + let stops do their job" is consistent with the
  rule-executor philosophy.

## Testing

### Unit tests in `services/sec_edgar_service_test.go`

Fixtures in `services/testdata/edgar/dilution/`:

- `s1-fixture.atom` — universe ticker in S-1 title → expect block,
  bucket=takedown, 2-day window.
- `s3-shelf-fixture.atom` — universe ticker in S-3 title → expect block,
  bucket=shelf, 5-day window.
- `s3-amendment-fixture.atom` — title contains "S-3/A" → expect block,
  bucket=shelf (NOT takedown — verifies the amendment-isn't-a-takedown
  decision).
- `424b5-fixture.atom` — universe ticker in 424B5 title → expect block,
  bucket=takedown.
- `f3-fixture.atom` — foreign issuer F-3 → expect block, bucket=shelf.
- `8k-item-302-fixture.atom` — 8-K with "Item 3.02" in summary → expect
  heuristic block.
- `8k-item-101-spa-fixture.atom` — 8-K with "Item 1.01" + "SECURITIES
  PURCHASE AGREEMENT" → expect heuristic block.
- `8k-item-101-asset-purchase-fixture.atom` — 8-K with "Item 1.01" +
  "ASSET PURCHASE AGREEMENT" only. **Must NOT contain any equity
  keywords from the pattern list** (constraint documented in the fixture
  file as a comment). → expect no block (false-positive guard).
- `8k-item-101-licensing-fixture.atom` — Item 1.01 entry for a software
  licensing deal, no equity component. → expect no block (additional
  false-positive guard).
- `8k-vague-financing-fixture.atom` — 8-K title "Strategic Financing
  Update", no item number, no keywords → expect no block (acknowledged
  false negative; documents the gap in code).
- `non-universe-ticker-fixture.atom` — dilution filing for ticker not in
  universe → expect no block (universe gating works).

Behavioral tests (no HTTP):

- `IsDilutionBlocked` returns `(false, "")` for unknown ticker.
- `IsDilutionBlocked` returns `(true, reason)` for blocked ticker
  within window.
- `IsDilutionBlocked` lazily evicts and returns `(false, "")` after
  window expires (uses the `nowFunc` injectable clock added in the
  Components section).
- Takedown filing replaces an active shelf entry on same ticker.
- Shelf filing does NOT replace an active takedown entry on same ticker.
- Concurrent-access race test (`go test -race`) on `dilutionMu`.

### Aggregator tests in `services/penny_signal_aggregator_test.go`

- Candidate with composite ≥ 60 + dilution block → not returned by
  `GetCandidates`.
- Candidate with composite ≥ 60 + no dilution block → returned as today.
- Lock-ordering smoke test: concurrent `GetCandidates` + `IsDilutionBlocked`
  calls under `-race`.

### Position-manager test (regression-prevention) in `services/position_manager_test.go`

`TestDilutionBlockDoesNotForceExit`:
- Create a managed penny position at entry $5.00, stop $4.60, target $5.75.
- Fire a dilution block on the same ticker via `SECEdgarService`.
- Tick the position manager through one heartbeat.
- Assert: position still open, stop and target unchanged, no sell order placed.
- Assert: dilution block IS logged at high severity (operator-visible).

This test exists to pin down an *absence* of behavior — preventing a
future change from accidentally wiring dilution blocks into exit logic.

## Rollout

**Shadow mode first (1 trading day):**
- `IsDilutionBlocked` returns `(true, reason)` to logger only.
- Aggregator does NOT yet drop the candidate.
- Operator reviews shadow logs against actual filings to audit
  false-positive rate before any candidate suppression goes live.
- Toggle: env var `PENNY_DILUTION_FILTER_MODE=shadow|enforce`.

**Enforce mode** after one trading day of shadow logs reviewed clean:
- Operator flips env var to `enforce`.
- Aggregator begins suppressing blocked candidates.

Default ships as `shadow`. Promotion to `enforce` requires explicit
operator action.

## Documentation Updates

- **`TRADING_RULES_PENNY.md`** — add a new section "Dilution Filter"
  documenting the form types, windows, and the "block ≠ exit" rule.
- **`data/agent-config.json` `customRules`** — same content, since
  that's the authoritative rule source per the existing note in
  `TRADING_RULES_PENNY.md:3-8`.

## Future Work (Explicitly Out of Scope for This Spec)

- v2: 8-K body-fetch path if heuristic miss rate proves material in
  shadow / enforce logs.
- Form 4 insider-cluster scoring (orthogonal alpha signal — separate spec).
- Short-interest mapping (squeeze-proxy signal — separate spec).
- FDA / PDUFA calendar integration (biotech catalyst leg — separate spec).
- Premarket gap scanner (technical-leg timing — separate spec).
- Regime gate (SPY/VIX hard gate — separate spec, requires design
  discussion on layer placement).
