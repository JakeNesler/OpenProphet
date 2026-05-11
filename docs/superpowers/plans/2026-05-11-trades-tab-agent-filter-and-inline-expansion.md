# Trades Tab — Agent Filter & Inline Expansion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the live Trades tab filter by agent (with an "All Agents" default) and expand each trade card in place to show its full field set, replacing the bygone scroll-to-detail behavior.

**Architecture:** Pure client-side change in a single file, `agent/public/index.html`. Each `trade` SSE event already carries `sandboxId`; we resolve it to an agent name via `config.sandboxes[sid].agent.activeAgentId → config.agents`. The filter is a `<select>` whose options are seeded as new agents are observed. Expansion is a CSS `is-expanded` class toggle on the card. No backend, no SSE, no schema changes.

**Tech Stack:** Vanilla JS / DOM, plain CSS variables already defined in `agent/public/index.html`. No build step. No JS test harness exists in this repo (`package.json` has `"test": "echo no test specified"`) — verification is manual browser inspection per task, matching the spec's testing plan.

**Spec:** `docs/superpowers/specs/2026-05-11-trades-tab-agent-filter-and-inline-expansion-design.md`

---

## File Structure

Only one file changes:

- **Modify:** `agent/public/index.html`
  - CSS block at lines 884–896 (`/* ── Trade Feed ── */`) — append new rules.
  - `#panel-trades` markup at lines 1048–1054 — insert filter row.
  - `addTradeCard` at lines 2225–2235 — rewrite to emit the new card structure and agent metadata.
  - New JS helpers placed immediately after `addTradeCard` (so they sit inside the same `// ── Trade feed ──` block).
  - New module-level variable `_tradesAgentFilter` declared near other module-level UI state (search for `_sbxHbExpanded` at the top of the script for an example sibling).

## Verification model

Each task ends with explicit manual verification in a browser. The agent server is started once via `npm run agent` from the repo root (it listens on `http://localhost:3737`). Hard-refresh the dashboard (`Ctrl+Shift+R`) after edits, since `index.html` is served as a static asset and the browser may cache. The Trades tab is reachable via the `Trades` tab button at the top of the dashboard.

If no real sandboxes/agents are available to produce trades, use the browser DevTools Console to synthesize a trade for verification:

```js
addTradeCard({
  type: 'order',
  tool: 'penny.place_buy_order',
  symbol: 'ABCD',
  side: 'buy',
  quantity: 100,
  price: 4.21,
  timestamp: new Date().toISOString(),
  sandboxId: Object.keys(config.sandboxes || {})[0] || null,
});
```

Replace the `sandboxId` to test other agents. To test "Unassigned", pass `sandboxId: null` or `sandboxId: '__missing__'`.

---

### Task 1: Add CSS for filter row, agent badge, chevron, and expanded detail block

**Files:**
- Modify: `agent/public/index.html:896` (append rules at the end of the existing `/* ── Trade Feed ── */` CSS block)

- [ ] **Step 1: Read the current trade-feed CSS block to confirm anchor location**

Open `agent/public/index.html` and confirm line 896 ends with:
```css
    .trade-card .trade-time { font-size: 11px; color: var(--ink-faint); font-family: 'IBM Plex Mono', monospace; }
```
The next blank line (897) is where the new CSS will be inserted.

- [ ] **Step 2: Append new CSS rules immediately after the existing `.trade-card .trade-time` rule**

Insert at line 897 (between the trade-time rule and the closing of the trade-feed block):

```css
    .trades-filter-row { display: flex; align-items: center; gap: 10px; margin-bottom: 12px; font-size: 12px; color: var(--ink-muted); }
    .trades-filter-row select { font-size: 12px; }
    .trades-filter-count { font-size: 11px; color: var(--ink-faint); margin-left: auto; }

    .trade-card { cursor: pointer; transition: background 0.12s; }
    .trade-card:hover { background: var(--paper-2, var(--paper)); }
    .trade-card .trade-chevron { display: inline-block; margin-left: 8px; transition: transform 0.18s; color: var(--ink-faint); font-size: 12px; }
    .trade-card.is-expanded .trade-chevron { transform: rotate(180deg); }
    .trade-card .trade-agent-badge {
      display: inline-block; font-size: 10px; font-weight: 600; text-transform: uppercase;
      padding: 2px 6px; margin-left: 8px; border: 1px solid var(--rule); border-radius: 4px;
      color: var(--ink-muted); background: var(--surface-2, transparent);
    }
    .trade-card .trade-expanded {
      display: none; margin-top: 10px; padding-top: 10px; border-top: 1px dashed var(--rule);
      font-size: 12px; color: var(--ink-muted); font-family: 'IBM Plex Mono', monospace;
    }
    .trade-card .trade-expanded .trade-field { display: flex; gap: 8px; padding: 2px 0; }
    .trade-card .trade-expanded .trade-field-label { color: var(--ink-faint); min-width: 90px; }
    .trade-card.is-expanded .trade-expanded { display: block; }
```

