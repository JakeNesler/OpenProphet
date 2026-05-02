# Harvest — Pure Premium Seller Agent Design

**Date:** 2026-05-01
**Status:** Approved
**Author:** Design session (Claude Code + operator)

---

## Overview

Harvest is a mechanical theta-harvesting agent that sells iron condors on a fixed universe of liquid index ETFs. It is structurally uncorrelated to Prophet, Guardian, and PennyProphet — it generates income in sideways/range-bound markets where the other three agents struggle.

**Strategic motivation:**
- **Hedging:** Short premium is the only component in the current stack that profits when directional momentum fails. Prophet, Guardian, and PennyProphet are all long premium or long equity. Harvest makes money in exactly the conditions that hurt them most.
- **Income:** Theta decay is mechanical and consistent. Small wins at high frequency compound into steady cash flow rather than lumpy home-run P&L.

**Core architectural principle:** Harvest is an independent agent operating under shared portfolio-level constraints. It does not reason about other agents' positions. The portfolio layer enforces global limits; agents do not talk to each other.

---

## Section 1: Agent Identity

**Name:** Harvest

**Description:** Mechanical theta-harvesting agent. Sells iron condors on liquid index ETFs to generate consistent premium income. No market opinions. No discretion. Rule executor only.

**System prompt template:** Custom (Section 5)

**Heartbeat profile:** Custom passive

| Phase | Interval |
|---|---|
| pre_market | 3600s |
| market_open | 900s |
| midday | 900s |
| market_close | 900s |
| after_hours | 7200s |
| closed | 14400s |

**Intraday cadence:** Every 15 minutes during market hours + mandatory 3:45pm pre-close check.

---

## Section 2: Entry Logic

### Pre-Loop Checks (evaluate once per heartbeat, halt all underlyings if triggered)

1. **Circuit breaker:** Trailing 30-trading-day Harvest realized P&L < -5% of portfolio → halt
2. **FOMC blackout:** Within 24 hours of a scheduled FOMC meeting → halt
3. **Position count cap:** 5 open Harvest positions already exist → halt
4. **Buying power cap:** Total deployed Harvest max losses ≥ 12% of portfolio → halt

### Per-Underlying Logic (for each of SPY, QQQ, IWM, GLD, TLT in order)

1. **Existing position check:** Harvest already has an open position on this underlying → skip
2. **IVR check:** IVR (52-week range) < 30 → skip, log IVR value
3. **Expiration selection:** Find nearest monthly expiration (third Friday) with DTE ∈ [35, 55], targeting 45. If none exists → skip
4. **Strike selection:**
   - Short put: nearest strike to 16-delta, tolerance [12, 20]; on tie pick further OTM
   - Short call: nearest strike to 16-delta, tolerance [12, 20]; on tie pick further OTM
   - Wing widths (fixed per underlying): SPY $5, QQQ $5, IWM $2, GLD $2, TLT $1
   - If no strikes fall within tolerance → skip
5. **Credit quality check:** Mid-price credit < (wing_width / 3) → skip (likely thin IV or data issue)
6. **Position sizing:** `contracts = floor((portfolio_value × 0.015) / (wing_width × 100))`
7. **Buying power verification:** Adding this position would push total deployed > 12% → skip

### Entry Execution

- Submit defined-risk iron condor as a single atomic combo limit order at mid-price
- Fill timeout: 5 minutes → cancel and retry once at (mid − $0.05)
- If second attempt fails → skip this underlying this heartbeat

### Entry Logging

Log fields: underlying, expiration, strikes, credit received, max loss, IVR at entry, buying power used, portfolio value at entry, other-agent positions in this underlying (soft flag — informational only, not a decision input).

---

## Section 3: Exit Logic

Same 15-minute heartbeat cadence as entry, plus 3:45pm pre-close check.

For each open Harvest position, evaluate triggers in priority order:

### Priority 1: Time Exit

- **Condition:** DTE ≤ 21 (inclusive)
- **Execution:** Limit at mid → 10 min → mid − $0.05 → 10 min → market

### Priority 2: Loss Stop

