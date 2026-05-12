// Tests for preflight skip logic, focused on the economic-blackout integration
// added for the cross-agent blackout feature. Uses node:test (Node ≥ 20).
//
// Run: npm test  (or: node --test agent/preflight.test.mjs)

import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  isEconomicBlackout,
  econBlackoutSkipIfNoPositions,
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
const managed = (positions) => ({ data: { positions, count: positions.length } });
const byStrategy = (count) => ({ data: { count } });
const blackoutOn = (reason = 'CPI release at 2026-05-13 12:30 UTC') => ({
  data: { is_blackout: true, reason },
});
const blackoutOff = () => ({ data: { is_blackout: false } });
const harvestState = (openCondors, deployedPct = 0) => ({
  data: { open_condors: openCondors, deployed_buying_power_pct: deployedPct },
});
const fomcStatus = (isBlackout) => ({ data: { is_blackout: isBlackout } });

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

// ── pennyPreflight integration ─────────────────────────────────────

test('penny: blackout + no managed positions + candidates exist → skip (was run before)', async () => {
  const rt = makeRuntime([
    ['/api/v1/penny/candidates?min_score=60', () => candidates(3)],
    ['/api/v1/positions/managed', () => managed([])],
    ['/api/v1/econ/blackout', () => blackoutOn('NFP release')],
  ]);
  const r = await resolvePreflight('penny-momentum', rt, {});
  assert.equal(r.skip, true);
  assert.match(r.reason, /econ blackout/);
});

test('penny: blackout + open managed position → run (exits must happen)', async () => {
  const rt = makeRuntime([
    ['/api/v1/penny/candidates?min_score=60', () => candidates(0)],
    ['/api/v1/positions/managed', () => managed([{ status: 'ACTIVE' }])],
    ['/api/v1/econ/blackout', () => blackoutOn('CPI release')],
  ]);
  const r = await resolvePreflight('penny-momentum', rt, {});
  assert.equal(r.skip, false);
});

test('penny: blackout endpoint error + candidates + no positions → run (fail open)', async () => {
  const rt = makeRuntime([
    ['/api/v1/penny/candidates?min_score=60', () => candidates(2)],
    ['/api/v1/positions/managed', () => managed([])],
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
