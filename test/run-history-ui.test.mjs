import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import vm from 'node:vm';

const html = await fs.readFile(new URL('../agent/public/index.html', import.meta.url), 'utf8');
const source = html.slice(html.indexOf('// ── Run history'), html.indexOf('// ── Chat history'));
const row = (accountId, overrides = {}) => ({ id: 'run-' + accountId, accountId, status: 'no_action', reason: 'no_order_confirmation',
  kind: 'heartbeat', model: 'openai/test', sessionMode: 'fresh', startedAt: new Date().toISOString(), durationMs: 1250,
  toolCalls: 1, orders: 0, tokens: 130, costUsd: null, reportedUsd: 0, billing: 'unavailable', ...overrides });

function setup(fetch) {
  let selected = 'a';
  const elements = new Map(['run-history-list', 'run-history-status', 'run-history-refresh'].map(id => [id, { innerHTML: '', textContent: '', disabled: false }]));
  const context = vm.createContext({ fetch, Date, Intl, Number, Object,
    document: { getElementById: id => elements.get(id) },
    getSelectedSandbox: () => selected ? { accountId: selected } : null,
    esc: value => String(value).replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;').replaceAll('"', '&quot;'),
  });
  vm.runInContext(source, context);
  return { context, elements, select: value => { selected = value; } };
}
const response = (accountId, runs) => ({ ok: true, json: async () => ({ accountId, runs }) });

test('late account responses cannot replace the newly selected account history', async () => {
  const pending = [];
  const ui = setup(() => new Promise(resolve => pending.push(resolve)));
  const first = ui.context.refreshRunHistory();
  assert.match(ui.elements.get('run-history-status').textContent, /Loading/);
  ui.select('b');
  const second = ui.context.refreshRunHistory();
  pending[1](response('b', [row('b', { model: 'MODEL_B' }), row('a', { model: 'PRIVATE_A' })]));
  await second;
  pending[0](response('a', [row('a', { model: 'PRIVATE_A' })]));
  await first;
  assert.match(ui.elements.get('run-history-list').innerHTML, /MODEL_B/);
  assert.doesNotMatch(ui.elements.get('run-history-list').innerHTML, /PRIVATE_A/);
});

test('history handles empty, unavailable, wrong-account, and manager states', async () => {
  let result = response('a', []);
  const ui = setup(async () => result);
  await ui.context.refreshRunHistory();
  assert.match(ui.elements.get('run-history-status').textContent, /No runs recorded/);
  result = { ok: false };
  await ui.context.refreshRunHistory();
  assert.match(ui.elements.get('run-history-status').textContent, /unavailable/);
  result = response('b', [row('b')]);
  await ui.context.refreshRunHistory();
  assert.equal(ui.elements.get('run-history-list').innerHTML, '');
  ui.select(null);
  await ui.context.refreshRunHistory();
  assert.equal(ui.elements.get('run-history-refresh').disabled, true);
  assert.match(ui.elements.get('run-history-status').textContent, /Select an account/);
});

test('history escapes metadata, marks unfinished runs, and distinguishes billing states', () => {
  const ui = setup(async () => response('a', []));
  ui.context.renderRunHistory([row('a', { model: '<img src=x onerror=alert(1)>', status: 'running', startedAt: '2000-01-01T00:00:00Z' })]);
  const rendered = ui.elements.get('run-history-list').innerHTML;
  assert.doesNotMatch(rendered, /<img/);
  assert.match(rendered, /&lt;img/);
  assert.match(rendered, /Unfinished/);
  assert.equal(ui.context.runCostLabel(row('a', { billing: 'subscription', costUsd: 0 })), 'Subscription');
  assert.equal(ui.context.runCostLabel(row('a', { costUsd: 0, billing: 'reported' })), '$0.0000');
  assert.equal(ui.context.runCostLabel(row('a')), 'Unpriced');
  assert.equal(ui.context.runCostLabel(row('a', { reportedUsd: 0.25 })), '$0.2500+');
});

test('history account selection does not change the viewed trading account', async () => {
  const requests = [];
  const ui = setup(async url => { requests.push(url); return response('b', [row('b')]); });
  await ui.context.selectRunHistoryAccount('b');
  assert.equal(ui.context.getSelectedSandbox().accountId, 'a');
  assert.equal(ui.context.runHistoryAccountId(), 'b');
  assert.match(requests[0], /accountId=b/);
});
