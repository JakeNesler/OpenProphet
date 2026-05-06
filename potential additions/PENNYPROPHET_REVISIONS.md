# PennyProphet — Spec Revisions

**Date:** 2026-05-02
**Status:** Draft for review
**Purpose:** Address serious issues identified in design review and bring operational discipline up to Harvest spec level
**Merge target:** `2026-04-27-pennyprophet-design.md` and `TRADING_RULES_PENNY.md`

---

## How to use this document

Each section below is labeled with where it goes in the existing specs. Sections marked **REPLACE** fully substitute existing content. Sections marked **ADD** are net-new. Sections marked **MERGE** modify existing content with surgical changes called out inline.

Apply order matters: review and approve revisions, then apply them in order, then re-read the assembled spec end-to-end before moving to implementation.

---

## REVISION 1: Composite Score Math — Per-Signal Minimums

**Target:** `2026-04-27-pennyprophet-design.md` Section 3.6 (PennySignalAggregator)
**Action:** REPLACE

### Problem being fixed

The original formula `CompositeScore = TechnicalScore + RegulatoryScore + SocialScore` allows an 8-K filing alone (40 pts) plus mild noise contributions from social and technical to cross the 60-point threshold without genuine multi-signal confluence. The position sizing tiers reference "strong multi-signal confluence" but the math doesn't enforce it.

### Revised section

```
### 3.6 PennySignalAggregator

Combines the three sub-scores with per-signal minimum thresholds.

Per-signal minimums (a signal must clear its minimum to contribute to composite):

  Technical:   ≥ 15 of 40 to count
  Regulatory:  ≥ 25 of 40 to count
  Social:      ≥ 10 of 20 to count

Below its minimum, a signal contributes 0 to the composite score regardless of
its raw value. This prevents trivial signal noise from accumulating into a
trade-triggering composite.

Composite formula:

  effective_technical  = TechnicalScore  if TechnicalScore  ≥ 15 else 0
  effective_regulatory = RegulatoryScore if RegulatoryScore ≥ 25 else 0
  effective_social     = SocialScore     if SocialScore     ≥ 10 else 0

  CompositeScore = effective_technical + effective_regulatory + effective_social

Multi-signal requirement:

  At least TWO signals must have non-zero effective contribution for composite
  to be eligible for entry, regardless of raw composite value.

  signal_count = count of signals with effective contribution > 0
  if signal_count < 2:
      composite_eligible = false
      log "single-signal candidate, below confluence requirement"

This means a 40-point regulatory signal with no other signal activity scores
40 composite but is NOT eligible for entry. The agent is only allowed to act
on multi-signal confluence.

Maintains map[string]CandidateScore in memory.

CandidateScore struct (revised):

  type CandidateScore struct {
      Ticker              string
      CompositeScore      float64
      SignalCount         int       // count of contributing signals (≥2 required)
      CompositeEligible   bool      // true only if SignalCount ≥ 2
      TechnicalScore      float64   // raw, may be below minimum
      TechnicalEffective  float64   // 0 if below minimum, else raw
      RegulatoryScore     float64
      RegulatoryEffective float64
      SocialScore         float64
      SocialEffective     float64
      DominantSignal      string    // highest effective signal, normalized by max
      LastUpdated         time.Time
      RegulatoryEvent     string
      SocialContext       string
  }

DominantSignal is computed from effective scores normalized by their respective
maxima (40, 40, 20). Ties broken in priority order: regulatory > technical > social.
```

### Implication for downstream consumers

`get_penny_candidates` must filter by `CompositeEligible = true`, not just `CompositeScore ≥ min_score`. Update the MCP tool's filter logic accordingly.

---

## REVISION 2: Decay Logic — Full Specification

**Target:** `2026-04-27-pennyprophet-design.md` Section 3 (multiple subsections)
**Action:** ADD as new section 3.7

### Problem being fixed

The original spec defines decay half-lives but doesn't specify when decay starts, whether it's continuous or discrete, what happens during market closed hours, or how new events of the same type interact with decaying scores.

