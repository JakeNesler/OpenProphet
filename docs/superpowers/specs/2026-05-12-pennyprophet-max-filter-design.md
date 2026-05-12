# PennyProphet MAX Filter (Shadow Mode) — Design

**Date:** 2026-05-12
**Status:** Approved, ready for implementation plan
**Owner:** PennyProphet

## Motivation

Bali, Cakici, and Whitelaw (2011), *"Maxing Out: Stocks as Lotteries and the
Cross-Section of Expected Returns"* (JFE), document a robust negative-alpha
relationship between the maximum single-day return over the prior 21 trading
days (MAX) and subsequent monthly returns. The effect is strongest in the
exact universe PennyProphet trades: low-priced, retail-attention-driven,
high-idiosyncratic-volatility stocks. Holding a high-MAX stock at month-start
is statistically a losing trade in the cross-section.

PennyProphet currently has no filter for "the lottery has already paid out."
A composite score ≥ 60 fires equally whether the ticker just printed a +5%
day or a +35% day. Anecdotally, post-pump entries are a meaningful slice of
PennyProphet's worst trades. This spec adds a measurement-only shadow filter
so that decision can be made from data four weeks from now rather than from
folklore.

The MAX filter is **defensive, not alpha-generating**. Its job, if validated,
would be to remove tickers from the candidate pool after their lottery
moment has already happened.

## Scope

In scope:
- A new `PennyMaxFilterService` that computes per-ticker 21-session MAX
  values once daily for the entire penny universe.
- An aggregator-side log line emitted for every candidate that passes
  composite + blacklist + dilution checks, recording the MAX value and
  several pre-computed threshold flags.
- Shadow-mode-only behavior on day one. The env var defaults to `shadow`
  and no candidate suppression happens.
- A documented four-week validation procedure that crosses the shadow logs
  against `activity_logs/` trade outcomes to make the enforce decision.

Out of scope (explicit non-goals):
- Enforcement on day one. Promotion to `enforce` is a separate operator
  decision after the four-week review.
- Intraday MAX recalculation. A ticker that rips 30% after the daily
  refresh will not get a fresh MAX value that day. Acceptable for v1; if
  validation succeeds, intraday refresh becomes v2.
- Auto-exit of existing managed positions when MAX crosses a threshold.
  Same "block ≠ exit" principle as the dilution filter — exits stay with
  the dominant-signal stop rules.
- A score-penalty mechanism. Same reasoning as the dilution filter — at
  realistic threshold values, a penalty large enough to matter is
  mathematically equivalent to a binary block.
- Sector or volatility-adjusted MAX variants. Paper-faithful first; any
  refinements are v2.

## Decisions Locked During Brainstorming

Recorded here so future readers do not re-litigate them:

1. **Lookback is 21 trading sessions.** Matches Bali, Cakici, Whitelaw
   (2011) exactly. The paper's effect is on next-month returns, while
   PennyProphet trades 1–5 days. The shadow window exists precisely to
   discover whether the academic signal transfers to PennyProphet's
   horizon. Picking a shorter (e.g., 5-day) window would test a different,
   less-validated hypothesis.
2. **MAX is paper-faithful close-to-close.** `MAX_t = max over the prior
   21 sessions of (close_d / close_{d-1}) - 1`. Bali et al. use simple
   daily returns; high/prev_close would capture intraday-pop-then-fade
   behavior but diverges from the academic signal. Documented as v2.
3. **Shadow mode default; enforce requires explicit env-var flip.** Same
   pattern as the existing `PENNY_DILUTION_FILTER_MODE`. Default ships as
   shadow; promotion requires operator action after the four-week review.
4. **Separate service, not embedded in `PennyScreenerService`.** The
   screener scans every 60 seconds using snapshots only. MAX needs 21
   days of historical bars per ticker; bundling them would inflate the
   60-second scan's API footprint by ~21x for no benefit. Daily cadence,
   daily service.
5. **Universe-level refresh, not per-candidate lookup.** Pre-compute MAX
   for every universe ticker once per day; aggregator just reads the
   cache. This bounds the API cost at 1× universe size per day instead
   of N× per `GetCandidates` call.
