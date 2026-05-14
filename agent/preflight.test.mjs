// Tests for preflight skip logic, focused on the economic-blackout integration
// added for the cross-agent blackout feature. Uses node:test (Node ≥ 20).
//
// Run: npm test  (or: node --test agent/preflight.test.mjs)

import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  isEconomicBlackout,
  econBlackoutSkipIfNoPositions,
  isRegimeGateBlock,
  regimeGateBlockSkipIfNoPositions,
  resolvePreflight,
} from './preflight.js';

// ── helpers ────────────────────────────────────────────────────────

function makeRuntime(routes) {
  return {
    goAxios: {
      async get(url, _opts) {
        for (const [pattern, handler] of routes) {
          const match = typeof pattern === 'string' ? url === pattern : pattern.test(url);
          if (match) return handler(url);
        }
        throw new Error(`unmocked URL: ${url}`);
      },
    },
  };
}

const candidates = (count) => ({ data: { count } });
// Post-merge: penny preflight now reads /api/v1/positions?strategy=penny-momentum
// (plain array) and /api/v1/orders?status=open (plain array). pennyPositions
// returns an array of position objects directly; pennyOrders returns orders.
const pennyPositions = (positions) => ({ data: positions });
const pennyOrders = (orders) => ({ data: orders });
const byStrategy = (count) => ({ data: { count } });
const blackoutOn = (reason = 'CPI release at 2026-05-13 12:30 UTC') => ({
  data: { is_blackout: true, reason },
});
const blackoutOff = () => ({ data: { is_blackout: false } });
const harvestState = (openCondors, deployedPct = 0) => ({
  data: { open_condors: openCondors, deployed_buying_power_pct: deployedPct },
});
const fomcStatus = (isBlackout) => ({ data: { is_blackout: isBlackout } });

// Regime gate response shapes. The Go side returns the full status payload —
// preflight only cares about block_new_entries + tier (tier goes into the
// skip reason for operator visibility).
const regimeBlock = (tier = 'RED', score = 10) => ({
  data: {
    score,
    tier,
    sizing_multiplier: 0.0,
    block_new_entries: true,
  },
});
const regimeAllow = (tier = 'NORMAL', score = 60) => ({
  data: {
    score,
    tier,
    sizing_multiplier: 0.8,
    block_new_entries: false,
  },
});

// ── isEconomicBlackout ─────────────────────────────────────────────

test('isEconomicBlackout: returns blackout=true when service reports blackout', async () => {
  const rt = makeRuntime([['/api/v1/econ/blackout', () => blackoutOn('NFP release')]]);
  const r = await isEconomicBlackout(new Date(), rt);
  assert.equal(r.blackout, true);
  assert.match(r.reason, /NFP/);
});

test('isEconomicBlackout: returns blackout=false when service reports no blackout', async () => {
  const rt = makeRuntime([['/api/v1/econ/blackout', () => blackoutOff()]]);
  const r = await isEconomicBlackout(new Date(), rt);
  assert.equal(r.blackout, false);
});

test('isEconomicBlackout: fails open on axios error', async () => {
  const rt = makeRuntime([['/api/v1/econ/blackout', () => { throw new Error('ECONNREFUSED'); }]]);
  const r = await isEconomicBlackout(new Date(), rt);
  assert.equal(r.blackout, false, 'preflight must fail open');
  assert.match(r.error || '', /ECONNREFUSED/);
});

// ── econBlackoutSkipIfNoPositions ──────────────────────────────────

test('econBlackoutSkipIfNoPositions: returns null when positions exist (do not even check blackout)', async () => {
  let calledBlackout = false;
  const rt = makeRuntime([
    ['/api/v1/econ/blackout', () => { calledBlackout = true; return blackoutOn(); }],
  ]);
  const r = await econBlackoutSkipIfNoPositions(rt, 1);
  assert.equal(r, null);
  assert.equal(calledBlackout, false, 'should not call blackout endpoint when positions exist');
});