### New section

```
### 3.7 Score Decay — Full Specification

DECAY ANCHOR

Decay begins at the timestamp of the underlying event, not the observation time:
  - Technical: timestamp of the bar that produced the score
  - Regulatory: filing timestamp from EDGAR or wire timestamp from press release
  - Social: timestamp of the most recent mention or sentiment shift

If the event timestamp is unavailable, fall back to first-observation time and
log "decay anchor: observation fallback used."

DECAY CONTINUITY

Decay is computed on every score read (continuous), not on polling cycles.

  decayed_score = base_score × 0.5^((now - event_time) / half_life)

This means a score read 1 hour after a 24-hour-half-life event returns
base × 0.5^(1/24) = base × 0.971, not base.

CLOSED-MARKET BEHAVIOR

Decay continues uninterrupted through closed market periods (overnight,
weekends, holidays). A regulatory event at 4:30pm Friday with 24-hour half-life
is worth base × 0.5^(64/24) = base × 0.156 by Monday 9:30am open.

Rationale: stale signals are stale regardless of whether markets are open.
Holding scores frozen overnight would create artificial "stockpiling" of
opportunities that may have already played out in extended hours.

NEW EVENT OF SAME TYPE

When a new event of the same signal type fires while a previous one is decaying:

  new_effective = max(decayed_old_score, new_event_base_score)

The new event's timestamp becomes the new decay anchor for that signal type.
This prevents pathological accumulation while preserving the freshest signal.

Example: 8-K at 9:00am scores 40. At 11:00am (still 39.4 decayed), a press
release adds 25 base points. Effective regulatory score = max(39.4, 25) = 39.4
with anchor still 9:00am. The press release is acknowledged but doesn't reset
or stack.

Example: 8-K at 9:00am scores 40. At 5:00pm next day (decayed to ~20), a new
8-K filed scores 40. Effective regulatory score = max(20, 40) = 40, anchor
resets to 5:00pm next day.

DECAY FLOOR

Below 5% of base score, a signal is considered fully decayed and contributes 0
regardless of its mathematical value. This prevents tickers from carrying
near-zero residual scores indefinitely.

  if decayed_score < 0.05 × base_score:
      decayed_score = 0
      remove from active scoring for this signal type

LOGGING

Every score read logs the decay state:
  - base_score, event_time, time_since_event_minutes, decayed_score
  - Whether the per-signal minimum is currently met
  - Whether the signal is at the decay floor (fully decayed)
```

---

## REVISION 3: Social Velocity Denominator

**Target:** `2026-04-27-pennyprophet-design.md` Section 3.5 (SocialSignalService)
**Action:** REPLACE the scoring formula and add denominator definition

### Problem being fixed

`avgMentionsPer30min` is undefined. Without a specified lookback window and minimum floor, the score is mathematically meaningless and prone to division-by-tiny-number explosions for previously-quiet tickers.

### Revised section

```
### 3.5 SocialSignalService (Social Signals, 0–20 pts)

Polls every 30 seconds. Two sources:

  Reddit r/pennystocks + r/RobinHoodPennyStocks:
    Public JSON API (/r/pennystocks/new.json), no auth required

  StockTwits:
    Public API https://api.stocktwits.com/api/2/streams/symbol/{ticker}.json
    Rate limit: 200 req/hour unauthenticated

VELOCITY DENOMINATOR

The mention velocity calculation requires a baseline. Defined as:

  baseline_mentions_per_30min = max(0.5, rolling_7day_avg_per_ticker_per_30min)

The 0.5 floor prevents division-by-tiny-number explosions for previously-
unmentioned tickers. A ticker with effectively zero baseline mentions cannot
score velocity points until it has accumulated meaningful baseline activity
over multiple 30-minute windows.

The 7-day rolling window is computed per-ticker, in 30-minute buckets, across
both Reddit and StockTwits combined. Buckets in market-closed hours are
included in the average (mentions don't pause overnight).

NEW-TICKER HANDLING

A ticker entering the universe has no historical mention data initially. Until
72 hours of baseline data accumulates:

  If ticker has < 72 hours of baseline observation:
      sentimentPts can still be computed (no baseline needed)
      mentionVelocityPts = 0 (insufficient baseline data)

This means newly-active tickers can score on sentiment quality alone but cannot
score on velocity until baseline is established. Prevents false-positive
"infinite velocity" scores on tickers that simply weren't being tracked yet.

SCORING

  mentionVelocityPts = min(mentionsLast30min / baseline_mentions_per_30min, 2.0) * 5

  sentimentPts:
    if bullishRatio > 0.65:  10 pts
    elif bullishRatio > 0.55: 5 pts
    else:                     0 pts

  SocialScore = mentionVelocityPts + sentimentPts   (max 20)

bullishRatio is computed from StockTwits sentiment-tagged messages only. Reddit
posts contribute to mention count but not to sentiment ratio (no reliable
sentiment tagging on Reddit).

Decay: half-life 4 hours, per Section 3.7 specification.
```

