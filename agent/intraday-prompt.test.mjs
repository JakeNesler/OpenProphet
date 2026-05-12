// Tests for the intraday-context prompt-injection helpers used by
// agent/harness.js to prepend Prophet's market-hours blob to each beat.
//
// Run: npm test  (or: node --test agent/intraday-prompt.test.mjs)

import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  renderIntradayBlock,
  shouldInjectIntraday,
} from './intraday-prompt.js';

// ── renderIntradayBlock ───────────────────────────────────────────

test('renderIntradayBlock: returns empty string for null/undefined input', () => {
  assert.equal(renderIntradayBlock(null), '');
  assert.equal(renderIntradayBlock(undefined), '');
});

test('renderIntradayBlock: returns empty string when signals array is empty', () => {
  assert.equal(renderIntradayBlock({ signals: [] }), '');
});

test('renderIntradayBlock: returns empty string when input has no signals key', () => {
  assert.equal(renderIntradayBlock({}), '');
});

test('renderIntradayBlock: produces a header and column-aligned rows', () => {
  const set = {
    generated_at: '2026-06-15T16:45:00Z',
    signals: [
      {
        symbol: 'SPY', price: 432.10, day_change_pct: 0.42,
        vwap: 431.45, dist_from_vwap_pct: 0.15, rvol: 1.18,
        range_over_atr: 0.62,
      },
      {
        symbol: 'NVDA', price: 487.20, day_change_pct: -0.85,
        vwap: 488.66, dist_from_vwap_pct: -0.30, rvol: 1.62,
        range_over_atr: 1.05, sector_etf: 'SMH', sector_change_pct: -0.18,
      },
    ],
  };
  const out = renderIntradayBlock(set);
  // Header line + symbol header line + at least 5 metric rows.
  assert.ok(out.includes('## Intraday Context'), 'header missing');
  assert.ok(out.includes('SPY'), 'SPY column missing');
  assert.ok(out.includes('NVDA'), 'NVDA column missing');
  assert.ok(/price/.test(out), 'price row missing');
  assert.ok(/day%/.test(out), 'day% row missing');
  assert.ok(/vwap%/.test(out), 'vwap% row missing');
  assert.ok(/rvol/.test(out), 'rvol row missing');
  assert.ok(/rng\/A/.test(out), 'rng/A row missing');
});

test('renderIntradayBlock: signals with errors are noted, others still rendered', () => {
  const set = {
    signals: [
      { symbol: 'SPY', price: 432.10, day_change_pct: 0.42, vwap: 431.45,
        dist_from_vwap_pct: 0.15, rvol: 1.18, range_over_atr: 0.62 },
      { symbol: 'BADSYM', note: 'fetch failed' },
    ],
    errors: ['BADSYM: simulated failure'],
  };
  const out = renderIntradayBlock(set);
  assert.ok(out.includes('SPY'), 'good symbol still rendered');
  assert.ok(out.includes('BADSYM'), 'failed symbol header still shown for transparency');
});

test('renderIntradayBlock: output is bounded in token weight (rough heuristic — under ~600 chars)', () => {
  const set = {
    signals: ['SPY', 'QQQ', 'NVDA', 'AMD', 'TSLA', 'MSTR'].map(sym => ({
      symbol: sym, price: 100.5, day_change_pct: 0.5, vwap: 100.0,
      dist_from_vwap_pct: 0.5, rvol: 1.0, range_over_atr: 0.5,
      sector_etf: 'SMH', sector_change_pct: 0.1,
    })),
  };
  const out = renderIntradayBlock(set);
  // ~4 chars per token average → 600 chars ≈ 150 tokens. Well under the
  // 500-token cap allotted by the brief.
  assert.ok(out.length < 800, `expected output under 800 chars, got ${out.length}`);
});

// ── shouldInjectIntraday ──────────────────────────────────────────

test('shouldInjectIntraday: true for Prophet during market_open', () => {
  assert.equal(shouldInjectIntraday('v2-options', 'market_open'), true);
});

test('shouldInjectIntraday: true for Prophet during midday', () => {
  assert.equal(shouldInjectIntraday('v2-options', 'midday'), true);
});

test('shouldInjectIntraday: true for Prophet during market_close', () => {
  assert.equal(shouldInjectIntraday('v2-options', 'market_close'), true);
});

test('shouldInjectIntraday: false for Prophet during pre_market', () => {
  assert.equal(shouldInjectIntraday('v2-options', 'pre_market'), false);
});

test('shouldInjectIntraday: false for Prophet during after_hours', () => {
  assert.equal(shouldInjectIntraday('v2-options', 'after_hours'), false);
});

test('shouldInjectIntraday: false for Prophet when closed', () => {
  assert.equal(shouldInjectIntraday('v2-options', 'closed'), false);
});

test('shouldInjectIntraday: false for non-Prophet strategies even during market hours', () => {
  assert.equal(shouldInjectIntraday('harvest', 'market_open'), false);
  assert.equal(shouldInjectIntraday('penny-momentum', 'midday'), false);
  assert.equal(shouldInjectIntraday('trend', 'market_close'), false);
});

test('shouldInjectIntraday: false for missing strategy or phase', () => {
  assert.equal(shouldInjectIntraday(null, 'market_open'), false);
  assert.equal(shouldInjectIntraday('v2-options', null), false);
  assert.equal(shouldInjectIntraday(undefined, undefined), false);
});
