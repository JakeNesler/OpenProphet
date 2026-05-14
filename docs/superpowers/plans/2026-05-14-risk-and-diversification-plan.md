# Risk Controls + Strategy Diversification Plan

**Date:** 2026-05-14
**Branch:** `add-new-strats` (current) — recommended to split into 3 feature branches when executing
**Status:** PLAN — awaiting approval before implementation

## Session-continuation context

If picking this up in a fresh session, read this block first:

- The user runs four trading agents: **Prophet** (main, discretionary, points at a strategy file), **PennyProphet** (penny momentum), **Harvest** (iron-condor premium seller, mechanical), **TrendProphet** (macro/cross-asset trend).
- Risk infrastructure lives in `services/trade_guard.go` (cross-agent symbol overlap + penny caps + daily-loss circuit). Harvest has its own circuit (`services/harvest_service.go`) and FOMC/econ blackouts.
- Sector-context already exists for *reporting* in `services/stock_analysis_sector.go` (`sectorETFMap` keyed NVDA→SMH style + `SectorSummary` type). NOT currently wired to TradeGuard.
- Regime detection already produces `data/reports/weekly_regime_${dateSlug}.json` via the scheduler in `agent/analysis-scheduler.js`. Daily skills (`macro-regime-detector`, `breadth-chart-analyst`, `market-top-detector`, `us-market-bubble-detector`) report but do not gate.
- Agent rules files: `TRADING_RULES_V2.md` (Prophet), `TRADING_RULES_PENNY.md`, `TRADING_RULES_HARVEST.md`, `TRADING_RULES_TREND.md`.
- Workflow rules (from user memory): plan → approval → TDD → one squashed commit per item → rules updates in same commit as behavior changes.

The three items below are ranked highest-EV first. Items 1 and 2 are pure risk infrastructure (no new strategies, no new agents). Item 3 adds a fifth agent (mean-reversion) as a non-correlated return stream.

---

## Item 1 — Cross-agent sector & beta-bucket aggregation in TradeGuard

### Why
TradeGuard prevents symbol overlap but not factor overlap. Today Prophet long NVDA + Penny long a fabless semi + Trend long SOXX + Harvest short QQQ puts can stack to 4–6% of equity all expressing "long tech beta" with no rule seeing the aggregate. This is the **biggest current blind spot** in the risk stack.

### Files touched
- `services/trade_guard.go` — extend (NOT rewrite)
- `services/trade_guard_test.go` — extend
- `services/stock_analysis_sector.go` — read-only (export `SectorETFMap` if not already)
- `cmd/bot/main.go` or wherever `TradeGuardConfig` is constructed — add new config fields
- `TRADING_RULES_V2.md`, `TRADING_RULES_PENNY.md`, `TRADING_RULES_TREND.md` — document the new gate (Harvest is index-only, already constrained)

### Data flow
1. `CheckBuy(ctx, agent, symbol, dollars)` — existing entry point
2. NEW: resolve `symbol → sector_bucket` via `stock_analysis_sector.go`'s `sectorETFMap`; default bucket = "OTHER" for unmapped
3. NEW: walk all managed positions across all agents, sum dollar exposure per bucket, add the proposed allocation
4. NEW: if any bucket > cap, return error
5. NEW: Harvest's "short puts on SPY/QQQ/IWM" contributes to `INDEX_BETA` bucket with delta-adjusted notional, not full notional (delta defaults to 0.30 for short puts at ~16-delta strikes; configurable)

