---
name: agent-health-penny
description: Quick operational status check for PennyProphet — active strategy, last heartbeat, current managed positions with stop/target levels, recent errors, and whether the agent is behaving consistently with its penny-momentum rules. Run this any time you want a fast situational read on the penny agent without a full performance review.
allowed-tools: Read Glob
---

You are doing a fast operational health check on the PennyProphet trading agent. This should take under 60 seconds to produce — be concise.

This skill reports per-sandbox status for **every** sandbox running the `penny-prophet` agent. Sandboxes are resolved by agent — never by sandbox name or hardcoded ID. If multiple sandboxes match, render one status block per sandbox (don't merge their portfolios; they're separate accounts).

## Step 1 — Config state and sandbox resolution

1. Read `data/agent-config.json`.
2. In `agents[]`, find `id === 'penny-prophet'` (fallback: name matching `/penny/i`). Note its `model` and `strategyId` (expected `penny-momentum`).
3. In `strategies[]`, find the strategy by that id. Note `name`, `id`, and `updatedAt`.
4. Iterate `sandboxes` and keep every entry where `agent.activeAgentId === 'penny-prophet'`. For each kept entry, record `(sandboxKey, name, accountId)`. Call this list `<PENNY_SANDBOXES>`. If empty, stop and tell the user no sandbox currently uses the agent.

For each `<S>` in `<PENNY_SANDBOXES>`, also extract and report:
- Heartbeat intervals (`<S>.heartbeat`: pre_market / market_open / midday / market_close / after_hours / closed)
- Key permissions (`<S>.permissions`): allowLiveTrading, maxPositionPct, maxDeployedPct, maxDailyLoss, maxOpenPositions, allowOptions (must be false), allow0DTE, maxToolRoundsPerBeat

Flag anything unusual at the sandbox level (call out which sandbox):
- `allowOptions = true` (penny is stocks-only by rule)
- `maxDeployedPct` > 30 (penny segment cap is 30% per `customRules`; if looser, the binding constraint is the rule itself — call out the divergence)
- `maxPositionPct` > 8 (penny hard cap per position is 8%)
- `maxDailyLoss` > 5 (circuit breaker is −5%)
- Active strategy not the resolved `penny-prophet` strategyId

## Step 2 — Per-sandbox: last session

For each `<S>`, read the most recent activity log from `data/sandboxes/<S.accountId>/activity_logs/`. Report:
- Date and session_start time
- Starting capital, ending capital, P&L for the day ($ and %)
- positions_opened / positions_closed / active_positions
- capital_deployed (penny segment utilization)
- stocks_analyzed, news_articles_read (signal-pipeline activity proxy)
- Any errors or anomalies in the `activities` array (type: "ERROR" or reasoning containing "failed", "error", "exception", "halt", "reconciliation mismatch", "operator review required")

## Step 3 — Per-sandbox: recent decisions (last 24 hours)

For each `<S>`, glob `data/sandboxes/<S.accountId>/decisive_actions/*.json`. Read the 25 most recent. Report a compact table per sandbox:

| Time | Action | Symbol | Score | Signal | One-line reasoning summary |
|---|---|---|---|---|---|
| HH:MM | BUY/SELL/SKIP/CIRCUIT_BREAKER | XYZ | 84 | social | ... |

(`Score` and `Signal` come from `details.composite_score` and `details.dominant_signal` where present; leave blank if not logged.)

Flag any decision inconsistent with the active penny-momentum rules:
- BUY with composite score < 60
- BUY without `place_managed_position` (no bracket)
- Position size outside the score-tier (e.g. 6% at score 65)
- Social entry within last 30 min of close
- Hold past the social 20-minute window
- Any entry after a circuit-breaker trip in the same session

## Step 4 — Per-sandbox: active managed positions

PennyProphet uses `place_managed_position` exclusively. For each `<S>`, from its most recent activity log extract `positions_opened` and any current open positions referenced in the latest decisions. For each currently open managed position, report:

| Symbol | Entry time | Entry $ | Size % | Signal | Stop level | Target level | Time-in-position |
|---|---|---|---|---|---|---|---|

For social positions: also note time-remaining-in-window (20-minute clock).
For technical positions: note whether breakeven trail has been activated (price ≥ +7% from entry).

If the most recent activity log doesn't include explicit position rows, infer open positions from the running tally of `positions_opened` minus `positions_closed`, and note "no open managed positions" if that nets to zero.

## Step 5 — Per-sandbox: loss-review / circuit breaker status

For each `<S>`, check whether any of today's decisions should have triggered the circuit breaker:
- Was portfolio P&L ≤ −5% intraday at any point? If so, did the agent cancel all open brackets, market-sell all penny positions, and halt new entries for the rest of the session?
- Is the `_lastPennyReviewWeek` schedule due? (Mondays 6:10 AM ET — flag if a Monday has gone by without a `review-performance-penny` run.)

## Step 6 — Health summary (one block per sandbox)

For each `<S>` in `<PENNY_SANDBOXES>`, produce one block:

```
Sandbox:        <S.name> (accountId: <S.accountId>)
Agent:          PennyProphet on <model>
Strategy:       <strategy name> (ID: <strategy id>, updated <updatedAt>)
Last session:   <date> | P&L: <+/-$X / +/-X%>
Open managed positions: <N> | Capital deployed: $<X> (<X>% of portfolio)
Status:         HEALTHY / WATCH / ALERT — <one-sentence reason>
```

Use HEALTHY if everything looks normal.
Use WATCH if there's a minor anomaly (a bracket-rejected ticker re-attempted, a near-miss on the segment cap, an entry close to the score-tier boundary).
Use ALERT if there's a rule violation, a strategy mismatch (active agent not `penny-prophet` or strategy not the resolved id), a session error, an `operator review required` halt, or a circuit-breaker trigger that was ignored.

If two sandboxes show different statuses, that's normal and useful information — the sandboxes are independent runs of the same agent.