---

## REVISION 4: Social Hold Bracket Order Reconciliation

**Target:** `TRADING_RULES_PENNY.md` Signal-Type Exit Rules (social section) AND design spec Section 8
**Action:** REPLACE the social exit rule and add explicit operational handling

### Problem being fixed

The 20-minute hard time stop on social signals conflicts with the bracket order requirement. The original spec doesn't specify how the agent transitions from a bracket-protected position to a time-triggered market sell.

### Revised section (for TRADING_RULES_PENNY.md)

```
### dominant_signal = "social" (Reddit/StockTwits momentum)

ENTRY:
  - Use place_managed_position with stop and target
  - Stop: −8% from entry
  - Target: +15% (50% scale) then +20% (remaining)

TIME-BASED EXIT (overrides bracket if not yet filled):
  - 20 minutes after entry: cancel bracket order, place market sell
  - End of trading session: cancel bracket, close at market regardless

CANCEL-AND-REPLACE PROTOCOL:

  At 20 minutes post-entry (or 15 minutes before market close, whichever first):

  1. Cancel the active bracket order via cancel_order
  2. Confirm cancellation succeeded:
     - If cancel succeeded → proceed to step 3
     - If cancel failed because bracket already filled → log and stop (the
       position is already closed)
     - If cancel failed for any other reason → halt agent, log
       "social-exit cancel failure, operator review required"
  3. Place market sell order for full position size
  4. Confirm fill within 60 seconds
     - If filled → log exit, mark position closed
     - If not filled within 60 seconds → halt agent, log
       "social-exit market order stalled, operator review required"

RACE CONDITION HANDLING:

If during step 1-3, the bracket's stop or target legs fire before the cancel
completes, the position is closed by the bracket (not by our market order).
This is fine — the cancel-then-replace operation is best-effort and natural
fills are acceptable.

Always confirm position state after the protocol completes via get_positions
before logging the exit. The fill source (bracket vs. our market order) goes
in the exit log.

ENTRY GATING:

  - Do not enter social-signal positions less than 30 minutes before market close
  - Social signals expiring during the last 30 minutes of trading are skipped,
    not entered with a shortened hold window
```

### Note for design spec Section 8

Update the broker verification checklist to include:

```
- [ ] cancel_order works reliably on active bracket orders for $2-$10 symbols
- [ ] place_sell_order (market) executes within 60 seconds during normal hours
- [ ] get_positions reflects bracket-fill state within 5 seconds of fill
```

---

## REVISION 5: Universe Race Condition

**Target:** `2026-04-27-pennyprophet-design.md` Section 3.2 (PennyUniverseService) AND Section 3.6 (entry path)
**Action:** MERGE — refresh interval change + add real-time entry-time check

### Problem being fixed

15-minute universe refresh allows trading on tickers that have moved out of the universe between refresh and entry. A stock that drops below $2.00 mid-cycle remains in the cached universe and remains a valid entry candidate.

### Changes to Section 3.2