### Function signatures (Go)
```go
// New type
type SectorBucket string  // e.g. "TECH", "FINANCIALS", "ENERGY", "INDEX_BETA", "OTHER"

// New fields on TradeGuardConfig
SectorMaxExposurePct      map[string]float64 `json:"sector_max_exposure_pct"`      // bucket → max % of portfolio
DefaultSectorMaxPct       float64            `json:"default_sector_max_pct"`        // fallback when bucket not in map; default 0.15
ShortPutDeltaProxy        float64            `json:"short_put_delta_proxy"`         // default 0.30
EnableSectorAggregation   bool               `json:"enable_sector_aggregation"`     // default false initially (flag-gated rollout)

// New method on TradeGuard
func (g *TradeGuard) currentSectorExposure() map[SectorBucket]float64
func (g *TradeGuard) bucketFor(symbol string) SectorBucket
func (g *TradeGuard) checkSectorCap(agent AgentSource, symbol string, additionalDollars float64, portfolioValue float64) error

// Update Status()
type GuardStatus struct {
    // ...existing fields...
    SectorExposure  map[string]float64 `json:"sector_exposure_dollars"`
    SectorMaxByBucket map[string]float64 `json:"sector_max_by_bucket_dollars"`
}
```

### Test plan (TDD)
RED first, then GREEN. Tests go in `services/trade_guard_test.go`:

1. `TestTradeGuard_SectorCap_BlocksOverConcentration` — two existing TECH positions sized at 12% each, third TECH buy blocked (cap 15% by default)
2. `TestTradeGuard_SectorCap_AllowsUnderCap` — same scenario but third buy fits under cap, succeeds
3. `TestTradeGuard_SectorCap_DisabledWhenFlagOff` — `EnableSectorAggregation=false` bypasses entire check
4. `TestTradeGuard_SectorCap_DefaultBucketForUnmappedSymbol` — symbol not in `sectorETFMap` falls to "OTHER" bucket, uses `DefaultSectorMaxPct`
5. `TestTradeGuard_SectorCap_HarvestShortPutsContributeDeltaAdjusted` — Harvest short SPY put with $50K notional adds $15K to INDEX_BETA bucket at 0.30 delta proxy
6. `TestTradeGuard_SectorCap_AccountFetchFails` — fail-closed (matches existing penny-cap policy)

### Open questions
- **Q: Default sector caps?** Recommended: `TECH=20%, INDEX_BETA=25%, ENERGY=15%, FINANCIALS=15%, HEALTHCARE=15%, OTHER=15%, DEFAULT=15%`. Index_beta higher because it's diversifying by definition.
- **Q: How does Harvest short put exposure map?** Recommended: contribute `notional × delta_proxy` to INDEX_BETA bucket. Default delta = 0.30 (typical 16-delta short put).
- **Q: Should we use sector ETF actual sector mapping or a simpler manual map?** Recommended: reuse `sectorETFMap` from `stock_analysis_sector.go` (already maintained). Add a sibling `etfToBucket` map that consolidates (XLK, SMH, SOXX → "TECH").
- **Q: Flag-gate or always-on?** Recommended: flag-gated initially (`EnableSectorAggregation=false`), enable after 2 weeks of `Status()` logging shows real exposure data.

### Token impact
- Zero per-beat — TradeGuard runs in Go, not in agent context.
- Adds ~5 fields to `Status()` JSON when agents query it. ~50 tokens additional per status read.

### Estimated effort
1.5–2 days. Half day TDD, half day wiring, half day for the etfToBucket map curation + sanity checking with real data.

### Risk
- **Calibration risk:** Initial sector caps may be too tight (blocking legitimate trades) or too loose (no actual gate). Mitigated by flag-gated rollout: enable observation-mode first, then enforcement.
- **Penny ticker mapping:** Many penny names won't be in `sectorETFMap`. They fall to "OTHER" bucket, which is fine — that bucket should have its own cap to prevent stacking 10 unmapped penny names.

---

## Item 2 — Regime-gated sizing throttle

### Why
You have four skills that detect regime (macro, breadth, market-top, bubble) and they report into `weekly_regime_${dateSlug}.json` but no agent's sizing changes based on the output. Manually deciding "VIX is high, I should size down" is what gets skipped in practice.

