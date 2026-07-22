import { test, after } from 'node:test';
import assert from 'node:assert/strict';
import os from 'node:os';
import path from 'node:path';
import fs from 'node:fs';

// vectorDB reads DATABASE_PATH at import — point it at a throwaway db.
const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'op-vecdb-'));
process.env.DATABASE_PATH = path.join(tmpDir, 'test.db');

after(() => { try { fs.rmSync(tmpDir, { recursive: true, force: true }); } catch { /* ignore */ } });

test('getTradeStats: win_rate is over RESOLVED trades; breakeven resolved-not-winner; pending excluded', async (t) => {
  let vec, Database;
  try {
    vec = await import('../vectorDB.js');
    ({ default: Database } = await import('better-sqlite3'));
    vec.getEmbeddingCount(); // triggers getDb() → schema creation
  } catch (err) {
    t.skip(`native deps unavailable (run npm install / build better-sqlite3): ${err.message.split('\n')[0]}`);
    return;
  }

  // Seed directly (bypassing the embedding model): 1 winner, 1 loser, 1 breakeven, 1 pending.
  const db = new Database(process.env.DATABASE_PATH);
  const ins = db.prepare(
    'INSERT INTO trade_embeddings (id,decision_file,symbol,action,strategy,result_pct,result_dollars,date,reasoning,market_context) VALUES (?,?,?,?,?,?,?,?,?,?)'
  );
  ins.run('winner', 'f', 'AAA', 'buy', 's', 5.0, 100, '2026-01-01', 'r', 'm');
  ins.run('loser', 'f', 'BBB', 'buy', 's', -3.0, -50, '2026-01-01', 'r', 'm');
  ins.run('breakeven', 'f', 'CCC', 'buy', 's', 0, 0, '2026-01-01', 'r', 'm');
  ins.run('pending', 'f', 'DDD', 'buy', 's', null, null, '2026-01-01', 'r', 'm');
  db.close();

  const s = vec.getTradeStats({});
  assert.equal(s.count, 4, 'total');
  assert.equal(s.resolved, 3, 'resolved = non-null result_pct (breakeven counts)');
  assert.equal(s.pending, 1, 'pending = null result_pct');
  assert.equal(s.winners, 1);
  assert.equal(s.losers, 1);
  // The R6 fix: win_rate = winners / resolved (~33.3%), NOT the old winners / count (25%).
  assert.ok(Math.abs(s.win_rate - 100 / 3) < 0.01, `win_rate ${s.win_rate} should be ~33.33 (winners/resolved)`);
});