- [ ] **Step 3: Manual verification**

1. From the repo root, run: `npm run agent` (skip if it is already running).
2. Open `http://localhost:3737` and click the `Trades` tab.
3. Hard-refresh (`Ctrl+Shift+R`).
4. Confirm the existing "No trades yet" placeholder still renders unchanged. No console errors. No visible layout shift. (No new markup exists yet, so the new CSS targets nothing — this step just guards against typos in the stylesheet.)

- [ ] **Step 4: Commit**

```
git add agent/public/index.html
git commit -m "ui(trades): add CSS for agent filter row and inline card expansion"
```

---

### Task 2: Insert filter row markup into the Trades panel

**Files:**
- Modify: `agent/public/index.html:1048-1054` (the `#panel-trades` block)

- [ ] **Step 1: Locate the current `#panel-trades` block**

Confirm lines 1048–1054 currently read:
```html
    <div class="panel" id="panel-trades">
      <div class="settings-content">
        <h2>Live Trades</h2>
        <p class="subtitle">Real-time order activity from the agent</p>
        <div id="trades-feed"><div class="no-data">No trades yet. Start the agent to see live activity.</div></div>
      </div>
    </div>
```

- [ ] **Step 2: Insert the filter row immediately before `#trades-feed`**

Replace the block above with:

```html
    <div class="panel" id="panel-trades">
      <div class="settings-content">
        <h2>Live Trades</h2>
        <p class="subtitle">Real-time order activity from the agent</p>
        <div class="trades-filter-row">
          <label for="trades-agent-filter">Filter by agent</label>
          <select id="trades-agent-filter" onchange="applyTradesFilter()">
            <option value="__all__">All Agents</option>
          </select>
          <span class="trades-filter-count" id="trades-filter-count"></span>
        </div>
        <div id="trades-feed"><div class="no-data">No trades yet. Start the agent to see live activity.</div></div>
      </div>
    </div>
```

- [ ] **Step 3: Manual verification**

1. Hard-refresh the dashboard and switch to the Trades tab.
2. Confirm a "Filter by agent" label with a `<select>` showing only "All Agents" appears above the placeholder.
3. Open DevTools Console. Changing the select fires `applyTradesFilter is not defined` — that is **expected** at this point and proves the wiring reached the handler. The error is fine; it'll be resolved in Task 5.
4. No layout or theme regressions in other tabs.

- [ ] **Step 4: Commit**

```
git add agent/public/index.html
git commit -m "ui(trades): add agent filter row above live trade feed"
```

---

### Task 3: Rewrite `addTradeCard` to emit the new card structure with agent badge and hidden expanded block

**Files:**
- Modify: `agent/public/index.html:2225-2235` (the `addTradeCard` function)

- [ ] **Step 1: Locate the current implementation**

Confirm lines 2225–2235 currently read:
```js
function addTradeCard(trade) {
  const feed = document.getElementById('trades-feed');
  if(feed.querySelector('.no-data')) feed.innerHTML = '';
  const card = document.createElement('div');
  card.className = 'trade-card';
  const t = trade.timestamp ? new Date(trade.timestamp).toLocaleTimeString('en-US',{hour12:false}) : '--';
  const qtyLabel = trade.quantity != null ? (String(trade.quantity).startsWith('$') ? ' | ' + trade.quantity : ' | Qty: ' + trade.quantity) : '';
  card.innerHTML = '<div class="trade-header"><span class="trade-symbol">' + esc(trade.symbol||'??') + '</span><span class="trade-side ' + esc(trade.side||'') + '">' + esc((trade.side||'--').toUpperCase()) + '</span></div><div class="trade-details">' + esc(trade.tool||'') + qtyLabel + (trade.price?' | $'+trade.price:'') + '</div><div class="trade-time">' + t + '</div>';
  feed.insertBefore(card, feed.firstChild);
  while(feed.children.length > 50) feed.removeChild(feed.lastChild);
}
```

