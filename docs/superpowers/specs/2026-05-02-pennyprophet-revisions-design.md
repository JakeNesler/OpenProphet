# PennyProphet — Revisions Design Spec

**Date:** 2026-05-02
**Status:** Approved
**Applies to:** `docs/superpowers/specs/2026-04-27-pennyprophet-design.md` + `TRADING_RULES_PENNY.md`
**Source revisions:** `potential additions/PENNYPROPHET_REVISIONS.md`

---

## Purpose

This spec documents 10 targeted revisions to the PennyProphet system addressing issues identified in design review. It is the authoritative source for implementation. The original design spec remains valid for everything not explicitly changed here.

Implementation order: infrastructure (config, types) → signal pipeline (three services + aggregator) → risk controls (blacklist, circuit breaker, operational defenses) → trading rules wiring → end-to-end paper trading validation.

---

## 1. Config Changes

### 1.1 `config/config.go`

Add field:

```go
OperatorEmail string // used in SEC EDGAR User-Agent header; set via OPERATOR_EMAIL env var
```

Default value: `"mtzuoo.pennyprophet.bot@gmail.com"` (overridable via env so the codebase is reusable without touching source).

The env var name is `OPERATOR_EMAIL`. The service instantiation passes the resolved value to `NewSECEdgarService`.

---

## 2. Shared Types — `services/penny_types.go`

### 2.1 `DecayEntry` — centralized decay + floor

All three signal services hold internal entries with a base score and event time. Previously, each service called `scoreWithDecay` directly and would need to independently implement the 5% floor. Instead, introduce `DecayEntry` as the canonical in-memory representation of a decaying signal:

```go
type DecayEntry struct {
    BaseScore    float64
    EventTime    time.Time  // decay anchor — see per-service definition below
    HalfLifeHrs  float64
}

// EffectiveScore returns the decayed score, floored to zero below 5% of base.
// Logs decay state; caller receives the result.
func (d DecayEntry) EffectiveScore() float64 {
    elapsed := time.Since(d.EventTime).Hours()
    lambda := math.Log(2) / d.HalfLifeHrs
    decayed := d.BaseScore * math.Exp(-lambda*elapsed)
    if decayed < 0.05*d.BaseScore {
        return 0
    }
    return decayed
}
```

All three services migrate their internal entry types to use `DecayEntry`. The decay floor (5% of base) is enforced once here; no per-service duplication.

### 2.2 `CandidateScore` — expanded struct

The `CandidateScore` struct gains effective-score fields, signal count, and the composite eligibility flag required by Revision 1:

```go
type CandidateScore struct {
    Ticker              string    `json:"ticker"`
    Price               float64   `json:"price"`
    CompositeScore      float64   `json:"composite_score"`
    SignalCount         int       `json:"signal_count"`
    CompositeEligible   bool      `json:"composite_eligible"`

    TechnicalScore      float64   `json:"technical_score"`
    TechnicalEffective  float64   `json:"technical_effective"`
    RegulatoryScore     float64   `json:"regulatory_score"`
    RegulatoryEffective float64   `json:"regulatory_effective"`
    SocialScore         float64   `json:"social_score"`
    SocialEffective     float64   `json:"social_effective"`

    DominantSignal      string    `json:"dominant_signal"`
    TechnicalContext    string    `json:"technical_context,omitempty"`
    RegulatoryEvent     string    `json:"regulatory_event,omitempty"`
    SocialContext       string    `json:"social_context,omitempty"`
    LastUpdated         time.Time `json:"last_updated"`
}
```

`scoreWithDecay` is kept for backward compatibility with existing tests. New code uses `DecayEntry.EffectiveScore()`.

---

## 3. Signal Pipeline — Service Changes

### 3.1 `PennyUniverseService` — market-hours-aware refresh

**Current:** fixed 15-minute ticker, every cycle.

**Revised:** three-speed refresh driven by the Alpaca `/v2/calendar` endpoint.

```
Market hours (9:30–16:00 ET, trading day):  every 5 minutes
Pre-market (04:00–9:30 ET, trading day):    every 30 minutes
After-hours (16:00–20:00 ET, trading day):  every 60 minutes
Non-trading day / closed:                   hold last refresh (no new calls)
```

**Implementation:**

