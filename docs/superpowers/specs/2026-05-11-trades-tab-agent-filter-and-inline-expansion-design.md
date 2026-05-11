# Trades Tab — Agent Filter & Inline Card Expansion

**Status**: Approved
**Date**: 2026-05-11
**Scope**: `agent/public/index.html` only. No backend changes.

## Problem

The Trades tab (`#panel-trades`) shows a single chronological feed of every trade across every sandbox. With multiple agents running side-by-side (Prophet, Penny, Harvest, Trend), it is hard to track what any one agent is doing. The user also recalls a prior UX where clicking a trade revealed details by auto-scrolling the page to a block below the feed; that mechanism is gone and was undesirable anyway. The user wants the details to expand inline within the card itself.

## Goals

1. Let the user filter the live trades feed by agent, while keeping "All Agents" as the default.
2. Show which agent produced each trade directly on the card.
3. Expand a trade card in-place to reveal its full field set, without page scrolling.

## Non-Goals

- Fetching rationale text from `decisive_actions` or `activity_logs` on expand. (Possible follow-up, not in this spec.)
- Persisted/server-side trade history beyond the existing in-memory 50-card cap.
- Grouping trades by agent into collapsible sections.
- Any backend or SSE schema changes.

## Background — Current State

Files of interest:

- `agent/public/index.html:1048` — `#panel-trades` markup. The panel contains only `<h2>`, subtitle, and `#trades-feed`.
- `agent/public/index.html:2225` — `addTradeCard(trade)`. Builds a small card with `symbol`, `side`, `tool`, `quantity`, `price`, `time`. Caps the feed at 50 cards.
- `agent/public/index.html:1771` — SSE `trade` listener that invokes `addTradeCard`.
- `agent/public/index.html:1838` — Replays `state.recentTrades` into `addTradeCard` on `state` updates.
- `agent/server.js:328` — `bindHarnessEvents` stamps every broadcast event (including `trade`) with `sandboxId`, so each trade reaching the client already knows which sandbox it came from.
- `agent/harness.js:1067` — Trade objects originate here with shape `{ type, tool, symbol, side, quantity, price }`; `addTrade` adds `timestamp`. After SSE wrapping the client sees: `{ type, tool, symbol, side, quantity, price, timestamp, sandboxId }`.
- `config.sandboxes[sandboxId].agent.activeAgentId` → `config.agents.find(a => a.id === activeAgentId).name` is the established sandbox→agent-name lookup.

No expansion logic for trade cards exists today.

## Design

### 1. Markup additions (`#panel-trades`)

Insert a filter row above `#trades-feed`:

```html
<div class="trades-filter-row">
  <label for="trades-agent-filter">Filter by agent</label>
  <select id="trades-agent-filter" onchange="applyTradesFilter()">
    <option value="__all__">All Agents</option>
  </select>
  <span class="trades-filter-count" id="trades-filter-count"></span>
</div>
```

- `trades-filter-count` shows `"N shown / M total"` and is updated on every filter change and every new card.
- The `<select>` is repopulated whenever a trade arrives whose agent is not already represented in the dropdown.

### 2. `addTradeCard(trade)` rewrite

New responsibilities:

1. Resolve agent metadata:
   - `agentId = config.sandboxes[trade.sandboxId]?.agent?.activeAgentId || '__none__'`
   - `agentName = config.agents.find(a => a.id === agentId)?.name`
     - If no agent assigned: `'Unassigned'`
     - If `trade.sandboxId` is unknown (config not yet loaded): `'—'`
2. Build a card with three always-visible rows plus one hidden detail block:

```
.trade-card[data-agent-id="<id>"][data-sandbox-id="<sid>"]
  .trade-header
    .trade-symbol                AAPL
    .trade-agent-badge           Penny
    .trade-side buy              BUY
    .trade-chevron               ▾    (rotates 180° when expanded)
  .trade-details                  place_buy_order | Qty: 100 | $187.20
  .trade-time                     14:23:11
  .trade-expanded                 (hidden unless .is-expanded)
    Sandbox: sbx_abc
    Agent: Penny
    Type: order
    Tool: prophet_v1.place_buy_order
    Side: buy
    Quantity: 100
    Price: $187.20
    Timestamp: 2026-05-11T14:23:11.421Z
```

