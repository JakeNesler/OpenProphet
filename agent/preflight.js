// Pre-flight skip registry.
//
// Each entry is an async function `(runtime, agentConfig) -> { skip, reason }`.
// When skip=true, the harness logs a beat_skip event and bypasses the LLM
// invocation for this heartbeat. When skip=false, the heartbeat proceeds
// normally.
//
// Strategies not registered here always run their LLM beat. The empty
// registry is the safe default — adding the wiring without registering any
// predicates is a no-op.
//
// See docs/preflight-skip-spec.md for the full design.

// isEconomicBlackout queries /api/v1/econ/blackout for the shared US-release
// blackout window (30 min before / 15 min after CPI, NFP, FOMC, PCE, PPI,
// core retail). Fails open: any error → { blackout: false, error } so the
// predicate runs the LLM beat. The LLM then sees the error via
// get_econ_blackout_status and applies the rules-side fail-closed policy
// (no new entries when the source is flaky).
//
// Uses a 1500ms inner timeout so a slow blackout endpoint doesn't consume
// the full 2s budget of the surrounding resolvePreflight Promise.race.
export async function isEconomicBlackout(_now, runtime) {
  if (!runtime || !runtime.goAxios) {
    return { blackout: false, error: 'no runtime' };
  }
  try {
    const resp = await runtime.goAxios.get('/api/v1/econ/blackout', { timeout: 1500 });
    const body = resp?.data || {};
    if (body.is_blackout === true) {
      return { blackout: true, reason: body.reason || 'econ blackout' };
    }
    if (body.error) {
      return { blackout: false, error: body.error };
    }
    return { blackout: false };
  } catch (err) {
    return { blackout: false, error: err.message };
  }
}

// econBlackoutSkipIfNoPositions returns a {skip:true, reason} object when the
// strategy has zero live positions AND we are in an econ blackout, otherwise
// null. Predicates call this AFTER they've decided they would otherwise run.
//
// Positions-existing always wins: exit-management beats must run during a
// blackout. Endpoint errors fail open here (null) so the predicate proceeds.
export async function econBlackoutSkipIfNoPositions(runtime, livePositionCount) {
  if (livePositionCount > 0) return null;
  const econ = await isEconomicBlackout(new Date(), runtime);
  if (econ.blackout) {
    return {
      skip: true,
      reason: `econ blackout: ${econ.reason} (no positions to manage)`,
    };
  }
  return null;
}

// isRegimeGateBlock queries /api/v1/regime-gate/status for the daily-computed
// cross-agent regime tier. Returns {block, tier?, error?}. block=true means
// tier=RED with enforcement on (scores < 20 plus ENABLE_REGIME_GATE=true).
//
// Fail-open at the preflight layer (per the dual-layer policy in
// memory/architectural-patterns.md): on any error or unparseable response,
// return block:false so the LLM beat runs. The rules side fails CLOSED —
// agents are told not to open new entries when get_regime_gate_status
// returns an error or tier=UNKNOWN.
//
// 1500ms inner timeout matches isEconomicBlackout so it doesn't consume the
// full 2s resolvePreflight budget when stacked.
export async function isRegimeGateBlock(runtime) {
  if (!runtime || !runtime.goAxios) {
    return { block: false, error: 'no runtime' };
  }
  try {
    const resp = await runtime.goAxios.get('/api/v1/regime-gate/status', { timeout: 1500 });
    const body = resp?.data || {};
    if (body.block_new_entries === true) {
      return { block: true, tier: body.tier || 'RED' };
    }
    return { block: false, tier: body.tier };
  } catch (err) {
    return { block: false, error: err.message };
  }
}

// regimeGateBlockSkipIfNoPositions mirrors econBlackoutSkipIfNoPositions:
// when an agent has no positions AND the regime tier is RED with enforcement
// on, skip the LLM beat — the LLM can't open new entries anyway and exit
// logic has nothing to manage.
//
// Positions-existing always wins (early return without hitting the
// endpoint). Endpoint errors fail open (null).
export async function regimeGateBlockSkipIfNoPositions(runtime, livePositionCount) {
  if (livePositionCount > 0) return null;
  const regime = await isRegimeGateBlock(runtime);
  if (regime.block) {
    return {
      skip: true,
      reason: `regime gate ${regime.tier || 'RED'}: new entries blocked (no positions to manage)`,
    };
  }
  return null;
}

