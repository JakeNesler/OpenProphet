import Database from 'better-sqlite3';
import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';

const safeId = /^[A-Za-z0-9_-]{1,128}$/;
const count = value => Number.isFinite(value) && value >= 0 ? value : 0;
const dayFormatter = new Intl.DateTimeFormat('en-CA', { timeZone: 'America/New_York', year: 'numeric', month: '2-digit', day: '2-digit' });

function dayKey(date) {
  const parts = Object.fromEntries(dayFormatter.formatToParts(date).map(p => [p.type, p.value]));
  return `${parts.year}-${parts.month}-${parts.day}`;
}

function emptyPeriod() {
  return { turns: 0, totalTokens: 0, inputTokens: 0, outputTokens: 0, reasoningTokens: 0, cacheReadTokens: 0, cacheWriteTokens: 0, reportedUsd: 0, unpricedTurns: 0, subscriptionTurns: 0 };
}

export function summarizeAiUsage(rows, { now = new Date(), oauthProviders = new Set() } = {}) {
  const today = emptyPeriod(), total = emptyPeriod(), seen = new Set();
  for (const row of rows) {
    if (seen.has(row.id)) continue;
    seen.add(row.id);
    let tokens;
    try { tokens = JSON.parse(row.tokens || '{}'); } catch { tokens = {}; }
    tokens ||= {};
    const input = count(tokens.input), output = count(tokens.output);
    const cacheRead = count(tokens.cache?.read), cacheWrite = count(tokens.cache?.write);
    const tokenTotal = Number.isFinite(tokens.total) && tokens.total >= 0 ? tokens.total : input + output + cacheRead + cacheWrite;
    const subscription = oauthProviders.has(row.provider);
    const priced = !subscription && Number.isFinite(row.cost) && row.cost >= 0;
    const at = new Date(row.time_created);
    const periods = [total];
    if (Number.isFinite(at.getTime()) && dayKey(at) === dayKey(now)) periods.push(today);
    for (const period of periods) {
      period.turns++;
      period.totalTokens += tokenTotal;
      period.inputTokens += input;
      period.outputTokens += output;
      period.reasoningTokens += count(tokens.reasoning);
      period.cacheReadTokens += cacheRead;
      period.cacheWriteTokens += cacheWrite;
      if (priced) period.reportedUsd += row.cost;
      else period.unpricedTurns++;
      if (subscription) period.subscriptionTurns++;
    }
  }
  for (const period of [today, total]) {
    period.costUsd = period.turns && !period.unpricedTurns ? period.reportedUsd : null;
    period.billing = !period.turns ? 'no_usage' : period.subscriptionTurns === period.turns ? 'subscription'
      : period.unpricedTurns ? 'partial' : 'reported';
  }
  return { available: true, updatedAt: now.toISOString(), timeZone: 'America/New_York', today, total };
}

export function createAiUsageReader({ dataDir, dbPath = path.join(process.env.XDG_DATA_HOME || path.join(os.homedir(), '.local', 'share'), 'opencode', 'opencode.db'), now = () => new Date(), cacheMs = 15000 }) {
  const cache = new Map();
  return {
    async getProviderBilling(provider) {
      try {
        const auth = JSON.parse(await fs.readFile(path.join(path.dirname(dbPath), 'auth.json'), 'utf8'));
        return auth[provider]?.type === 'oauth' ? 'subscription' : auth[provider]?.type === 'api' ? 'reported' : null;
      } catch { return null; }
    },
    async get(accountId) {
      if (!safeId.test(accountId)) throw new Error('Invalid account id');
      const cached = cache.get(accountId);
      if (cached && cached.expires > now().getTime()) return cached.value;
      let db;
      try {
        const dir = path.join(dataDir, 'sandboxes', accountId, 'chat-history');
        const files = await fs.readdir(dir);
        const sessionIds = files.map(f => /^session-(ses_[A-Za-z0-9_-]+)\.jsonl$/.exec(f)?.[1]).filter(Boolean);
        const oauthProviders = new Set();
        try {
          const auth = JSON.parse(await fs.readFile(path.join(path.dirname(dbPath), 'auth.json'), 'utf8'));
          for (const [provider, entry] of Object.entries(auth)) if (entry?.type === 'oauth') oauthProviders.add(provider);
        } catch { /* Missing auth metadata means the reported provider cost is used. */ }
        db = new Database(dbPath, { readonly: true, fileMustExist: true, timeout: 1000 });
        const rows = db.prepare(`
          WITH RECURSIVE owned(id) AS (
            SELECT id FROM session WHERE id IN (SELECT value FROM json_each(?))
            UNION SELECT s.id FROM session s JOIN owned o ON s.parent_id = o.id
          )
          SELECT p.id, p.time_created,
                 json_extract(p.data, '$.cost') AS cost,
                 json_extract(p.data, '$.tokens') AS tokens,
                 json_extract(m.data, '$.providerID') AS provider
          FROM part p JOIN owned o ON p.session_id = o.id
          JOIN message m ON p.message_id = m.id
          WHERE json_extract(p.data, '$.type') = 'step-finish'
        `).all(JSON.stringify(sessionIds));
        const value = { accountId, ...summarizeAiUsage(rows, { now: now(), oauthProviders }), linkedSessions: sessionIds.length };
        cache.set(accountId, { expires: now().getTime() + cacheMs, value });
        return value;
      } catch {
        return { accountId, available: false, updatedAt: now().toISOString(), reason: 'AI usage data unavailable' };
      } finally { db?.close(); }
    },
  };
}