6. **Log raw value + multiple `would_skip_at_X` booleans.** Logging at
   {15%, 20%, 25%} thresholds lets the four-week analysis sweep
   thresholds without re-running the experiment. The 20% center is from
   Bali's top-decile cutoff in the penny price range; the surrounding
   values bracket reasonable alternatives.

## Components

### New service — `services/penny_max_filter.go`

```go
type MaxEntry struct {
    Value      float64   // e.g. 0.32 for a +32% best day
    BestDay    time.Time // which session produced the max
    BarsUsed   int       // 21 normally; less if history is short
    ComputedAt time.Time
}

type PennyMaxFilterService struct {
    universe   *PennyUniverseService
    bars       *AlpacaDataService
    mu         sync.RWMutex
    cache      map[string]MaxEntry  // keyed by ticker
    nowFunc    func() time.Time     // injectable for tests
    logger     *logrus.Logger
    refreshAt  time.Time            // last full-universe refresh
}
```

`MaxEntry` is exported because `GetMax` returns it across the package
boundary to the aggregator.

### Constructor

```go
func NewPennyMaxFilterService(
    universe *PennyUniverseService,
    bars *AlpacaDataService,
) *PennyMaxFilterService
```

`nowFunc` defaults to `time.Now`. `cache` starts empty.

### Refresh strategy

A single `refresh()` method fetches 21 daily bars per universe ticker and
recomputes MAX. Implementation must prefer Alpaca's multi-symbol bars
endpoint (`GetMultiBars`) over sequential per-symbol fetches; the universe
is typically 200–500 tickers and sequential calls would take ~30 seconds
unnecessarily. Multi-bar requests are chunked to stay under Alpaca's
per-request symbol cap.

**Lookback math:** request 30 calendar days of daily bars (cushion for
holidays and weekends). Use the most recent 22 bars to compute 21
returns. If fewer than 22 bars are available (newly listed ticker),
compute MAX over whatever returns exist and record `BarsUsed` < 21 in
the cache entry so log analysis can filter low-confidence values.

**MAX formula** (paper-faithful):

```
returns_d = (close_d / close_{d-1}) - 1, for the most recent 21 sessions
MAX       = max(returns_d)
BestDay   = argmax_d (close_d / close_{d-1}) - 1
```

### Refresh cadence

Two triggers:

1. **Startup refresh.** `Start(ctx)` calls `refresh()` once immediately
   so the cache is non-empty before any `GetMax` calls arrive.
2. **Daily refresh.** A ticker fires at 07:00 ET (pre-market, after
   prior-day EOD bars have fully settled). Implementation uses a
   `time.AfterFunc` chain or a 24-hour `time.Ticker` aligned to 07:00 ET
   on startup. EOD bars are reliably available by ~17:00 ET the prior
   day, so 07:00 ET is safely past the publish horizon.

A blanket Alpaca bars outage during refresh degrades the cache to "stale"
rather than empty — existing entries remain readable. The aggregator's
log line records `computed_at` so stale reads are obvious in the logs.

### Public API

```go
// GetMax returns the cached MAX entry for ticker.
// ok=false when: ticker is not in the universe; the first refresh has
// not yet completed (cache empty); or the ticker had fewer than 2 daily
// bars available (cannot compute even one return).
// ok=true with BarsUsed < 21 indicates a low-confidence value (newly
// listed ticker with limited history); the caller should still log it
// but downstream analysis can filter by BarsUsed.
func (s *PennyMaxFilterService) GetMax(ticker string) (MaxEntry, bool)
```

No public methods modify cache state from outside the service.

**Empty-cache startup behavior:** before the first successful refresh,
`GetMax` returns ok=false for all tickers. The aggregator's
`if entry, ok := ...; ok` guard skips the log line entirely. Operator
sees no MAX log lines until the first refresh completes (typically
within seconds of agent start). This is intentional — emitting a log
line with a zero-value MAX would pollute the four-week dataset.

### Lock ordering