Add a helper `isMarketHours(now time.Time, calEntry AlpacaCalendarEntry) (phase string)` that returns `"open"`, `"pre"`, `"after"`, or `"closed"` based on the calendar entry's `open` and `close` fields. The calendar is fetched once per day on service start (and re-fetched if `now.Date() != calEntry.Date`). This handles holidays, early closes, and futures-rollover days correctly without hardcoding NYSE rules.

Alpaca calendar endpoint: `GET /v2/calendar?start={YYYY-MM-DD}&end={YYYY-MM-DD}` using the existing trading API key. The response field `session_open` gives market open time, `session_close` gives close time. If the calendar fetch fails, fall back to static Mon–Fri 09:30–16:00 ET logic and log a warning.

**ADV threshold change:** `dollarVol < 300_000` → `dollarVol < 500_000` (Revision 7).

Universe size estimate drops to 150–350 symbols from 200–500.

### 3.2 `PennyScreenerService` — decay anchor and `DecayEntry` migration

**Decay anchor definition:** "most recent meaningful score change" — the timestamp at which the computed technical score last changed by more than 10% relative to its prior value. This prevents scores that have been flat for hours from appearing fresh.

```go
type TechnicalEntry struct {
    Entry        DecayEntry  // BaseScore, EventTime (last significant change), HalfLifeHrs=2.0
    VolumeRatio  float64
    GapPct       float64
    Context      string
}
```

`GetTechnicalScore` calls `entry.Entry.EffectiveScore()` and returns the context string. The score stored in `Entry.BaseScore` is the raw computed value at the time of last significant change. When a new scan produces a score within 10% of the stored base, `EventTime` is not updated (decay continues from the original event). When the score changes by >10%, `BaseScore` and `EventTime` both update.

This makes the decay semantics correct: a volume spike that then stabilizes decays from when the spike occurred, not from the last scan cycle.

### 3.3 `SECEdgarService` — configurable user-agent, decay anchor, event-handling

**User-Agent header:** The hardcoded `ProphetBot/1.0 (contact: trading@example.com)` is replaced:

```go
fmt.Sprintf("PennyProphet Trading Bot %s", s.operatorEmail)
```

`operatorEmail` is passed in via `NewSECEdgarService(universe, httpClient, operatorEmail string)`. Applied to all outbound HTTP requests the service makes (EDGAR atom feed, GlobeNewswire RSS, PR Newswire).

**Decay anchor:** EDGAR filings carry an `<updated>` timestamp in the Atom feed; use that as `EventTime`. GlobeNewswire items carry `<pubDate>` in RSS; use that. If either is unparseable, fall back to `time.Now()` with a warning log: `"decay anchor: observation fallback used"`.

**New-event handling (max rule):** `upsertEntry` is refactored to implement the max rule from Revision 2:

```
When a new event of the same signal type arrives:
  existing_decayed = existing.Entry.EffectiveScore()
  if new_base > existing_decayed:
      replace entry (new BaseScore, new EventTime)
  else:
      discard new event (decayed old score is still higher)
```

This prevents accumulation while preserving the freshest signal value. The prior implementation kept the highest base score unconditionally; the new implementation compares against the *decayed* prior score.

**Migrate internal `regulatoryEntry`** to use `DecayEntry`. The `EventDesc` field is kept alongside it.

### 3.4 `SocialSignalService` — 7-day rolling baseline, universe cleanup

**Per-ticker 7-day rolling baseline:**

```go
type mentionBaseline struct {
    buckets  [336]int  // 7 days × 48 half-hour buckets; ring buffer indexed by bucket number
    total    int       // sum of all buckets (maintained incrementally)
    firstSeen time.Time
}
```

The ring index for a given time `t` is `(t.Unix() / 1800) % 336`. On each Reddit poll, before scoring, the current bucket is updated: if the bucket index has advanced since the last poll, zero out the passed buckets (reducing `total` accordingly) and add the new count.

`baselineMentionsPer30min(ticker)` returns `max(0.5, float64(b.total) / 336.0)`. If `time.Since(b.firstSeen) < 72*time.Hour`, the baseline is considered insufficiently established and `mentionVelocityPts = 0` for that ticker.

**Scoring (Revision 3):**

```go
mentionVelocityPts = min(mentionsLast30min / baselineMentionsPer30min, 2.0) * 5   // max 10
sentimentPts = ...                                                                   // max 10 (unchanged)
SocialScore = mentionVelocityPts + sentimentPts                                      // max 20
```