test('econBlackoutSkipIfNoPositions: returns skip:true when no positions and blackout', async () => {
  const rt = makeRuntime([['/api/v1/econ/blackout', () => blackoutOn('CPI release')]]);
  const r = await econBlackoutSkipIfNoPositions(rt, 0);
  assert.ok(r);
  assert.equal(r.skip, true);
  assert.match(r.reason, /econ blackout/);
  assert.match(r.reason, /CPI/);
});

test('econBlackoutSkipIfNoPositions: returns null when no positions but no blackout', async () => {
  const rt = makeRuntime([['/api/v1/econ/blackout', () => blackoutOff()]]);
  const r = await econBlackoutSkipIfNoPositions(rt, 0);
  assert.equal(r, null);
});

test('econBlackoutSkipIfNoPositions: returns null on endpoint error (fail-open in preflight)', async () => {
  const rt = makeRuntime([['/api/v1/econ/blackout', () => { throw new Error('boom'); }]]);
  const r = await econBlackoutSkipIfNoPositions(rt, 0);
  assert.equal(r, null, 'should fail open — predicate then runs normally');
});

// ── isRegimeGateBlock ──────────────────────────────────────────────

test('isRegimeGateBlock: returns block=true with tier when service reports block', async () => {
  const rt = makeRuntime([['/api/v1/regime-gate/status', () => regimeBlock('RED', 12)]]);
  const r = await isRegimeGateBlock(rt);
  assert.equal(r.block, true);
  assert.equal(r.tier, 'RED');
});

test('isRegimeGateBlock: returns block=false when service reports no block', async () => {
  const rt = makeRuntime([['/api/v1/regime-gate/status', () => regimeAllow('GREEN', 80)]]);
  const r = await isRegimeGateBlock(rt);
  assert.equal(r.block, false);
});

test('isRegimeGateBlock: fails open on axios error (preflight fail-open layer)', async () => {
  // Per the dual-layer fail policy: preflight fails OPEN on regime errors
  // (let the LLM run), the rules side fails CLOSED (LLM does not open new
  // entries when get_regime_gate_status returns an error). The combination
  // protects against silent breakage either way.
  const rt = makeRuntime([['/api/v1/regime-gate/status', () => { throw new Error('ECONNREFUSED'); }]]);
  const r = await isRegimeGateBlock(rt);
  assert.equal(r.block, false, 'preflight must fail open');
  assert.match(r.error || '', /ECONNREFUSED/);
});

test('isRegimeGateBlock: UNKNOWN tier (Go fail-open) is treated as not-blocking', async () => {
  // The Go service returns tier=UNKNOWN, block=false when the daily file is
  // missing. Preflight must not skip the LLM just because regime data is
  // absent — rules layer enforces the closed policy via get_regime_gate_status.
  const rt = makeRuntime([['/api/v1/regime-gate/status', () => ({
    data: { score: 0, tier: 'UNKNOWN', sizing_multiplier: 1.0, block_new_entries: false },
  })]]);
  const r = await isRegimeGateBlock(rt);
  assert.equal(r.block, false);
});

// ── regimeGateBlockSkipIfNoPositions ───────────────────────────────

test('regimeGateBlockSkipIfNoPositions: returns null when positions exist', async () => {
  // Positions-existing always wins. Exit logic must run during RED tier;
  // skipping when positions are open would orphan stop-loss management.
  let called = false;
  const rt = makeRuntime([
    ['/api/v1/regime-gate/status', () => { called = true; return regimeBlock(); }],
  ]);
  const r = await regimeGateBlockSkipIfNoPositions(rt, 3);
  assert.equal(r, null);
  assert.equal(called, false, 'should not even call the endpoint when positions exist');
});

