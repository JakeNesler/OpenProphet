---
name: review-performance
description: Read recent activity logs and decisive actions to produce a structured performance review — win rate, profit factor, rule violations, behavioral patterns, and lessons. Use this before any strategy update session.
allowed-tools: Read Glob
---

You are doing a structured performance review for the Prophet autonomous trading agent. Work through the following steps in order.

## Step 1 — Load data

This skill aggregates history from every sandbox running the **`default`** agent (name "Prophet"). Sandboxes are resolved by agent, never by sandbox name or hardcoded ID.

1. Read `data/agent-config.json`.
2. In `agents[]`, find `id === 'default'` (fallback: name containing `"Prophet"` case-insensitive, excluding `"PennyProphet"` and `"TrendProphet"`). Note its `strategyId`.
3. In `strategies[]`, find the entry with that id — extract strategy `name`, `id`, and `customRules`. This is the rulebook the audit will compare behavior against.
4. Iterate `sandboxes` and keep every entry where `agent.activeAgentId === 'default'`. Collect their `accountId` values as `<PROPHET_DIRS>`. State the sandbox list (name → accountId) before continuing. If empty, stop and tell the user no sandbox currently uses the agent.
5. For each `<DIR>` in `<PROPHET_DIRS>`:
   - Glob `data/sandboxes/<DIR>/activity_logs/activity_*.json`, read the **5 most recent per sandbox**.
   - Glob `data/sandboxes/<DIR>/decisive_actions/*.json`, merge across sandboxes, read the **40 most recent overall** by file mtime.

   Tag every loaded record with the sandbox it came from — large per-sandbox divergences are themselves a finding worth surfacing in Step 5.

## Step 2 — Calculate metrics

From the activity logs `summary` blocks, aggregate across all loaded days:
- Total trades (positions_opened + positions_closed)
- Winning trades vs losing trades → **win rate %**
- Total P&L in dollars and percent
- Largest single win and largest single loss
- Starting vs ending capital trajectory

From the decisive actions, for each SELL or CLOSE action that contains a P&L signal in `reasoning`:
- Classify as winner or loser
- Record the size of the move (% gain or loss mentioned in reasoning)
- Compute **profit factor** = sum of winning trade P&L / abs(sum of losing trade P&L). Flag if < 1.0.

## Step 3 — Rule violation audit

For each decisive action, compare the `reasoning` field against these hard rules from the strategy:
- Position size ≤ 15% of portfolio
- Max 10 positions simultaneously
- Scalps (≤5 DTE): must close by EOD; stop at -15%
- Swing positions: 50–120 DTE, delta 0.40–0.70
- No market orders — limit orders only
- Bid-ask spread < 10% of mid
- Max 5 scalp entries per day
- Max 10 total trades per day
- No re-entry within 2 hours of a stop-out (no revenge trading)

List each apparent violation with: timestamp, symbol, rule broken, and the quoted reasoning excerpt.

## Step 4 — Behavioral pattern analysis

Look across all decisions for:
- **Hesitation**: multiple HOLD decisions on a position that was already at or past stop
- **Revenge trading**: BUY within 2 hours of a SELL on the same symbol
- **Thesis drift**: holding past -15% with reasoning that shifts ("giving it more time")
- **Missed exits**: winning positions that turned to losers (can infer from reasoning language like "reversed" or "gave back")
- **Good discipline**: fast cuts, profit-taking at targets, consistent logging

## Step 5 — Report

Produce a clean report with these sections:

### Performance Summary
| Metric | Value |
|---|---|
| Period covered | |
| Total trades | |
| Win rate | |
| Profit factor | |
| Total P&L | |
| Largest win | |
| Largest loss | |

### Rule Violations
List each violation found. If none, say so.

### Behavioral Patterns
What is the agent doing well? What bad habits are emerging?

### Top 3 Lessons
The three most actionable insights from this review period. Each lesson should be specific and tied to a real decision, not generic advice.

### Recommended Strategy Adjustments
List any rule changes these findings suggest. Be specific — quote the current rule and propose the replacement text. Do not apply changes here; use `/adapt-strategy` for that.
