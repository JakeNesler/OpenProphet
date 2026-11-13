import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { EventEmitter } from 'node:events';
import { PassThrough } from 'node:stream';
import { AgentHarness } from '../agent/harness.js';

async function fakeHarness(t, { events = [], tail = null, exitCode = 0, signal = false, hang = false, ...options } = {}) {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'openprophet-run-outcome-'));
  t.after(() => fs.rm(dir, { recursive: true, force: true }));
  await fs.writeFile(path.join(dir, 'opencode'), `#!/usr/bin/env node
if (process.env.TEST_HANG === '1') { process.on('SIGTERM', () => {}); setInterval(() => {}, 1000); }
process.stdin.resume();
process.stdin.on('end', () => {
  for (const event of JSON.parse(process.env.TEST_EVENTS)) process.stdout.write(JSON.stringify(event) + '\\n');
  if (process.env.TEST_TAIL !== 'null') process.stdout.write(process.env.TEST_TAIL);
  if (process.env.TEST_SIGNAL === '1') process.kill(process.pid, 'SIGTERM');
  else process.exitCode = Number(process.env.TEST_EXIT);
});\n`, { mode: 0o755 });
  const harness = new AgentHarness({
    sandboxId: 'sandbox-a', accountId: 'account-a',
    getAccount: () => ({ id: 'account-a' }), getPermissions: () => ({}), getCurrentPhaseFn: () => 'midday',
    opencodeEnv: { PATH: dir + path.delimiter + process.env.PATH, TEST_EVENTS: JSON.stringify(events), TEST_TAIL: JSON.stringify(tail), TEST_EXIT: String(exitCode), TEST_SIGNAL: signal ? '1' : '0', TEST_HANG: hang ? '1' : '0' },
    ...options,
  });
  harness._agentConfig = { sessionMode: 'continuous' };
  harness.systemPrompt = 'Test prompt';
  harness.state.activeModel = 'openai/test';
  harness.state.activeAccountId = 'account-a';
  return harness;
}

const text = { type: 'text', sessionID: 'ses_root', part: { text: 'Partial response' } };
const tool = (name, output, overrides = {}) => ({ type: 'tool_use', sessionID: 'ses_root', part: {
  id: 'tool-1', tool: 'prophet_' + name, state: { status: 'completed', input: { symbol: 'TEST', quantity: 1 }, output }, ...overrides,
} });
const receipt = { id: 'broker-order-1', symbol: 'TEST', side: 'buy', qty: '1', status: 'accepted' };
const usageEvent = { type: 'step_finish', sessionID: 'ses_root', part: { id: 'finish-1', cost: 0.25, tokens: { input: 100, output: 20, cache: { read: 10 } } } };

test('nonzero CLI exits preserve partial output and fail the heartbeat', async t => {
  const harness = await fakeHarness(t, { events: [text], exitCode: 1 });
  const result = await harness._runClaude('test', 'openai/test');
  assert.match(result.error, /code 1/);
  assert.equal(result.text, 'Partial response');
  let outcome;
  harness.state.on('beat_end', value => { outcome = value; });
  await harness._beat();
  assert.equal(harness.state.stats.errors, 1);
  assert.equal(harness._consecutiveErrors, 1);
  assert.equal(outcome.status, 'failed');
  assert.equal(outcome.reason, 'cli_exit');
});

test('signals fail the run even after partial output', async t => {
  const harness = await fakeHarness(t, { events: [text], signal: true });
  const result = await harness._runClaude('test', 'openai/test');
  assert.match(result.error, /SIGTERM/);
  assert.equal(result.text, 'Partial response');
});

test('an unterminated final event cannot replace the primary session', async t => {
  const harness = await fakeHarness(t, { events: [text], tail: { type: 'step_finish', sessionID: 'ses_child', part: {} } });
  assert.equal((await harness._runClaude('test', 'openai/test')).sessionId, 'ses_root');
});

test('one read-only order query counts as one tool call and zero orders', async t => {
  const harness = await fakeHarness(t, { events: [tool('get_orders', JSON.stringify([receipt]))] });
  await harness._beat();
  assert.equal(harness.state.stats.toolCalls, 1);
  assert.equal(harness.state.stats.trades, 0);
  assert.equal(harness.state.recentTrades.length, 0);
});

test('only successful order submissions with broker receipts produce order activity', async t => {
  for (const [name, output, expected] of [
    ['place_buy_order', JSON.stringify(receipt), 1],
    ['place_buy_order', JSON.stringify(receipt) + '\n\n⚠ No `thesis` provided — this trade was NOT captured to your trade-memory.', 1],
    ['place_buy_order', { content: [{ type: 'text', text: JSON.stringify(receipt) }] }, 1],
    ['place_buy_order', JSON.stringify({ ...receipt, status: 'rejected' }), 0],
    ['place_buy_order', { isError: true, content: [{ type: 'text', text: 'rejected' }] }, 0],
    ['place_buy_order', 'Order placed', 0],
    ['get_managed_positions', JSON.stringify({ position: receipt }), 0],
    ['cancel_order', JSON.stringify(receipt), 0],
    ['place_managed_position', JSON.stringify({ position: { entry_order_id: 'entry-1', symbol: 'TEST', side: 'buy', quantity: 1, status: 'PENDING_ENTRY' } }), 1],
  ]) {
    const harness = await fakeHarness(t, { events: [tool(name, output)] });
    await harness._beat();
    assert.equal(harness.state.stats.trades, expected, name + ': ' + JSON.stringify(output));
  }
});

