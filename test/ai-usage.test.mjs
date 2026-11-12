import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import vm from 'node:vm';
import { execFileSync } from 'node:child_process';
import Database from 'better-sqlite3';
import { createAiUsageReader, summarizeAiUsage } from '../agent/ai-usage.js';

const now = new Date('2026-11-10T02:00:00Z'); // November 9 in New York.
const row = (id, overrides = {}) => ({ id, provider: 'openai', time_created: now.getTime(), cost: 0.25,
  tokens: JSON.stringify({ total: 150, input: 100, output: 20, reasoning: 5, cache: { read: 30, write: 0 } }), ...overrides });

test('costs and tokens count each step once, with New York daily boundaries', () => {
  const yesterday = row('old', { time_created: Date.parse('2026-11-09T04:59:00Z') });
  const current = row('current');
  const usage = summarizeAiUsage([yesterday, current, current], { now });
  assert.equal(usage.total.turns, 2);
  assert.equal(usage.total.costUsd, 0.5);
  assert.equal(usage.today.turns, 1);
  assert.equal(usage.today.costUsd, 0.25);
  assert.equal(usage.total.totalTokens, 300);
  assert.equal(usage.total.cacheReadTokens, 60);
});

test('OAuth subscription usage is never presented as a zero-dollar charge', () => {
  const usage = summarizeAiUsage([row('oauth', { cost: 0 })], { now, oauthProviders: new Set(['openai']) });
  assert.equal(usage.total.billing, 'subscription');
  assert.equal(usage.total.costUsd, null);
  assert.equal(usage.total.totalTokens, 150);
});

test('missing prices remain unpriced, while an explicitly reported API zero stays zero', () => {
  const free = summarizeAiUsage([row('free', { cost: 0 })], { now });
  assert.equal(free.total.costUsd, 0);
  const mixed = summarizeAiUsage([row('paid'), row('unknown', { cost: null })], { now });
  assert.equal(mixed.total.billing, 'partial');
  assert.equal(mixed.total.costUsd, null);
  assert.equal(mixed.total.reportedUsd, 0.25);
  assert.equal(mixed.total.unpricedTurns, 1);
});

test('missing total is derived without counting reasoning twice', () => {
  const usage = summarizeAiUsage([row('fallback', { tokens: JSON.stringify({ input: 100, output: 20, reasoning: 5, cache: { read: 30 } }) })], { now });
  assert.equal(usage.total.totalTokens, 150);
  assert.equal(usage.total.reasoningTokens, 5);
  assert.equal(summarizeAiUsage([], { now }).total.costUsd, null);
});

async function fixture(t) {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'openprophet-ai-usage-'));
  t.after(() => fs.rm(dir, { recursive: true, force: true }));
  const dbDir = path.join(dir, 'opencode-home', 'opencode');
  await fs.mkdir(dbDir, { recursive: true });
  const dbPath = path.join(dbDir, 'opencode.db');
  const db = new Database(dbPath);
  db.exec('CREATE TABLE session (id TEXT PRIMARY KEY, parent_id TEXT); CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, data TEXT); CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, session_id TEXT, time_created INTEGER, data TEXT)');
  for (const [id, parent, cost] of [['ses_a', null, 1], ['ses_child', 'ses_a', 2], ['ses_b', null, 3], ['ses_probe', null, 99]]) {
    db.prepare('INSERT INTO session VALUES (?, ?)').run(id, parent);
    db.prepare('INSERT INTO message VALUES (?, ?, ?)').run('m_' + id, id, JSON.stringify({ providerID: 'openai', privateText: 'DO_NOT_EXPOSE' }));
    db.prepare('INSERT INTO part VALUES (?, ?, ?, ?, ?)').run('p_' + id, 'm_' + id, id, now.getTime(), JSON.stringify({ type: 'step-finish', cost, tokens: { input: 10, output: 5, total: 15 } }));
  }
  db.close();
  for (const id of ['a', 'b']) {
    const history = path.join(dir, 'sandboxes', id, 'chat-history');
    await fs.mkdir(history, { recursive: true });
    await fs.writeFile(path.join(history, `session-ses_${id}.jsonl`), '');
  }
  await fs.writeFile(path.join(dbDir, 'auth.json'), JSON.stringify({ openai: { type: 'api', key: 'PRIVATE_TEST_KEY' } }));
  return { dir, dbPath };
}

