# Agent Capability Improvements — Brief for New Claude Session

> **How to use this file:** paste the "Prompt to Start the Session" block at the
> bottom into a new Claude Code session. This document is the full context that
> session will need; the prompt just points at it.

---

## 1. Project Context

This repo is a multi-agent trading system that runs four distinct strategies, each
with its own LLM beat loop, rules file, and Alpaca-backed execution:

| Agent | Strategy ID | Rules file | What it does |
|---|---|---|---|
| **Prophet** | `v2-options` | `TRADING_RULES_V2.md` | Aggressive discretionary options trading (calls/puts, 2-120 DTE, scalps + swings) |
| **Harvest** | `harvest` | `TRADING_RULES_HARVEST.md` | Mechanical iron-condor premium selling, 35-55 DTE |
| **PennyProphet** | `penny-momentum` | `TRADING_RULES_PENNY.md` | Sub-$10 penny-stock momentum, intraday/swing |
| **TrendProphet** | `trend` | `TRADING_RULES_TREND.md` | Donchian-100 breakout trend-following on TLT/GLD/USO/DBC/UUP/EEM |

Each agent has a **preflight predicate** in `agent/preflight.js` that decides
whether to skip the LLM beat. This saves tokens (a Harvest beat with no
chain data can burn 110-200K tokens investigating before giving up — the
preflight short-circuits it).

The MCP tool surface the agents call is in `mcp-server.js`. Core Go services
for Alpaca data, technical analysis, and stock analysis are in `services/`.

---

## 2. Gap Analysis (what's missing today)

### Prophet (`v2-options`)
- **TA is daily-only.** `services/stock_analysis_service.go:131` hardcodes
  `"1Day"` bars over a 30-day window. Indicators in
  `services/technical_analysis.go` (SMA20/SMA50, RSI(14), simplified MACD,
  1D/5D momentum, 20-bar volume ratio) all run on daily bars.
- **No intraday signals.** No VWAP, no 5-min RSI, no opening-range data,
  no cumulative RVOL, no capitulation/reversal detection, no
  higher-low/lower-high structure. (Alpaca-side intraday timeframes are
  *available* — `services/alpaca_data.go:228-236` supports 5Min/15Min/30Min/1Hour
  — but nothing computes signals from them.)
- **No options-state awareness.** No IV rank, IV percentile, term structure,
  skew, dealer gamma, or unusual-options-activity surfacing. The agent picks
  strikes blind to whether premium is cheap or expensive.
- **No sector/breadth context.** The agent sees AMD's daily move but not
  SMH's, $TICK, or the SPY tape.
- **Runs every scheduled beat during market hours regardless of opportunity.**
  Preflight only skips during the overnight `closed` phase
  (`agent/preflight.js:98-119`).

### Harvest (`harvest`)
- **Entries not gated on IV state.** Selling condors with IV rank 15 vs 75
  is night and day on edge, but the strategy doesn't read IV rank anywhere.
- **No realized-vs-implied vol spread filter.** The whole premium-selling
  thesis hinges on implied > realized; currently entries fire mechanically
  regardless.
- **No dealer-gamma regime check.** Negative-GEX regimes are
  volatility-amplifying and condor-hostile, but nothing flags them.

### PennyProphet (`penny-momentum`)
- **Volume scoring uses absolute volume, not time-of-day relative.**
  See `services/penny_signal_aggregator.go`. Cumulative RVOL by time-of-day
  would be much higher signal.
- **No pre-market high/low or opening-range data** in entry confirmation.
- **No short-interest / float data** to detect squeeze setups.

### TrendProphet (`trend`)
- Well-defined Donchian-100 + SMA-200 + ATR/close filter, but...
- **No cross-asset confirmation.** TLT signal is stronger when DXY weakens
  and yields fall; USO signal is stronger when DXY weakens and inventory
  data agrees. None of that context surfaces.
- **No credit-spread regime filter.** HY spread widening leads risk-asset
  selloffs by days; ignoring it is a known blind spot for breakout systems.

### All agents
- **No economic-calendar awareness.** No auto-blackout for the 30 minutes
  around CPI, NFP, FOMC, PCE, or PPI. Entries in those windows are some of
  the most common avoidable losses.
- **No OPEX-week sizing adjustment.** Third-Friday liquidity vacuum is a
  known quirk.

---

## 3. Critical Token-Cost Lesson (read before designing anything)

The preflight pattern in this codebase is the *token-saver* shape — it
short-circuits the LLM when there's nothing to do. Any new "wake the agent
when X happens" design is the *inverse* and spends tokens.

Three design archetypes, in cost order:

| Archetype | Cost | Use when |
|---|---|---|
| **Pull-based MCP tool** (LLM calls when it wants) | ~0 | Adding any new data source. Schema is ~150 tokens once; responses are tiny. |
| **Push-based context on existing beats** (extra fields in `analyze_stocks` or a pre-computed blob handed to the LLM) | ~200-500 input tokens per beat. Trivial. | Adding intraday context, IV state, breadth — anywhere the LLM was already going to run. |
| **Event-driven extra beats** (wake the LLM early when a trigger fires) | ~100K input tokens per extra beat. 0-8 extra beats/day → $0-$8/day worst case. | Only when cadence is genuinely the bottleneck and a tight, deduped trigger is provable. |

