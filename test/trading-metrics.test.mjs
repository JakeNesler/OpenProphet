import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createTradingMetrics, renderTradingMetrics } from '../agent/metrics.js';

test('metrics report broker equity and exposure, exclude secrets, and coalesce scrapes', async () => {
  let requests = 0;
  const now = new Date('2026-11-09T15:00:00Z');
  const metrics = createTradingMetrics({
    now: () => now,
    getSandboxes: () => [{ id: 'sandbox', accountId: 'account', agent: { model: 'openai/gpt-6.1-sol' } }],
    getAccount: () => ({ paper: true, publicKey: 'private-key', secretKey: 'private-secret' }),
    getState: () => ({ running: true, stats: { totalBeats: 2, errors: 0 } }),
    fetchFn: async url => {
      requests++;
      if (url.pathname === '/v2/account') return Response.json({ equity: '1010', last_equity: '1000', cash: '800', buying_power: '1600', account_number: 'PRIVATE' });
      if (url.pathname === '/v2/positions') return Response.json([{ market_value: '210', unrealized_pl: '10' }, { market_value: '-50', unrealized_pl: '-2' }]);
      if (url.pathname === '/v2/orders') return Response.json([{ id: 'private-order' }]);
      return Response.json({ is_open: true, timestamp: now.toISOString(), next_open: now.toISOString(), next_close: now.toISOString() });
    },
  });
  const [a, b] = await Promise.all([metrics.render(), metrics.render()]);
  assert.equal(a, b);
  assert.equal(requests, 4);
  await metrics.render();
  assert.equal(requests, 4);
  for (const [name, value] of Object.entries({ broker_up: 1, equity_usd: 1010, daily_equity_change_usd: 10, daily_equity_change_percent: 1, unrealized_pl_usd: 8, gross_exposure_usd: 260, open_positions: 2, open_orders: 1 })) {
    assert.match(a, new RegExp(`openprophet_${name}\\{[^\\n]+\\} ${value}\\n`));
  }
  assert.doesNotMatch(a, /private-key|private-secret|PRIVATE|private-order|NaN|undefined/);
});

test('failed broker reads omit financial series instead of publishing zero balances', async () => {
  const metrics = createTradingMetrics({
    getSandboxes: () => [{ id: 's', accountId: 'a' }],
    getAccount: () => ({ paper: true, publicKey: 'k', secretKey: 's' }),
    getState: () => ({ stats: { errors: 1 } }),
    fetchFn: async () => Response.json({}, { status: 401 }),
  });
  const text = await metrics.render();
  assert.match(text, /openprophet_broker_up\{[^\n]+\} 0\n/);
  assert.doesNotMatch(text, /^openprophet_equity_usd\{/m);
  assert.doesNotMatch(text, /^openprophet_snapshot_timestamp_seconds\{/m);
});

test('Prometheus labels escape quotes, backslashes, and newlines', () => {
  const text = renderTradingMetrics([{ labels: { sandbox: 'a"b\\c\nd' }, values: { broker_up: 0 } }]);
  assert.ok(text.includes('sandbox="a\\"b\\\\c\\nd"'));
});
