---
name: review-performance-penny
description: Read recent PennyProphet activity logs and decisive actions to produce a structured performance review — win rate, profit factor, rule violations, behavioral patterns, and lessons. Use this before any penny-momentum strategy update session.
allowed-tools: Read Glob
---

You are doing a structured performance review for the PennyProphet autonomous trading agent. Work through the following steps in order.

## Step 1 — Load data

This skill aggregates history from every sandbox running the **`penny-prophet`** agent (name "PennyProphet"). Sandboxes are resolved by agent, never by sandbox name or hardcoded ID.

1. Read `data/agent-config.json`.
2. In `agents[]`, find `id === 'penny-prophet'` (fallback: name matching `/penny/i`). Note its `strategyId` (expected: `penny-momentum`).
3. In `strategies[]`, find the entry with that id — extract strategy `name`, `id`, and `customRules`. This is the rulebook the audit will compare behavior against.
4. Iterate `sandboxes` and keep every entry where `agent.activeAgentId === 'penny-prophet'`. Collect their `accountId` values as `<PENNY_DIRS>`. State the sandbox list (name → accountId) before continuing. If empty, stop and tell the user no sandbox currently uses the agent.
5. For each `<DIR>` in `<PENNY_DIRS>`:
   - Glob `data/sandboxes/<DIR>/activity_logs/activity_*.json`, read the **7 most recent per sandbox**.
   - Glob `data/sandboxes/<DIR>/decisive_actions/*.json`, merge across sandboxes, read the **80 most recent overall** by file mtime.

   Tag every loaded record with the sandbox it came from — large per-sandbox divergences are themselves a finding worth surfacing in Step 5.

PennyProphet generates more trades per day than Prophet (60-second heartbeat during market_open vs Prophet's 600-second), so the windows are wider here than in `review-performance`.

## Step 2 — Calculate metrics

From the activity logs `summary` blocks, aggregate across all loaded days:
- Total trades (positions_opened + positions_closed)
- Winning trades vs losing trades → **win rate %**
- Total P&L in dollars and percent
- Largest single win and largest single loss
- Starting vs ending capital trajectory
- Average `capital_deployed` (track segment-cap utilization vs 30% target)

From the decisive actions, for each SELL or CLOSE action that contains a P&L signal in `reasoning` or `details`:
- Classify as winner or loser
- Record the size of the move (% gain or loss mentioned in reasoning or pnl_pct field)
- Compute **profit factor** = sum of winning trade P&L / abs(sum of losing trade P&L). Flag if < 1.0.

Also break down by `dominant_signal` (social / regulatory / technical) where possible — penny win rates can vary dramatically by signal type.

## Step 3 — Rule violation audit

For each decisive action, compare the `reasoning` and `details` fields against these hard rules from the penny-momentum strategy:

- Composite score ≥ 60 at entry (no sub-60 entries)
- Tiered sizing: 5–7% at score 80–100, 2–3% at score 60–79, hard cap 8%
- Maximum 10 open penny positions simultaneously
- Maximum 30% of portfolio deployed in penny positions (segment cap)
- All entries via `place_managed_position` with stop and target pre-set
- Signal-type-correct stops/targets:
  - social: −8% stop, +15%/+20% target
  - regulatory: −10% stop, +20% day 1
  - technical: −7% stop, +14% target, trail to breakeven at +7%
- Social positions exited within 20 minutes (or 15 min before close)
- No social entries in the last 30 minutes of trading
- Daily circuit breaker: at portfolio P&L ≤ −5%, all penny positions closed, no further entries until next session

List each apparent violation with: timestamp, symbol, rule broken, and the quoted reasoning excerpt.

## Step 4 — Behavioral pattern analysis

PennyProphet's rules explicitly forbid "helpful improvisation". Look across all decisions for:

- **Score-override improvisation**: entries where reasoning argues a sub-60 score was acceptable
- **Stop discipline failure**: holding past the signal-type stop ("might recover", "looks like a flush")
- **Social-window drift**: social positions held past the 20-minute mark with reasoning attempting to justify the hold
- **Tier-collapse**: position sizing converging to a single default regardless of composite score
- **Re-entry on faded signal**: entering the same ticker again shortly after a stop-out without a new high-score signal
- **Free-form commentary**: reasoning fields that read like market analysis rather than rule-execution logs
- **Good discipline**: prompt time-based exits, fast circuit-breaker compliance, accurate signal-typed stops, skipping when score < 60

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
| Avg deployed (penny segment) | |
| Win rate by signal type (social/reg/tech) | |

### Rule Violations
List each violation found. If none, say so.

### Behavioral Patterns
What is the agent doing well? What bad habits are emerging? Is improvisation creeping in?

### Top 3 Lessons
The three most actionable insights from this review period. Each lesson should be specific and tied to a real decision, not generic advice.

### Recommended Strategy Adjustments
List any rule changes these findings suggest. Be specific — quote the current rule and propose the replacement text. Do not apply changes here; use `/adapt-strategy-penny` for that.
