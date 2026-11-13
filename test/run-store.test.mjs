import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import express from 'express';
import { RunStore, runHistoryHandler } from '../agent/run-store.js';
import { createAuthMiddleware } from '../agent/server-lifecycle.js';

const now = new Date('2026-11-13T15:00:00Z');
const run = (id, overrides = {}) => ({ id, sandboxId: 'sandbox-a', kind: 'heartbeat', phase: 'midday',
  model: 'openai/test', sessionMode: 'fresh', startedAt: now.toISOString(), status: 'running', reason: 'running', ...overrides });

async function fixture(t) {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'openprophet-run-store-'));
  const options = { dbPath: path.join(dir, 'runs.db'), now: () => now };
  const store = new RunStore(options);
  t.after(async () => { store.close(); await fs.rm(dir, { recursive: true, force: true }); });
  return { store, options };
}

test('run history survives reopen, replaces its started record, and isolates accounts', async t => {
  const { store, options } = await fixture(t);
  store.record('a', run('run-a'));
  store.record('b', run('run-b'));
  store.record('a', run('run-a', { status: 'no_action', reason: 'no_order_confirmation', finishedAt: now.toISOString(), durationMs: 12 }));
  assert.throws(() => store.record('b', run('run-a')), /another account/);
  store.close();
  const reopened = new RunStore(options);
  try {
    assert.equal(reopened.list('a').length, 1);
    assert.equal(reopened.list('a')[0].status, 'no_action');
    assert.deepEqual(reopened.list('b').map(item => item.id), ['run-b']);
    assert.equal(reopened.list('b')[0].finishedAt, null);
  } finally { reopened.close(); }
});

test('only safe metadata is retained and OAuth usage is never priced as zero', async t => {
  const { store } = await fixture(t);
  store.record('a', run('safe', { status: 'completed', reason: 'order_confirmed', tokens: 120, costUsd: 0, billing: 'subscription', prompt: 'PRIVATE_SENTINEL', error: 'PRIVATE_SENTINEL', credentials: 'PRIVATE_SENTINEL' }));
  const result = store.list('a')[0];
  assert.equal(result.tokens, 120);
  assert.equal(result.costUsd, null);
  assert.doesNotMatch(JSON.stringify(result), /PRIVATE_SENTINEL|prompt|credentials/);
  assert.throws(() => store.record('../a', run('bad')), /Invalid/);
  assert.throws(() => store.record('a', run('bad', { reason: 'PRIVATE_SENTINEL' })), /Invalid run outcome/);
  assert.equal((await fs.stat(store.dbPath)).mode & 0o777, 0o600);
});

test('history has bounded queries, deterministic newest-first ordering, and retention', async t => {
  const { store } = await fixture(t);
  store.record('a', run('expired', { startedAt: '2026-09-01T00:00:00Z' }));
  store.record('a', run('first', { startedAt: '2026-11-13T14:00:00Z' }));
  store.record('a', run('second'));
  assert.deepEqual(store.list('a').map(item => item.id), ['second', 'first']);
  assert.deepEqual(store.list('a', { limit: 1 }).map(item => item.id), ['second']);
  for (const limit of [0, -1, 101, 1.5, Infinity]) assert.throws(() => store.list('a', { limit }), /limit/);
  store.now = () => new Date('2027-01-01T00:00:00Z');
  assert.deepEqual(store.list('a'), []);
});

test('the history endpoint requires auth and never mixes account records or exposes storage errors', async t => {
  const { store } = await fixture(t);
  store.record('a', run('run-a'));
  store.record('b', run('run-b'));
  const app = express();
  app.use('/api', createAuthMiddleware('operator-test'));
  app.get('/api/runs', runHistoryHandler({ runStore: store, getAccountById: id => ['a', 'b'].includes(id), getActiveAccount: () => ({ id: 'a' }) }));
  const server = await new Promise(resolve => { const s = app.listen(0, '127.0.0.1', () => resolve(s)); });
  t.after(() => new Promise(resolve => server.close(resolve)));
  const base = `http://127.0.0.1:${server.address().port}/api/runs`;
  assert.equal((await fetch(base)).status, 401);
  const headers = { Authorization: 'Bearer operator-test' };
  const a = await (await fetch(base, { headers })).json();
  assert.deepEqual(a.runs.map(item => item.id), ['run-a']);
  const b = await (await fetch(base + '?accountId=b', { headers })).json();
  assert.deepEqual(b.runs.map(item => item.id), ['run-b']);
  assert.equal((await fetch(base + '?accountId=missing', { headers })).status, 404);
  assert.equal((await fetch(base + '?accountId=..%2Fa', { headers })).status, 400);
  assert.equal((await fetch(base + '?limit=101', { headers })).status, 400);
  store.list = () => { throw new Error('PRIVATE_STORAGE_ERROR'); };
  const response = await fetch(base, { headers });
  assert.equal(response.status, 503);
  assert.doesNotMatch(await response.text(), /PRIVATE_STORAGE_ERROR/);
});
