# Trade History Persistence + Historic View — Design

**Date:** 2026-05-13
**Status:** Approved, ready for implementation plan
**Owner:** Agent harness / dashboard

## Motivation

Today the harness emits a `'trade'` event whenever an order tool fires, and
`AgentState.recentTrades` buffers the last 50 in memory. The dashboard renders
those into `#trades-feed` and caps the DOM at 50 cards. Two consequences:

1. Restarting the agent server (or just refreshing the browser tab) wipes the
   buffer. Trades from earlier in the trading day silently disappear from the
   UI even though Alpaca still has them.
2. There is no way to look at yesterday, last week, or last month from the
   dashboard. Performance review currently relies on `activity_logs/` JSON
   files and Alpaca's own UI.

The operator wants the live "trades today" feed unchanged for daily use, plus
a toggleable historic view so a date range can be inspected without leaving
the dashboard.

## Scope

In scope:

- File-backed append-only persistence of every harness-emitted trade event,
  partitioned per-account per-ET-trading-day as NDJSON.
- A new `GET /api/trades` endpoint that reads those files for a date range.
- A new "Show historic trades" UI toggle with date inputs, an Apply button,
  and a truncation indicator. The existing per-agent dropdown filter
  continues to work over whichever set is loaded.
- Page-load seeding of the live feed with today's persisted trades, so a
  refresh mid-day stops wiping the morning's cards from the DOM.

Out of scope (explicit non-goals):

- Alpaca-side reconciliation. Trades persisted are what the LLM tool emitted,
  not what actually filled. Same source of truth as the existing live feed.
  An "actual fills" history is a separate feature.
- Per-trade P&L computation, CSV export, or any aggregate analytics view.
- Retention enforcement / auto-prune. Files are forever; operator deletes
  manually if disk pressure ever shows up.
- Mass deletion or edit UI for stored trades. The files are append-only and
  treated as a log.
- Restructuring the harness state machine. The only harness-side change is a
  one-line fix to `AgentState.addTrade` so the emitted payload carries the
  same `timestamp` that already lands in `recentTrades` — see Architecture
  for why this is in-scope and not separable.

## Decisions Locked During Brainstorming

Recorded here so future readers do not re-litigate them:

1. **Source of truth is harness events**, not Alpaca activities. This matches
   what the live feed already shows, requires zero Go-side changes, and is
   the cheapest path. Divergence from actual fills (rejects, partials) is
   accepted for v1.
2. **File rotation is by ET trading day**, not UTC. A trade fired at 7 PM ET
   stays in the *today* file until midnight ET, matching operator mental model.
3. **Date range picker** is the historic-mode interaction, not single-date or
   all-time scroll. Bounded by a 90-day max range to keep the response and
   DOM tractable.
4. **Live feed freezes when historic view is active.** Toggling back to live
   resumes SSE-driven appends. Showing both simultaneously is confusing when
   the user is inspecting an old date.
5. **Forever retention.** Files are tiny (each line ~500 bytes; a heavy day
   is well under 100 KB); auto-prune is a future feature if disk usage ever
   matters.

## Architecture

### Storage layout

```
data/sandboxes/<accountId>/trades/<YYYY-MM-DD>.jsonl
```

- One file per ET trading day per account, sibling to the existing
  `activity_logs/` directory under the same account folder.
- File name is the ET date of the trade timestamp, computed at write time.
- NDJSON format — one JSON object per line, append-only writes.
- Each line is the trade event payload (which carries `timestamp` after the
  one-line harness fix described below) plus `sandboxId` and a resolved
  `agentName` / `agentId` snapshot taken at write time (so historic view
  still labels correctly after the operator renames an agent).

### Writer module

New file: `agent/trades-store.js`. Pure ESM, no dependencies beyond `node:fs`
and `node:path`. Two exported functions:

```
async function appendTrade(projectRoot, accountId, trade)
async function readTrades(projectRoot, { from, to, sandboxId? })
```