**Universe-exit cleanup (approved refinement):** After each Reddit poll, prune the baseline map to remove tickers no longer present in the current universe snapshot. Call `s.universe.GetTickers()` and build a ticker set; delete baseline entries for absent tickers. This prevents unbounded memory growth from tickers that briefly entered the universe and left.

**`DecayEntry` migration:** `socialEntry` is refactored to hold a `DecayEntry` (half-life 4 hours). `EventTime` is the timestamp of the Reddit mention batch that produced the current score. `GetSocialScore` calls `entry.EffectiveScore()`.

---

## 4. Signal Aggregator — `services/penny_signal_aggregator.go`

### 4.1 Composite score with per-signal minimums (Revision 1)

Per-signal minimums:

| Signal | Minimum to contribute | Max |
|---|---|---|
| Technical | ≥ 15 | 40 |
| Regulatory | ≥ 25 | 40 |
| Social | ≥ 10 | 20 |

In `aggregate()`:

```go
techEff  := techScore  if techScore  >= 15 else 0
regEff   := regScore   if regScore   >= 25 else 0
socEff   := socScore   if socScore   >= 10 else 0

signalCount := 0
if techEff > 0 { signalCount++ }
if regEff  > 0 { signalCount++ }
if socEff  > 0 { signalCount++ }

composite := math.Min(techEff+regEff+socEff, 100.0)
eligible  := signalCount >= 2
```

`DominantSignal` is computed from effective scores (not raw scores).

### 4.2 `GetCandidates` filter update

```go
func (a *PennySignalAggregator) GetCandidates(minScore float64) []CandidateScore {
    // Returns only candidates where CompositeEligible == true AND CompositeScore >= minScore
}
```

Single-signal candidates with high raw composite scores are excluded regardless of numeric value.

### 4.3 Embedded bracket blacklist (Revision 9)

```go
type BracketBlacklist struct {
    mu      sync.RWMutex
    entries map[string]BracketBlacklistEntry
}

type BracketBlacklistEntry struct {
    Ticker       string
    RejectedAt   time.Time
    RejectReason string
    AttemptCount int
}
```

Methods on `PennySignalAggregator`:
- `AddToBlacklist(ticker, reason string)` — adds entry, logs it
- `RemoveFromBlacklist(ticker string)` — operator override
- `ClearBlacklist()` — operator override (full reset)
- `IsBlacklisted(ticker string) bool` — checked inside `GetCandidates` before returning