// PennyProphet predicate. Skips the LLM beat when there are no candidates
// above the composite-score threshold AND no penny-tagged positions to manage
// AND no open broker orders pending fill.
//
// Positions are filtered by strategy=penny-momentum so that other agents
// sharing the same paper account (Prophet, Trend, Harvest) do not keep
// PennyProphet awake on their positions. Attribution is by symbol-of-most-
// recent-buy in storage.GetSymbolStrategyAttribution; positions placed via
// PennyProphet's place_buy_order flow forward AgentStrategy onto the order
// row so they attribute correctly.
//
// The open-orders check closes a gap: a buy submitted via place_buy_order
// before the broker fills it does not yet appear in /positions, but the agent
// may still need to react (cancel on price drift, follow up on partial fills).
// Counting open orders ensures we don't skip the beat while one is in flight.
// Note: /api/v1/orders does NOT yet support strategy filtering, so a pending
// order from another agent on the shared account will still wake PennyProphet.
// This is a small leak — pending orders are rare and short-lived on paper —
// but if it becomes a problem, add ?strategy filtering to HandleGetOrders too.
async function pennyPreflight(runtime, agentConfig) {
  const { goAxios } = runtime;

  const [candidatesResp, positionsResp, ordersResp] = await Promise.all([
    goAxios.get('/api/v1/penny/candidates?min_score=60'),
    goAxios.get('/api/v1/positions?strategy=penny-momentum'),
    goAxios.get('/api/v1/orders?status=open'),
  ]);

  // Validate response shapes before deciding. A malformed response (e.g., a
  // 200 with a null body or an unexpected payload) is ambiguous — fail open
  // and let the LLM evaluate, rather than treating "fields missing" as "0".
  if (typeof candidatesResp.data?.count !== 'number') {
    return { skip: false, reason: 'penny candidates response shape unexpected' };
  }
  if (!Array.isArray(positionsResp.data)) {
    return { skip: false, reason: 'positions response shape unexpected' };
  }
  if (!Array.isArray(ordersResp.data)) {
    return { skip: false, reason: 'orders response shape unexpected' };
  }

  const candidateCount = candidatesResp.data.count;
  const positions = positionsResp.data;
  const openOrders = ordersResp.data;

  if (candidateCount === 0 && positions.length === 0 && openOrders.length === 0) {
    return {
      skip: true,
      reason: 'no candidates above min_score=60, no open positions, no open orders',
    };
  }

  // Regime gate: tier=RED with enforcement on blocks new entries; with no
  // open positions/orders, no exit logic to run either — skip the beat.
  // Checked before econ so a RED tier reports correctly even if a blackout
  // window overlaps.
  const liveCount = positions.length + openOrders.length;
  const regimeSkip = await regimeGateBlockSkipIfNoPositions(runtime, liveCount);
  if (regimeSkip) return regimeSkip;

  // Econ-blackout gate: if there's nothing to manage (no positions, no orders
  // in flight) AND we're inside a US-release blackout window, skip the beat.
  // The LLM can't open new entries anyway during blackout.
  const econSkip = await econBlackoutSkipIfNoPositions(runtime, liveCount);
  if (econSkip) return econSkip;

  if (candidateCount > 0) {
    return { skip: false, reason: `${candidateCount} candidate(s) above threshold` };
  }
  if (positions.length > 0) {
    return { skip: false, reason: `${positions.length} open position(s) to manage` };
  }
  return { skip: false, reason: `${openOrders.length} open order(s) pending fill` };
}

// isClosedPhase returns { closed, reason } for a given Date. Mirrors the
// 'closed' bucket from harness.js getCurrentPhase(): weekends, OR weekdays
// before 04:00 ET (240 min), OR weekdays at/after 20:00 ET (1200 min).
//
// Duplicated from harness.js to avoid an import cycle (harness.js imports
// from preflight.js). Same pattern as outOfTrendWindow below. If the phase
// boundaries in harness.js PHASE_DEFAULTS change, update this function too.
export function isClosedPhase(now) {
  const day = now.getDay();
  if (day === 0 || day === 6) {
    return { closed: true, reason: 'weekend' };
  }
  const etTime = now.toLocaleTimeString('en-US', {
    timeZone: 'America/New_York',
    hour: '2-digit', minute: '2-digit', hour12: false,
  });
  const [h, m] = etTime.split(':').map(Number);
  const mins = h * 60 + m;
  if (mins < 240) {
    return { closed: true, reason: `${etTime} ET — overnight (pre-4am)` };
  }
  if (mins >= 1200) {
    return { closed: true, reason: `${etTime} ET — overnight (post-8pm)` };
  }
  return { closed: false, reason: '' };
}