`appendTrade` performs an `fs.appendFile` with `{ flag: 'a' }` so concurrent
writers from multiple sandboxes don't race. ET-date computation reuses the
same `America/New_York` `Intl.DateTimeFormat` pattern already used by the
phase-detection code in `harness.js`.

`readTrades` enumerates the per-day files for every account in the date
range, parses each line, applies the optional `sandboxId` filter, sorts by
timestamp descending, and truncates at `MAX_RETURNED = 2000`.

### Harness one-line fix

`AgentState.addTrade` (`agent/harness.js:176`) currently does:

```
this.recentTrades.unshift({ ...trade, timestamp: new Date().toISOString() });
if (this.recentTrades.length > 50) this.recentTrades.pop();
this.emit('trade', trade);   // <-- emits without the timestamp
```

The emit goes out *without* the timestamp that was just added to the ring,
so today's live cards in the dashboard render `--` for time. This becomes
load-bearing once we mix seeded "today" cards (which have timestamps from
disk) with live SSE cards (which do not), so the fix lands as part of this
work:

```
const stamped = { ...trade, timestamp: new Date().toISOString() };
this.recentTrades.unshift(stamped);
if (this.recentTrades.length > 50) this.recentTrades.pop();
this.emit('trade', stamped);
```

### Server wiring

In `agent/server.js`, the existing per-runtime listener block that already
subscribes to `'trade'` for Slack notifications gains one more line:

```
targetHarness.state.on('trade', async (trade) => {
  await appendTrade(PROJECT_ROOT, account.id, { ...trade, sandboxId });
  // existing Slack code unchanged
});
```

Errors from `appendTrade` are logged but never thrown — persistence is
soft-fail; a disk hiccup must not break live trading.

New REST endpoint: `GET /api/trades?from=YYYY-MM-DD&to=YYYY-MM-DD&sandboxId=`.
Both date params default to today (ET). Validation:

- 400 if `from > to`.
- 400 if range > 90 days.
- 400 on malformed dates.

Response shape:

```
{
  "from": "2026-05-13",
  "to": "2026-05-13",
  "count": 42,
  "truncated": false,
  "trades": [ { ...trade, sandboxId, agentName, agentId }, ... ]
}
```

### UI changes (agent/public/index.html)

Two surgical edits in the existing trade-feed block:

1. **Page-load seed.** On dashboard init, before the SSE connection is
   attached, fetch `/api/trades` with the today/today defaults and render
   the result through the existing `addTradeCard` path. The 50-cap on DOM
   nodes remains active.

2. **Historic toggle.** New controls beside the existing
   `#trades-agent-filter`:
   - A `Show historic trades` checkbox.
   - Two `<input type="date">` fields (`from-date`, `to-date`), both
     defaulting to today.
   - An `Apply` button.
   - A small text element that shows `truncated: showing 2000 of N` when
     the API flags truncation.

A module-scope `_historicMode` flag gates the existing SSE `'trade'` handler.
The handler stays subscribed; when the flag is true it returns immediately
instead of appending. The DOM cap inside `addTradeCard` becomes a function of
the flag (`50` in live mode, `2000` in historic mode); the hard-coded `50` in
the current code becomes a single constant lookup.

Toggle-on behavior:
- Set `_historicMode = true` (live appends are now ignored).
- Clear the feed DOM.
- Fetch the endpoint with the chosen range.
- Render results through `addTradeCard`; cap of 2000 applies.

Toggle-off behavior:
- Set `_historicMode = false`.
- Clear the feed DOM.
- Refetch today and render the seed; cap of 50 applies.
- Live SSE cards resume appending on top.

The existing `applyTradesFilter` (per-agent dropdown) continues to operate
on whichever set of cards is currently in the DOM.

### Seed / live merge correctness

Two race / ordering concerns the implementation must handle:

