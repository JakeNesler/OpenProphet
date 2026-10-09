import { test } from 'node:test';
import assert from 'node:assert/strict';
import { checkPermissions, ORDER_TOOLS } from '../permissions.js';

// Baseline: everything permitted. Individual tests override one field to prove each gate.
const ALLOW_ALL = {
  allowLiveTrading: true, allowStocks: true, allowOptions: true, allow0DTE: true,
  requireConfirmation: false, maxOrderValue: 0, blockedTools: [],
};

test('non-order tools are never gated', () => {
  assert.doesNotThrow(() => checkPermissions('get_account', {}, {}));
  assert.doesNotThrow(() => checkPermissions('find_similar_setups', {}, { allowLiveTrading: false }));
});

test('blockedTools rejects the named tool', () => {
  assert.throws(() => checkPermissions('get_quote', {}, { blockedTools: ['get_quote'] }), /blocked by permissions/);
});

test('allowLiveTrading=false blocks every order tool', () => {
  for (const tool of ORDER_TOOLS) {
    assert.throws(() => checkPermissions(tool, { symbol: 'AAPL', quantity: 1 }, { ...ALLOW_ALL, allowLiveTrading: false }), /Live trading is DISABLED/, tool);
  }
});

test('allowStocks=false blocks stock buy/sell', () => {
  assert.throws(() => checkPermissions('place_buy_order', { symbol: 'AAPL', quantity: 1 }, { ...ALLOW_ALL, allowStocks: false }), /Stock trading is DISABLED/);
});

test('allowOptions=false blocks options orders (and long OCC symbols)', () => {
  assert.throws(() => checkPermissions('place_options_order', { symbol: 'AAPL260320C00400000', quantity: 1 }, { ...ALLOW_ALL, allowOptions: false }), /Options trading is DISABLED/);
});

test('allow0DTE=false blocks a same-day-expiry option, allows a later one', () => {
  const now = new Date('2026-03-20T12:00:00Z');
  const perms = { ...ALLOW_ALL, allow0DTE: false };
  // OCC: AAPL + 260320 (2026-03-20) + C + strike → expires today
  assert.throws(() => checkPermissions('place_options_order', { symbol: 'AAPL260320C00400000', quantity: 1 }, perms, now), /0DTE options are NOT allowed/);
  // A 2026-12-20 expiry is not 0DTE → allowed
  assert.doesNotThrow(() => checkPermissions('place_options_order', { symbol: 'AAPL261220C00400000', quantity: 1 }, perms, now));
});

test('requireConfirmation blocks orders', () => {
  assert.throws(() => checkPermissions('place_buy_order', { symbol: 'AAPL', quantity: 1 }, { ...ALLOW_ALL, requireConfirmation: true }), /requires operator confirmation/);
});

test('maxOrderValue enforced via limit_price*quantity and via allocation_dollars', () => {
  // stock: 100 * $10 = $1000 > $500
  assert.throws(() => checkPermissions('place_buy_order', { symbol: 'AAPL', quantity: 100, limit_price: 10 }, { ...ALLOW_ALL, maxOrderValue: 500 }), /exceeds max allowed/);
  // managed position: allocation_dollars drives the check
  assert.throws(() => checkPermissions('place_managed_position', { symbol: 'AAPL', allocation_dollars: 1000 }, { ...ALLOW_ALL, maxOrderValue: 500 }), /exceeds max allowed/);
  // under the cap → allowed
  assert.doesNotThrow(() => checkPermissions('place_buy_order', { symbol: 'AAPL', quantity: 10, limit_price: 10 }, { ...ALLOW_ALL, maxOrderValue: 500 }));
});

test('maxOrderValue values options at premium × 100 × contracts', () => {
  const perms = { ...ALLOW_ALL, maxOrderValue: 500 };
  // 5 contracts @ $2.50 = $1,250 notional, not $12.50
  assert.throws(() => checkPermissions('place_options_order', { symbol: 'SPY251219C00680000', quantity: 5, limit_price: 2.5 }, perms), /\$1250\.00 exceeds/);
  // an OCC symbol routed through place_buy_order gets the same multiplier
  assert.throws(() => checkPermissions('place_buy_order', { symbol: 'SPY251219C00680000', quantity: 5, limit_price: 2.5 }, perms), /\$1250\.00 exceeds/);
  // 1 contract @ $2.50 = $250 → allowed
  assert.doesNotThrow(() => checkPermissions('place_options_order', { symbol: 'SPY251219C00680000', quantity: 1, limit_price: 2.5 }, perms));
});

test('maxOrderValue fails closed on an unpriced market order, accepts a quote hint, exempts exits', () => {
  const perms = { ...ALLOW_ALL, maxOrderValue: 500 };
  // market order, no price anywhere → previously valued at $0 and waved through
  assert.throws(() => checkPermissions('place_buy_order', { symbol: 'AAPL', quantity: 10000, order_type: 'market' }, perms), /no price to value it against/);
  // the MCP server attaches a live quote as estimated_price → valued normally
  assert.throws(() => checkPermissions('place_buy_order', { symbol: 'AAPL', quantity: 10, order_type: 'market', estimated_price: 100 }, perms), /\$1000\.00 exceeds/);
  assert.doesNotThrow(() => checkPermissions('place_buy_order', { symbol: 'AAPL', quantity: 4, order_type: 'market', estimated_price: 100 }, perms));
  // closing a managed position carries no size and reduces risk → never blocked by the cap
  assert.doesNotThrow(() => checkPermissions('close_managed_position', { position_id: 'abc' }, perms));
  // cap unset → market orders are not gated on price at all
  assert.doesNotThrow(() => checkPermissions('place_buy_order', { symbol: 'AAPL', quantity: 10000, order_type: 'market' }, ALLOW_ALL));
});

test('fully-permitted order passes', () => {
  assert.doesNotThrow(() => checkPermissions('place_buy_order', { symbol: 'AAPL', quantity: 1, limit_price: 100 }, ALLOW_ALL));
  assert.doesNotThrow(() => checkPermissions('place_options_order', { symbol: 'AAPL261220C00400000', quantity: 1, limit_price: 2 }, ALLOW_ALL));
});