```
PennyUniverseService

REFRESH INTERVAL

  Market hours:    every 5 minutes (changed from 15)
  Pre-market:      every 30 minutes
  After-hours:     every 60 minutes
  Closed:          static (last refresh held)

The market-hours refresh is shortened because penny stocks can exit the universe
(price/volume excursions) within a single 15-minute window during momentum events.

The screener API call is cheap (~$0.0001 per call). 5-minute cadence during
market hours is operationally sustainable.

PUBLISHED LIST

The published universe list is the floor for signal evaluation but NOT the
final gate for entry. Entry-time price verification is mandatory (see below).
```

### Changes to entry path (cross-reference Section 3.6 and trading rules)

Add new mandatory pre-entry check:

```
ENTRY-TIME UNIVERSE VERIFICATION

Before placing any order, the agent MUST call get_quote (or equivalent
real-time quote) on the candidate ticker and verify:

  - current_price ∈ [$2.00, $10.00]
  - quote_age_seconds ≤ 30
  - bid_ask_spread_pct ≤ 5% (sanity check on liquidity)

If any check fails:
  - Skip the trade
  - Log "entry-time universe gate failed: {reason}"
  - Do not retry on the same heartbeat

This is a hard gate enforced at order-placement time, separate from the
universe service's signal-eligibility scoping. The universe service tells the
signal pipeline "these tickers are worth scoring." The entry-time check tells
the agent "this ticker is still valid to trade RIGHT NOW."
```

---

## REVISION 6: Position Limit Reconciliation

**Target:** `2026-04-27-pennyprophet-design.md` Section 7 (Permission overrides) AND `TRADING_RULES_PENNY.md` Position Sizing
**Action:** MERGE — adjust limits to be internally consistent

### Problem being fixed

12 positions × 8% max = 96% theoretical deployment, but the deployed cap is 60%. The 12-position limit is decorative — the deployed cap fires first.

### Revised limits

Update Section 7 permission overrides:

```json
{
  "allowLiveTrading": true,
  "allowStocks": true,
  "allowOptions": false,
  "allow0DTE": false,
  "maxPositionPct": 8,
  "maxDeployedPct": 60,
  "maxDailyLoss": 5,
  "maxOpenPositions": 10,
  "maxOrderValue": 0,
  "maxToolRoundsPerBeat": 18
}
```

Changes from original:
  - maxOpenPositions: 12 → 10 (consistent with realistic deployment math)
  - maxToolRoundsPerBeat: 30 → 18 (tighten LLM rope; sufficient for normal heartbeat)

Update `TRADING_RULES_PENNY.md` position sizing section:

```
Position Sizing (Tiered by Composite Score)

  Composite ≥ 80:  5–7% of portfolio (hard cap 8%)
  Composite 60–79: 2–3% of portfolio (hard cap 8%)
  Composite < 60:  No trade

Concurrent position limits (all must be true at entry):

  - Open penny positions < 10
  - Total deployed in penny positions < 60% of portfolio
  - Adding this position would not breach either limit above

Note: The deployed cap (60%) typically binds before the position count cap
(10). At 6% average sizing, 10 positions = 60% deployed, both hit
simultaneously. At smaller sizes you'll hit the position cap; at larger sizes
you'll hit the deployed cap. Both are intentional.
```

---

## REVISION 7: Minimum Liquidity Threshold

**Target:** `2026-04-27-pennyprophet-design.md` Section 2 (Tradeable Universe) AND `TRADING_RULES_PENNY.md` Universe section
**Action:** MERGE — raise ADV minimum

### Problem being fixed

$300K average daily dollar volume is borderline for the position sizes the strategy uses. A 6% position on a $50K account ($3,000) at a $5 stock is 600 shares. On a stock doing $300K/day total volume (60K shares), that's 1% of daily volume — fillable, but the slippage on stops during sharp moves will be painful.

### Revised threshold