// Prophet predicate. Only considers skipping during the 'closed' phase
// (overnight 8pm-4am ET on weekdays + full weekends). All other phases
// always run the LLM because Prophet's mandates depend on them.
//
// Uses /api/v1/positions?strategy=v2-options so co-located agents on a
// shared Alpaca account (e.g., PennyProphet, TrendProphet) don't keep
// Prophet awake. Attribution is derived from each position's most recent
// buy order's strategy tag (see HandleGetPositions). The open-orders check
// from the prior implementation is dropped: during 'closed' phase the
// broker is shut, partial fills can't occur, and day orders are
// auto-canceled by Alpaca at close.
async function prophetPreflight(runtime, agentConfig) {
  const phase = isClosedPhase(new Date());
  const { goAxios } = runtime;

  if (phase.closed) {
    const positionsResp = await goAxios.get('/api/v1/positions?strategy=v2-options');
    if (typeof positionsResp.data?.count !== 'number') {
      return { skip: false, reason: 'positions response shape unexpected' };
    }
    const positionCount = positionsResp.data.count;
    if (positionCount === 0) {
      return {
        skip: true,
        reason: `closed phase (${phase.reason}), no v2-options positions`,
      };
    }
    return { skip: false, reason: `${positionCount} open position(s) to evaluate` };
  }

  // Open phase — Prophet normally always runs. New: gate on econ blackout
  // when there are no positions to manage. Adds one /api/v1/positions call
  // per open-phase beat (~5ms) in exchange for skipping high-noise release
  // windows that would otherwise burn tokens.
  const positionsResp = await goAxios.get('/api/v1/positions?strategy=v2-options');
  const positionCount = (typeof positionsResp.data?.count === 'number') ? positionsResp.data.count : -1;
  if (positionCount === 0) {
    const regimeSkip = await regimeGateBlockSkipIfNoPositions(runtime, 0);
    if (regimeSkip) return regimeSkip;
    const econSkip = await econBlackoutSkipIfNoPositions(runtime, 0);
    if (econSkip) return econSkip;
  }
  return { skip: false, reason: 'phase active — Prophet runs' };
}

const TREND_UNIVERSE = ['TLT', 'GLD', 'USO', 'DBC', 'UUP', 'EEM'];
const TREND_WINDOW_START_MIN = 16 * 60 + 55; // 16:55 ET
const TREND_WINDOW_END_MIN   = 17 * 60 + 5;  // 17:05 ET

// outOfTrendWindow returns { out, reason } for a given Date. Extracted so
// tests can drive it with arbitrary timestamps without mocking Date globally.
export function outOfTrendWindow(now) {
  const day = now.getDay();
  if (day === 0 || day === 6) {
    return { out: true, reason: 'weekend (TrendProphet runs weekdays only)' };
  }
  const etTime = now.toLocaleTimeString('en-US', {
    timeZone: 'America/New_York',
    hour: '2-digit', minute: '2-digit', hour12: false,
  });
  const [h, m] = etTime.split(':').map(Number);
  const mins = h * 60 + m;
  if (mins < TREND_WINDOW_START_MIN || mins > TREND_WINDOW_END_MIN) {
    return { out: true, reason: `out of window (${etTime} ET; runs 16:55-17:05 only)` };
  }
  return { out: false, reason: '' };
}