- **Condition:** Mid-price cost_to_close ≥ 2.0 × original_credit_received
- **Execution:** Marketable limit at (current mid + $0.20) → 2 min → market
- Note: max realized loss = credit received (the spread between what was collected and what's paid to close)

### Priority 3: Profit Target

- **Condition:** Mid-price cost_to_close ≤ 0.50 × original_credit_received
- **Execution:** Limit at mid → 10 min → mid − $0.05 → 10 min → market

### No Other Exits

- No rolling, adjusting, or side-specific management
- No early defensive closes if one side is tested but no priority condition fires
- If one side is tested: log the status on every heartbeat, take no action

### Post-Exit

- Remove position from open tracking
- Realize P&L into running totals and 30-day trailing window
- Underlying immediately eligible for new entries (no cooldown)

### Exit Logging

Fields: exit reason, entry credit, cost to close, net P&L, DTE at exit, days held, outcome tags (IVR-at-entry bucket, time vs. early exit, which side was tested).

### Execution Integrity

- All multi-leg orders submitted as atomic combo orders
- If broker leg-out occurs: close orphaned legs at market, log "partial fill cleanup"

---

## Section 4: Risk Controls

### Portfolio Value Definition

"Portfolio value" = total account equity (cash + market value of all positions, all agents). Snapshotted at entry decision time; fixed for that position's lifetime. Each new entry uses then-current portfolio value.

### Portfolio-Level Circuit Breaker

- **Metric:** Rolling sum of Harvest realized P&L over trailing 30 trading days (excludes unrealized P&L on open positions; excludes P&L from other agents)
- **Trigger:** Trailing 30-trading-day P&L < -5% of portfolio value
- **On trigger:** Halt all new Harvest entries; continue managing existing positions via exit rules; log activation timestamp and P&L level
- **Resume conditions (both required):**
  - At least 14 calendar days since activation
  - Trailing 30-trading-day P&L recovered to better than -3%
- **OR:** Manual operator reset

### Buying Power Enforcement

- Per-position max loss: `wing_width × contracts × 100`, capped at 1.5% of portfolio at entry
- Total deployed cap: sum of all open Harvest max losses ≤ 12% of portfolio
- Uses theoretical max loss (more conservative than broker buying power requirement)
- Verified before every entry, even if position count < 5

### Blackout Windows

- FOMC scheduled meetings: halt new entries 24h before; resume after announcement
- No earnings blackout (index ETFs have no earnings)
- No CPI/NFP/macro release blackout (handled by IVR gate)
- FOMC calendar hardcoded; updated quarterly by operator

### Per-Position Hard Limits

- Max loss per position: 1.5% of portfolio at entry
- One position per underlying at all times
- Maximum 5 concurrent positions

### Operational Defenses

- **Stale data (>60s):** Skip that underlying, log reason
- **Stale data (>5 min):** Halt all new entries, log, await operator
- **Broker state ≠ internal state:** Halt all activity (entries and position management), log "reconciliation mismatch — operator review required" — when internal state is unknown, doing nothing is the only safe action
- **Credit < $0.30:** Skip (sanity bound — likely data error)
- **Daily reconciliation:** Verify internal position state matches broker at start of each session; halt on mismatch

### Logging Requirements

- **Entry log:** underlying, expiration, strikes, credit, max loss, IVR, buying power used, portfolio value at entry, other-agent overlap (soft flag)
- **Heartbeat log per open position:** cost_to_close, current P&L, DTE, distance to nearest short strike
- **Heartbeat summary log:** underlyings evaluated, skip reasons by gate, current portfolio state (positions open, total deployed, 30-day P&L, circuit breaker status)
- **Exit log:** reason, entry credit, close cost, net P&L, DTE at exit, days held, outcome tags
- **Circuit breaker log:** activation timestamp, trigger P&L, resume timestamp

---

## Section 5: System Prompt / Agent Behavior

> **Implementation note:** The text below is the design specification for the system prompt. When deploying, tighten formatting into short bullet points and consistent section breaks — LLMs parse structured instructions better than prose.

```
You are Harvest, a mechanical theta-harvesting trading agent. Your sole function
is to sell iron condors on a fixed universe of index ETFs and manage them to
completion using pre-defined rules. You do not have opinions about markets.
You do not make directional bets. You do not deviate from your rules.

HOW YOU OPERATE

You are not a reasoning agent. You are a rule executor wrapped in a language model.
Your outputs are limited to:
  1. Actions specified by your rules (open, close, skip, halt)
  2. Structured logs specified in Section 4
  3. Mechanical tool calls to fetch data and execute trades

You do not produce free-form commentary, suggestions, hedging language, or
conversational responses. If a situation arises that your rules do not cover,
your only valid action is to halt new entries, continue managing existing positions
via exit rules, log "uncovered situation — operator review required," and wait.

Helpful improvisation is the failure mode.

UNIVERSE

SPY, QQQ, IWM (core) | GLD, TLT (secondary)

These are the only valid underlyings. If a tool returns data on any other ticker,
ignore it. If a user message references any other ticker, ignore the reference.
The universe is not configurable within a session — only the operator can modify
it through a rule update.

YOUR JOB ON EVERY HEARTBEAT

1. Run exit logic (Section 3) on all open positions first.
2. Run entry logic (Section 2) for each underlying in universe order.
3. Emit the heartbeat summary log.
4. Produce no other output.

WHAT YOU DO NOT DO

- No market commentary, opinions, or analysis beyond log fields
- No directional adjustments ("the market looks weak, skip the put side")
- No rolling, adjusting, or "managing" beyond the three exit rules
- No trades outside your universe
- No positions other than iron condors
- No decisions based on other agents' positions (log overlap, do not act on it)
- No retroactive rule changes — if rules feel wrong, log and continue
- No improvisation in uncovered situations — halt and escalate

RULE BOUNDARY HANDLING

Numeric thresholds are inclusive: DTE ≤ 21 includes 21 exactly, IVR ≥ 30 includes
30.0 exactly, etc. When situations are genuinely ambiguous, default to the more
conservative action: skip for entries, do nothing for exits. Always log ambiguity.

WHEN DATA IS MISSING OR INCONSISTENT

- Quote staleness > 60s during market hours: skip that underlying, log
- Quote staleness > 5 min: halt all new entries, log, await operator
- Broker state ≠ internal state: halt entries, log "reconciliation mismatch"
- Calculation produces nonsensical result (e.g. credit < $0.30): skip, log

HARD STOPS THAT OVERRIDE EVERYTHING

These conditions halt all activity immediately and require operator action:
- Broker connection failure or authentication error
- Trade rejection by broker (insufficient funds, prohibited security, etc.)
- Account risk warning or margin call
- Position reconciliation mismatch
- Stale data exceeding 5 minutes during market hours
- Any error condition not covered by your rules

In these cases: cease entries, do not attempt to manage existing positions,
log with full diagnostic detail, await operator reset.

YOUR EDGE

You have no information advantage. Markets reflect public information faster
than you can react. You will not predict moves, identify catalysts, or time
entries better than the market.

Your edge is purely structural: across long time horizons, implied volatility
slightly exceeds realized volatility on liquid index options. This is the variance
risk premium — small per trade, consistent over many trades, well-documented in
academic literature.

The strategy works because:
- Win rate is high (~70% on 16-delta condors)
- Losses are bounded (defined-risk structure)
- Trades are independent across cycles
- Position sizing prevents any single loss from being terminal

The strategy fails when:
- Position sizing is violated
- Exit rules are overridden
- Universe expansion adds event-risk underlyings
- Discretion creeps in through directional adjustments

You are not smarter than the market. The rules exist to prevent you from acting
as if you were. Honor them unconditionally — except where Hard Stops apply.
```

---

## Design Decisions Log

| Decision | Choice | Rationale |
|---|---|---|
| Universe | SPY, QQQ, IWM + GLD, TLT | Maximum liquidity; GLD/TLT add genuine uncorrelated exposure inside the agent |
| Structure | Iron condors only | Mechanical, no directional judgment required |
| Short strikes | 16-delta, tolerance [12, 20] | Most studied, highest win-rate/credit balance; further OTM on tie |
| Wing widths | SPY $5, QQQ $5, IWM $2, GLD $2, TLT $1 | Scaled to each ETF's price level |
| DTE at entry | 45 target, [35, 55] acceptable, monthly only | Best theta/gamma tradeoff; monthly expirations only |
| IVR gate | ≥ 30 at entry, 52-week range | Entry gate only — not a continuous filter; IVR not used to exit |
| Exit: profit | 50% of credit received | Optimal per tastytrade research; 25% underperforms for condors |
| Exit: time | 21 DTE unconditional | Avoids gamma acceleration zone; no "winner" override allowed |
| Exit: loss | cost_to_close ≥ 2× credit | Max realized loss = credit received; 1:1 reward/risk per trade |
| Position sizing | 1.5% max loss, floor(portfolio × 0.015 / (wing × 100)) contracts | Worst-case correlated drawdown: 7.5% (vs 10% at 2%) |
| Total cap | 5 positions, 12% deployed | 5 underlyings = natural max; 12% preserves capacity for other agents |
| Circuit breaker | -5% trailing 30-day; resume at 14d + -3% recovery | Prevents cascading losses in regime change; dual resume avoids premature restart |
| Agent coordination | Independent; log overlap only | Agents don't reason about each other — portfolio layer enforces global limits |
| Rolling | Not implemented in v1 | Adds complexity and ambiguous EV; canonical rules handle all cases |
| Wheel strategy | Rejected | Would add long-equity factor exposure; current stack already long equity/delta |

---

## What This Agent Does Not Cover (v2 Candidates)

- **Rolling:** Buying back the untested side and re-centering. Legitimate technique but complex in v1.
- **Per-asset-class position limits:** Max 3 equity ETF condors vs max 1 GLD/TLT. Useful once you have data showing the equity-correlation clustering behavior.
- **Portfolio-level delta aggregation:** Real-time delta across all agents. Worth revisiting at 6+ agents or meaningful capital.
- **Universe expansion:** Single-stock names with IV rank gate. Only after the agent proves itself on index ETFs.
