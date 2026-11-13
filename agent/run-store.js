import Database from 'better-sqlite3';
import fs from 'node:fs';
import path from 'node:path';

const safeId = /^[A-Za-z0-9._-]{1,128}$/;
const statuses = new Set(['running', 'completed', 'no_action', 'skipped', 'failed', 'interrupted']);
const reasons = new Set(['running', 'order_confirmed', 'no_order_confirmation', 'message_completed', 'market_closed', 'market_clock_unavailable', 'auth_missing', 'paused', 'cli_exit', 'cli_signal', 'spawn_failed', 'timeout', 'provider_error', 'interrupted', 'run_failed']);
const count = value => Number.isFinite(value) && value >= 0 ? value : 0;
const optionalCount = value => Number.isFinite(value) && value >= 0 ? value : null;
const id = value => {
  if (!safeId.test(value || '') || value === '.' || value === '..') throw new Error('Invalid run or account id');
  return value;
};

export class RunStore {
  constructor({ dbPath, retentionDays = 30, now = () => new Date() }) {
    this.dbPath = dbPath;
    this.retentionDays = retentionDays;
    this.now = now;
    this.db = null;
  }

  _open() {
    if (this.db) return this.db;
    fs.mkdirSync(path.dirname(this.dbPath), { recursive: true, mode: 0o700 });
    const db = new Database(this.dbPath, { timeout: 3000 });
    try {
      fs.chmodSync(this.dbPath, 0o600);
      db.pragma('journal_mode = WAL');
      db.exec(`CREATE TABLE IF NOT EXISTS harness_runs (
        id TEXT PRIMARY KEY, account_id TEXT NOT NULL, started_at TEXT NOT NULL, record TEXT NOT NULL
      ); CREATE INDEX IF NOT EXISTS harness_runs_account_time ON harness_runs(account_id, started_at DESC);
      CREATE INDEX IF NOT EXISTS harness_runs_time ON harness_runs(started_at);`);
    } catch (error) { db.close(); throw error; }
    this.db = db;
    return db;
  }

  record(accountId, value) {
    id(accountId); id(value.id);
    if (!statuses.has(value.status) || !reasons.has(value.reason)) throw new Error('Invalid run outcome');
    const started = new Date(value.startedAt);
    if (!Number.isFinite(started.getTime())) throw new Error('Invalid run timestamp');
    const billing = ['reported', 'subscription', 'partial'].includes(value.billing) ? value.billing : 'unavailable';
    // Allowlisted metadata only: no prompts, tool arguments, raw errors, or credentials.
    const record = {
      id: value.id, accountId,
      sandboxId: value.sandboxId && safeId.test(value.sandboxId) ? value.sandboxId : null,
      kind: ['heartbeat', 'message', 'startup'].includes(value.kind) ? value.kind : 'heartbeat',
      phase: String(value.phase || '').slice(0, 32), model: String(value.model || '').slice(0, 160),
      sessionMode: value.sessionMode === 'fresh' ? 'fresh' : 'continuous',
      sessionId: value.sessionId && safeId.test(value.sessionId) ? value.sessionId : null,
      status: value.status, reason: value.reason, startedAt: started.toISOString(),
      finishedAt: value.finishedAt ? new Date(value.finishedAt).toISOString() : null,
      durationMs: value.finishedAt ? count(value.durationMs) : null,
      toolCalls: count(value.toolCalls), orders: count(value.orders), tokens: optionalCount(value.tokens),
      costUsd: billing === 'subscription' ? null : optionalCount(value.costUsd),
      reportedUsd: count(value.reportedUsd), billing,
    };
    const db = this._open();
    const saved = db.prepare(`INSERT INTO harness_runs VALUES (?, ?, ?, ?)
      ON CONFLICT(id) DO UPDATE SET record = excluded.record WHERE harness_runs.account_id = excluded.account_id`)
      .run(record.id, accountId, record.startedAt, JSON.stringify(record));
    if (!saved.changes) throw new Error('Run belongs to another account');
    const cutoff = new Date(this.now().getTime() - this.retentionDays * 86400000).toISOString();
    db.prepare('DELETE FROM harness_runs WHERE started_at < ?').run(cutoff);
    return record;
  }

  list(accountId, { limit = 50 } = {}) {
    id(accountId);
    if (!Number.isInteger(limit) || limit < 1 || limit > 100) throw new RangeError('Invalid history limit');
    const cutoff = new Date(this.now().getTime() - this.retentionDays * 86400000).toISOString();
    return this._open().prepare('SELECT record FROM harness_runs WHERE account_id = ? AND started_at >= ? ORDER BY started_at DESC, rowid DESC LIMIT ?')
      .all(accountId, cutoff, limit).map(row => JSON.parse(row.record));
  }

  close() { this.db?.close(); this.db = null; }
}

export function runHistoryHandler({ runStore, getAccountById, getActiveAccount }) {
  return (req, res) => {
    const accountId = req.query.accountId || getActiveAccount()?.id;
    if (typeof accountId !== 'string' || !safeId.test(accountId)) return res.status(400).json({ error: 'Invalid account id' });
    if (!getAccountById(accountId)) return res.status(404).json({ error: 'Account not found' });
    const limit = Number(req.query.limit || 50);
    if (!Number.isInteger(limit) || limit < 1 || limit > 100) return res.status(400).json({ error: 'Limit must be between 1 and 100' });
    try { res.json({ accountId, runs: runStore.list(accountId, { limit }) }); }
    catch { res.status(503).json({ error: 'Run history is unavailable' }); }
  };
}
