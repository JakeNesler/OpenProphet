# Trade History Persistence + Historic View — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist every harness-emitted trade to disk as NDJSON, expose a `/api/trades` endpoint, and add a date-range historic toggle to the dashboard.

**Architecture:** A new `agent/trades-store.js` module owns the filesystem layer (append + read). `server.js` subscribes to the existing `'trade'` event per runtime and writes via the store. The dashboard seeds today's trades on load and gains a `_historicMode` flag plus date inputs for browsing prior days.

**Tech Stack:** Node.js (ESM), `node:test`, Express, vanilla JS dashboard. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-05-13-trade-history-persistence-design.md`

---

## File Structure

**Create:**
- `agent/trades-store.js` — append/read NDJSON; pure filesystem module, no config-store imports
- `agent/trades-store.test.mjs` — node:test suite

**Modify:**
- `agent/harness.js` — one-line emit fix in `AgentState.addTrade`
- `agent/server.js` — wire `appendTrade` into the existing per-runtime `'trade'` listener; add `GET /api/trades` route
- `agent/public/index.html` — page-load seed, `_historicMode` flag, dedup helper, historic toggle UI

---

## Task 1: Build trades-store with appendTrade (TDD)

**Files:**
- Create: `agent/trades-store.js`
- Test: `agent/trades-store.test.mjs`

- [ ] **Step 1.1: Write the failing test for ET date computation**

Create `agent/trades-store.test.mjs`:

```js
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
```

- [ ] **Step 1.2: Run the test to verify it fails**

Run: `node --test agent/trades-store.test.mjs`

Expected: FAIL — `Cannot find module './trades-store.js'`.

- [ ] **Step 1.3: Implement the minimal module**

Create `agent/trades-store.js`:

```js
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
```

- [ ] **Step 1.4: Run the test to verify it passes**

Run: `node --test agent/trades-store.test.mjs`

Expected: PASS — both `_etDate` tests green.

- [ ] **Step 1.5: Commit**

```bash
git add agent/trades-store.js agent/trades-store.test.mjs
git commit -m "feat(trades): trades-store module with ET-date helper"
```

---

## Task 2: Cover appendTrade + readTrades behavior (TDD)

**Files:**
- Test: `agent/trades-store.test.mjs`

- [ ] **Step 2.1: Add failing tests for append + read round-trip**

Append to `agent/trades-store.test.mjs`:

```js
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
```

- [ ] **Step 2.2: Run tests to verify they pass**

Run: `node --test agent/trades-store.test.mjs`

Expected: PASS — all 9 tests green (2 ET-date + 7 new).

- [ ] **Step 2.3: Commit**

```bash
git add agent/trades-store.test.mjs
git commit -m "test(trades): cover append + read round-trip, filter, dedup, cap"
```

---

## Task 3: Fix harness addTrade emit to include timestamp

**Files:**
- Modify: `agent/harness.js:176-180`

- [ ] **Step 3.1: Apply the one-line fix**

Edit `agent/harness.js`, replace:

```js
  addTrade(trade) {
    this.recentTrades.unshift({ ...trade, timestamp: new Date().toISOString() });
    if (this.recentTrades.length > 50) this.recentTrades.pop();
    this.emit('trade', trade);
  }
```

with:

```js
  addTrade(trade) {
    const stamped = { ...trade, timestamp: new Date().toISOString() };
    this.recentTrades.unshift(stamped);
    if (this.recentTrades.length > 50) this.recentTrades.pop();
    this.emit('trade', stamped);
  }
