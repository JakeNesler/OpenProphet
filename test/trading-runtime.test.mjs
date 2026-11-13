import { test } from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import net from 'node:net';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { Client } from '@modelcontextprotocol/sdk/client/index.js';
import { StdioClientTransport } from '@modelcontextprotocol/sdk/client/stdio.js';

async function freePort() {
  const server = net.createServer();
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const port = server.address().port;
  await new Promise(resolve => server.close(resolve));
  return port;
}

test('financial scrape requires its own bearer token; disabled scrapes return 503', async t => {
  for (const enabled of [true, false]) {
    const directory = await mkdtemp(path.join(tmpdir(), 'openprophet-metrics-test-'));
    const config = path.join(directory, 'config.json');
    await writeFile(config, JSON.stringify({ accounts: [], sandboxes: {} }));
    const port = await freePort();
    const child = spawn(process.execPath, ['agent/server.js'], {
      env: { ...process.env, OPENPROPHET_CONFIG_PATH: config, AGENT_HOST: '127.0.0.1', AGENT_PORT: String(port), AGENT_AUTH_TOKEN: 'operator-test', OPENPROPHET_METRICS_TOKEN: enabled ? 'metrics-test' : '', OPENPROPHET_UPDATE_CHECK: '0', OPENPROPHET_AUTOSTART: '0', ALPACA_PUBLIC_KEY: '', ALPACA_API_KEY: '', ALPACA_SECRET_KEY: '' },
      stdio: 'ignore',
    });
    t.after(async () => {
      if (child.exitCode === null) { child.kill('SIGTERM'); await once(child, 'exit'); }
      await rm(directory, { recursive: true, force: true });
    });
    const url = `http://127.0.0.1:${port}/metrics`;
    let ready = false;
    // Startup shares CPU with subprocess and cgo tests in the full suite.
    for (let attempt = 0; attempt < 300; attempt++) {
      assert.equal(child.exitCode, null, 'dashboard exited before readiness');
      try { await fetch(url); ready = true; break; } catch { await new Promise(resolve => setTimeout(resolve, 50)); }
    }
    assert.ok(ready, 'dashboard started');
    assert.equal((await fetch(url)).status, enabled ? 401 : 503);
    assert.equal((await fetch(url, { headers: { Authorization: 'Bearer operator-test' } })).status, enabled ? 401 : 503);
    const response = await fetch(url, { headers: { Authorization: 'Bearer metrics-test' } });
    assert.equal(response.status, enabled ? 200 : 503);
    if (enabled) {
      assert.match(response.headers.get('content-type'), /text\/plain/);
      assert.match(await response.text(), /# TYPE openprophet_equity_usd gauge/);
    }
  }
});

test('MCP order path rejects a closed or unavailable broker session before any broker submit', async t => {
  let brokerSubmits = 0;
  let clockStatus = 200;
  const server = http.createServer((req, res) => {
    res.setHeader('content-type', 'application/json');
    if (req.url.startsWith('/api/permissions')) return res.end(JSON.stringify({ allowLiveTrading: true, allowStocks: true, regularMarketHoursOnly: true }));
    if (req.url.startsWith('/api/market-session')) { res.statusCode = clockStatus; return res.end(JSON.stringify({ isOpen: false })); }
    brokerSubmits++;
    res.end('{}');
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise(resolve => server.close(resolve)));
  const url = `http://127.0.0.1:${server.address().port}`;
  const transport = new StdioClientTransport({ command: process.execPath, args: ['mcp-server.js'], env: { ...process.env, AGENT_URL: url, TRADING_BOT_URL: url, OPENPROPHET_ACCOUNT_ID: 'test-clock-gate', OPENPROPHET_SANDBOX_ID: 'test-clock-gate' }, stderr: 'ignore' });
  const client = new Client({ name: 'clock-gate-test', version: '1.0.0' });
  t.after(async () => {
    await client.close();
    await rm('data/sandboxes/test-clock-gate', { recursive: true, force: true });
  });
  await client.connect(transport);
  for (clockStatus of [200, 503]) {
    const result = await client.callTool({ name: 'place_buy_order', arguments: { symbol: 'TEST', quantity: 1, limit_price: 1 } });
    assert.match(JSON.stringify(result), /Order blocked: cannot verify an open weekday exchange session/);
  }
  assert.equal(brokerSubmits, 0);
});