// TrendProphet predicate. Skips the LLM beat when:
//  (a) it is outside the 16:55-17:05 ET window (cheap; no API calls), OR
//  (b) it is in window but there are no open trend positions and no universe
//      ticker shows an entry signal.
//
// Uses /api/v1/positions?strategy=trend so co-located agents on a shared
// Alpaca account don't keep TrendProphet awake. Attribution is derived from
// each position's most recent buy order's strategy tag.
async function trendPreflight(runtime, agentConfig) {
  // Step 1 — time window. Skips the bulk of beats without any API calls.
  const window = outOfTrendWindow(new Date());
  if (window.out) {
    return { skip: true, reason: window.reason };
  }

  // Step 2 — in window. Check trend-attributed positions first; if any exist,
  // the agent must run to evaluate exit rules.
  const { goAxios } = runtime;
  const positionsResp = await goAxios.get('/api/v1/positions?strategy=trend');
  if (typeof positionsResp.data?.count !== 'number') {
    return { skip: false, reason: 'positions response shape unexpected' };
  }
  const positionCount = positionsResp.data.count;
  if (positionCount > 0) {
    return { skip: false, reason: `${positionCount} open position(s) to evaluate` };
  }

  // Step 3 — no positions. Check if any universe ticker has a fresh entry
  // signal (close > Donchian-100 high, close > SMA-200, ATR/close >= 0.5%).
  // If any signal call fails, the error propagates to resolvePreflight which
  // catches it and fails open (runs the LLM).
  const signals = await Promise.all(
    TREND_UNIVERSE.map(ticker =>
      goAxios.get(`/api/v1/trend/signal/${ticker}`).then(r => r.data)
    )
  );
  const hasEntrySignal = signals.some(s =>
    s
    && typeof s.last_close === 'number'
    && typeof s.donchian_100_high === 'number'
    && typeof s.sma_200 === 'number'
    && typeof s.atr_20 === 'number'
    && s.last_close > s.donchian_100_high
    && s.last_close > s.sma_200
    && (s.atr_20 / s.last_close) >= 0.005
  );
  if (!hasEntrySignal) {
    return {
      skip: true,
      reason: 'in window, no positions, no entry signals across 6-ticker universe',
    };
  }

  // Regime gate (no positions branch — positions>0 already returned above).
  const regimeSkip = await regimeGateBlockSkipIfNoPositions(runtime, 0);
  if (regimeSkip) return regimeSkip;

  // Econ blackout (no positions branch — positions>0 already returned above).
  const econSkip = await econBlackoutSkipIfNoPositions(runtime, 0);
  if (econSkip) return econSkip;

  return { skip: false, reason: 'in window, entry signal available' };
}