test('regimeGateBlockSkipIfNoPositions: returns skip:true when no positions and tier=RED', async () => {
  const rt = makeRuntime([['/api/v1/regime-gate/status', () => regimeBlock('RED', 8)]]);
  const r = await regimeGateBlockSkipIfNoPositions(rt, 0);
  assert.equal(r.skip, true);
  assert.match(r.reason, /RED/);
});

test('regimeGateBlockSkipIfNoPositions: returns null when no positions but block=false', async () => {
  const rt = makeRuntime([['/api/v1/regime-gate/status', () => regimeAllow()]]);
  const r = await regimeGateBlockSkipIfNoPositions(rt, 0);
  assert.equal(r, null);
});

test('regimeGateBlockSkipIfNoPositions: returns null on endpoint error (fail-open)', async () => {
  const rt = makeRuntime([['/api/v1/regime-gate/status', () => { throw new Error('boom'); }]]);
  const r = await regimeGateBlockSkipIfNoPositions(rt, 0);
  assert.equal(r, null);
});

// ── pennyPreflight integration ─────────────────────────────────────

test('penny: blackout + no positions + no orders + candidates exist → skip (was run before)', async () => {
  const rt = makeRuntime([
    ['/api/v1/penny/candidates?min_score=60', () => candidates(3)],
    ['/api/v1/positions?strategy=penny-momentum', () => pennyPositions([])],
    ['/api/v1/orders?status=open', () => pennyOrders([])],
    ['/api/v1/econ/blackout', () => blackoutOn('NFP release')],
  ]);
  const r = await resolvePreflight('penny-momentum', rt, {});
  assert.equal(r.skip, true);
  assert.match(r.reason, /econ blackout/);
});

test('penny: blackout + open position → run (exits must happen)', async () => {
  const rt = makeRuntime([
    ['/api/v1/penny/candidates?min_score=60', () => candidates(0)],
    ['/api/v1/positions?strategy=penny-momentum', () => pennyPositions([{ symbol: 'ABCD', qty: 100 }])],
    ['/api/v1/orders?status=open', () => pennyOrders([])],
    ['/api/v1/econ/blackout', () => blackoutOn('CPI release')],
  ]);
  const r = await resolvePreflight('penny-momentum', rt, {});
  assert.equal(r.skip, false);
});

test('penny: regime RED + no positions + no orders + candidates exist → skip (regime fires before econ)', async () => {
  // Regime gate is checked before econ blackout, so the reason should point
  // to regime even if both gates would fire. Lets operators see the broader
  // signal that's actually limiting the agent.
  const rt = makeRuntime([
    ['/api/v1/penny/candidates?min_score=60', () => candidates(3)],
    ['/api/v1/positions?strategy=penny-momentum', () => pennyPositions([])],
    ['/api/v1/orders?status=open', () => pennyOrders([])],
    ['/api/v1/regime-gate/status', () => regimeBlock('RED', 12)],
    ['/api/v1/econ/blackout', () => blackoutOff()],
  ]);
  const r = await resolvePreflight('penny-momentum', rt, {});
  assert.equal(r.skip, true);
  assert.match(r.reason, /regime gate RED/);
});

test('penny: regime RED + open position → run (exits must happen even at RED tier)', async () => {
  const rt = makeRuntime([
    ['/api/v1/penny/candidates?min_score=60', () => candidates(0)],
    ['/api/v1/positions?strategy=penny-momentum', () => pennyPositions([{ symbol: 'ABCD', qty: 100 }])],
    ['/api/v1/orders?status=open', () => pennyOrders([])],
    ['/api/v1/regime-gate/status', () => regimeBlock('RED', 8)],
    ['/api/v1/econ/blackout', () => blackoutOff()],
  ]);
  const r = await resolvePreflight('penny-momentum', rt, {});
  assert.equal(r.skip, false);
});

