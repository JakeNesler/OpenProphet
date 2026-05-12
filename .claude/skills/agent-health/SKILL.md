---
name: agent-health
description: Quick operational status check — active strategy, last heartbeat, current positions, recent errors, and whether the agent is behaving consistently with its rules. Run this any time you want a fast situational read without a full performance review.
allowed-tools: Read Glob
---

You are doing a fast operational health check on the Prophet trading agent. This should take under 60 seconds to produce — be concise.

This skill reports per-sandbox status for **every** sandbox running the `default` agent (name "Prophet"). Sandboxes are resolved by agent — never by sandbox name or hardcoded ID. If multiple sandboxes match, render one status block per sandbox (don't merge their portfolios; they're separate accounts).

## Step 1 — Config state and sandbox resolution

1. Read `data/agent-config.json`.
2. In `agents[]`, find `id === 'default'` (fallback: name containing `"Prophet"`, excluding `"PennyProphet"` and `"TrendProphet"`). Note its `model` and `strategyId`.
3. In `strategies[]`, find the entry with that id. Note `name`, `id`, and `updatedAt`. The previously-mentioned "Aggressive Options v2" naming is not authoritative — follow the agent linkage.
4. Iterate `sandboxes` and keep every entry where `agent.activeAgentId === 'default'`. For each kept entry, record `(sandboxKey, name, accountId)`. Call this list `<PROPHET_SANDBOXES>`. If empty, stop and tell the user no sandbox currently uses the agent.

For each `<S>` in `<PROPHET_SANDBOXES>`, also report:
- Heartbeat intervals (`<S>.heartbeat`: pre_market / market_open / midday / market_close / after_hours)
- Key permissions (`<S>.permissions`): allowLiveTrading, maxPositionPct, maxDailyLoss, allow0DTE, maxToolRoundsPerBeat

Flag anything unusual at the sandbox level (e.g. 0DTE enabled when it should be off, maxDailyLoss set above 5%) and note which sandbox it appears in.

## Step 2 — Per-sandbox: last session

For each `<S>` in `<PROPHET_SANDBOXES>`, read the most recent activity log from `data/sandboxes/<S.accountId>/activity_logs/`. Report:
- Date and session start time
- Starting capital, ending capital, P&L for the day ($ and %)
- Positions opened / closed / currently active
- Any errors or anomalies in the `activities` array (type: "ERROR" or reasoning containing "failed", "error", "exception")

## Step 3 — Per-sandbox: recent decisions (last 24 hours)

For each `<S>`, glob `data/sandboxes/<S.accountId>/decisive_actions/*.json`. Read the 15 most recent. Report a compact table per sandbox:

| Time | Action | Symbol | One-line reasoning summary |
|---|---|---|---|
| HH:MM | BUY/SELL/HOLD | XYZ | ... |

Flag any decision inconsistent with the active strategy (a hold past -15%, a scalp held overnight, an oversized position).

## Step 4 — Per-sandbox: loss-review protocol status

For each `<S>`, check whether any of today's decisions should have triggered the loss-review thresholds:
- Was the portfolio down >3.5% at any point? If so, did the agent pause entries?
- Was the portfolio down >5%? If so, did the agent stop trading for the day?
- Is today Monday pre-market? If so, was a weekly review logged?

## Step 5 — Health summary (one block per sandbox)

For each `<S>` in `<PROPHET_SANDBOXES>`, produce one block:

```
Sandbox:        <S.name> (accountId: <S.accountId>)
Agent:          <agent name> on <model>
Strategy:       <strategy name> (ID: <strategy id>, updated <updatedAt>)
Last session:   <date> | P&L: <+/-$X / +/-X%>
Open positions: <N> | Cash: $<X> (<X>% of portfolio)
Status:         HEALTHY / WATCH / ALERT — <one-sentence reason>
```

Use HEALTHY if everything looks normal.
Use WATCH if there's a minor anomaly (a rule bent, a loss approaching thresholds).
Use ALERT if there's a rule violation, a strategy mismatch, a session error, or a loss-review trigger that was ignored.

If two sandboxes show different statuses, that's normal and useful information — the sandboxes are independent runs of the same agent.
