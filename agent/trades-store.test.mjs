import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';

import { appendTrade, readTrades, _etDate } from './trades-store.js';

test('_etDate: UTC late-night maps to prior ET date', () => {
  // 2026-05-13 03:30 UTC = 2026-05-12 23:30 ET (during EDT, UTC-4)
  const d = new Date('2026-05-13T03:30:00Z');
  assert.equal(_etDate(d), '2026-05-12');
});

test('_etDate: UTC midday maps to same ET date', () => {
  // 2026-05-13 18:00 UTC = 2026-05-13 14:00 ET
  const d = new Date('2026-05-13T18:00:00Z');
  assert.equal(_etDate(d), '2026-05-13');
});

async function mkTempRoot() {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'trades-store-'));
  return dir;
}

function makeTrade(overrides = {}) {
  return {
    type: 'order',
    tool: 'place_buy_order',
    symbol: 'AAPL',
    side: 'buy',
    quantity: 100,
    price: null,
    timestamp: '2026-05-13T18:00:00Z', // 14:00 ET → 2026-05-13 file
    sandboxId: 'sbx_test',
    agentId: 'default',
    agentName: 'Prophet',
    ...overrides,
  };
}

test('appendTrade writes to the expected ET-dated file', async () => {
  const root = await mkTempRoot();
  await appendTrade(root, 'acct1', makeTrade());
  const file = path.join(root, 'data', 'sandboxes', 'acct1', 'trades', '2026-05-13.jsonl');
  const raw = await fs.readFile(file, 'utf-8');
  assert.equal(raw.split('\n').filter(Boolean).length, 1);
  assert.match(raw, /"symbol":"AAPL"/);
});

test('readTrades returns newest-first across days and accounts', async () => {
  const root = await mkTempRoot();
  await appendTrade(root, 'acct1', makeTrade({ timestamp: '2026-05-12T18:00:00Z', symbol: 'OLD' }));
  await appendTrade(root, 'acct1', makeTrade({ timestamp: '2026-05-13T18:00:00Z', symbol: 'NEW' }));
  await appendTrade(root, 'acct2', makeTrade({ timestamp: '2026-05-13T19:00:00Z', symbol: 'NEWEST' }));

  const { trades, truncated } = await readTrades(root, { from: '2026-05-12', to: '2026-05-13' });
  assert.equal(trades.length, 3);
  assert.deepEqual(trades.map(t => t.symbol), ['NEWEST', 'NEW', 'OLD']);
  assert.equal(truncated, false);
});

test('readTrades sandboxId filter narrows results', async () => {
  const root = await mkTempRoot();
  await appendTrade(root, 'acct1', makeTrade({ sandboxId: 'sbx_a', symbol: 'A' }));
  await appendTrade(root, 'acct1', makeTrade({ sandboxId: 'sbx_b', symbol: 'B' }));
  const { trades } = await readTrades(root, { from: '2026-05-13', to: '2026-05-13', sandboxId: 'sbx_b' });
  assert.equal(trades.length, 1);
  assert.equal(trades[0].symbol, 'B');
});

test('readTrades skips corrupt lines', async () => {
  const root = await mkTempRoot();
  const file = path.join(root, 'data', 'sandboxes', 'acct1', 'trades', '2026-05-13.jsonl');
  await fs.mkdir(path.dirname(file), { recursive: true });
  await fs.writeFile(
    file,
    JSON.stringify(makeTrade({ symbol: 'GOOD1' })) + '\n' +
    '{not valid json\n' +
    JSON.stringify(makeTrade({ symbol: 'GOOD2' })) + '\n'
  );
  const { trades } = await readTrades(root, { from: '2026-05-13', to: '2026-05-13' });
  assert.equal(trades.length, 2);
  assert.deepEqual(trades.map(t => t.symbol).sort(), ['GOOD1', 'GOOD2']);
});

test('readTrades returns empty on missing day file', async () => {
  const root = await mkTempRoot();
  await appendTrade(root, 'acct1', makeTrade());
  const { trades } = await readTrades(root, { from: '2026-05-10', to: '2026-05-10' });
  assert.deepEqual(trades, []);
});

test('readTrades returns empty when sandboxes dir does not exist yet', async () => {
  const root = await mkTempRoot();
  const { trades, truncated } = await readTrades(root, { from: '2026-05-13', to: '2026-05-13' });
  assert.deepEqual(trades, []);
  assert.equal(truncated, false);
});

test('readTrades sets truncated and caps at 2000', async () => {
  const root = await mkTempRoot();
  const lines = [];
  for (let i = 0; i < 2100; i++) {
    lines.push(JSON.stringify(makeTrade({ symbol: 'S' + i, timestamp: `2026-05-13T18:${String(i % 60).padStart(2, '0')}:${String(Math.floor(i / 60) % 60).padStart(2, '0')}Z` })));
  }
  const file = path.join(root, 'data', 'sandboxes', 'acct1', 'trades', '2026-05-13.jsonl');
  await fs.mkdir(path.dirname(file), { recursive: true });
  await fs.writeFile(file, lines.join('\n') + '\n');
  const { trades, truncated } = await readTrades(root, { from: '2026-05-13', to: '2026-05-13' });
  assert.equal(trades.length, 2000);
  assert.equal(truncated, true);
});