### Files touched
- NEW: `services/regime_gate_service.go` — reads regime score, exposes `GetSizingMultiplier()`
- NEW: `services/regime_gate_service_test.go`
- `agent/analysis-scheduler.js` — already writes weekly regime; ADD a daily regime score writer
- NEW: `scripts/compute_daily_regime_score.py` (or inline in scheduler) — consolidates the four skills' outputs into a single 0–100 score
- `services/server.go` (HTTP handlers) — expose a tool `get_regime_gate_status` to all agents
- `agent/preflight.js` — preflight check that reads regime score and short-circuits new entries when score < 15
- `TRADING_RULES_V2.md`, `TRADING_RULES_PENNY.md`, `TRADING_RULES_TREND.md` — document sizing throttle and entry block thresholds. Harvest already has its own circuit; can optionally integrate.

### Data flow
1. **Daily (pre-market):** scheduler runs `compute_daily_regime_score.py`. Reads from `data/reports/`:
   - Latest `breadth_*.json` (breadth-chart-analyst output)
   - Latest `regime_*.json` (macro-regime-detector)
   - Latest `market_top_*.json` (market-top-detector)
   - Latest `bubble_*.json` (us-market-bubble-detector)
2. Weighted average into single score 0–100 (higher = better environment).
3. Writes `data/reports/regime_gate.json`:
   ```json
   {
     "score": 62,
     "tier": "NORMAL",
     "sizing_multiplier": 1.0,
     "block_new_entries": false,
     "components": { "breadth": 70, "macro": 55, "top_risk": 40, "bubble": 30 },
     "as_of": "2026-05-14T08:30:00Z",
     "stale_after": "2026-05-15T13:30:00Z"
   }
   ```
4. **Per heartbeat:** agent calls `get_regime_gate_status` → gets `{tier, sizing_multiplier, block_new_entries}`. Rules consume the multiplier.

### Tier mapping (proposal)
| Score | Tier | Sizing Mult | New Entries |
|---|---|---|---|
| 70–100 | GREEN | 1.0× | Yes |
| 40–69 | NORMAL | 0.8× | Yes |
| 20–39 | DEFENSIVE | 0.5× | Yes, only A-grade setups |
| 0–19 | RED | 0.0× | **Blocked** (exits only) |

### Function signatures (Go)
```go
type RegimeGateStatus struct {
    Score            int     `json:"score"`
    Tier             string  `json:"tier"`
    SizingMultiplier float64 `json:"sizing_multiplier"`
    BlockNewEntries  bool    `json:"block_new_entries"`
    AsOf             time.Time `json:"as_of"`
    StaleAfter       time.Time `json:"stale_after"`
    IsStale          bool    `json:"is_stale"`
}

type RegimeGateService struct {
    reportPath string  // default: "data/reports/regime_gate.json"
    cache      *cachedRegimeGate
    mu         sync.RWMutex
}

func NewRegimeGateService(reportPath string) *RegimeGateService
func (s *RegimeGateService) GetStatus() RegimeGateStatus  // fail-open if file missing
```