Blacklist is session-scoped: cleared on `NewPennySignalAggregator`. `GetCandidates` filters blacklisted tickers silently (they simply don't appear in results).

**v2 note:** If additional broker-state feedback filters are needed (e.g., symbols with rejected market orders, symbols flagged by broker risk engine), the blacklist should be refactored into a dedicated `BrokerStateService` so all broker-feedback state is co-located. The embedded approach is appropriate for v1 where only bracket rejection needs tracking.

---

## 5. Controller — `controllers/penny_controller.go`

Two new HTTP endpoints for operator blacklist management:

```
DELETE /api/v1/penny/blacklist          → ClearBlacklist()
DELETE /api/v1/penny/blacklist/:ticker  → RemoveFromBlacklist(ticker)
```

Both return `{"status": "ok"}` on success. These are operator-facing only — not exposed as MCP tools and not called by the agent.

---

## 6. Agent Config — `data/agent-config.json`

Update the `sbx_a788a4e3` (PennyTrades) sandbox `permissions` block (Revision 6):

| Field | Old | New |
|---|---|---|
| `maxPositionPct` | 12 | 8 |
| `maxDeployedPct` | 80 | 60 |
| `allowOptions` | true | false |
| `maxToolRoundsPerBeat` | 25 | 18 |
| `maxOpenPositions` | 10 | 10 (unchanged) |

---

## 7. `TRADING_RULES_PENNY.md` Changes

### 7.1 Prepend operational discipline sections (Revision 10)

The following new sections are prepended immediately after the `## Core Philosophy` section, in this order:

1. `## How You Operate` — LLM role framing, output limits, improvisation prohibition, uncovered-situation protocol
2. `## Rule Boundary Handling` — inclusive threshold definitions, conservative-default rule
3. `## When Data Is Missing or Inconsistent` — per-tool response handling
4. `## Hard Stops That Override Everything` — 7 trigger conditions and response protocol
5. `## Startup / Restart Behavior` — reconciliation protocol, 4-step flow
6. `## Circuit Breaker Behavior` — −5% trigger, session scope, heartbeat-alive behavior during trip, manual override
7. `## Glossary` — 12 defined terms

Full text of these sections is specified verbatim in `PENNYPROPHET_REVISIONS.md` Revision 10.

### 7.2 Update position sizing section (Revision 6)

```
Concurrent position limits (all must be true at entry):
  - Open penny positions < 10
  - Total deployed in penny positions < 60% of portfolio
```

Narrative note added: "The deployed cap (60%) typically binds before the position count cap (10). At 6% average sizing, 10 positions = 60% deployed — both hit simultaneously."

Pre-trade checklist updated: `< 12` positions → `< 10`.

### 7.3 Update universe section (Revision 7)

ADV: `≥ $300,000` → `≥ $500,000`. Add calculation note: `avg_volume_30d × avg_price_30d ≥ $500,000`.

### 7.4 Replace social exit rule (Revision 4)

The social dominant-signal exit rule gains an explicit cancel-and-replace protocol:

```
TIME-BASED EXIT (overrides bracket if not yet filled):
  At 20 minutes post-entry (or 15 minutes before market close, whichever first):
  1. Cancel the active bracket order via cancel_order
  2. Confirm cancellation:
     - Succeeded → proceed to step 3
     - Failed because bracket already filled → log and stop (position closed by bracket)
     - Failed for other reason → halt agent, log "social-exit cancel failure, operator review required"
  3. Place market sell for full position size
  4. Confirm fill within 60 seconds
     - Filled → log exit
     - Not filled within 60s → halt agent, log "social-exit market order stalled, operator review required"

ENTRY GATING:
  - Do not enter social positions < 30 minutes before market close
  - Social signals expiring during last 30 minutes of trading are skipped
```

### 7.5 Add bracket blacklist note (Revision 9)

After the Bracket Order Requirement section:

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

## 8. Design Spec Updates (`2026-04-27-pennyprophet-design.md`)

These sections in the original design spec are updated:

| Section | Change |
|---|---|
| §2 Tradeable Universe | ADV $300K → $500K; universe size 150–350 |
| §3.2 PennyUniverseService | Refresh cadence table: 5/30/60/static; Alpaca calendar integration note |
| §3.4 SECEdgarService | Add OPERATIONAL REQUIREMENTS block (user-agent, rate limit, EDGAR latency disclosure) |
| §3.5 SocialSignalService | Replace scoring formula; add VELOCITY DENOMINATOR and NEW-TICKER HANDLING sub-sections |
| §3.6 PennySignalAggregator | Replace composite formula with per-signal minimums + eligibility gate; update `CandidateScore` struct |
| Add §3.7 | Score Decay — Full Specification (decay anchor, continuity, closed-market behavior, new-event handling, decay floor, logging) |
| Add §3.8 | Bracket-Rejection Blacklist (schema, behavior, session scope, v2 refactor note) |
| §7 Agent Configuration | Update permission overrides to match Revision 6 values |
| §9 Broker Verification Checklist | Add three items from Revision 4 |

---

## 9. Test Coverage Expectations

Each modified service gets tests for the new behavior:

| Service/File | New test cases |
|---|---|
| `penny_types_test.go` | `DecayEntry.EffectiveScore` at 0%, 50%, 95%, 100% decay; floor threshold |
| `penny_universe_service_test.go` | ADV filter at $499K (excluded), $500K (included); market-phase detection with mock calendar |
| `penny_screener_service_test.go` | Meaningful-change anchor: score within 10% doesn't update EventTime; score >10% change does |
| `penny_signal_aggregator_test.go` | Single-signal candidate blocked (CompositeEligible=false); two-signal candidate passes; blacklist filter |
| `sec_edgar_service_test.go` | Max rule for upsert (decayed-old vs. new-base); event timestamp parsing from Atom feed |
| `social_signal_service_test.go` | 7-day baseline ring buffer; new-ticker 72h guard; universe-exit cleanup; denominator floor |

---

## 10. What Is Not Changed

- MCP tool schemas: no new tools, no signature changes; `get_penny_candidates` filter behavior changes transparently
- `cmd/bot/main.go`: no new goroutines (all changes are internal to existing services)
- Other agents (Prophet, Harvest): no changes
- Dashboard UI: two new DELETE endpoints are operator-only; no UI changes required for v1
