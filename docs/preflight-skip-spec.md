# Pre-Flight Skip Spec

**Status:** Draft for review
**Owner:** TBD
**Updated:** 2026-05-07

## Problem

Every heartbeat currently spawns an opencode subprocess, which invokes Claude. The LLM call carries the full system prompt (rules file ~6-12KB) and the heartbeat prompt, generates output, and runs tool calls. Cost is dominated by tokens, not wall-clock.

For agents whose work is fully gated by signal pipeline output, the LLM is woken up to do nothing roughly half the time:

- **PennyProphet:** `get_penny_candidates(min_score=60)` returns empty most of the day. With no candidates and no open positions, the LLM has nothing to do but log a heartbeat. Tokens are spent reading the rules and emitting "no candidates above threshold."
- **TrendProphet (when built):** Once-daily heartbeat in the after-hours phase. With no Donchian breakout signals and no open positions, the LLM has nothing to do.
- **Harvest:** Rarely fully idle (open condors must be evaluated each beat) but during FOMC blackout with `open_condors: 0` it has no work.
- **V2:** Discretionary trader with broad responsibilities (news scanning, position monitoring, scalp setups). Hard to pre-flight skip cleanly; out of scope for v1.

The fix is a **deterministic pre-flight check** that runs before the LLM is invoked. If the check determines there is no work, the harness logs a skip and reschedules without spawning opencode. The check itself is cheap: a few HTTP calls to the per-sandbox Go bot, no tokens.

## Goals

- Eliminate LLM invocations on heartbeats where the agent has no candidates to evaluate and no positions to manage.
- Maintain identical agent behavior on heartbeats that *are* invoked (no behavior change when there is work).
- Per-agent predicates so each strategy controls its own skip logic; no global rule.
- Fail open: if the pre-flight check itself errors, run the LLM as before.
- Observable: skipped beats emit events and are visible in dashboards/logs alongside normal beats.

## Non-goals (v1)