```
Tradeable Universe (revised)

  Price range:           $2.00–$10.00/share
  Market cap:            $50M–$500M
  Avg daily $ volume:    ≥ $500,000 (raised from $300K)
  Exchange:              Nasdaq CM, NYSE Arca, NYSE American
  Excluded:              OTC, Pink Sheets, Grey Market

ADV calculation: avg_volume_30d × avg_price_30d ≥ $500,000

The threshold is raised because typical position sizing (3-7% of portfolio)
plus stop-loss execution during volatile penny moves requires sufficient depth
to exit without catastrophic slippage. $500K ADV is a workable minimum;
position sizes scale within this floor.

Universe size: approximately 150-350 symbols depending on market conditions
(reduced from prior estimate due to raised threshold).
```

---

## REVISION 8: SEC EDGAR User-Agent and Latency

**Target:** `2026-04-27-pennyprophet-design.md` Section 3.4 (SECEdgarService)
**Action:** ADD operational requirements

### Problem being fixed

SEC EDGAR fair-access policy requires a User-Agent header identifying the requester. Requests without one will eventually be blocked. Original spec doesn't specify this. Also, full-text search has 5-30 minute indexing latency that should be acknowledged.

### Additions to Section 3.4

```
OPERATIONAL REQUIREMENTS

User-Agent header (mandatory per SEC fair-access policy):

  User-Agent: "PennyProphet Trading Bot {operator_email}"

  Where {operator_email} is a real, monitored email address. The SEC may
  contact this address regarding access patterns. Failure to set this header
  will result in eventual IP-level blocking.

Rate limit: 10 requests per second per the SEC's published guidance. The
30-second polling cadence is well within limits.

LATENCY DISCLOSURE

EDGAR full-text search indexes filings with a typical delay of 5-30 minutes
after filing acceptance. This means regulatory signals may lag the underlying
event by that window. The market often moves on filings before our service
sees them.

Mitigation in v1: accept the latency. Position sizing and confluence
requirements are designed assuming delayed signal delivery.

v2 candidate: per-CIK polling via https://data.sec.gov/submissions/CIK{cik}.json
which updates within seconds of filing. Requires CIK lookup table for the
universe (~150-350 symbols). Defer until v1 operational data confirms latency
is the limiting factor.
```

---

## REVISION 9: Bracket Rejection Blacklist

**Target:** `2026-04-27-pennyprophet-design.md` Section 3 (new subsection) AND `TRADING_RULES_PENNY.md` Bracket Order section
**Action:** ADD

### Problem being fixed

If a symbol can't accept bracket orders, the original spec has the agent re-attempt and re-fail every heartbeat. Wasted API calls, wasted log entries, and the LLM may eventually try to "be helpful" and skip the bracket requirement.

### New subsection in design spec

```
### 3.8 Bracket-Rejection Blacklist

Per-session in-memory blacklist of symbols where place_managed_position has
failed with a bracket-order rejection.

Schema:

  type BracketBlacklist struct {
      Ticker        string
      RejectedAt    time.Time
      RejectReason  string
      AttemptCount  int
  }

Behavior:

  - On bracket rejection: add ticker to blacklist with timestamp and reason
  - On signal evaluation: filter out blacklisted tickers BEFORE composite
    score evaluation (prevents repeated re-attempts)
  - On session start: clear blacklist (broker policy may have changed)
  - Blacklist additions logged for operator visibility

The blacklist is exposed to the agent via candidate filtering — blacklisted
tickers do not appear in get_penny_candidates results.

Operator override: dashboard control to clear individual tickers or full
blacklist mid-session if needed.
```

### Addition to TRADING_RULES_PENNY.md

```
BRACKET ORDER BLACKLIST

If place_managed_position rejects a symbol due to bracket-order limitations,
that symbol is automatically blacklisted for the remainder of the session.

The agent does not need to track this — blacklisted tickers will not appear
in get_penny_candidates results during the session.

The agent must NEVER attempt to enter a position without a bracket order,
even if a candidate appears highly attractive. If place_managed_position
fails for any reason, skip the trade and log.
```

---