test('penny: blackout + pending open order → run (in-flight order needs evaluation)', async () => {
  const rt = makeRuntime([
    ['/api/v1/penny/candidates?min_score=60', () => candidates(0)],
    ['/api/v1/positions?strategy=penny-momentum', () => pennyPositions([])],
    ['/api/v1/orders?status=open', () => pennyOrders([{ symbol: 'XYZ', status: 'new' }])],
    ['/api/v1/econ/blackout', () => blackoutOn('CPI release')],
  ]);
  const r = await resolvePreflight('penny-momentum', rt, {});
  assert.equal(r.skip, false);
});

test('penny: blackout endpoint error + candidates + nothing in flight → run (fail open)', async () => {
  const rt = makeRuntime([
    ['/api/v1/penny/candidates?min_score=60', () => candidates(2)],
    ['/api/v1/positions?strategy=penny-momentum', () => pennyPositions([])],
    ['/api/v1/orders?status=open', () => pennyOrders([])],
    ['/api/v1/econ/blackout', () => { throw new Error('boom'); }],
  ]);
  const r = await resolvePreflight('penny-momentum', rt, {});
  assert.equal(r.skip, false);
});

// ── harvestPreflight integration ───────────────────────────────────
// Harvest has its own 24h pre-FOMC blackout that stays as a strategy-specific
// guardrail. The new econ blackout layers on top for CPI, NFP, PCE, PPI,
// core retail.

test('harvest: no condors + econ blackout (non-FOMC) → skip', async () => {
  const rt = makeRuntime([
    ['/api/v1/harvest/state', () => harvestState(0)],
    ['/api/v1/harvest/fomc', () => fomcStatus(false)],
    ['/api/v1/econ/blackout', () => blackoutOn('Core PCE release')],
    // chain probe routes shouldn't be reached because we skip before them.
  ]);
  const r = await resolvePreflight('harvest', rt, {});
  assert.equal(r.skip, true);
  assert.match(r.reason, /econ blackout/);
});

test('harvest: open condor + econ blackout → run (exits must happen)', async () => {
  const rt = makeRuntime([
    ['/api/v1/harvest/state', () => harvestState(2)],
    ['/api/v1/harvest/fomc', () => fomcStatus(false)],
    ['/api/v1/econ/blackout', () => blackoutOn('CPI release')],
  ]);
  const r = await resolvePreflight('harvest', rt, {});
  assert.equal(r.skip, false);
});

test('harvest: existing 24h FOMC blackout still skips (econ check not required)', async () => {
  const rt = makeRuntime([
    ['/api/v1/harvest/state', () => harvestState(0)],
    ['/api/v1/harvest/fomc', () => fomcStatus(true)],
    // The FOMC path returns before econ blackout is consulted — leave the
    // econ route unmocked to assert it is not called.
  ]);
  const r = await resolvePreflight('harvest', rt, {});
  assert.equal(r.skip, true);
  assert.match(r.reason, /FOMC blackout/);
});

// ── harvest IV/RV spread gate ──────────────────────────────────────
//
// All these tests succeed past the chain probe — i.e., everything before the
// new gate passes — so the new gate decides the outcome.

const harvestExpiration = (date = '2026-06-19') => ({ data: { expiration_date: date } });
const chainNonEmpty = () => ({ data: { total: 100 } });
const ivSpread = (rv, ivMinusRV) => ({
  data: {
    underlying: 'SPY',
    current_iv: rv + ivMinusRV,
    realized_vol_20d: rv,
    iv_minus_rv: ivMinusRV,
  },
});

test('harvest: IV > RV → run (premium edge present)', async () => {
  const rt = makeRuntime([
    ['/api/v1/harvest/state', () => harvestState(0)],
    ['/api/v1/harvest/fomc', () => fomcStatus(false)],
    ['/api/v1/econ/blackout', () => blackoutOff()],
    ['/api/v1/harvest/expirations/SPY', () => harvestExpiration()],
    [/^\/api\/v1\/options\/chain\/SPY/, () => chainNonEmpty()],
    ['/api/v1/iv/SPY', () => ivSpread(0.15, 0.04)], // IV 19, RV 15 → spread +4
  ]);
  const r = await resolvePreflight('harvest', rt, {});
  assert.equal(r.skip, false, `expected run, got skip: ${r.reason}`);
});