### Test plan
1. `TestRegimeGate_LoadsValidFile` — given fixture, returns correct fields
2. `TestRegimeGate_FailOpenOnMissingFile` — file absent → returns `tier="UNKNOWN", sizing_multiplier=1.0, block=false` (fail-open: don't brick the system on missing regime data, but log loudly)
3. `TestRegimeGate_FailOpenOnStaleFile` — file > 24h old → `is_stale=true` BUT keeps the same sizing (don't switch behavior on stale data, just flag)
4. `TestRegimeGate_TierBoundaries` — score 19→RED, 20→DEFENSIVE, 39→DEFENSIVE, 40→NORMAL, 70→GREEN
5. `TestRegimeGate_BlockNewEntriesOnlyInRedTier` — only RED has `block=true`

### Token impact
- Per beat: agent reads ~150-token JSON via tool. Negligible.
- Daily preflight: ~5k tokens (reads four daily report files + computes). Once per day.
- Total monthly: <200k tokens additional. <$3/month at Opus rates.

### Estimated effort
2.5–3 days. Half day to nail the score formula (which weights for breadth/macro/top/bubble), 1 day Go service + tests, 1 day scheduler integration, half day for rules updates and observability.

### Open questions
- **Q: Weighting formula?** Recommended defaults: `score = 0.35×breadth + 0.30×macro + 0.20×(100-top_risk) + 0.15×(100-bubble)`. Bubble and top-risk are inverted (they score *risk*, not *health*).
- **Q: What about Harvest?** Harvest already has independent circuit breakers (BP cap + FOMC blackout). Recommended: integrate but make Harvest only consume `BlockNewEntries` (don't multiply premium-collection sizing — it's already small per trade).
- **Q: Fail-open vs fail-closed when file is missing?** Recommended: **fail-open** (sizing_multiplier=1.0, block=false) BUT emit a loud warning. Reason: regime file going missing should not silently halt trading; pair with monitoring.
- **Q: Should agents see the tier name or just the multiplier?** Recommended: both. Tier name helps agent prose make sense ("DEFENSIVE regime — only A-grade setups"), multiplier is the actual sizing math.

### Risk
- **Whipsaw risk:** Regime score flipping daily would whipsaw sizing. Mitigated by daily-not-intraday updates and tier bands (39→40 doesn't change tier; only 39→20 does).
- **Single point of failure:** If `regime_gate.json` is wrong, all four agents make worse decisions. Mitigation: fail-open + alert. Also: the four input skills are already independent, so a single bad skill produces a small score drift, not a catastrophic miscalibration.

---

## Item 3 — Mean-reversion agent (5th agent)

### Why
Your four current agents are all positively correlated to "trending market that doesn't crash." Mean reversion is **structurally counter-cyclical** to momentum/trend — it does best when momentum fails (chop, mild bear). Adding it as a 5th agent introduces a genuinely uncorrelated return stream.

### Strategy spec
- **Universe:** SPX top 100 by market cap (no penny, no ADRs of EM names with FX risk)
- **Entry signal:** RSI(2) ≤ 10 AND price ≤ MA200 ± 5% (oversold on a quality name not in collapse)
- **Confirmation:** stock above its own 30-day low by ≤ 3% (catching the dip near support, not falling knife)
- **Sizing:** 1% of equity per position, max 5 concurrent (5% gross exposure cap)
- **Stop:** -7% from entry, hard stop
- **Target:** exit at RSI(2) ≥ 80 OR at MA20 reclaim OR T+10 days (time stop)
- **Position cap:** Max 1 per sector (forces diversification across the 5 concurrent positions)
- **Filter:** Skip if earnings within 5 days. Skip if regime tier = RED. Skip if VIX > 35.

### Agent name
**Reverberate** — fits the naming convention (Prophet, PennyProphet, Harvest, TrendProphet). Or **Rebound**. User to pick.

### Files touched (new agent shell mirrors Harvest)
- NEW: `services/reverberate_service.go` (state + scan + entry/exit logic) — model after `services/harvest_service.go`
- NEW: `services/reverberate_service_test.go`
- NEW: `TRADING_RULES_REVERBERATE.md` (mechanical, like Harvest — rules-executor, not reasoning agent)
- NEW: `agent/reverberate-prompt.js` — agent system prompt (loads the rules file)
- `agent/server.js` — add agent registration + heartbeat scheduling
- `agent/orchestrator.js` — add to agent registry
- `services/trade_guard.go` — register `AgentReverberate` source (one-line constant + map entry)
- `agent/config-store.js` — add config entry for Reverberate
- `cmd/bot/main.go` — wire it up

### Function signatures (Go)
```go
type ReverberateService struct {
    universe     []string  // SPX top 100 — refreshed weekly
    universeAge  time.Time
    positions    *PositionManager
    dataService  interfaces.DataService
    tradingService interfaces.TradingService
    guard        *TradeGuard
    earningsService *EarningsCalendarService  // existing
    regimeGate   *RegimeGateService  // from Item 2 if available
    cfg          ReverberateConfig
    mu           sync.RWMutex
}

type ReverberateConfig struct {
    MaxConcurrent       int     // default 5
    PositionSizePct     float64 // default 0.01 (1%)
    EntryRSI2Threshold  float64 // default 10
    ExitRSI2Threshold   float64 // default 80
    StopPct             float64 // default -0.07
    TimeStopDays        int     // default 10
    MinDistFromMA200Pct float64 // default -0.05
    MaxDistFromMA200Pct float64 // default 0.05
    MaxDistFrom30dLow   float64 // default 0.03
    VIXMax              float64 // default 35
    EarningsBlackoutDays int    // default 5
}

// Heartbeat-callable entry points
func (s *ReverberateService) ScanForEntries(ctx context.Context) ([]EntryCandidate, error)
func (s *ReverberateService) ManageOpenPositions(ctx context.Context) ([]ExitDirective, error)
func (s *ReverberateService) RefreshUniverse(ctx context.Context) error
```

### Test plan
Mirror Harvest's test pattern — narrow data interfaces, stubs for `dataService`. Tests:

1. `TestReverberate_Entry_AllConditionsMet_ReturnsAsCandidate`
2. `TestReverberate_Entry_RSI2Above10_Rejected`
3. `TestReverberate_Entry_PriceFarBelowMA200_Rejected` (falling knife filter)
4. `TestReverberate_Entry_PriceFarAbove30dLow_Rejected` (too late, missed the dip)
5. `TestReverberate_Entry_EarningsWithin5Days_Rejected`
6. `TestReverberate_Entry_RegimeRed_Rejected`
7. `TestReverberate_Entry_VIXAbove35_Rejected`
8. `TestReverberate_Entry_SectorAlreadyFilled_Rejected`
9. `TestReverberate_Entry_AtMaxConcurrent_Rejected`
10. `TestReverberate_Exit_RSI2Above80_Exits`
11. `TestReverberate_Exit_MA20Reclaim_Exits`
12. `TestReverberate_Exit_TimeStop10Days_Exits`
13. `TestReverberate_Exit_HardStop7Pct_Exits`
14. `TestReverberate_UniverseRefresh_OnceWeekly`

### Heartbeat behavior (in TRADING_RULES_REVERBERATE.md)
Mechanical, no LLM judgment. Pattern matches Harvest:

```
Every heartbeat:
1. Call get_reverberate_state → returns open positions list
2. For each open position, call manage_position(symbol) → returns "hold" | "exit"
   - On "exit", call sell_position(symbol)
3. If open_count < MaxConcurrent AND regime_tier != "RED":
   - Call scan_entries → returns ranked candidates
   - For each candidate (top to bottom): call buy_position(symbol, dollars)
     - Stop on first guard rejection or when MaxConcurrent reached
4. Emit one-line heartbeat: "reverberate: open=N, scanned=M, entered=K, exited=L"
```

### Token impact
- Per beat: ~3k tokens (read rules + state + run mechanical loop). 4 beats/day × 22 trading days = ~265k/month. <$5/month.
- ~1/10th the token spend of Prophet (which does discretionary reasoning).

### Estimated effort
1.5–2 weeks calendar time. Breakdown:
- Day 1–2: TDD entry/exit logic in Go (RSI(2), MA reclaim, etc.)
- Day 3–4: Wire universe refresh, position management, trade_guard integration
- Day 5: Agent shell — prompt + rules file + scheduler hookup
- Day 6–7: End-to-end testing in paper mode
- Day 8: Documentation, commit, deploy to paper with 0.5% sizing (half default)
- Week 2: Paper-trade observation, tune entry threshold if too aggressive/conservative

### Open questions
- **Q: RSI(2) vs RSI(14)?** Recommended RSI(2) — Connors' classic mean-reversion signal. Faster, more trades, better empirical track record on this style.
- **Q: 1% per position vs 2%?** Recommended 1%. Five concurrent → 5% gross max. Start conservative; tune up if working.
- **Q: Should it short overbought too?** Recommended **no** — long-only first. Short mean-reversion in a bull market is a graveyard. Revisit after 6 months of long-only data.
- **Q: How to refresh SPX top 100 universe?** Recommended: weekly cron via FMP `/index-constituents` or top-100 by market cap from screener. Already have FMP infrastructure.
- **Q: Where does Reverberate slot in TradeGuard's `AgentSource` enum?** Add `AgentReverberate AgentSource = "reverberate"`. No symbol overlap concerns because it trades SPX top 100, but the cross-agent sector cap from Item 1 absolutely applies (means Reverberate could be the third TECH bucket exposure).
- **Q: Should the regime gate from Item 2 affect Reverberate?** Yes — but the *block threshold* should differ. Reverberate WANTS some chop/fear. Recommended: block only at RED tier (score < 15). Sizing multiplier still applies in DEFENSIVE.

### Risk
- **Look-ahead bias in backtests:** Don't validate the strategy on the same data you tuned the parameters on. Tune on 2022–2023, validate on 2024–2025.
- **Falling-knife failure mode:** RSI(2) ≤ 10 can persist for days. The MA200 filter and 30d-low filter are the two safeguards. If they're insufficient empirically, add a "RSI(2) crossing up from below" trigger to avoid catching the down-day itself.
- **Sector concentration:** Tech selloffs would oversold-trigger many tech names simultaneously. The "max 1 per sector" rule prevents this, but it depends on accurate sector mapping (relies on Item 1's bucket map).

---

## Execution order recommendation

Build in this order:

1. **Item 1 first** (sector aggregation in TradeGuard). 2 days. Pure additive change. Validates the sector mapping infrastructure that Item 3 will depend on.
2. **Item 2 second** (regime gate). 3 days. Pure additive change. Once it ships, Items 1 and 3 can both consume regime tier (e.g. relax sector caps in GREEN, tighten in DEFENSIVE).
3. **Item 3 third** (Reverberate). 1.5–2 weeks. Depends on Items 1 and 2 being live. Adds a real new return stream.

Total calendar time: ~3 weeks at a steady pace. None of the three items are blocking each other strictly — you *could* build Item 3 first, but you'd be repeating sector-bucket and regime-gate logic that Items 1 and 2 institutionalize.

## Cross-cutting concerns

- **Single commit per item** (per workflow preference). Each item's commit includes Go code + tests + corresponding TRADING_RULES update where applicable.
- **No backwards-compat shims needed** — these are all additive.
- **Branch hygiene:** current branch `add-new-strats` has uncommitted modifications (`agent/analysis-scheduler.js`, `agent/server.js`, `services/econ_calendar_service.go`, `prophet_bot.new.exe`). Decide whether to ship those first or pull them into Item 1's commit.
- **Paper-trade Item 3 for 2 weeks before live capital.** Items 1 and 2 are safe to ship live immediately (flag-gated for Item 1).

## Token budget summary

| Item | One-time build | Steady-state monthly | Cost @ Opus |
|---|---|---|---|
| Item 1 | ~50k (planning + impl) | ~0 (Go-side) | <$1 |
| Item 2 | ~70k | ~200k | ~$3 |
| Item 3 | ~150k | ~265k | ~$5 |
| **Total ongoing** | — | **~465k/mo** | **~$9/mo** |

Negligible. The constraint is calendar time and discipline, not token cost.

## Decision points for the next session

When you start the next session, decide:

1. Ship existing uncommitted work first, or bundle into Item 1's commit?
2. Confirm default sector caps (or tune them)?
3. Confirm regime weighting formula (or tune)?
4. Name the new agent (Reverberate / Rebound / your choice)?
5. Begin Item 1 immediately, or run an `adapt-strategy` cycle first to make sure existing strategies are healthy before adding to the stack?

---

*End of plan.*