test('reader isolates accounts, includes child sessions, and ignores unrelated model probes', async t => {
  const { dir, dbPath } = await fixture(t);
  const reader = createAiUsageReader({ dataDir: dir, dbPath, now: () => now });
  const a = await reader.get('a'), b = await reader.get('b');
  assert.equal(a.total.costUsd, 3);
  assert.equal(a.total.turns, 2);
  assert.equal(b.total.costUsd, 3);
  assert.equal(b.total.turns, 1);
  assert.doesNotMatch(JSON.stringify(a), /PRIVATE_TEST_KEY|DO_NOT_EXPOSE|ses_probe/);
  await assert.rejects(() => reader.get('../a'), /Invalid account/);
});

test('reader recognizes OAuth billing from auth metadata without returning credentials', async t => {
  const { dir, dbPath } = await fixture(t);
  await fs.writeFile(path.join(path.dirname(dbPath), 'auth.json'), JSON.stringify({ openai: { type: 'oauth', access: 'PRIVATE_TEST_TOKEN' } }));
  const usage = await createAiUsageReader({ dataDir: dir, dbPath, now: () => now }).get('a');
  assert.equal(usage.total.billing, 'subscription');
  assert.equal(usage.total.costUsd, null);
  assert.doesNotMatch(JSON.stringify(usage), /PRIVATE_TEST_TOKEN/);
});

for (const useXdg of [false, true]) {
  test(`default reader locates database and OAuth metadata via ${useXdg ? 'XDG_DATA_HOME' : 'HOME'}`, async t => {
    const { dir, dbPath } = await fixture(t);
    const home = path.join(dir, 'user-home');
    const xdg = path.join(dir, 'xdg-data');
    const dbDir = path.join(useXdg ? xdg : path.join(home, '.local', 'share'), 'opencode');
    await fs.mkdir(dbDir, { recursive: true });
    await fs.copyFile(dbPath, path.join(dbDir, 'opencode.db'));
    await fs.writeFile(path.join(dbDir, 'auth.json'), JSON.stringify({ openai: { type: 'oauth', access: 'PRIVATE_TEST_TOKEN' } }));
    const env = { ...process.env, HOME: home, XDG_DATA_HOME: useXdg ? xdg : '' };
    delete env.OPENCODE_DB;
    const moduleUrl = new URL('../agent/ai-usage.js', import.meta.url).href;
    const source = `import { createAiUsageReader } from ${JSON.stringify(moduleUrl)}; console.log(JSON.stringify(await createAiUsageReader({ dataDir: ${JSON.stringify(dir)} }).get('a')));`;
    const usage = JSON.parse(execFileSync(process.execPath, ['--input-type=module', '-e', source], { env, encoding: 'utf8' }));
    assert.equal(usage.available, true);
    assert.equal(usage.total.turns, 2);
    assert.equal(usage.total.billing, 'subscription');
    assert.equal(usage.total.costUsd, null);
    assert.doesNotMatch(JSON.stringify(usage), /PRIVATE_TEST_TOKEN/);
  });
}

test('missing usage database is unavailable and is never created by a read', async t => {
  const { dir } = await fixture(t);
  const absent = path.join(dir, 'absent.db');
  const usage = await createAiUsageReader({ dataDir: dir, dbPath: absent }).get('a');
  assert.equal(usage.available, false);
  assert.equal(usage.total, undefined);
  await assert.rejects(() => fs.stat(absent), { code: 'ENOENT' });
});

test('cost display distinguishes subscription, unavailable, partial, zero, and stale usage', async () => {
  const html = await fs.readFile(new URL('../agent/public/index.html', import.meta.url), 'utf8');
  const source = html.slice(html.indexOf('const aiUsageByAccount'), html.indexOf('async function refreshAiUsage'));
  const context = vm.createContext({ Intl, Date, esc: value => String(value) });
  vm.runInContext(source, context);
  assert.equal(context.aiCostLabel({ turns: 1, billing: 'subscription', costUsd: null }), 'Subscription');
  assert.equal(context.aiCostLabel({ turns: 1, costUsd: null, reportedUsd: 0 }), 'Unavailable');
  assert.equal(context.aiCostLabel({ turns: 1, costUsd: 0 }), '$0.0000');
  assert.equal(context.aiCostLabel({ turns: 1, costUsd: null, reportedUsd: 0.25 }), '$0.2500+');
  vm.runInContext("aiUsageByAccount.set('a', {available:true,updatedAt:'2000-01-01'})", context);
  assert.match(context.renderAiUsage('a'), /unavailable/);
});
