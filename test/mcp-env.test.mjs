import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { execFileSync } from 'node:child_process';

test('MCP loads its own env and data location when launched from another project', async t => {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'prophet-mcp-env-'));
  t.after(() => fs.rm(dir, { recursive: true, force: true }));
  const app = path.join(dir, 'app');
  const client = path.join(dir, 'client');
  await fs.mkdir(app);
  await fs.mkdir(client);
  await fs.copyFile(new URL('../mcp-env.js', import.meta.url), path.join(app, 'mcp-env.mjs'));
  await fs.symlink(fileURLToPath(new URL('../node_modules', import.meta.url)), path.join(app, 'node_modules'), 'dir');
  await fs.writeFile(path.join(app, '.env'), 'TRADING_BOT_TOKEN=app-test-token\n');
  await fs.writeFile(path.join(client, '.env'), 'TRADING_BOT_TOKEN=wrong-project-token\n');
  const script = `import { dataDir } from ${JSON.stringify(pathToFileURL(path.join(app, 'mcp-env.mjs')).href)}; console.log(JSON.stringify({ dataDir, token: process.env.TRADING_BOT_TOKEN }));`;
  const run = env => JSON.parse(execFileSync(process.execPath, ['--input-type=module', '-e', script], {
    cwd: client, env: { PATH: process.env.PATH, ...env }, encoding: 'utf8',
  }));
  assert.deepEqual(run({}), { dataDir: path.join(app, 'data'), token: 'app-test-token' });
  assert.equal(run({ TRADING_BOT_TOKEN: 'runtime-test-token' }).token, 'runtime-test-token');
  assert.equal(run({ DOTENV_CONFIG_PATH: path.join(client, '.env') }).token, 'wrong-project-token');
});
