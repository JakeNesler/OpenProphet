// Opt-in integration check: downloads the public MiniLM model, never calls a broker or LLM API.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'openprophet-embedding-'));
process.env.DATABASE_PATH = path.join(dir, 'trades.db');

try {
  const { getEmbedding, storeTrade, findSimilarTrades } = await import('../vectorDB.js');
  const reasoning = 'Strong revenue growth supports a bullish technology stock thesis.';
  const embedding = await getEmbedding(reasoning);
  assert.equal(embedding.length, 384);
  assert.ok(embedding.every(Number.isFinite));
  const norm = Math.sqrt(embedding.reduce((sum, value) => sum + value * value, 0));
  assert.ok(Math.abs(norm - 1) < 1e-5);
  await storeTrade({
    id: 'smoke', decision_file: 'smoke.json', symbol: 'TEST', action: 'buy',
    strategy: 'test', result_pct: null, result_dollars: null, date: '2026-09-24',
    reasoning, market_context: 'Earnings growth',
  });
  const matches = await findSimilarTrades(reasoning, 1);
  assert.equal(matches.length, 1);
  assert.equal(matches[0].id, 'smoke');
  console.log('Embedding smoke passed: normalized 384-dimensional vector stored and retrieved.');
} finally {
  fs.rmSync(dir, { recursive: true, force: true });
}