- [ ] **Step 2: Replace `addTradeCard` with the new implementation**

```js
function addTradeCard(trade) {
  const feed = document.getElementById('trades-feed');
  if(feed.querySelector('.no-data')) feed.innerHTML = '';

  // Resolve sandbox -> agent
  const sandboxId = trade.sandboxId || '';
  const sandbox = sandboxId ? (config.sandboxes || {})[sandboxId] : null;
  const activeAgentId = sandbox?.agent?.activeAgentId || '';
  const agent = activeAgentId ? (config.agents || []).find(a => a.id === activeAgentId) : null;
  const agentId = agent ? agent.id : '__none__';
  const agentName = agent ? agent.name : (sandbox ? 'Unassigned' : '—');

  const card = document.createElement('div');
  card.className = 'trade-card';
  card.dataset.agentId = agentId;
  card.dataset.sandboxId = sandboxId;
  card.onclick = (ev) => toggleTradeCard(card, ev);

  const t = trade.timestamp ? new Date(trade.timestamp).toLocaleTimeString('en-US',{hour12:false}) : '--';
  const qtyLabel = trade.quantity != null ? (String(trade.quantity).startsWith('$') ? ' | ' + trade.quantity : ' | Qty: ' + trade.quantity) : '';

  const headerHtml =
    '<div class="trade-header">' +
      '<span style="display:flex;align-items:center">' +
        '<span class="trade-symbol">' + esc(trade.symbol||'??') + '</span>' +
        '<span class="trade-agent-badge">' + esc(agentName) + '</span>' +
      '</span>' +
      '<span style="display:flex;align-items:center">' +
        '<span class="trade-side ' + esc(trade.side||'') + '">' + esc((trade.side||'--').toUpperCase()) + '</span>' +
        '<span class="trade-chevron">&#9662;</span>' +
      '</span>' +
    '</div>';

  const detailsHtml = '<div class="trade-details">' + esc(trade.tool||'') + qtyLabel + (trade.price?' | $'+esc(String(trade.price)):'') + '</div>';
  const timeHtml = '<div class="trade-time">' + esc(t) + '</div>';

  const fields = [
    ['Sandbox',   sandboxId || '—'],
    ['Agent',     agentName],
    ['Type',      trade.type || '—'],
    ['Tool',      trade.tool || '—'],
    ['Side',      trade.side || '—'],
    ['Quantity',  trade.quantity != null ? String(trade.quantity) : '—'],
    ['Price',     trade.price != null ? '$' + trade.price : '—'],
    ['Timestamp', trade.timestamp || '—'],
  ];
  const expandedHtml = '<div class="trade-expanded">' +
    fields.map(([label, value]) =>
      '<div class="trade-field"><span class="trade-field-label">' + esc(label) + ':</span><span>' + esc(String(value)) + '</span></div>'
    ).join('') +
  '</div>';

  card.innerHTML = headerHtml + detailsHtml + timeHtml + expandedHtml;

  feed.insertBefore(card, feed.firstChild);
  while(feed.children.length > 50) feed.removeChild(feed.lastChild);
}
```

Note: `toggleTradeCard` is referenced here and defined in Task 4. Until Task 4 lands, clicking a card will throw a ReferenceError — verification for this task uses console-synthesised trades only, and does not click cards.

- [ ] **Step 3: Manual verification**

1. Hard-refresh the dashboard, open the Trades tab.
2. In DevTools Console, run the synthetic-trade snippet from the "Verification model" section above.
3. Confirm the card renders with: symbol on the left, an `Unassigned` or agent-name badge next to it, side label and a `▾` chevron on the right; the one-line details row and the time row are visible below. The expanded detail block is **not** visible (CSS hides it without `is-expanded`).
4. In Elements panel, expand the card markup and verify the hidden `.trade-expanded` block contains all eight `.trade-field` rows.
5. Do **not** click the card yet (will throw — Task 4 fixes it).

- [ ] **Step 4: Commit**

```
git add agent/public/index.html
git commit -m "ui(trades): render agent badge, chevron, and hidden detail block on trade cards"
```

---

### Task 4: Add `toggleTradeCard` for inline expand/collapse

**Files:**
- Modify: `agent/public/index.html` — add immediately after the closing `}` of `addTradeCard` (the line after where `while(feed.children.length > 50)` previously was; you should be just before the `// ── Render config panels ───` comment).

