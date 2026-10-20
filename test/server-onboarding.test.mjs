import { test, after } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import express from 'express';

// Isolate config-store before it is imported: a throwaway config file, and no Alpaca keys in
// the environment for loadConfig() to seed an account from. No brokerage is ever contacted.
const tmpRoot = mkdtempSync(path.join(tmpdir(), 'openprophet-onboarding-'));
process.env.OPENPROPHET_CONFIG_PATH = path.join(tmpRoot, 'agent-config.json');
for (const key of ['ALPACA_PUBLIC_KEY', 'ALPACA_API_KEY', 'ALPACA_SECRET_KEY', 'ALPACA_BASE_URL', 'ALPACA_ENDPOINT']) {
  delete process.env[key];
}
after(() => rmSync(tmpRoot, { recursive: true, force: true }));

const { createAuthMiddleware, ensureGoBinary, addAccountAndActivate, GO_BINARY_NAME } = await import('../agent/server-lifecycle.js');
const configStore = await import('../agent/config-store.js');

async function withApi(token, fn) {
  // Mounted exactly as server.js mounts it.
  const app = express();
  app.use('/api', createAuthMiddleware(token));
  app.get('/api/health', (req, res) => res.json({ agent: 'healthy' }));
  app.get('/api/accounts', (req, res) => res.json({ accounts: [] }));
  const server = await new Promise(resolve => { const s = app.listen(0, '127.0.0.1', () => resolve(s)); });
  const base = `http://127.0.0.1:${server.address().port}`;
  try { await fn(base); } finally { await new Promise(resolve => server.close(resolve)); }
}

test('auth: /api/health stays open when AGENT_AUTH_TOKEN is set (container healthcheck)', async () => {
  await withApi('s3cret', async (base) => {
    assert.equal((await fetch(`${base}/api/health`)).status, 200);
  });
});

test('auth: other /api routes still require the token', async () => {
  await withApi('s3cret', async (base) => {
    assert.equal((await fetch(`${base}/api/accounts`)).status, 401);
    assert.equal((await fetch(`${base}/api/accounts`, { headers: { Authorization: 'Bearer wrong' } })).status, 401);
    assert.equal((await fetch(`${base}/api/accounts`, { headers: { Authorization: 'Bearer s3cret' } })).status, 200);
    assert.equal((await fetch(`${base}/api/accounts?token=s3cret`)).status, 200);
    // Only the exact health path is exempt.
    assert.equal((await fetch(`${base}/api/healthz`)).status, 401);
  });
});

test('auth: no token configured leaves the API open', async () => {
  await withApi('', async (base) => {
    assert.equal((await fetch(`${base}/api/accounts`)).status, 200);
  });
});

function freshProjectRoot() {
  return mkdtempSync(path.join(tmpRoot, 'project-'));
}

test('ensureGoBinary(force): failed rebuild preserves the working binary', () => {
  const root = freshProjectRoot();
  const binaryPath = path.join(root, GO_BINARY_NAME);
  writeFileSync(binaryPath, 'ORIGINAL');
  const build = () => { throw new Error('go: command not found'); };

  assert.throws(() => ensureGoBinary({ projectRoot: root, force: true, build, log() {} }), /go: command not found/);
  assert.equal(readFileSync(binaryPath, 'utf-8'), 'ORIGINAL');
  assert.deepEqual(readdirSync(root), [GO_BINARY_NAME], 'no temp build output left behind');
});

test('ensureGoBinary(force): a build that writes nothing also preserves the binary', () => {
  const root = freshProjectRoot();
  const binaryPath = path.join(root, GO_BINARY_NAME);
  writeFileSync(binaryPath, 'ORIGINAL');

  assert.throws(() => ensureGoBinary({ projectRoot: root, force: true, build() {}, log() {} }), /produced no binary/);
  assert.equal(readFileSync(binaryPath, 'utf-8'), 'ORIGINAL');
});

test('ensureGoBinary(force): successful rebuild builds to a temp path then replaces', () => {
  const root = freshProjectRoot();
  const binaryPath = path.join(root, GO_BINARY_NAME);
  writeFileSync(binaryPath, 'ORIGINAL');
  const outputs = [];
  const build = (outputPath, { cwd }) => {
    assert.equal(cwd, root);
    // The original must still be in place while the build runs.
    assert.equal(readFileSync(binaryPath, 'utf-8'), 'ORIGINAL');
    outputs.push(outputPath);
    writeFileSync(outputPath, 'REBUILT');
  };

  assert.equal(ensureGoBinary({ projectRoot: root, force: true, build, log() {} }), binaryPath);
  assert.equal(outputs.length, 1);
  assert.notEqual(outputs[0], binaryPath);
  assert.equal(path.dirname(outputs[0]), root, 'temp output lives beside the binary so rename is atomic');
  assert.equal(readFileSync(binaryPath, 'utf-8'), 'REBUILT');
  assert.deepEqual(readdirSync(root), [GO_BINARY_NAME]);
});

test('ensureGoBinary: existing binary is reused, missing binary is built', () => {
  const root = freshProjectRoot();
  const binaryPath = path.join(root, GO_BINARY_NAME);
  let builds = 0;
  const build = (outputPath) => { builds++; writeFileSync(outputPath, 'BUILT'); };

  assert.equal(ensureGoBinary({ projectRoot: root, build, log() {} }), binaryPath);
  assert.equal(builds, 1);
  assert.equal(readFileSync(binaryPath, 'utf-8'), 'BUILT');

  ensureGoBinary({ projectRoot: root, build, log() {} });
  assert.equal(builds, 1, 'no rebuild without force when the binary exists');
});

test('sandbox backend preserves its binary when Go is unavailable for rebuild', async () => {
  const { default: AgentOrchestrator } = await import('../agent/orchestrator.js');
  const root = freshProjectRoot();
  const binaryPath = path.join(root, GO_BINARY_NAME);
  writeFileSync(binaryPath, 'ORIGINAL');
  const orchestrator = new AgentOrchestrator({ projectRoot: root });
  const previousPath = process.env.PATH;
  try {
    process.env.PATH = root;
    await assert.rejects(orchestrator._ensureBinary(true), /ENOENT/);
    assert.equal(readFileSync(binaryPath, 'utf8'), 'ORIGINAL');
    assert.deepEqual(readdirSync(root), [GO_BINARY_NAME]);
  } finally {
    process.env.PATH = previousPath;
  }
});

test('POST /api/accounts flow: first account activates harness + backend, later ones do not', async () => {
  await configStore.loadConfig();
  assert.equal(configStore.getActiveAccount(), null, 'starts with no accounts');

  const activations = [];
  const deps = {
    addAccount: configStore.addAccount,
    getActiveAccount: configStore.getActiveAccount,
    activate: async (account) => { activations.push(account); },
  };

  const first = await addAccountAndActivate(
    { name: 'Onboarding', publicKey: 'PK_TEST_ONLY', secretKey: 'not-a-real-secret', paper: true },
    deps,
  );
  assert.equal(first.activated, true);
  assert.equal(activations.length, 1);
  assert.equal(activations[0].id, first.account.id);
  assert.equal(configStore.getActiveSandbox()?.id, `sbx_${first.account.id}`);

  const second = await addAccountAndActivate(
    { name: 'Second', publicKey: 'PK_TEST_ONLY_2', secretKey: 'not-a-real-secret', paper: true },
    deps,
  );
  assert.equal(second.activated, false);
  assert.equal(activations.length, 1, 'adding a non-active account must not restart the backend');
  assert.equal(configStore.getActiveAccount()?.id, first.account.id);
});
