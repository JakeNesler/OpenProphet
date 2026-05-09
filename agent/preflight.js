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

// PennyProphet predicate. Skips the LLM beat when there are no candidates
// above the composite-score threshold AND no open positions to manage AND
// no open broker orders pending fill.
//
// Today each agent runs in its own paper account (per data/agent-config.json),
// so "no positions in this sandbox" reliably means "no PennyProphet positions"
// without needing strategy-tag filtering. When the shared-account work lands
// (see docs/shared-account-backend-spec.md), this predicate should filter
// positions by strategy="penny" tag instead of relying on account isolation.
//
// The open-orders check closes a gap: a buy submitted via place_buy_order
// before the broker fills it does not yet appear in /positions, but the agent
// may still need to react (cancel on price drift, follow up on partial fills).
// Counting open orders ensures we don't skip the beat while one is in flight.
async function pennyPreflight(runtime, agentConfig) {
  const { goAxios } = runtime;

  const [candidatesResp, positionsResp, ordersResp] = await Promise.all([
    goAxios.get('/api/v1/penny/candidates?min_score=60'),
    goAxios.get('/api/v1/positions'),
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
// always run the LLM because Prophet's mandates depend on them:
//   - pre_market: read daily_brief, scan bulletins, plan entries
//   - market_open / midday / market_close: monitor positions, execute
//   - after_hours: scan after-hours earnings, log activity
//
// During 'closed', if there are no open positions AND no open broker orders,
// there is literally nothing to manage — the broker is shut, news will be
// re-read at 6am via the daily_briefing scheduler job, and the harness fires
// an early heartbeat exactly at the next phase boundary (pre_market start)
// so we don't miss the morning wake-up.
async function prophetPreflight(runtime, agentConfig) {
  const phase = isClosedPhase(new Date());
  if (!phase.closed) {
    return { skip: false, reason: 'phase active — Prophet runs' };
  }

  const { goAxios } = runtime;
  const [positionsResp, ordersResp] = await Promise.all([
    goAxios.get('/api/v1/positions'),
    goAxios.get('/api/v1/orders?status=open'),
  ]);

  if (!Array.isArray(positionsResp.data)) {
    return { skip: false, reason: 'positions response shape unexpected' };
  }
  if (!Array.isArray(ordersResp.data)) {
    return { skip: false, reason: 'orders response shape unexpected' };
  }

  const positions = positionsResp.data;
  const openOrders = ordersResp.data;

  if (positions.length === 0 && openOrders.length === 0) {
    return {
      skip: true,
      reason: `closed phase (${phase.reason}), no positions, no open orders`,
    };
  }

  if (positions.length > 0) {
    return { skip: false, reason: `${positions.length} open position(s) to evaluate` };
  }
  return { skip: false, reason: `${openOrders.length} open order(s) pending fill` };
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
//  (b) it is in window but there are no open positions and no universe ticker
//      shows an entry signal.
//
// Account isolation makes "any position in this sandbox" === "TrendProphet
// position" today. When shared-account tagging lands, filter by strategy="trend".
async function trendPreflight(runtime, agentConfig) {
  // Step 1 — time window. Skips the bulk of beats without any API calls.
  const window = outOfTrendWindow(new Date());
  if (window.out) {
    return { skip: true, reason: window.reason };
  }

  // Step 2 — in window. Check positions first; if any exist, the agent must
  // run to evaluate exit rules.
  const { goAxios } = runtime;
  const positionsResp = await goAxios.get('/api/v1/positions');
  if (!Array.isArray(positionsResp.data)) {
    return { skip: false, reason: 'positions response shape unexpected' };
  }
  const positions = positionsResp.data;
  if (positions.length > 0) {
    return { skip: false, reason: `${positions.length} open position(s) to evaluate` };
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