- [ ] **Step 1: Insert the toggle function**

Add this function directly after `addTradeCard`:

```js
function toggleTradeCard(card, ev) {
  // Ignore clicks that are part of a text selection drag.
  const sel = window.getSelection && window.getSelection();
  if (sel && sel.toString && sel.toString().length > 0) return;
  card.classList.toggle('is-expanded');
}
```

- [ ] **Step 2: Manual verification**

1. Hard-refresh the dashboard, open the Trades tab.
2. Synthesise a trade via the console snippet.
3. Click the card → it expands inline, chevron rotates 180°, all eight field rows are visible. No page scroll.
4. Click again → it collapses, chevron returns.
5. Synthesise a second trade. Expand both → confirm multiple cards stay open simultaneously.
6. Inside an expanded card, drag-select some text in a field value. Release the mouse without clicking elsewhere. The card must **not** collapse. (If it does, the selection check is broken.)
7. No console errors.

- [ ] **Step 3: Commit**

```
git add agent/public/index.html
git commit -m "ui(trades): toggle trade card expansion on click, preserve text selection"
```

---

### Task 5: Add filter state, helpers, and `applyTradesFilter`

**Files:**
- Modify: `agent/public/index.html` — declare `_tradesAgentFilter` next to other module-level UI state, and add helpers after `toggleTradeCard`.

- [ ] **Step 1: Find the existing module-level state declarations**

Search for `_sbxHbExpanded` (declared near the top of the `<script>` block). Place `let _tradesAgentFilter = '__all__';` on the line immediately below it. This keeps related session-scoped UI state together.

- [ ] **Step 2: Add filter helpers after `toggleTradeCard`**

Insert this block directly after `toggleTradeCard`:

```js
function ensureAgentInFilter(agentId, agentName) {
  const sel = document.getElementById('trades-agent-filter');
  if (!sel) return;
  if (sel.querySelector('option[value="' + CSS.escape(agentId) + '"]')) return;
  const opt = document.createElement('option');
  opt.value = agentId;
  opt.textContent = agentName;
  sel.appendChild(opt);
}

function updateTradesFilterCount(shown, total) {
  const el = document.getElementById('trades-filter-count');
  if (!el) return;
  el.textContent = total ? (shown + ' shown / ' + total + ' total') : '';
}

function refreshTradesFilterCount() {
  const cards = document.querySelectorAll('#trades-feed .trade-card');
  let shown = 0;
  cards.forEach(c => { if (c.style.display !== 'none') shown++; });
  updateTradesFilterCount(shown, cards.length);
}

function applyTradesFilter() {
  const sel = document.getElementById('trades-agent-filter');
  _tradesAgentFilter = sel ? sel.value : '__all__';
  const cards = document.querySelectorAll('#trades-feed .trade-card');
  let shown = 0;
  cards.forEach(c => {
    const match = _tradesAgentFilter === '__all__' || c.dataset.agentId === _tradesAgentFilter;
    c.style.display = match ? '' : 'none';
    if (match) shown++;
  });
  updateTradesFilterCount(shown, cards.length);
}

function applyTradesFilterToCard(card) {
  if (_tradesAgentFilter !== '__all__' && card.dataset.agentId !== _tradesAgentFilter) {
    card.style.display = 'none';
  }
  refreshTradesFilterCount();
}
```

- [ ] **Step 3: Manual verification (helpers in isolation)**

1. Hard-refresh the dashboard, open the Trades tab.
2. In the Console, confirm `_tradesAgentFilter` is accessible: type `_tradesAgentFilter` → should print `'__all__'`.
3. Type `applyTradesFilter` → should print the function (not undefined).
4. Synthesise two trades via the console snippet using two different `sandboxId` values that map to two different agents in your `config`. The dropdown still only contains "All Agents" because `ensureAgentInFilter` is not yet wired into `addTradeCard` (that lands in Task 6). This is expected.
5. No console errors.

- [ ] **Step 4: Commit**

```
git add agent/public/index.html
git commit -m "ui(trades): add filter state and helpers (applyTradesFilter, ensureAgentInFilter, count)"
```

---

### Task 6: Wire filter into `addTradeCard` insertion path

**Files:**
- Modify: `agent/public/index.html` — `addTradeCard` (last few lines, just before the closing `}`)

- [ ] **Step 1: Update the tail of `addTradeCard`**

Replace the current tail of `addTradeCard`:

