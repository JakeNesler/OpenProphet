import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';

async function freshStore(t, env = {}) {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'prophet-persistence-'));
  const configPath = path.join(dir, 'config.json');
  const values = { OPENPROPHET_CONFIG_PATH: configPath, ALPACA_PUBLIC_KEY: '',
    ALPACA_API_KEY: '', ALPACA_SECRET_KEY: '', ALPACA_BASE_URL: '',
    ALPACA_ENDPOINT: '', ALPACA_PAPER: '', ...env };
  const previous = {};
  for (const [key, value] of Object.entries(values)) {
    previous[key] = process.env[key];
    process.env[key] = value;
  }
  t.after(async () => {
    for (const [key, value] of Object.entries(previous)) {
      if (value === undefined) delete process.env[key];
      else process.env[key] = value;
    }
    await fs.rm(dir, { recursive: true, force: true });
  });
  const store = await import(`../agent/config-store.js?persistence=${crypto.randomUUID()}`);
  return { store, configPath, dir };
}

test('credentials imported without a trading mode default to paper', async t => {
  const { store } = await freshStore(t, { ALPACA_API_KEY: 'test-key', ALPACA_SECRET_KEY: 'test-secret' });
  const config = await store.loadConfig();
  assert.equal(config.accounts[0].paper, true);
  assert.equal(config.accounts[0].baseUrl, 'https://paper-api.alpaca.markets');
});

test('explicit live mode remains available for environment imports', async t => {
  const { store } = await freshStore(t, { ALPACA_API_KEY: 'test-key', ALPACA_SECRET_KEY: 'test-secret', ALPACA_PAPER: 'false' });
  const config = await store.loadConfig();
  assert.equal(config.accounts[0].paper, false);
  assert.equal(config.accounts[0].baseUrl, 'https://api.alpaca.markets');
});

test('new accounts default both their mode and endpoint to paper', async t => {
  const { store } = await freshStore(t);
  await store.loadConfig();
  const account = await store.addAccount({ publicKey: 'test-key', secretKey: 'test-secret' });
  assert.equal(account.paper, true);
  assert.equal(account.baseUrl, 'https://paper-api.alpaca.markets');
});

test('blank account credentials are rejected before changing configuration', async t => {
  const { store, configPath } = await freshStore(t);
  await store.loadConfig();
  const before = await fs.readFile(configPath, 'utf8');
  await assert.rejects(store.addAccount({ publicKey: ' ', secretKey: 'test-secret' }), /key/i);
  assert.equal(store.getConfig().accounts.length, 0);
  assert.equal(await fs.readFile(configPath, 'utf8'), before);
});

test('invalid configuration is preserved and startup fails visibly', async t => {
  const { store, configPath } = await freshStore(t);
  const damaged = '{"accounts": [truncated';
  await fs.writeFile(configPath, damaged);
  await assert.rejects(store.loadConfig(), /config/i);
  assert.equal(await fs.readFile(configPath, 'utf8'), damaged);
});

test('failed atomic replacement is reported and does not poison later saves', async t => {
  const { store, configPath, dir } = await freshStore(t);
  await store.loadConfig();
  const original = await fs.readFile(configPath, 'utf8');
  await fs.rename(configPath, `${configPath}.original`);
  await fs.mkdir(configPath);
  await assert.rejects(store.saveConfig());
  assert.equal(await fs.readFile(`${configPath}.original`, 'utf8'), original);
  assert.deepEqual((await fs.readdir(dir)).sort(), ['config.json', 'config.json.original']);
  await fs.rmdir(configPath);
  await fs.rename(`${configPath}.original`, configPath);
  await store.saveConfig();
  assert.deepEqual(JSON.parse(await fs.readFile(configPath, 'utf8')), store.getConfig());
  assert.equal((await fs.stat(configPath)).mode & 0o777, 0o600);
});