`PennyMaxFilterService.mu` is a leaf lock. It is never acquired while
holding `PennySignalAggregator.mu`. The aggregator's call into `GetMax`
takes `s.mu.RLock` for the duration of one map lookup and releases
before returning. No cycle is possible.

The lock-ordering comment in `penny_signal_aggregator.go:24-30` will be
extended to document this.

## Aggregator Integration

`PennySignalAggregator` gains an optional dependency:

```go
type PennySignalAggregator struct {
    // ...existing fields...
    maxFilter *PennyMaxFilterService  // optional; nil-safe
    maxMode   string                  // "shadow" | "enforce"; default "shadow"
}
```

Constructor signature extends:

```go
func NewPennySignalAggregator(
    universe *PennyUniverseService,
    screener *PennyScreenerService,
    edgar *SECEdgarService,
    social *SocialSignalService,
    maxFilter *PennyMaxFilterService,  // NEW, nil-safe
) *PennySignalAggregator
```

The constructor reads `PENNY_MAX_FILTER_MODE` env var. Any value other
than `"enforce"` resolves to `"shadow"`. Mode is logged at startup,
matching the dilution filter pattern.

Inside `GetCandidates`, after the existing dilution block at
`penny_signal_aggregator.go:149-161` and before `out = append(out, c)`:

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