test('harvest: IV ≤ RV with positive RV → skip (no premium edge)', async () => {
  const rt = makeRuntime([
    ['/api/v1/harvest/state', () => harvestState(0)],
    ['/api/v1/harvest/fomc', () => fomcStatus(false)],
    ['/api/v1/econ/blackout', () => blackoutOff()],
    ['/api/v1/harvest/expirations/SPY', () => harvestExpiration()],
    [/^\/api\/v1\/options\/chain\/SPY/, () => chainNonEmpty()],
    ['/api/v1/iv/SPY', () => ivSpread(0.20, -0.02)], // IV 18, RV 20 → spread -2
  ]);
  const r = await resolvePreflight('harvest', rt, {});
  assert.equal(r.skip, true);
  assert.match(r.reason, /IV.*RV|premium edge/i);
});

test('harvest: IV = RV exactly → skip (spread ≤ 0)', async () => {
  const rt = makeRuntime([
    ['/api/v1/harvest/state', () => harvestState(0)],
    ['/api/v1/harvest/fomc', () => fomcStatus(false)],
    ['/api/v1/econ/blackout', () => blackoutOff()],
    ['/api/v1/harvest/expirations/SPY', () => harvestExpiration()],
    [/^\/api\/v1\/options\/chain\/SPY/, () => chainNonEmpty()],
    ['/api/v1/iv/SPY', () => ivSpread(0.20, 0)],
  ]);
  const r = await resolvePreflight('harvest', rt, {});
  assert.equal(r.skip, true);
});

test('harvest: RV = 0 (no signal) → fall through, do not skip', async () => {
  // RealizedVol unavailable: gate must not fire (would otherwise pass since
  // iv_minus_rv = current_iv - 0 > 0, but we explicitly require RV > 0).
  const rt = makeRuntime([
    ['/api/v1/harvest/state', () => harvestState(0)],
    ['/api/v1/harvest/fomc', () => fomcStatus(false)],
    ['/api/v1/econ/blackout', () => blackoutOff()],
    ['/api/v1/harvest/expirations/SPY', () => harvestExpiration()],
    [/^\/api\/v1\/options\/chain\/SPY/, () => chainNonEmpty()],
    ['/api/v1/iv/SPY', () => ({ data: { current_iv: 0.20, realized_vol_20d: 0, iv_minus_rv: 0 } })],
  ]);
  const r = await resolvePreflight('harvest', rt, {});
  assert.equal(r.skip, false);
});

test('harvest: IV endpoint errors → fall through (soft-fail)', async () => {
  const rt = makeRuntime([
    ['/api/v1/harvest/state', () => harvestState(0)],
    ['/api/v1/harvest/fomc', () => fomcStatus(false)],
    ['/api/v1/econ/blackout', () => blackoutOff()],
    ['/api/v1/harvest/expirations/SPY', () => harvestExpiration()],
    [/^\/api\/v1\/options\/chain\/SPY/, () => chainNonEmpty()],
    ['/api/v1/iv/SPY', () => { throw new Error('iv endpoint down'); }],
  ]);
  const r = await resolvePreflight('harvest', rt, {});
  assert.equal(r.skip, false, 'soft-fail expected on IV endpoint error');
});

test('harvest: open condor + IV ≤ RV → run (exits must happen)', async () => {
  const rt = makeRuntime([
    ['/api/v1/harvest/state', () => harvestState(2)],
    ['/api/v1/harvest/fomc', () => fomcStatus(false)],
    // IV endpoint should not even be hit because open condors > 0 returns
    // before the gate runs. Leave it unmocked to assert it isn't called.
  ]);
  const r = await resolvePreflight('harvest', rt, {});
  assert.equal(r.skip, false);
});
