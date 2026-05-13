// Trade history persistence: NDJSON files per account per ET trading day.
// Source-of-truth-purity: this module owns filesystem layout only. Callers
// are responsible for resolving agentId/agentName/sandboxId before invoking.
import fs from 'node:fs/promises';
import path from 'node:path';

const MAX_RETURNED = 2000;

const _etFormatter = new Intl.DateTimeFormat('en-CA', {
  timeZone: 'America/New_York',
  year: 'numeric', month: '2-digit', day: '2-digit',
});

// _etDate returns YYYY-MM-DD for the given Date in America/New_York.
// Exposed for tests; treat as internal otherwise.
export function _etDate(date) {
  return _etFormatter.format(date);
}

function tradesDir(projectRoot, accountId) {
  return path.join(projectRoot, 'data', 'sandboxes', accountId, 'trades');
}

function tradesFile(projectRoot, accountId, ymd) {
  return path.join(tradesDir(projectRoot, accountId), `${ymd}.jsonl`);
}

// appendTrade writes one trade to the per-day NDJSON file. Trade must already
// carry sandboxId, agentId, agentName, and timestamp (ISO string) — store
// stays pure; resolution happens in the caller.
export async function appendTrade(projectRoot, accountId, trade) {
  if (!trade || !trade.timestamp) {
    throw new Error('appendTrade: trade.timestamp is required');
  }
  const ymd = _etDate(new Date(trade.timestamp));
  const dir = tradesDir(projectRoot, accountId);
  await fs.mkdir(dir, { recursive: true });
  await fs.appendFile(tradesFile(projectRoot, accountId, ymd), JSON.stringify(trade) + '\n', { flag: 'a' });
}

// readTrades enumerates per-day files across all accounts in `data/sandboxes/`
// between `from` and `to` (inclusive, YYYY-MM-DD), parses each NDJSON line,
// applies the optional sandboxId filter, and returns newest-first. Truncates
// at MAX_RETURNED and sets `truncated` accordingly.
export async function readTrades(projectRoot, { from, to, sandboxId } = {}) {
  if (!from || !to) throw new Error('readTrades: from and to are required (YYYY-MM-DD)');

  const dates = _enumerateDates(from, to);
  const sandboxesRoot = path.join(projectRoot, 'data', 'sandboxes');
  let accountIds;
  try {
    accountIds = await fs.readdir(sandboxesRoot);
  } catch (err) {
    if (err.code === 'ENOENT') return { trades: [], truncated: false };
    throw err;
  }

  const trades = [];
  for (const accountId of accountIds) {
    for (const ymd of dates) {
      let raw;
      try {
        raw = await fs.readFile(tradesFile(projectRoot, accountId, ymd), 'utf-8');
      } catch (err) {
        if (err.code === 'ENOENT' || err.code === 'ENOTDIR') continue;
        throw err;
      }
      for (const line of raw.split('\n')) {
        if (!line) continue;
        try {
          const trade = JSON.parse(line);
          if (sandboxId && trade.sandboxId !== sandboxId) continue;
          trades.push(trade);
        } catch {
          // Skip corrupt line, continue reading the file.
        }
      }
    }
  }

  trades.sort((a, b) => (b.timestamp || '').localeCompare(a.timestamp || ''));
  const truncated = trades.length > MAX_RETURNED;
  return { trades: truncated ? trades.slice(0, MAX_RETURNED) : trades, truncated };
}

function _enumerateDates(from, to) {
  const dates = [];
  const start = new Date(from + 'T00:00:00Z');
  const end = new Date(to + 'T00:00:00Z');
  for (let t = start.getTime(); t <= end.getTime(); t += 86400000) {
    dates.push(new Date(t).toISOString().slice(0, 10));
  }
  return dates;
}