Shadow mode always logs and never skips. Enforce mode (future, requires
operator flip) suppresses candidates whose 21-session MAX is at least
20% (inclusive — matches PennyProphet's documented boundary convention).

The 20% threshold ships as a hard-coded placeholder. The four-week
validation procedure may shift it; promotion to enforce is therefore a
two-step change: (1) flip env var, (2) edit the hard-coded threshold to
the validated value if different from 20%. Both changes happen at the
same time, gated on the validation memo. Stating this explicitly so the
threshold is never "promoted to enforce as-is" without the validation
step.

When `maxFilter` is nil (e.g., in tests that don't wire it up), this
block is a no-op — preserves backward-compatible aggregator behavior.

## Logging

A single event site emits one structured log line per candidate that
reaches the MAX-check stage of `GetCandidates`. Fields above; severity
is `info`. Operational telemetry only — does not write `log_decision`.
The agent has not yet seen the candidate; there is no decision to record.

This is high-frequency logging. With ~30 candidates per scan and a 60s
aggregate cadence, expect ~2,500–3,000 lines per trading day. Log
volume is acceptable given the structured format and the four-week
validation window. After the enforce decision is made, the line is
retained but log retention can be tuned independently.

## Behavior on Existing Held Positions

Same as the dilution filter: a MAX value crossing any threshold has
**no effect on existing managed positions**. The dominant-signal stop
rules remain the exit authority. The MAX filter is exclusively a
new-entry gate.

No high-severity "MAX threshold crossed on held position" event is
emitted. Unlike a dilution filing, a high MAX is not a real-time event —
it's a slow-moving statistical descriptor. Logging every held
position's daily MAX would produce noise without signal.

## Testing

### Unit tests in `services/penny_max_filter_test.go`

Table-driven tests using synthetic daily bars fed through a fake bars
provider (small interface around `GetMultiBars`):

- **Standard case:** 22 bars, MAX value matches hand-computed expected
  result, `BestDay` matches the correct date, `BarsUsed = 21`.
- **Short history:** 10 bars available, MAX computed over 9 returns,
  `BarsUsed = 9`, no panic.
- **Insufficient history:** 1 bar available, ok=false on `GetMax` (no
  cache entry written).
- **Single-day rip:** prior 20 days flat, 1 day at +40%, MAX = 0.40,
  BestDay correct.
- **Negative-only history:** all returns negative, MAX is the
  least-negative value (paper-faithful — MAX is just the maximum, not
  the max-positive).
- **Cache freshness:** `ComputedAt` is set, `BestDay` is preserved
  through cache round-trips.
- **Clock injection:** advance `nowFunc`, trigger refresh, assert new
  `ComputedAt` reflects the injected time.

### Aggregator tests in `services/penny_signal_aggregator_test.go`

- **Shadow mode never suppresses:** seed candidate with composite=70,
  seed maxFilter with value=0.50, mode=shadow → candidate IS returned;
  log line IS emitted with `would_skip_25pct=true`.
- **Enforce mode suppresses above 20%:** same seed, mode=enforce, value
  0.50 → candidate is NOT returned.
- **Enforce mode passes below 20%:** mode=enforce, value=0.10 →
  candidate IS returned.
- **Nil maxFilter is a no-op:** aggregator constructed with nil filter →
  candidate IS returned, no log line, no panic.
- **Race test:** concurrent `GetCandidates` + `GetMax` under `-race`.

### No position-manager test required

Because the MAX filter never touches managed positions, there is no
"does-not-force-exit" behavior to pin down. The dilution filter's
position-manager test is the regression guard for the broader principle.

## Rollout

**Day one — shadow only:**
- Service ships with `PENNY_MAX_FILTER_MODE` defaulting to `shadow`.
- Aggregator logs every candidate's MAX value and threshold booleans.
- No candidate suppression. Behavior is observably identical to today.
- Operator confirms log volume and field shape are as expected within
  the first 1–2 trading days.

**Day 28 (four weeks in) — validation:**
- Run the four-week validation procedure below.
- Outcome is one of: promote to enforce at 20%, promote at a different
  threshold, or remove the service entirely.

### Four-week validation procedure

1. **Collect shadow logs.** Filter application logs for
   `msg="max filter evaluation"` over the four-week window.
2. **Join to actual trades.** For each ticker that PennyProphet actually
   entered during the window, extract the MAX value that was in effect
   at entry time (closest prior `computed_at`) and the realized P&L
   from `activity_logs/` and `decisive_actions/`.
3. **Stratify.** Group trades into the eight buckets defined by the
   three `would_skip_at_X` flags. Compute mean and median P&L per
   bucket, plus win rate.
4. **Decision rule:**
   - Pick the threshold X that maximizes the P&L *gap* between
     `would_skip=true` and `would_skip=false` trades, with at least 5
     trades in each bucket.
   - If the gap is meaningfully negative (skipped trades had worse
     mean P&L by ≥ 1% absolute, with the result not driven by a single
     outlier), promote env var to `enforce` and update the 20%
     hard-coded threshold in the aggregator to the validated value.
   - If the gap is null or skipped trades had equal-or-better P&L,
     remove the service. The academic signal does not transfer to
     PennyProphet's horizon.
5. **Document the outcome** in `docs/superpowers/specs/` as a follow-up
   memo. Promotion or removal is irreversible without a new spec.

Four weeks of PennyProphet trades is a small sample (probably 30–80
trades). The validation rule is deliberately stricter than a t-test
threshold would suggest, because the cost of a wrong "promote"
decision is real (filters out winners forever) and the cost of a wrong
"remove" decision is small (no behavior change).

## Documentation Updates

- **`TRADING_RULES_PENNY.md`** — add a "MAX Filter (Shadow)" section
  noting that the agent logs but does not act on MAX values, and that
  the validation outcome will be recorded in a follow-up spec.
- **`data/agent-config.json` `customRules`** — same content, since
  that's the authoritative rule source.
- **No agent prompt changes.** The shadow filter is invisible to the
  agent. Only an operator reads its logs.

## Future Work (Explicitly Out of Scope for This Spec)

- v2: intraday MAX refresh on candidate-entry hot path, if validation
  succeeds and the once-daily lag proves material.
- v2: high-of-day variant `MAX_high = max((high_d / close_{d-1}) - 1)`,
  if the close-to-close variant proves weak but the intraday-pop
  hypothesis is worth a separate shadow round.
- v2: volatility-normalized MAX (Bali et al. discuss `MAX / IVOL` as a
  refinement). Requires intraday IV estimates not currently in the
  pipeline.
- v2: sector-relative MAX, if same-sector co-movement makes raw MAX
  noisy in shadow logs.
- Cross-strategy applicability: V2 (options) trades on much larger,
  more efficient names where the MAX effect is documented as weak.
  No port planned.