- Pre-flight skip for V2 (discretionary; too varied to model cleanly).
- Pre-flight skip for emergency wake events (always invoke LLM).
- Pre-flight skip for ad-hoc message beats (user explicitly invoked; always run).
- Pre-flight skip for the first beat of a session (always run; the agent needs to establish state).
- Per-phase variation in skip logic (a skip is a skip regardless of phase; phase-specific behavior is handled inside the agent's prompt).

## Architecture

The pre-flight check lives in `agent/harness.js` between `_runBeatCycle` and `_runClaude`. The flow becomes:

```
_runBeatCycle
  → _beat
      → _resolvePreflight()        // NEW: returns { skip: bool, reason: string }
      → if skip:
            emit 'beat_skip'
            persist a lightweight skip record
            return
        else:
            _runClaude (existing behavior)
```

The pre-flight predicate is a per-agent async function that takes the sandbox runtime and returns `{ skip, reason }`. The function can call the Go bot via the existing `goAxios` client. It must be fast (target < 500ms total) and side-effect-free.

### Predicate registration

Each strategy registers a pre-flight function in a single registry keyed by strategy ID:

```js
// agent/preflight.js
export const PREFLIGHT_REGISTRY = {
  penny:   pennyPreflight,
  trend:   trendPreflight,
  harvest: harvestPreflight,
  // v2: not registered → never skips
};

export async function resolvePreflight(strategyId, runtime, agentConfig) {
  const fn = PREFLIGHT_REGISTRY[strategyId];
  if (!fn) return { skip: false, reason: 'no preflight registered' };
  try {
    return await fn(runtime, agentConfig);
  } catch (err) {
    return { skip: false, reason: `preflight error: ${err.message}` };
  }
}
```

The harness resolves the strategy ID from `this._agentConfig.strategyId` (already loaded during `reloadConfig`) and calls `resolvePreflight` before each non-message beat.

### Wire-up in the harness

In `_beat`, after the beat header is logged but before `_runClaude` is called:

```js
// Pre-flight skip check (skip for first beat of session and emergency wakes)
const isFirstBeat = !this._sessionId;
const isEmergency = !!this._emergencyReason;
if (!isFirstBeat && !isEmergency && !this._isMessageBeat) {
  const strategyId = this._agentConfig?.strategyId;
  const runtime = this._getRuntime?.();  // injected by orchestrator
  const preflight = await resolvePreflight(strategyId, runtime, this._agentConfig);
  if (preflight.skip) {
    this.state.emit('agent_log', {
      message: `Beat #${beatNum} skipped (preflight): ${preflight.reason}`,
      level: 'info',
    });
    this.state.emit('beat_skip', { beat: beatNum, phase, reason: preflight.reason });
    this.state.stats.skippedBeats = (this.state.stats.skippedBeats || 0) + 1;
    this.state.emit('beat_end', { beat: beatNum, phase, skipped: true });
    this._beating = false;
    return;
  }
}
```

The `_getRuntime` accessor needs to be injected by the orchestrator. Currently the harness doesn't have direct access to the runtime that contains `goAxios`; the orchestrator owns that. Smallest change: add a `getRuntime` callback to `AgentHarness` constructor options and pass it from `ensureRuntime` in `orchestrator.js`.

## Per-agent predicates

### PennyProphet

```js
async function pennyPreflight(runtime, agentConfig) {
  const { goAxios } = runtime;
  const [candidates, positions] = await Promise.all([
    goAxios.get('/api/penny/candidates?min_score=60').then(r => r.data),
    goAxios.get('/api/positions?strategy=penny').then(r => r.data),
  ]);
  const hasCandidates = Array.isArray(candidates) && candidates.length > 0;
  const hasPositions  = Array.isArray(positions) && positions.length > 0;
  if (!hasCandidates && !hasPositions) {
    return { skip: true, reason: 'no candidates above min_score=60 and no open penny positions' };
  }
  return { skip: false, reason: '' };
}
```

This is the highest-value case. PennyProphet runs frequently (5-15 minute heartbeats during market hours) and is empty-handed most of the time. Conservative estimate: 60-80% of beats currently produce no action. The predicate eliminates those.

**Edge case — candidates exist but agent rules would reject all of them.** The pre-flight check uses a coarse filter (`min_score=60`); the agent applies finer logic (signal-type-specific entry rules, daily circuit breaker, etc.). On a heartbeat where 1 candidate exists at score 60 but the agent decides it is in revenue blackout and skips — that is an LLM invocation that produces no trade. Acceptable: the agent's finer rules can change between beats (P&L state, position counts), so we cannot pre-compute them cheaply.

### TrendProphet

```js
async function trendPreflight(runtime, agentConfig) {
  const { goAxios } = runtime;
  const positions = await goAxios.get('/api/positions?strategy=trend').then(r => r.data);
  if (Array.isArray(positions) && positions.length > 0) {
    return { skip: false, reason: '' };  // open positions need exit-rule evaluation
  }
  // No open positions — check if any universe ticker has a fresh entry signal
  const universe = ['TLT', 'GLD', 'USO', 'DBC', 'UUP', 'EEM'];
  const signals = await Promise.all(
    universe.map(t => goAxios.get(`/api/trend/signal/${t}`).then(r => r.data).catch(() => null))
  );
  const anyEntrySignal = signals.some(s =>
    s &&
    s.last_close > s.donchian_100_high &&
    s.last_close > s.sma_200 &&
    (s.atr_20 / s.last_close) >= 0.005
  );
  if (!anyEntrySignal) {
    return { skip: true, reason: 'no open trend positions and no entry signals across universe' };
  }
  return { skip: false, reason: '' };
}
```

This pre-flight depends on the `get_trend_signal` endpoint (separate spec). Once available, TrendProphet's daily heartbeat will skip cleanly on days with no breakouts and no positions. Out-of-window heartbeats (those that fire outside 4:55-5:05 PM ET) are a separate skip case handled inside the agent's rules; pre-flight does not need to duplicate that gate but can if it's cheap.

### Harvest

```js
async function harvestPreflight(runtime, agentConfig) {
  const { goAxios } = runtime;
  const [state, fomc] = await Promise.all([
    goAxios.get('/api/harvest/state').then(r => r.data),
    goAxios.get('/api/harvest/fomc').then(r => r.data),
  ]);
  const hasOpenCondors = (state?.open_condors ?? 0) > 0;
  const isBlackout     = !!fomc?.is_blackout;
  const atCap          = (state?.deployed_buying_power_pct ?? 0) >= 12.0
                      || (state?.open_condors ?? 0) >= 5;
  if (!hasOpenCondors && (isBlackout || atCap)) {
    return { skip: true, reason: isBlackout ? 'fomc blackout, no open condors' : 'at cap, no open condors' };
  }
  return { skip: false, reason: '' };
}
```

Harvest skips less often than PennyProphet because it almost always has open condors. The skip case is narrow: empty book + (blackout or at-cap). Still useful during multi-day blackouts.

### V2 — not registered

V2 is intentionally absent from the registry. Its responsibilities span news scanning, intraday position monitoring, scalp opportunity detection, and discretionary judgment that does not reduce to a deterministic predicate. Adding pre-flight skip here would either be unsafe (skipping while news is breaking) or so conservative that no beats actually skip. Not worth the engineering cost.

## Backend endpoints required

The pre-flight predicates assume these Go endpoints exist (some exist already, some do not):

| Endpoint | Status | Notes |
|---|---|---|
| `GET /api/penny/candidates?min_score=N` | Exists (used by penny agent's `get_penny_candidates`) | Confirm shape returns array |
| `GET /api/positions?strategy=X` | Partial | Need strategy-tag filter; depends on order tagging work |
| `GET /api/trend/signal/{ticker}` | Does not exist | Separate spec — required for TrendProphet at all |
| `GET /api/harvest/state` | Exists | Already used by `get_harvest_state` MCP tool |
| `GET /api/harvest/fomc` | Exists | Already used by `get_harvest_fomc` MCP tool |

The position-strategy filter is the cleanest dependency. If order tagging is not yet implemented, PennyProphet's pre-flight can fall back to filtering positions by ticker universe (penny stocks priced $2-10 with market cap $50M-$500M — known constraint). Same fallback for TrendProphet (positions matching the 6-ticker universe).

## Edge cases

**First beat of session:** always invoke LLM. Establishes initial state, allows the agent to reconcile positions, and surfaces any startup issues. The skip kicks in from beat #2 onward.

**Emergency wake:** always invoke LLM. The orchestrator triggers an emergency wake when external scanners detect breaking news; the whole point is for the agent to assess and react.

**Ad-hoc message beat:** always invoke LLM. The user explicitly sent a message; respond to it.

**Pre-flight check throws:** fail open. Log the error, invoke the LLM as if no pre-flight were configured. A broken pre-flight must never silently disable an agent.

**Pre-flight check is slow:** apply a 2-second timeout. If exceeded, fail open. Pre-flight that adds 10 seconds to every beat is worse than the LLM cost it saves.

**Stale data in pre-flight:** the predicate reads from the Go bot, which holds quote/position data with its own freshness. If quotes are stale, treat as "needs LLM evaluation" (do not skip). Same conservative bias as the agents' own rules.

**Pre-market and after-hours:** PennyProphet's existing rules already gate entries to market hours; pre-flight will correctly skip many pre-market beats (no candidates outside market hours). TrendProphet's pre-flight should not reject after-hours beats since that is exactly when it runs.

**Phase transitions:** if a phase transition fires an early heartbeat (`_scheduleNext` does this when the next phase boundary is closer than the heartbeat interval), pre-flight runs normally. Phase transition itself is not a reason to skip pre-flight.

## Observability

New event: `beat_skip` with payload `{ beat, phase, reason }`. Emitted alongside the existing `agent_log` skip message and a corresponding `beat_end` with `skipped: true`.

`AgentState.stats.skippedBeats` counter, included in `toJSON` output and surfaced on the dashboard. Useful metrics:
- Skip ratio (skipped / total beats) per agent
- Average tokens saved per skip (rough estimate from baseline beat token cost)
- Skip reasons distribution

The chat store should persist skip records as lightweight rows (no `assistant` message content). This lets dashboards show "the agent ran but had nothing to do" rather than gaps in the timeline.

## Implementation plan

Three small changes, each independently shippable:

1. **Harness wiring (no per-agent logic).** Add `_getRuntime` callback to `AgentHarness`, add the skip block in `_beat`, plumb the `beat_skip` event through the orchestrator's `HARNESS_EVENTS` array. Without any registered predicates, behavior is unchanged. ~50 LOC.

2. **PennyProphet predicate.** Add `pennyPreflight` to `agent/preflight.js` and register it. Validate against existing `/api/penny/candidates` endpoint. Ship behind a per-sandbox feature flag (`enablePreflightSkip: true` in sandbox config) so it can be toggled without code changes. ~30 LOC.

3. **Harvest and Trend predicates.** Add as needed once the corresponding agents are in shared paper. Trend specifically depends on the `get_trend_signal` endpoint shipping first.

The position-strategy filter can be deferred. PennyProphet's pre-flight can use a ticker-universe filter as an interim until order tagging lands.

## Testing

- **Unit:** mock `goAxios`, exercise each predicate with combinations of (candidates, positions, blackout, at-cap). Verify skip conditions match the rule logic.
- **Integration in solo paper:** enable on PennyProphet first, observe the skip ratio over a week. Expected baseline: 50-70% skip rate during regular market hours.
- **Compare cost before/after:** track tokens-per-day for PennyProphet pre- and post-rollout. The expected drop is roughly proportional to the skip rate. Anything substantially less means the predicate is not catching what it should.
- **Failure mode test:** kill the Go bot mid-beat and verify the harness fails open (logs the error, runs the LLM normally) rather than silently skipping.

## Open questions

1. **Skip-rate ceiling for healthy operation.** If PennyProphet skips 95% of beats, is the strategy still doing its job, or is the universe filter set too tight? A persistent 90%+ skip rate may indicate the strategy is mis-tuned, not just that pre-flight works. Worth setting an operator alert if skip rate exceeds 85% for a full session.
2. **Should the predicate consider news/external signals?** Currently it only checks signal pipeline output and position state. A news-driven scenario (PENNY ticker spikes 30% on an 8-K filed 10 min ago) would still produce a candidate via the regulatory signal pipeline, so it should fire correctly. But if the news pipeline is async and runs slower than the heartbeat, pre-flight could skip a beat where the agent should have woken to assess. Confirm the news-to-candidate latency is shorter than the heartbeat interval.
3. **Logging granularity.** Skip records are cheap but could accumulate quickly (PennyProphet at 5-min beats with 70% skips = ~50 skips/day). Decide whether to persist every skip or aggregate them in 15-minute buckets.
4. **Cost-attribution telemetry.** Today the harness emits cost per beat from opencode's `step_finish` events. Skipped beats have zero cost; the dashboard should distinguish "ran cheaply" from "skipped entirely" so operators can see the savings.
