import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { Client } from '@modelcontextprotocol/sdk/client/index.js';
import { StdioClientTransport } from '@modelcontextprotocol/sdk/client/stdio.js';

// A fresh config store with one account whose sandbox starts with a $1,000 order cap.
async function freshSandbox(t) {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'prophet-perm-race-'));
  const keys = ['OPENPROPHET_CONFIG_PATH', 'ALPACA_PUBLIC_KEY', 'ALPACA_API_KEY', 'ALPACA_SECRET_KEY',
    'ALPACA_BASE_URL', 'ALPACA_ENDPOINT', 'ALPACA_PAPER'];
  const previous = Object.fromEntries(keys.map(key => [key, process.env[key]]));
  for (const key of keys) process.env[key] = '';
  process.env.OPENPROPHET_CONFIG_PATH = path.join(dir, 'config.json');
  t.after(async () => {
    for (const [key, value] of Object.entries(previous)) {
      if (value === undefined) delete process.env[key];
      else process.env[key] = value;
    }
    await fs.rm(dir, { recursive: true, force: true });
  });
  const store = await import(`../agent/config-store.js?race=${crypto.randomUUID()}`);
  await store.loadConfig();
  const account = await store.addAccount({ publicKey: 'test-key', secretKey: 'test-secret' });
  const sandboxId = store.getSandboxByAccountId(account.id).id;
  await store.updatePermissionsForSandbox(sandboxId, { maxOrderValue: 1000, requireConfirmation: true });
  return { store, sandboxId, dir };
}

test('an agent update is checked against an operator change that landed after the agent read the cap', async t => {
  const { store, sandboxId } = await freshSandbox(t);
  // The agent saw $1,000 and asks for $500; before its write is applied, the operator lowers the cap to $100.
  await store.updatePermissionsForSandbox(sandboxId, { maxOrderValue: 100 });
  await assert.rejects(store.updatePermissionsForSandbox(sandboxId, { maxOrderValue: 500 }, { tightenOnly: true }),
    /refused for maxOrderValue/);
  assert.equal(store.getPermissionsForSandbox(sandboxId).maxOrderValue, 100);
});

test('two concurrent agent updates from the same snapshot cannot leave the looser cap', async t => {
  const { store, sandboxId } = await freshSandbox(t);
  const [first, second] = await Promise.allSettled([
    store.updatePermissionsForSandbox(sandboxId, { maxOrderValue: 100 }, { tightenOnly: true }),
    store.updatePermissionsForSandbox(sandboxId, { maxOrderValue: 500 }, { tightenOnly: true }),
  ]);
  assert.equal(first.status, 'fulfilled');
  assert.equal(second.status, 'rejected');
  assert.match(second.reason.message, /refused for maxOrderValue/);
  assert.equal(store.getPermissionsForSandbox(sandboxId).maxOrderValue, 100);
});

test('the operator path can still loosen guardrails', async t => {
  const { store, sandboxId } = await freshSandbox(t);
  await store.updatePermissionsForSandbox(sandboxId, { maxOrderValue: 0, requireConfirmation: false });
  const perms = store.getPermissionsForSandbox(sandboxId);
  assert.equal(perms.maxOrderValue, 0);
  assert.equal(perms.requireConfirmation, false);
});

// End to end through the MCP update_permissions handler: a stand-in dashboard backed by the real config
// store lowers the cap to $100 just before it applies the agent's request, as an operator could.
test('MCP update_permissions cannot overwrite a cap the operator lowered mid-request', { timeout: 20000 }, async t => {
  const { store, sandboxId, dir } = await freshSandbox(t);
  const requests = [];
  const dashboard = http.createServer(async (req, res) => {
    let body = '';
    for await (const chunk of req) body += chunk;
    const url = new URL(req.url, 'http://127.0.0.1');
    requests.push(`${req.method} ${url.pathname}`);
    const send = (status, data) => { res.writeHead(status, { 'Content-Type': 'application/json' }); res.end(JSON.stringify(data)); };
    try {
      if (req.method === 'GET' && url.pathname === '/api/permissions') return send(200, store.getPermissionsForSandbox(sandboxId));
      if (req.method === 'PUT' && url.pathname === '/api/permissions/agent') {
        const { sandboxId: id, ...perms } = JSON.parse(body || '{}');
        await store.updatePermissionsForSandbox(sandboxId, { maxOrderValue: 100 }); // operator, mid-request
        await store.updatePermissionsForSandbox(id, perms, { tightenOnly: true });
        return send(200, { ok: true });
      }
      if (req.method === 'PUT' && url.pathname === '/api/permissions') {
        const { sandboxId: id, ...perms } = JSON.parse(body || '{}');
        await store.updatePermissionsForSandbox(id, perms);
        return send(200, { ok: true });
      }
      send(404, { error: 'not found' });
    } catch (err) { send(400, { error: err.message }); }
  });
  await new Promise(resolve => dashboard.listen(0, '127.0.0.1', resolve));
  const client = new Client({ name: 'perm-race-test', version: '1.0.0' });
  const transport = new StdioClientTransport({
    command: process.execPath,
    args: [fileURLToPath(new URL('../mcp-server.js', import.meta.url))],
    cwd: dir,
    env: { PATH: process.env.PATH, GEMINI_API_KEY: '', GOOGLE_API_KEY: '',
      DOTENV_CONFIG_PATH: path.join(dir, '.env'), OPENPROPHET_DATA_DIR: path.join(dir, 'app-data'),
      DATABASE_PATH: path.join(dir, 'test.db'), AGENT_URL: `http://127.0.0.1:${dashboard.address().port}`,
      OPENPROPHET_SANDBOX_ID: sandboxId },
    stderr: 'pipe',
  });
  t.after(async () => {
    await client.close();
    await transport.close();
    await new Promise(resolve => dashboard.close(resolve));
  });
  await client.connect(transport);
  const result = await client.callTool({ name: 'update_permissions', arguments: { maxOrderValue: 500 } });
  assert.equal(result.isError, true);
  assert.match(result.content[0].text, /refused for maxOrderValue/);
  assert.equal(store.getPermissionsForSandbox(sandboxId).maxOrderValue, 100);
  assert.ok(!requests.includes('PUT /api/permissions'), 'the agent never uses the operator route');
});