## REVISION 10: Operational Discipline — Bring to Harvest Level

**Target:** `TRADING_RULES_PENNY.md` — add new sections at top
**Action:** ADD

### Problem being fixed

Harvest's spec has explicit sections on LLM behavioral constraints, hard stops, startup behavior, rule boundary handling, and operational defenses that PennyProphet lacks. These cover failure modes that arise specifically because the decision layer is an LLM rather than deterministic code.

### New sections to prepend to TRADING_RULES_PENNY.md (after Core Philosophy)

```
## How You Operate

You are PennyProphet, a signal-gated penny stock momentum trading agent. You
are not a reasoning agent. You are a rule executor wrapped in a language
model. Your job is to apply the rules below mechanically against the candidate
data provided by the signal pipeline.

Your outputs are limited to:
  1. Actions specified by your rules (enter, exit, manage, skip, halt)
  2. Structured logs via log_activity and log_decision
  3. Mechanical tool calls to fetch data and execute trades

You do not:
  - Produce free-form market commentary or opinions
  - Override exit rules because a position "looks like it might recover"
  - Override entry filters because a candidate "looks promising despite low score"
  - Suggest improvements to your own rules during a session
  - Improvise responses to situations not covered by your rules

If a situation arises that your rules do not cover, your only valid action is:
  - Halt new entries
  - Continue managing existing positions per their dominant-signal exit rules
  - Log "uncovered situation: {description}" via log_decision
  - Wait for operator instruction

Helpful improvisation is the failure mode. The signal pipeline does the
analysis. You execute against its output.

## Rule Boundary Handling

Numeric thresholds are inclusive unless explicitly stated otherwise:
  - "composite score ≥ 60" includes exactly 60
  - "P&L ≤ −5%" includes exactly −5%
  - "−8% from entry" stops at exactly −8%

For genuinely ambiguous situations not covered by rules:
  - Default to the more conservative action
  - Conservative for entries: skip
  - Conservative for exits: do not exit early; let the dominant-signal rules play out
  - Always log the ambiguity via log_decision

## When Data Is Missing or Inconsistent

  - get_penny_candidates returns empty: do nothing, log "no candidates above threshold"
  - get_penny_signal_detail returns stale data (>60s): skip that ticker, log
  - get_account fails or returns inconsistent state: halt entries, log
  - Quote at entry-time check is stale (>30s): skip that trade, log
  - Position state in get_positions doesn't match expected state: halt all
    activity, log "reconciliation mismatch — operator review required"

## Hard Stops That Override Everything

These conditions halt all trading activity immediately and require operator
action to resume:

  - Broker connection failure or authentication error
  - Trade rejection by broker for reason other than bracket-order limitation
  - Account risk warning or margin call from broker
  - Position reconciliation mismatch (internal state ≠ broker state)
  - Quote staleness exceeding 5 minutes during market hours
  - Multiple consecutive (3+) failed orders within a single heartbeat
  - Any error condition not covered by your rules

In these cases:
  - Cease all new entries
  - Do NOT attempt to manage existing positions (broker may have closed them
    or position state may be unknown)
  - Log the condition with full diagnostic detail via log_decision
  - Do not retry until operator confirms reset

This is not a rule violation — these are signals that something has broken
and continuing operation could cause harm.

## Startup / Restart Behavior

On agent startup or after a restart:

  1. Call get_positions to fetch current state from broker
  2. Compare against last known internal state (if any)
  3. If reconciliation succeeds:
       - Resume normal heartbeat operation
       - Log "session start" with full position inventory
  4. If reconciliation fails:
       - Halt all trading activity
       - Log "startup reconciliation failed — operator review required"
       - Wait for operator
  5. If no prior internal state exists (fresh start):
       - Adopt broker positions as starting state
       - Log "fresh start — adopted N broker positions"
       - Resume normal operation

The bracket-rejection blacklist is empty on every startup (cleared by
session boundary).

The daily circuit breaker is reset on startup if the prior session has ended.
If startup occurs mid-session and the breaker was tripped, it remains tripped
until the next session boundary.

## Circuit Breaker Behavior

Trigger: portfolio P&L ≤ −5% intraday (Harvest positions excluded; this is
PennyProphet-scoped P&L only).

On trigger:
  - Cancel all open bracket orders for penny positions
  - Place market sell orders for all open penny positions
  - Cease evaluating new candidates for the rest of the session
  - Continue emitting heartbeat-alive logs every interval (so operator can
    confirm agent is alive vs. crashed)
  - Do NOT poll signals or call get_penny_candidates while breaker is tripped
    (reduces unnecessary API load)

Reset: at the next market open following the trip. Breaker state is
session-scoped, not persistent across days.

Manual override: operator can reset mid-session via dashboard if conditions
warrant. Manual reset logs operator identity and timestamp.

## Glossary

  Composite score:        Sum of effective signal scores; max 100
  Effective signal score: Raw signal score if above per-signal minimum, else 0
  Dominant signal:        Highest effective signal normalized by its max
  Multi-signal confluence: At least 2 signals contributing non-zero
  Decay anchor:           Timestamp from which decay is computed
  Decay floor:            5% of base score; below this, signal is fully decayed
  ADV:                    Average daily dollar volume = avg shares × avg price
  Bracket order:          Order with stop and target legs, atomic execution
  Session:                One trading day, market open to close
  1R:                     One unit of risk; for −8% stop, 1R = +8% target
  Universe:               Set of tickers eligible for signal evaluation
  Candidate:              Universe ticker with composite score above threshold
```