// Harvest predicate. Skips the LLM beat when:
//   (a) open condors > 0 → false (exit checks must run)  [does NOT skip]
//   (b) no open condors AND FOMC blackout → skip
//   (c) no open condors AND deployed_pct at cap → skip (defensive; should be
//       impossible since the cap is on condors themselves)
//   (d) no open condors AND options chain probe returns 0 contracts → skip
//
// Case (d) addresses an observed paper-account quirk: Alpaca occasionally
// returns empty chains across all five underlyings during certain hours,
// causing the LLM beat to burn ~110-200K tokens investigating before giving
// up. The probe is one expirations call + one chain call against SPY (the
// most liquid); if SPY's chain is empty for the target expiration, the other
// underlyings will be too (same data feed).
//
// Lifecycle invariant: state.open_condors counts both OPEN and CLOSING rows
// (see database/storage.go ListOpenHarvestCondors). A condor row is created
// with status=OPEN immediately when the entry order is placed, before broker
// fill, so a pending-fill condor still keeps the agent awake — no gap there.
//
// Drift risk: the 12.0% deployed-buying-power cap below is duplicated from
// TRADING_RULES_HARVEST.md and the Harvest design spec. If the strategy cap
// changes, both this file and the rules doc must be updated together.
async function harvestPreflight(runtime, agentConfig) {
  const { goAxios } = runtime;

  const [stateResp, fomcResp] = await Promise.all([
    goAxios.get('/api/v1/harvest/state'),
    goAxios.get('/api/v1/harvest/fomc'),
  ]);

  if (typeof stateResp.data?.open_condors !== 'number') {
    return { skip: false, reason: 'harvest state response shape unexpected' };
  }
  if (typeof fomcResp.data?.is_blackout !== 'boolean') {
    return { skip: false, reason: 'harvest fomc response shape unexpected' };
  }

  const state = stateResp.data;
  const fomc = fomcResp.data;
  const openCondors = state.open_condors;

  // Open condors require exit-rule evaluation each beat.
  if (openCondors > 0) {
    return { skip: false, reason: `${openCondors} open condor(s) to evaluate` };
  }

  if (fomc.is_blackout) {
    return { skip: true, reason: 'no open condors and FOMC blackout' };
  }

  // Regime gate (RED tier blocks new condor entries). Mirrors the V2/penny/trend
  // pattern; only the block flag is honored here — Harvest does not consume the
  // sizing multiplier (its premium-collection sizing is already small per trade,
  // per the Item 2 plan revision).
  const regimeSkip = await regimeGateBlockSkipIfNoPositions(runtime, 0);
  if (regimeSkip) return regimeSkip;

  // Shared US-release blackout (CPI, NFP, PCE, PPI, core retail). The 24h
  // pre-FOMC ban above remains as a Harvest-specific strategy guardrail.
  const econSkip = await econBlackoutSkipIfNoPositions(runtime, 0);
  if (econSkip) return econSkip;

  // Defensive: at-cap with zero condors should be impossible, but treat as skip.
  if ((state.deployed_buying_power_pct ?? 0) >= 12.0) {
    return { skip: true, reason: 'no open condors but deployed buying power at cap' };
  }

  // Options chain probe — single SPY check. If empty, no entries are possible
  // across the universe. Errors fail open (run the LLM).
  try {
    const expResp = await goAxios.get('/api/v1/harvest/expirations/SPY');
    const exp = expResp.data?.expiration_date;
    if (!exp) {
      return { skip: true, reason: 'no monthly expiration in [35,55] DTE for SPY' };
    }
    const expDate = String(exp).split('T')[0];
    const chainResp = await goAxios.get(
      `/api/v1/options/chain/SPY?expiration=${expDate}&type=put`
    );
    if (typeof chainResp.data?.total === 'number' && chainResp.data.total === 0) {
      return {
        skip: true,
        reason: `no options chain data for SPY ${expDate} (broker feed unavailable; entries impossible)`,
      };
    }
  } catch (err) {
    return { skip: false, reason: `harvest chain probe error: ${err.message}` };
  }

  // SPY IV–RV premium-edge gate. When SPY's stored ATM IV ≤ trailing 20-day
  // realized vol, the whole vol-selling regime is weak and entries across the
  // universe would have no edge. Skip the beat. Per-underlying check still
  // runs in the LLM's Step-3 routine (TRADING_RULES_HARVEST.md).
  //
  // Cold-start safety: gate fires only when realized_vol_20d > 0. If the RV
  // service isn't wired or has insufficient bars, fall through rather than
  // misread a fabricated positive spread.
  //
  // Soft-fail on endpoint error: missing IV data should not block beats.
  try {
    const ivResp = await goAxios.get('/api/v1/iv/SPY');
    const rv = Number(ivResp.data?.realized_vol_20d);
    const spread = Number(ivResp.data?.iv_minus_rv);
    if (rv > 0 && Number.isFinite(spread) && spread <= 0) {
      return {
        skip: true,
        reason: `no open condors and SPY IV ≤ RV (spread ${spread.toFixed(4)}, RV ${rv.toFixed(4)}) — no premium-selling edge`,
      };
    }
  } catch (_err) {
    // Soft-fail; do not block on IV endpoint outage.
  }

  return { skip: false, reason: 'no open condors but chain data available' };
}

export const PREFLIGHT_REGISTRY = {
  'penny-momentum': pennyPreflight,
  'trend':          trendPreflight,
  'harvest':        harvestPreflight,
  'v2-options':     prophetPreflight,
};

const PREFLIGHT_TIMEOUT_MS = 2000;

export async function resolvePreflight(strategyId, runtime, agentConfig) {
  if (!strategyId) return { skip: false, reason: 'no strategy id on agent config' };
  const fn = PREFLIGHT_REGISTRY[strategyId];
  if (!fn) return { skip: false, reason: 'no preflight registered' };
  if (!runtime) return { skip: false, reason: 'no runtime available to predicate' };

  try {
    const result = await Promise.race([
      fn(runtime, agentConfig),
      new Promise((_, reject) =>
        setTimeout(() => reject(new Error(`preflight timeout after ${PREFLIGHT_TIMEOUT_MS}ms`)), PREFLIGHT_TIMEOUT_MS)
      ),
    ]);
    if (typeof result?.skip !== 'boolean') {
      return { skip: false, reason: 'preflight returned invalid shape' };
    }
    return { skip: result.skip, reason: result.reason || '' };
  } catch (err) {
    return { skip: false, reason: `preflight error: ${err.message}` };
  }
}