1. **Duplicate on seed.** Page load fires the seed fetch and attaches SSE in
   parallel. A live trade that arrives while the seed is in flight will be
   rendered as a live card, and may also appear in the seed response
   (because `appendTrade` ran before `readTrades`). Solution: maintain a
   `Set<tradeKey>` keyed by `sandboxId|timestamp|tool|symbol`; `addTradeCard`
   short-circuits if the key is already present. Same key is used during
   the historic-toggle clear-and-reload to reset the set.

2. **Render order during bulk render.** `readTrades` returns
   timestamp-descending so the API consumer can paginate cheaply later.
   `addTradeCard` always inserts at `firstChild`, which would reverse the
   intended order on a bulk render. The seed and historic-load paths must
   iterate the array in *ascending* order before calling `addTradeCard`, so
   the newest trade ends up at the top of the DOM.

### Agent resolution

`agentId` / `agentName` are resolved by the `server.js` listener *before*
calling `appendTrade`, using `getResolvedAgentForSandbox(sandboxId)`. The
store module stays pure (filesystem + JSON only) and does not import from
`config-store`. This also means a trade fired while the operator is in the
middle of editing agent fields is labeled with whatever was resolved at
emit time — consistent with the existing live UI behavior.

### Empty state

Historic view with zero results: feed shows a single `.no-data` element
reading `No trades in this range.` — same pattern as the existing
`No trades yet. Start the agent to see live activity.` placeholder.

## Data shape

Trade payload as it lands on disk:

```json
{
  "type": "order",
  "tool": "place_buy_order",
  "symbol": "AAPL",
  "side": "buy",
  "quantity": 100,
  "price": null,
  "timestamp": "2026-05-13T14:32:17.881Z",
  "sandboxId": "sbx_abcd1234",
  "agentId": "default",
  "agentName": "Prophet"
}
```

`agentName` is captured at write time so renaming an agent later does not
relabel the historical record. `agentId` is the resolved active agent id
for that sandbox at the moment of the trade.

## Error handling

- `appendTrade` errors: logged via the existing `agent_log` emitter at
  `level: 'warning'`, never thrown. The trade still appears in the live UI
  and in the in-memory `recentTrades` ring — we just lose the persistent
  copy of that one event.
- `readTrades` errors on a single corrupt line: log and skip that line,
  continue parsing the rest of the file.
- `readTrades` errors on a missing day file: treated as "no trades that
  day" (normal case for weekends), not an error.
- Endpoint validation errors return 400 with a `{ error: "..." }` body.

## Testing

Node `node:test` suite at `agent/trades-store.test.mjs` covering:

- `appendTrade` writes to the expected path and survives concurrent calls.
- ET date rotation: a write at 23:59 UTC on the day before the ET rollover
  lands in yesterday's file; a write just after midnight ET lands in
  today's file.
- `readTrades` returns trades sorted newest-first, filters by sandboxId,
  applies the date range, and honors the 2000-cap with `truncated: true`.
- Duplicate-key short-circuit in the UI render path (covered by a small
  unit-style test against the dedup helper extracted from `addTradeCard`).
- A corrupt line in the middle of an NDJSON file is skipped without
  aborting the read.
- A missing day file produces an empty array, not a throw.

Endpoint tests are not added — the endpoint is a thin wrapper over
`readTrades` and the validation paths are checked in the route handler
inline. No UI tests; the historic toggle is small and observable.

## Migration / rollout

Pure additive. No existing files moved. No DB schema. Existing in-memory
`recentTrades` ring is unchanged. Operators see no behavior change until
they tick the "Show historic trades" checkbox. The seed-on-load path is
visible immediately but matches expectation (trades that already happened
today stay on the page across a refresh).

The first time the server starts after this change, the `trades/`
directories are auto-created lazily by `appendTrade` on the first event.
No backfill of historic trades from `activity_logs/` is performed.