**Strong default: enrich existing beats. Don't add new wakes unless it's the
last resort.** Adding noise-driven extra beats risks LLM overreaction and
overtrading — the bigger risk than token cost itself.

---

## 4. Prioritized Backlog

Each item lists: scope, files to touch, acceptance criteria, sizing.

### P0 — Highest leverage, lowest cost

#### 4.1 Economic-calendar auto-skip (all agents)
**Scope:** Add a shared preflight helper `isEconomicBlackout(now)` that
returns `{ blackout: true, reason }` for 30 minutes before and 15 minutes
after CPI, NFP, FOMC rate decisions, PCE, PPI, and core retail sales. Wire
it into all four preflight functions in `agent/preflight.js` — skip new
entries during blackout but still run exit logic when positions are open.

**Files:**
- `agent/preflight.js` — add helper + integrate into each predicate
- New `services/econ_calendar_service.go` — fetch/cache upcoming releases
  (FMP API key already present per `.env.example`; see existing
  `.claude/skills/economic-calendar-fetcher/SKILL.md` for the source pattern)
- `mcp-server.js` — expose `get_econ_blackout_status` so the LLM can also
  read it when reasoning about timing

**Acceptance:**
- Preflight skips new-entry beats within the blackout window across all agents
- Open-position management beats still run
- Tested with at least one past release event in the calendar fixture

**Size:** ~1 day. **Token impact:** negative (saves tokens by skipping).

#### 4.2 IV Rank gate for Prophet options entries
**Scope:** Compute IV rank (current ATM IV percentile vs trailing 252 days)
for any underlying Prophet considers. Expose via MCP tool. Update
`TRADING_RULES_V2.md` to require IV-rank read on every options entry —
prefer buying premium below 30, prefer selling/spreads above 70.

**Files:**
- New `services/iv_rank_service.go` — pull IV history from Alpaca options
  chain or compute from historical close-to-close vol if chain history
  unavailable; cache per symbol per day
- `mcp-server.js` — add `get_iv_rank` MCP tool
- `TRADING_RULES_V2.md` — add an entry-checklist line
- `services/stock_analysis_service.go` — optionally include `iv_rank` in
  the `analyze_stocks` response so the agent sees it without a separate call

**Acceptance:**
- IV rank computable for SPY, QQQ, NVDA, AMD, TSLA, MSTR (the V2 watchlist)
- `analyze_stocks` response includes `iv_rank` and `iv_percentile`
- New rule in V2 trading rules referenced and enforced in pre-trade checklist

**Size:** ~1-2 days. **Token impact:** ~30 tokens per `analyze_stocks` call.

#### 4.3 Intraday context blob on existing Prophet beats (Design 2a)
**Scope:** When Prophet beats during market hours, pre-compute a small
intraday-state blob for the watchlist and hand it to the LLM as context.
No extra beats. No new wake-ups.

Blob includes per symbol:
- Session VWAP and current price's distance from VWAP (%)
- Cumulative RVOL (today's volume / median volume at same time-of-day over
  last 20 trading days)
- % change vs prior close
- Session high / low / range as % of ATR-20
- Sector ETF % change (SMH for semis, XLK for tech, XLE for energy, etc.)
- $TICK current and 5-min average (just for SPY/QQQ beats)

**Files:**
- New `services/intraday_signal_service.go`
- `agent/harness.js` or wherever the Prophet beat prompt is assembled —
  inject the blob into context
- `mcp-server.js` — also expose as `get_intraday_signals` for ad-hoc LLM calls

**Acceptance:**
- Prophet beats during market hours include the blob in context
- Blob generation completes in <500ms (cached aggressively)
- Token addition per beat: <500 input tokens

**Size:** ~2-3 days. **Token impact:** ~300-500 tokens per Prophet beat
during market hours. ~30 beats/day → ~10-15K extra tokens/day.

### P1 — High leverage, moderate cost

#### 4.4 Realized-vs-implied vol filter for Harvest
**Scope:** Compute trailing 20-day realized vol; compare to ATM 30-day IV.
Block new condor entries when implied < realized (no edge). Surface the
spread in the Harvest beat context.

**Files:**
- `services/realized_vol_service.go` (new)
- `agent/preflight.js` — extend `harvestPreflight` with the gate
- `TRADING_RULES_HARVEST.md` — document the rule

**Size:** ~1 day. **Token impact:** ~0 (preflight check).

#### 4.5 PennyProphet RVOL + ORB integration
**Scope:** Replace absolute-volume scoring with time-of-day cumulative
RVOL. Add opening-range break flag (high/low of first 15 minutes) as an
entry-confirmation field.

**Files:**
- `services/penny_signal_aggregator.go` — replace volume score
- New helper for ORB-15 calculation
- Tests in `services/penny_signal_aggregator_test.go`