```js
  feed.insertBefore(card, feed.firstChild);
  while(feed.children.length > 50) feed.removeChild(feed.lastChild);
}
```

with:

```js
  feed.insertBefore(card, feed.firstChild);
  while(feed.children.length > 50) feed.removeChild(feed.lastChild);

  ensureAgentInFilter(agentId, agentName);
  applyTradesFilterToCard(card);
}
```

- [ ] **Step 2: Manual verification (end-to-end filter)**

1. Hard-refresh the dashboard, open the Trades tab. The count span is empty (no cards).
2. Synthesise a trade for `sandboxId = Object.keys(config.sandboxes)[0]` (mapped to e.g. "Prophet"). The filter dropdown gains a "Prophet" option, and the count reads `1 shown / 1 total`.
3. Synthesise another trade for a different sandbox (different agent, e.g. "Penny"). The dropdown gains a "Penny" option, count reads `2 shown / 2 total`.
4. Synthesise a trade with `sandboxId: null` → the dropdown gains an "Unassigned" option, count reads `3 shown / 3 total`.
5. Select "Penny" in the dropdown → only the Penny card stays visible, count reads `1 shown / 3 total`.
6. Switch back to "All Agents" → all three reappear, count reads `3 shown / 3 total`.
7. With "Penny" selected, synthesise another "Prophet" trade. The new card is added but immediately hidden; count updates to `1 shown / 4 total`.
8. Switch back to "All Agents" → the hidden card is now visible, count reads `4 shown / 4 total`.
9. Click any card → it still expands inline (regression check on Task 4).
10. No console errors.

- [ ] **Step 3: Commit**

```
git add agent/public/index.html
git commit -m "ui(trades): hide non-matching trades on insertion and keep filter count in sync"
```

---

### Task 7: Full spec test plan (end-to-end with real agents)

This task runs the spec's testing plan verbatim. It produces no new code; if any step fails, file a follow-up fix (most likely tiny CSS or copy adjustments) and re-run.

**Files:** none modified unless a regression is found.

- [ ] **Step 1: Start the agent server with at least two sandboxes assigned to different agents**

Run: `npm run agent` (from repo root). Use the dashboard to confirm two sandboxes exist with different `Assigned Agent` values. If no agents are running real trades, the synthetic-console method from prior tasks satisfies the same verification.

- [ ] **Step 2: Execute spec testing plan, top to bottom**

From `docs/superpowers/specs/2026-05-11-trades-tab-agent-filter-and-inline-expansion-design.md`, the "Testing plan" section, run all 12 steps and check off each:

  1. [ ] Open Trades tab — filter row visible, only "All Agents" option, no cards.
  2. [ ] Trigger trades from at least two sandboxes assigned to different agents (or synthesise).
  3. [ ] Confirm each card shows the agent badge and the filter dropdown gains an option per agent.
  4. [ ] Select an agent in the dropdown → only that agent's cards remain; count updates.
  5. [ ] Switch back to "All Agents" → all cards reappear.
  6. [ ] Click a card → expands inline (no page scroll), chevron rotates, all 8 fields readable.
  7. [ ] Click again → collapses.
  8. [ ] Open two cards simultaneously → both stay open.
  9. [ ] Select text inside an expanded card → card does not collapse.
  10. [ ] Trigger (or synthesise) a trade from a sandbox with no assigned agent → labels "Unassigned" and filter gains an "Unassigned" option.
  11. [ ] Synthesise > 50 trades → oldest card is trimmed, count reconciles (run a `for` loop in console calling `addTradeCard` 55 times).
  12. [ ] No console errors during any of the above.

- [ ] **Step 3: Visual sanity in other themes**

Cycle through any theme switcher the dashboard exposes and confirm the agent badge, chevron, and expanded block remain legible in each theme (the rules use only existing CSS variables, so this should pass — but verify rather than assume).

- [ ] **Step 4: Final commit (only if any tweaks were needed; otherwise skip)**

```
git add agent/public/index.html
git commit -m "ui(trades): polish from end-to-end verification"
```

---

## Done definition

- All 12 spec test-plan items pass with no console errors.
- The Trades tab filter dropdown defaults to "All Agents" and populates lazily as agents are observed.
- Cards expand in place, chevron animates, multiple may be open simultaneously, and text selection inside an expanded card does not collapse it.
- The 50-card cap continues to trim oldest cards and the filter count stays accurate.
- No backend, SSE, or schema changes were introduced.