test('duplicate terminal tool events do not duplicate counters or order activity', async t => {
  const event = tool('place_buy_order', JSON.stringify(receipt));
  const harness = await fakeHarness(t, { events: [event, event] });
  await harness._beat();
  assert.equal(harness.state.stats.toolCalls, 1);
  assert.equal(harness.state.stats.trades, 1);
});

test('run outcomes retain partial usage on failure and keep OAuth usage unpriced', async t => {
  const saved = [];
  const harness = await fakeHarness(t, { events: [text, usageEvent], exitCode: 1,
    runStore: { record: (accountId, value) => { saved.push({ accountId, ...value }); return value; } },
    getProviderBilling: async () => 'subscription',
  });
  await harness._beat();
  assert.deepEqual(saved.map(item => item.status), ['running', 'failed']);
  const result = saved.at(-1);
  assert.equal(result.id, saved[0].id);
  assert.equal(result.reason, 'cli_exit');
  assert.equal(result.accountId, 'account-a');
  assert.equal(result.tokens, 130);
  assert.equal(result.costUsd, null);
  assert.equal(result.billing, 'subscription');
  assert.equal(result.reportedUsd, 0);
  assert.ok(result.durationMs >= 0);
});

test('successful runs distinguish a read-only outcome, confirmed orders, and messages', async t => {
  for (const [events, message, status, reason] of [
    [[text, usageEvent], false, 'no_action', 'no_order_confirmation'],
    [[tool('place_buy_order', JSON.stringify(receipt)), usageEvent], false, 'completed', 'order_confirmed'],
    [[text, usageEvent], true, 'completed', 'message_completed'],
  ]) {
    const saved = [];
    const harness = await fakeHarness(t, { events, runStore: { record: (_, value) => { saved.push(value); return value; } }, getProviderBilling: async () => 'reported' });
    harness.state.running = true;
    if (message) await harness._adHocBeat('Operator test'); else await harness._beat();
    assert.equal(saved.at(-1).status, status);
    assert.equal(saved.at(-1).reason, reason);
    assert.equal(saved.at(-1).costUsd, 0.25);
    assert.equal(saved.at(-1).tokens, 130);
  }
});

test('a closed or unavailable market records its reason without invoking the model', async t => {
  const saved = [];
  const harness = await fakeHarness(t, { getPermissions: () => ({ regularMarketHoursOnly: true }),
    getMarketSessionFn: async () => ({ isOpen: false, nextOpen: '2026-11-16T14:30:00Z' }),
    runStore: { record: (_, value) => { saved.push(value); return value; } },
  });
  harness._runClaude = () => { throw new Error('model must not run'); };
  await harness._beat();
  assert.equal(saved.at(-1).reason, 'market_closed');
  assert.equal(saved.at(-1).status, 'skipped');
  harness.getMarketSessionFn = async () => { throw new Error('unavailable'); };
  await harness._beat();
  assert.equal(saved.at(-1).reason, 'market_clock_unavailable');
  assert.equal(saved.at(-1).tokens, null);
  assert.equal(harness.state.beatCount, 0);
});

test('provider error events fail the run even when the CLI exits zero', async t => {
  const harness = await fakeHarness(t, { events: [text, { type: 'error', error: { message: 'PRIVATE_PROVIDER_ERROR' } }] });
  const result = await harness._runClaude('test', 'openai/test');
  assert.equal(result.reason, 'provider_error');
  assert.doesNotMatch(result.error, /PRIVATE_PROVIDER_ERROR/);
});

test('partial-output failures engage backoff and a clean run resets it', async t => {
  const harness = await fakeHarness(t, { events: [text], exitCode: 1 });
  harness._getHeartbeatSeconds = AgentHarness.prototype._getHeartbeatSeconds.bind(harness);
  harness._baseHeartbeatSeconds = () => 120;
  for (let i = 0; i < 3; i++) await harness._beat();
  assert.equal(harness._consecutiveErrors, 3);
  assert.ok(harness._getHeartbeatSeconds() > 120);
  harness.opencodeEnv.TEST_EXIT = '0';
  await harness._beat();
  assert.equal(harness._consecutiveErrors, 0);
  assert.equal(harness._getHeartbeatSeconds(), 120);
});

test('stopping a process that ignores SIGTERM escalates and records interruption', { timeout: 20000 }, async t => {
  const saved = [];
  const harness = await fakeHarness(t, { events: [text], hang: true, runStore: { record: (_, value) => { saved.push(value); return value; } } });
  t.after(() => harness._proc?.kill('SIGKILL'));
  harness.state.running = true;
  const ready = new Promise(resolve => harness.state.once('agent_text', resolve));
  const beat = harness._beat();
  await ready;
  const child = harness._proc;
  await harness.stop();
  await beat;
  assert.equal(child.signalCode, 'SIGKILL');
  assert.equal(harness._proc, null);
  assert.equal(harness._beating, false);
  assert.equal(saved.at(-1).status, 'interrupted');
  assert.equal(harness.state.stats.errors, 0);
});

test('the deadline settles an exited CLI whose inherited output pipes stay open', { timeout: 5000 }, async () => {
  const proc = Object.assign(new EventEmitter(), { stdin: new PassThrough(), stdout: new PassThrough(), stderr: new PassThrough(), exitCode: 0, signalCode: null });
  const harness = new AgentHarness({ spawnFn: () => proc, beatTimeoutMs: 20, sigkillGraceMs: 10 });
  const run = harness._runClaude('test', 'openai/test');
  proc.stdout.write(JSON.stringify(text) + '\n');
  const result = await run;
  assert.equal(result.reason, 'timeout');
  assert.equal(result.text, 'Partial response');
  assert.equal(harness._proc, null);
  assert.equal(proc.stdout.destroyed, true);
  assert.equal(proc.stderr.destroyed, true);
});
