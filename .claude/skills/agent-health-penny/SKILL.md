---
name: agent-health-penny
description: Quick operational status check for PennyProphet — active strategy, last heartbeat, current managed positions with stop/target levels, recent errors, and whether the agent is behaving consistently with its penny-momentum rules. Run this any time you want a fast situational read on the penny agent without a full performance review.
allowed-tools: Read Glob
---

You are doing a fast operational health check on the PennyProphet trading agent. This should take under 60 seconds to produce — be concise.

## Step 1 — Config state

Read `data/agent-config.json`. Look up the PennyProphet sandbox at `sandboxes.sbx_a788a4e3`. Extract and report:
- Active agent (`sandboxes.sbx_a788a4e3.agent.activeAgentId` — should be `penny-prophet`)
- Active strategy: cross-reference the agent (`agents[]` with id `penny-prophet`) → its `strategyId` should be `penny-momentum`. Confirm a strategy with id `penny-momentum` (name `Penny Stock Momentum`) exists in `strategies[]`.
- Active model (`sandboxes.sbx_a788a4e3.agent.model`)
- Heartbeat intervals (`sandboxes.sbx_a788a4e3.heartbeat`: pre_market / market_open / midday / market_close / after_hours / closed)
- Key permissions (`sandboxes.sbx_a788a4e3.permissions`): allowLiveTrading, maxPositionPct, maxDeployedPct, maxDailyLoss, maxOpenPositions, allowOptions (must be false), allow0DTE, maxToolRoundsPerBeat
- Strategy `updatedAt` timestamp

Flag anything unusual:
- `allowOptions = true` (penny is stocks-only by rule)
- `maxDeployedPct` > 30 (penny segment cap is 30% per `customRules` — sandbox permission may be looser; if so, the binding constraint is the rule itself, but call out the divergence)
- `maxPositionPct` > 8 (penny hard cap per position is 8%)
- `maxDailyLoss` > 5 (circuit breaker is −5%)
- Active strategy not `penny-momentum`

## Step 2 — Last session

Read the most recent activity log from `data/sandboxes/a788a4e3/activity_logs/`. Report:
- Date and session_start time
- Starting capital, ending capital, P&L for the day ($ and %)
- positions_opened / positions_closed / active_positions
- capital_deployed (penny segment utilization)
- stocks_analyzed, news_articles_read (signal-pipeline activity proxy)
- Any errors or anomalies in the `activities` array (look for type: "ERROR" or reasoning containing "failed", "error", "exception", "halt", "reconciliation mismatch", "operator review required")

## Step 3 — Recent decisions (last 24 hours)

Glob `data/sandboxes/a788a4e3/decisive_actions/*.json`. Read the 25 most recent. Report a compact table:

| Time | Action | Symbol | Score | Signal | One-line reasoning summary |
|---|---|---|---|---|---|
| HH:MM | BUY/SELL/SKIP/CIRCUIT_BREAKER | XYZ | 84 | social | ... |

(`Score` and `Signal` come from `details.composite_score` and `details.dominant_signal` where present; leave blank if not logged.)

Flag any decision that looks inconsistent with the active penny-momentum rules:
- BUY with composite score < 60
- BUY without `place_managed_position` (no bracket)
- Position size outside the score-tier (e.g. 6% at score 65)
- Social entry within last 30 min of close
- Hold past the social 20-minute window
- Any entry after a circuit-breaker trip in the same session

## Step 4 — Active managed positions

PennyProphet uses `place_managed_position` exclusively. From the most recent activity log, extract `positions_opened` and any current open positions referenced in the latest decisions. For each currently open managed position, report:

| Symbol | Entry time | Entry $ | Size % | Signal | Stop level | Target level | Time-in-position |
|---|---|---|---|---|---|---|---|

For social positions: also note time-remaining-in-window (20-minute clock).
For technical positions: note whether breakeven trail has been activated (price ≥ +7% from entry).

If the most recent activity log doesn't include explicit position rows, infer open positions from the running tally of `positions_opened` minus `positions_closed`, and note "no open managed positions" if that nets to zero.

## Step 5 — Loss-review / circuit breaker status

Check whether any of today's decisions should have triggered the circuit breaker:
- Was portfolio P&L ≤ −5% intraday at any point? If so, did the agent cancel all open brackets, market-sell all penny positions, and halt new entries for the rest of the session?
- Is the `_lastPennyReviewWeek` schedule due? (Mondays 6:10 AM ET — flag if a Monday has gone by without a `review-performance-penny` run.)

## Step 6 — Health summary

Produce a clean 5-line status block:

```
Agent:        PennyProphet on [model]
Strategy:     [strategy name] (ID: penny-momentum, updated [updatedAt])
Last session: [date] | P&L: [+/-$X / +/-X%]
Open managed positions: [N] | Capital deployed: $[X] ([X]% of portfolio)
Status:       HEALTHY / WATCH / ALERT — [one-sentence reason]
```

Use HEALTHY if everything looks normal.
Use WATCH if there's a minor anomaly (a bracket-rejected ticker re-attempted, a near-miss on the segment cap, an entry close to the score-tier boundary).
Use ALERT if there's a rule violation, a strategy mismatch (active agent not `penny-prophet` or strategy not `penny-momentum`), a session error, an `operator review required` halt, or a circuit-breaker trigger that was ignored.