**Size:** ~1-2 days. **Token impact:** negligible.

#### 4.6 Sector / cross-asset confirmation for `analyze_stocks`
**Scope:** When `analyze_stocks` is called, also fetch and return the
sector ETF's % change and 5-day relative strength. For TrendProphet
underlyings, include DXY, TNX, and HYG snapshots.

**Files:**
- `services/stock_analysis_service.go` — extend response struct
- Sector mapping table (probably a small static map of symbol → sector ETF)

**Size:** ~1 day. **Token impact:** ~50 tokens per `analyze_stocks` call.

### P2 — Strategic, not urgent

#### 4.7 Skew / term-structure surfacing for Harvest
Compute 25-delta put-call skew and front-vs-back-month IV ratio. Useful
for sizing condors but not blocking; can add later once IV rank lands.

#### 4.8 Dealer GEX regime proxy
No free clean source. Can approximate from SPY OI distribution but
accuracy is debatable. Defer until P0/P1 are in.

#### 4.9 Short interest / float data for PennyProphet
Need a data provider (FMP has it). Useful for squeeze-setup detection.

#### 4.10 Credit-spread regime filter for TrendProphet
Watch HYG/LQD spread or simply HYG % change vs 50-day. Block long-trend
entries when spread widens >10% in 5 days.

---

## 5. Implementation Principles (non-negotiables)

1. **Enrich existing beats; don't add new wakes.** Default to Design 2a
   (context on scheduled beats). Event-driven extra beats only with
   explicit cap-per-day and dedupe logic, after measuring whether
   cadence is actually the bottleneck.
2. **Every options entry gates on IV rank** once 4.2 lands. This is the
   single missing edge that matters most for Prophet and Harvest.
3. **All new entry signals respect economic blackout windows** from 4.1.
4. **All new computations cache aggressively.** Intraday blobs should
   refresh at most every 60s; daily computations cache for the session.
5. **Maintain the preflight pattern.** New skip conditions go into
   `agent/preflight.js`. Don't bypass it.
6. **No new LLM tools that bypass the rules files.** If a new capability
   changes how an agent trades, the corresponding `TRADING_RULES_*.md`
   must be updated in the same PR.
7. **Token budget per addition is explicit.** Each new feature
   documents its per-beat token impact in its PR.

---

## 6. Key File Map

```
agent/preflight.js                       # skip predicates (extend, don't replace)
agent/harness.js                         # beat scheduling + prompt assembly
mcp-server.js                            # MCP tool surface for all agents
services/alpaca_data.go                  # GetHistoricalBars, supports 1Min..1Day
services/stock_analysis_service.go       # daily-bar TA — extend for intraday/IV
services/technical_analysis.go           # RSI/SMA/MACD math
services/penny_signal_aggregator.go      # penny momentum scoring
services/trend_signal_service.go         # trend Donchian/SMA/ATR
TRADING_RULES_V2.md                      # Prophet rules
TRADING_RULES_HARVEST.md                 # Harvest rules
TRADING_RULES_PENNY.md                   # PennyProphet rules
TRADING_RULES_TREND.md                   # TrendProphet rules
docs/preflight-skip-spec.md              # preflight design doc
```

Skills under `.claude/skills/` are LLM-side analysis tools, separate
from the agent runtime. Don't confuse them — skills are user-invoked,
not agent-invoked.

---

## 7. Suggested Order

1. **Start with 4.1 (econ calendar).** Lowest cost, highest defensive
   value, sets a pattern other items reuse.
2. **Then 4.2 (IV rank).** Highest offensive-edge gap for the options
   agents.
3. **Then 4.3 (intraday blob).** Builds on the preflight pattern and the
   "enrich existing beats" principle.
4. **Then P1 items** in any order based on which agent's performance
   review most needs the lift.

---

## 8. Prompt to Start the Session

```
Read docs/agent-capability-improvements-brief.md in full. That document
is the context for this session — it covers project architecture, the
current capability gaps in the four trading agents (Prophet, Harvest,
PennyProphet, TrendProphet), a critical token-cost lesson about beat
design, a prioritized backlog (sections 4.1-4.10), and non-negotiable
implementation principles.

Once you've read it, do not start coding. Instead:

1. Confirm you've internalized the token-cost lesson in section 3
   (enrich-existing-beats vs. extra-wakes). Briefly state it back.
2. Pick the first item to work on. Default is 4.1 (economic-calendar
   auto-skip) unless I tell you otherwise.
3. Before writing code, produce a short implementation plan for that
   single item: files to create/modify, function signatures, data flow,
   test plan, and per-beat token impact. Show me the plan and wait for
   my approval.
4. Only after approval, implement it — following the principles in
   section 5, particularly the rule that the corresponding
   TRADING_RULES file must be updated in the same change if behavior
   changes.

Ground rules:
- Don't bolt on new heartbeat wakes; enrich existing beats.
- Cache aggressively.
- Update the relevant TRADING_RULES_*.md if you change agent behavior.
- Ask before destructive ops or anything you're unsure about.
```
