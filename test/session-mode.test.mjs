import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { AgentHarness } from '../agent/harness.js';

async function storeFixture(t) {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'openprophet-session-mode-'));
  const file = path.join(dir, 'config.json');
  await fs.writeFile(file, JSON.stringify({ accounts: [{ id: 'test', name: 'Test', paper: true, publicKey: 'test', secretKey: 'test' }], activeAccountId: 'test' }));
  const previous = process.env.OPENPROPHET_CONFIG_PATH;
  process.env.OPENPROPHET_CONFIG_PATH = file;
  const store = await import(`../agent/config-store.js?session-test=${Date.now()}-${Math.random()}`);
  t.after(async () => {
    if (previous === undefined) delete process.env.OPENPROPHET_CONFIG_PATH;
    else process.env.OPENPROPHET_CONFIG_PATH = previous;
    await fs.rm(dir, { recursive: true, force: true });
  });
  await store.loadConfig();
  return store;
}

test('saved per-account Fresh mode reaches the resolved agent and harness', async t => {
  const store = await storeFixture(t);
  const sandbox = store.getActiveSandbox();
  await store.updateSandboxAgentOverrides(sandbox.id, { sessionMode: 'fresh' });
  assert.equal(store.getResolvedAgentForSandbox(sandbox.id).sessionMode, 'fresh');
  const harness = new AgentHarness({ sandboxId: sandbox.id, getSandbox: store.getSandbox, getAccount: store.getAccountById, getResolvedAgent: store.getResolvedAgentForSandbox, getStrategyById: store.getStrategyById });
  await harness.reloadConfig({ silent: true });
  assert.equal(harness.state.toJSON().sessionMode, 'fresh');
});

test('new agents preserve their mode; explicit account mode overrides the template', async t => {
  const store = await storeFixture(t);
  const agent = await store.addAgent({ name: 'Fresh template', sessionMode: 'fresh' });
  assert.equal(agent.sessionMode, 'fresh');
  const sandbox = store.getActiveSandbox();
  await store.updateSandboxAgentSelection(sandbox.id, { activeAgentId: agent.id });
  assert.equal(store.getResolvedAgentForSandbox(sandbox.id).sessionMode, 'fresh');
  await store.updateSandboxAgentOverrides(sandbox.id, { sessionMode: 'continuous' });
  assert.equal(store.getResolvedAgentForSandbox(sandbox.id).sessionMode, 'continuous');
});

async function fakeCli(t, mode) {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'openprophet-fake-cli-'));
  const log = path.join(dir, 'calls.jsonl');
  const cli = `#!/usr/bin/env node
const fs = require('node:fs');
const args = process.argv.slice(2);
const file = process.env.SESSION_TEST_LOG;
let previous = [];
try { previous = fs.readFileSync(file, 'utf8').trim().split('\\n').filter(Boolean); } catch {}
const position = args.indexOf('--session');
const continued = position < 0 ? null : args[position + 1];
const id = continued || 'ses_test_' + (previous.length + 1);
let prompt = '';
process.stdin.on('data', c => prompt += c);
process.stdin.on('end', () => {
 fs.appendFileSync(file, JSON.stringify({ continued, hasSystemPrompt: prompt.includes('[SYSTEM INSTRUCTIONS') }) + '\\n');
 process.stdout.write(JSON.stringify({ type: 'text', sessionID: id, part: { text: 'Test response' } }) + '\\n');
});
`;
  await fs.writeFile(path.join(dir, 'opencode'), cli, { mode: 0o755 });
  t.after(() => fs.rm(dir, { recursive: true, force: true }));
  const harness = new AgentHarness({ getPermissions: () => ({}), opencodeEnv: { PATH: dir + path.delimiter + process.env.PATH, SESSION_TEST_LOG: log } });
  harness._agentConfig = { sessionMode: mode };
  harness.systemPrompt = 'Test system prompt';
  return { harness, readCalls: async () => (await fs.readFile(log, 'utf8')).trim().split('\n').map(JSON.parse) };
}

test('Fresh model runs never continue an earlier session and always include current instructions', async t => {
  const { harness, readCalls } = await fakeCli(t, 'fresh');
  harness._sessionId = 'ses_stale';
  const first = await harness._runClaude('Test heartbeat one', 'openai/test');
  harness._sessionId = first.sessionId;
  const second = await harness._runClaude('Test heartbeat two', 'openai/test');
  assert.notEqual(first.sessionId, second.sessionId);
  assert.deepEqual(await readCalls(), [{ continued: null, hasSystemPrompt: true }, { continued: null, hasSystemPrompt: true }]);
});

test('Continuous runs resume the same session and still retain its ID for history', async t => {
  const { harness, readCalls } = await fakeCli(t, 'continuous');
  const first = await harness._runClaude('Test heartbeat one', 'openai/test');
  harness._sessionId = first.sessionId;
  const second = await harness._runClaude('Test heartbeat two', 'openai/test');
  assert.equal(second.sessionId, first.sessionId);
  assert.deepEqual(await readCalls(), [{ continued: null, hasSystemPrompt: true }, { continued: first.sessionId, hasSystemPrompt: false }]);
});

test('consecutive Fresh heartbeats use distinct sessions and retain both in history', async t => {
  const { harness, readCalls } = await fakeCli(t, 'fresh');
  const sessions = [];
  harness.state.running = true;
  harness.state.activeModel = 'openai/test';
  harness.state.activeAccountId = 'test';
  harness.chatStore = { startSession: async (_, id) => sessions.push(id), addMessage: async () => {} };
  await harness._beat();
  assert.equal(harness._sessionId, null);
  await harness._beat();
  assert.equal(harness._sessionId, null);
  assert.equal(sessions.length, 2);
  assert.notEqual(sessions[0], sessions[1]);
  assert.ok((await readCalls()).every(c => c.continued === null && c.hasSystemPrompt));
});

test('Fresh message beats also discard the previous conversation after responding', async t => {
  const { harness, readCalls } = await fakeCli(t, 'fresh');
  harness.state.running = true;
  harness.state.activeModel = 'openai/test';
  harness._sessionId = 'ses_stale_message';
  await harness._adHocBeat('Test message');
  assert.equal(harness._sessionId, null);
  assert.deepEqual(await readCalls(), [{ continued: null, hasSystemPrompt: true }]);
});