---

## Summary of Changes

| # | Issue | Severity | Files affected |
|---|-------|----------|----------------|
| 1 | Composite score math allows single-signal entries | Serious | Design spec §3.6 |
| 2 | Decay logic underspecified | Serious | Design spec §3.7 (new) |
| 3 | Social velocity denominator undefined | Serious | Design spec §3.5 |
| 4 | Social hold conflicts with bracket orders | Serious | Trading rules + design spec §8 |
| 5 | Universe refresh race condition | Serious | Design spec §3.2 + entry path |
| 6 | Position limits internally inconsistent | Moderate | Design spec §7 + trading rules |
| 7 | ADV threshold too low for position sizes | Moderate | Design spec §2 + trading rules |
| 8 | EDGAR User-Agent missing, latency undisclosed | Moderate | Design spec §3.4 |
| 9 | Bracket rejection causes wasted re-attempts | Moderate | Design spec §3.8 (new) + trading rules |
| 10 | Operational discipline below Harvest level | Moderate | Trading rules (multiple new sections) |

---

## Recommended Application Order

1. Apply Revision 10 first (operational discipline). This establishes the framing for everything else and makes subsequent rule additions consistent in tone.
2. Apply Revisions 1, 2, 3 (signal pipeline math). These are coupled — composite scoring depends on per-signal effective scores, decay specification governs how those scores evolve, social velocity is one of the inputs.
3. Apply Revision 5 (universe race) before Revision 4 (social hold), because the entry-time universe check is referenced in the social hold protocol.
4. Apply Revision 4 (social hold reconciliation).
5. Apply Revisions 6, 7, 8, 9 in any order — they're independent operational fixes.
6. Re-read assembled spec end-to-end. Look for cross-references that may have broken.
7. Update version date and status on both files.

---

## What Still Isn't Addressed (Deferred to v2)

These came up in the review but are out of scope for the immediate revision:

- **Twitter/X integration** — already deferred in original spec
- **FDA event calendar** — already deferred
- **Per-CIK EDGAR polling** for sub-minute regulatory signal latency
- **Liquidity-scaled position sizing** (smaller positions on lower-ADV names)
- **Cross-agent portfolio correlation** between PennyProphet and Prophet
- **Extended-hours trading** — explicitly disabled in v1; revisit if signal data shows meaningful extended-hours opportunities being missed
- **Backtesting harness** — separate workstream

These are documented for future reference but do not block v1 operational readiness.