3. Wire `onclick` on the whole card to a `toggleTradeCard(cardEl, ev)` handler that:
   - Returns early if the user has a non-empty text selection (so highlighting text inside the card doesn't collapse/expand).
   - Toggles the `.is-expanded` class on the card.
4. After inserting the card, call `ensureAgentInFilter(agentId, agentName)` then `applyTradesFilterToCard(card)` so that newly arrived trades that don't match the active filter are hidden on insertion.
5. Existing 50-card cap remains; when trimming, also account for filter count updates.

### 3. Filter logic

```
let _tradesAgentFilter = '__all__';

function applyTradesFilter() {
  _tradesAgentFilter = document.getElementById('trades-agent-filter').value;
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
  // Recompute count from DOM
  refreshTradesFilterCount();
}

function ensureAgentInFilter(agentId, agentName) {
  const sel = document.getElementById('trades-agent-filter');
  if (!sel || sel.querySelector(`option[value="${CSS.escape(agentId)}"]`)) return;
  const opt = document.createElement('option');
  opt.value = agentId;
  opt.textContent = agentName;
  sel.appendChild(opt);
}
```

The filter state is module-level and session-scoped. It does not persist across reloads. This is simpler and matches the existing pattern in this file (e.g., `_sbxHbExpanded`, `_sbxActiveProfile`).

### 4. CSS additions

Append to the existing `/* ── Trade Feed ── */` block (`agent/public/index.html:884`):

```css
.trades-filter-row { display: flex; align-items: center; gap: 10px; margin-bottom: 12px; font-size: 12px; color: var(--ink-muted); }
.trades-filter-row select { font-size: 12px; }
.trades-filter-count { font-size: 11px; color: var(--ink-faint); }

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

### 5. Edge cases & invariants

- **Trade arrives before config loads**: agent renders as `'—'`, `data-agent-id='__none__'`. Acceptable degradation; user can refresh.
- **Sandbox has no agent assigned**: label `Unassigned`, `data-agent-id='__none__'`. Filterable as a distinct group.
- **Filter active when a non-matching trade arrives**: card is hidden on insertion; the total count still increments.
- **Card pruned at 50-cap while filter active**: count automatically reflects the new DOM state via `refreshTradesFilterCount`.
- **Text selection inside a card**: `toggleTradeCard` bails when `window.getSelection().toString()` is non-empty, so selecting fields in the expanded block doesn't toggle.
- **Multiple cards open simultaneously**: explicitly allowed per UX choice. No singleton tracking needed.
- **`esc()` already used on all user-visible strings**; reuse it for agent names and sandbox ids.

### 6. What is NOT changing

- SSE event names or payload shape.
- `addTradeCard` call sites (`agent/public/index.html:1773` and `:1838`).
- Backend trade emission (`agent/harness.js`).
- The 50-card in-memory cap.

## Files touched

- `agent/public/index.html` — CSS block additions, `#panel-trades` markup additions, `addTradeCard` rewrite, six new functions (`toggleTradeCard`, `applyTradesFilter`, `applyTradesFilterToCard`, `ensureAgentInFilter`, `refreshTradesFilterCount`, `updateTradesFilterCount`), and a module-level `_tradesAgentFilter` variable. `refreshTradesFilterCount` is a thin helper that counts visible vs. total cards in `#trades-feed` and calls `updateTradesFilterCount` to write the result into `#trades-filter-count`.

## Testing plan

Manual, since this is a single-file UI change with no unit-testable seams:

1. Start the agent server, open the dashboard, switch to the Trades tab.
2. Verify the filter row appears with only "All Agents" before any trades have arrived.
3. Trigger trades from at least two sandboxes assigned to different agents.
4. Confirm each card shows the agent badge and the filter dropdown gains an option per agent.
5. Select an agent in the dropdown → confirm only that agent's cards remain visible and the count updates.
6. Switch back to "All Agents" → all cards reappear.
7. Click a card → it expands inline (no page scroll), chevron rotates, fields are readable.
8. Click again → it collapses.
9. Open two cards simultaneously → both stay open.
10. Select text inside an expanded card → card does not collapse.
11. Trigger a trade from a sandbox with no assigned agent → it labels as "Unassigned" and the filter gains an "Unassigned" option.
12. Trigger > 50 trades to confirm the cap still trims the oldest card and the count reconciles.

## Out of scope / follow-ups

- On-expand fetch of decisive-action rationale per trade (would require a new `/api/trades/:id/rationale` endpoint and stable trade IDs).
- Persistent trade history beyond the in-memory cap.
- Agent-color theming on the badge.
- Grouped/sectioned view by agent.