```

- [ ] **Step 3.2: Run the existing test suite to confirm nothing broke**

Run: `node --test agent/*.test.mjs`

Expected: PASS — all 34 existing tests still pass; this is a pure additive change to the emit payload.

- [ ] **Step 3.3: Commit**

```bash
git add agent/harness.js
git commit -m "fix(harness): emit trade events with timestamp matching recentTrades"
```

---

## Task 4: Wire appendTrade into server.js trade listener

**Files:**
- Modify: `agent/server.js` (top-of-file imports + the `'trade'` listener block at ~line 230)

- [ ] **Step 4.1: Add the import**

Edit `agent/server.js` and add `appendTrade` import near the existing imports (top of file, after the config-store import block):

```js
import { appendTrade } from './trades-store.js';
```

- [ ] **Step 4.2: Hook appendTrade into the existing trade listener**

In `agent/server.js`, find the `targetHarness.state.on('trade', (trade) => {` block (around line 230). Replace it with:

```js
  targetHarness.state.on('trade', async (trade) => {
    const sandboxId = targetHarness.sandboxId;

    // Persist before the Slack notifications so a slow webhook doesn't delay
    // the disk write. Errors are soft-fail: log and continue, never throw.
    try {
      const sandbox = getSandbox(sandboxId);
      const accountId = sandbox?.accountId;
      const resolved = getResolvedAgentForSandbox(sandboxId);
      if (accountId) {
        await appendTrade(PROJECT_ROOT, accountId, {
          ...trade,
          sandboxId,
          agentId: resolved?.id || null,
          agentName: resolved?.name || null,
        });
      }
    } catch (err) {
      broadcast('agent_log', {
        message: `appendTrade failed: ${err.message}`,
        level: 'warning',
        sandboxId,
        timestamp: new Date().toISOString(),
      });
    }

    if (slackEnabled('tradeExecuted', sandboxId)) {
      const side = (trade.side || '').toUpperCase();
      const emoji = side === 'BUY' ? ':chart_with_upwards_trend:' : ':chart_with_downwards_trend:';
      notifySlack(`${emoji} *Trade Executed*\n${side} ${trade.quantity || '?'}x ${trade.symbol || '??'}${trade.price ? ' @ $' + trade.price : ''}\nTool: ${trade.tool || 'unknown'}`, sandboxId);
    }
    const sideLower = (trade.side || '').toLowerCase();
    if (sideLower === 'buy' && slackEnabled('positionOpened', sandboxId)) {
      notifySlack(`:new: *Position Opened*\n${trade.symbol || '??'} | ${trade.quantity || '?'} contracts${trade.price ? ' @ $' + trade.price : ''}`, sandboxId);
    }
    if (sideLower === 'sell' && slackEnabled('positionClosed', sandboxId)) {
      notifySlack(`:checkered_flag: *Position Closed*\n${trade.symbol || '??'} | ${trade.quantity || '?'} contracts${trade.price ? ' @ $' + trade.price : ''}`, sandboxId);
    }
  });
```

(Note: `getSandbox` is already imported on line 29.)

- [ ] **Step 4.3: Smoke-check the server still starts**

Run: `node -e "import('./agent/server.js').then(() => { console.log('IMPORT_OK'); process.exit(0); })"`

Expected: Prints `IMPORT_OK`. No syntax errors. (The server will try to bind to port 3737 — kill it with Ctrl-C if it hangs past the OK print.)

- [ ] **Step 4.4: Commit**

```bash
git add agent/server.js
git commit -m "feat(server): persist trade events to disk on emit"
```

---

## Task 5: Add GET /api/trades endpoint

**Files:**
- Modify: `agent/server.js`

- [ ] **Step 5.1: Import readTrades**

Update the existing import added in Task 4 to also pull `readTrades`:

```js
import { appendTrade, readTrades } from './trades-store.js';
```

- [ ] **Step 5.2: Add the endpoint route**

Find a logical spot in `agent/server.js` next to other GET routes (e.g. right after the `/api/sandboxes` route around line 662). Insert:

```js
// ── Trade history ──────────────────────────────────────────────────
// Reads NDJSON files written by the per-runtime trade listener. Defaults to
// today (ET). Hard caps: max 90-day range, max 2000 trades returned.
app.get('/api/trades', async (req, res) => {
  const _etFmt = new Intl.DateTimeFormat('en-CA', {
    timeZone: 'America/New_York',
    year: 'numeric', month: '2-digit', day: '2-digit',
  });
  const today = _etFmt.format(new Date());
  const from = String(req.query.from || today);
  const to = String(req.query.to || today);
  const sandboxId = req.query.sandboxId ? String(req.query.sandboxId) : undefined;

  const ymdRe = /^\d{4}-\d{2}-\d{2}$/;
  if (!ymdRe.test(from) || !ymdRe.test(to)) {
    return res.status(400).json({ error: 'from/to must be YYYY-MM-DD' });
  }
  if (from > to) {
    return res.status(400).json({ error: 'from must be <= to' });
  }
  const fromMs = Date.parse(from + 'T00:00:00Z');
  const toMs = Date.parse(to + 'T00:00:00Z');
  if (Number.isNaN(fromMs) || Number.isNaN(toMs)) {
    return res.status(400).json({ error: 'unparseable date' });
  }
  const days = (toMs - fromMs) / 86400000 + 1;
  if (days > 90) {
    return res.status(400).json({ error: 'range exceeds 90 days' });
  }

  try {
    const { trades, truncated } = await readTrades(PROJECT_ROOT, { from, to, sandboxId });
    res.json({ from, to, count: trades.length, truncated, trades });
  } catch (err) {
    res.status(500).json({ error: err.message });
  }
});
```

- [ ] **Step 5.3: Manual smoke test**

In a separate shell:

```bash
node agent/server.js &
sleep 2
curl -s "http://localhost:3737/api/trades" | head -c 200
curl -s "http://localhost:3737/api/trades?from=2026-05-01&to=2025-05-01" # should 400
curl -s "http://localhost:3737/api/trades?from=2026-01-01&to=2026-05-13" # should 400 (>90d)
kill %1
```

Expected: First call returns `{"from":"<today>","to":"<today>","count":0,"truncated":false,"trades":[]}` (no trades yet). Second returns 400. Third returns 400.

- [ ] **Step 5.4: Commit**

```bash
git add agent/server.js
git commit -m "feat(api): GET /api/trades with date-range validation"
```

---

## Task 6: Dashboard — page-load seed + render-order + dedup

**Files:**
- Modify: `agent/public/index.html` (the trade-feed block around lines 2251-2311)

- [ ] **Step 6.1: Lift the DOM cap into a const and add a dedup set**

Find the `addTradeCard` function (around line 2252). At the top of the trade-feed section (just before `function addTradeCard(trade) {`), insert:

```js
// ── Trade feed module state ───────────────
let _historicMode = false;
const TRADE_CAP_LIVE = 50;
const TRADE_CAP_HISTORIC = 2000;
const _renderedTradeKeys = new Set();

function _tradeKey(t) {
  return [t.sandboxId || '', t.timestamp || '', t.tool || '', t.symbol || ''].join('|');
}

function _resetRenderedTradeKeys() {
  _renderedTradeKeys.clear();
}
```

- [ ] **Step 6.2: Wire dedup + dynamic cap into addTradeCard**

In `addTradeCard`, near the top after `const feed = document.getElementById('trades-feed');`, add:

```js
  // Skip if this trade key was already rendered (seed/live overlap).
  const key = _tradeKey(trade);
  if (_renderedTradeKeys.has(key)) return;
  _renderedTradeKeys.add(key);
```

Then replace the hard-coded `50` cap line near the end of the function:

```js
  while(feed.children.length > 50) feed.removeChild(feed.lastChild);
```

with:

```js
  const cap = _historicMode ? TRADE_CAP_HISTORIC : TRADE_CAP_LIVE;
  while(feed.children.length > cap) feed.removeChild(feed.lastChild);
```

- [ ] **Step 6.3: Add a bulk-render helper that respects insert-before ordering**

Below `addTradeCard`, add:

```js
// Render a batch of trades (newest-first as returned from /api/trades) so the
// final DOM order matches: iterate ascending so each insertBefore puts older
// trades below the previously-inserted one.
function renderTradesBulk(trades) {
  const feed = document.getElementById('trades-feed');
  if (feed.querySelector('.no-data')) feed.innerHTML = '';
  if (!trades.length) {
    feed.innerHTML = '<div class="no-data">No trades in this range.</div>';
    return;
  }
  for (let i = trades.length - 1; i >= 0; i--) {
    addTradeCard(trades[i]);
  }
}

async function fetchTrades(from, to) {
  const params = new URLSearchParams({ from, to });
  const res = await fetch('/api/trades?' + params.toString());
  if (!res.ok) throw new Error('fetch failed: ' + res.status);
  return res.json();
}

function _todayEt() {
  return new Intl.DateTimeFormat('en-CA', {
    timeZone: 'America/New_York',
    year: 'numeric', month: '2-digit', day: '2-digit',
  }).format(new Date());
}

async function seedTodayTrades() {
  try {
    const today = _todayEt();
    const data = await fetchTrades(today, today);
    renderTradesBulk(data.trades);
  } catch (err) {
    console.warn('seedTodayTrades failed:', err);
  }
}
```

- [ ] **Step 6.4: Wire seedTodayTrades into page init**

Find where the EventSource is created (search for `new EventSource('/api/events')` — around line 1720). Immediately *before* the `new EventSource` line, add:

```js
  seedTodayTrades();
```

(The seed fetch runs ahead of SSE attach. Live trades arriving during the in-flight fetch are still rendered by SSE, and the dedup Set prevents duplicates when the seed returns.)

- [ ] **Step 6.5: Guard the SSE trade handler with _historicMode**

Find the existing SSE trade listener (likely `es.addEventListener('trade', ...)` near the other listeners around line 1730-1780). Replace its body with a guarded version:

```js
  es.addEventListener('trade', e => {
    if (_historicMode) return;
    const d = JSON.parse(e.data);
    addTradeCard(d);
  });
```

(If the listener already does additional work beyond `addTradeCard`, preserve that work *inside* the `if (_historicMode) return;` guard.)

- [ ] **Step 6.6: Manual smoke check**

Run `node agent/server.js`, open the dashboard, refresh the page. Expected:
- Trade feed shows "No trades in this range." if nothing was traded today.
- If any harness emits a trade after this loads, a card appears with a non-`--` timestamp.
- Refreshing the page keeps already-emitted trades visible (seed-on-load works).

- [ ] **Step 6.7: Commit**

```bash
git add agent/public/index.html
git commit -m "feat(ui): seed today's trades on page load + dedup live cards"
```

---

## Task 7: Dashboard — historic toggle with date range

**Files:**
- Modify: `agent/public/index.html`

- [ ] **Step 7.1: Add the toggle controls to the trade-feed header**

Find the trade-feed filter bar (search for `trades-agent-filter` around line 1075). Just after the closing `</select>` and the filter-count span, before `</div>` of the row, insert:

```html
          <label class="trades-historic-toggle">
            <input type="checkbox" id="trades-historic-toggle" />
            Show historic trades
          </label>
          <span id="trades-historic-controls" style="display:none">
            <input type="date" id="trades-from-date" />
            <input type="date" id="trades-to-date" />
            <button id="trades-apply-btn">Apply</button>
            <span id="trades-truncation-notice" class="trades-filter-count"></span>
          </span>
```

- [ ] **Step 7.2: Wire the toggle and Apply button**

Near the other trade-feed helpers (after `seedTodayTrades`), add:

```js
function _wireHistoricControls() {
  const toggle = document.getElementById('trades-historic-toggle');
  const controls = document.getElementById('trades-historic-controls');
  const fromInput = document.getElementById('trades-from-date');
  const toInput = document.getElementById('trades-to-date');
  const applyBtn = document.getElementById('trades-apply-btn');
  const truncEl = document.getElementById('trades-truncation-notice');
  if (!toggle) return;

  const today = _todayEt();
  fromInput.value = today;
  toInput.value = today;

  toggle.addEventListener('change', async () => {
    const feed = document.getElementById('trades-feed');
    if (toggle.checked) {
      _historicMode = true;
      controls.style.display = '';
      feed.innerHTML = '';
      _resetRenderedTradeKeys();
      truncEl.textContent = '';
      await applyHistoricRange();
    } else {
      _historicMode = false;
      controls.style.display = 'none';
      feed.innerHTML = '';
      _resetRenderedTradeKeys();
      truncEl.textContent = '';
      await seedTodayTrades();
    }
  });

  applyBtn.addEventListener('click', applyHistoricRange);
}

async function applyHistoricRange() {
  const from = document.getElementById('trades-from-date').value;
  const to = document.getElementById('trades-to-date').value;
  const truncEl = document.getElementById('trades-truncation-notice');
  const feed = document.getElementById('trades-feed');
  if (!from || !to) return;
  feed.innerHTML = '<div class="no-data">Loading…</div>';
  _resetRenderedTradeKeys();
  try {
    const data = await fetchTrades(from, to);
    feed.innerHTML = '';
    renderTradesBulk(data.trades);
    truncEl.textContent = data.truncated
      ? `truncated: showing ${data.trades.length} of more`
      : `${data.count} trade${data.count === 1 ? '' : 's'}`;
  } catch (err) {
    feed.innerHTML = '<div class="no-data">Error: ' + (err.message || 'unknown') + '</div>';
  }
}
```

- [ ] **Step 7.3: Call the wiring function on page init**

Find where `seedTodayTrades()` was added in Step 6.4. Immediately *after* that line, add:

```js
  _wireHistoricControls();
```

- [ ] **Step 7.4: Manual smoke check**

Run `node agent/server.js`, open the dashboard:
- Verify the new checkbox + hidden date controls are visible.
- Check the box → date inputs + Apply button appear; feed clears.
- Click Apply with default (today/today) → shows today's trades (or "No trades in this range.").
- Change `from` to two days ago → click Apply → shows trades from that range, newest on top.
- Uncheck the box → live mode resumes, today's seed re-renders, SSE-driven live cards work again.

- [ ] **Step 7.5: Commit**

```bash
git add agent/public/index.html
git commit -m "feat(ui): historic trade view with date range picker"
```

---

## Task 8: Final verification

- [ ] **Step 8.1: Run all JS tests**

Run: `node --test agent/*.test.mjs`

Expected: All tests pass (34 existing + 9 new from `trades-store.test.mjs` = 43 total).

- [ ] **Step 8.2: Re-confirm Go side untouched**

Run: `go build ./cmd/bot && go test ./services -count=1`

Expected: Build clean, all Go tests pass. (This change is JS-only; this is a safety check.)

- [ ] **Step 8.3: Final commit if anything dangling**

```bash
git status
# If clean, no commit needed.
```

---

## Self-Review (planner)

**Spec coverage check:**

- ✅ NDJSON per-account per-ET-day storage → Task 1
- ✅ `appendTrade` + `readTrades` interface → Task 1/2
- ✅ Harness emit-with-timestamp fix → Task 3
- ✅ Server.js wiring (soft-fail, agent resolution at call site) → Task 4
- ✅ `GET /api/trades` endpoint with validation → Task 5
- ✅ Page-load seed → Task 6
- ✅ Dedup keyed by `sandboxId|timestamp|tool|symbol` → Task 6
- ✅ Bulk render in ascending order so newest ends up on top → Task 6
- ✅ Historic toggle with date inputs + Apply + truncation notice → Task 7
- ✅ `_historicMode` flag-gated SSE handler → Task 6
- ✅ Empty-state placeholder for zero-result historic range → Task 6 (`renderTradesBulk`)
- ✅ Tests for ET-date, round-trip, sandbox filter, corrupt-line skip, missing-file, truncation cap → Task 2
- ✅ Errors soft-fail via `agent_log` warning → Task 4

**Out-of-scope items confirmed not in plan:** Alpaca reconciliation, P&L, CSV export, auto-prune. Correct.
