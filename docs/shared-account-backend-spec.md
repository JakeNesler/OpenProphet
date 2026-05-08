# Shared-Account Backend Spec

**Status:** Draft for review
**Owner:** TBD
**Updated:** 2026-05-07

## Purpose

Today each agent runs against its own paper account. Going live (and the recommended shared-paper integration phase) requires four agents — V2, HARVEST, PENNY, TREND — to share a single broker account safely. This spec covers the backend changes needed to make that work: order tagging, segment-scoped P&L, reconciliation, segment-scoped circuit breakers, and a daily reconciliation script.

The relevant agent-level rule changes are already done in `TRADING_RULES_V2.md`, `TRADING_RULES_HARVEST.md`, `TRADING_RULES_PENNY.md`, and `TRADING_RULES_TREND.md`. Each agent's segment cap (40/12/30/18) sums to 100%. This spec covers the system-level work that makes those caps enforceable when all four agents share an account.

## Existing state

The data model already has partial support for strategy tagging:

| Model | Strategy field | Notes |
|---|---|---|
| `DBOrder` | `StrategyName` | Tagged at order placement |
| `DBManagedPosition` | `Strategy` | Tagged on managed-position entry |
| `DBTrade` | `StrategyName` | Tagged when trade closes |
| `DBSignal` | `StrategyName` | Tagged when signal generated |
| `DBPosition` | **none** | Snapshot table, broker-truth only |

The gap is `DBPosition`: it represents broker-side position state and has no strategy attribution because it is reconciled directly from broker `get_positions` calls. Two agents holding the same symbol collapse into one row. This is the primary structural defect.

The other gap is the *flow* from order placement to fill to position: even though `DBOrder.StrategyName` is set on placement, there is no documented contract for how that tag survives the round-trip through the broker and is reattached to the resulting position. This spec pins that contract.

## Architecture

Three layers, each with one specific responsibility:

```
┌────────────────────────────────────────────────────────────┐
│  Agent layer (V2, HARVEST, PENNY, TREND)                   │
│  - Calls place_order with strategy: "trend"                │
│  - Reads positions filtered by strategy                    │
│  - Computes segment-scoped P&L for circuit breaker         │
└────────────────────────────────────────────────────────────┘
                              │
┌────────────────────────────────────────────────────────────┐
│  Order/position service layer                              │
│  - Encodes strategy tag into Alpaca client_order_id        │
│  - Persists tag mapping to DB                              │
│  - Exposes GET /api/positions?strategy=X                   │
│  - Computes segment P&L from DBTrade + DBManagedPosition   │
└────────────────────────────────────────────────────────────┘
                              │
┌────────────────────────────────────────────────────────────┐
│  Reconciler                                                 │
│  - Startup: tag-mapping consistency check                  │
│  - Daily: position truth-test, P&L truth-test              │
│  - Drift > threshold → log + alert                         │
└────────────────────────────────────────────────────────────┘
```

## Order tagging contract

### Tag propagation

```
Agent                Service              Broker (Alpaca)         DB
  │                    │                     │                      │
  │ place_order        │                     │                      │
  │ {strategy: "trend"}│                     │                      │
  │───────────────────>│                     │                      │
  │                    │ build client_order_id="trend:<uuid>"        │
  │                    │ POST /v2/orders     │                      │
  │                    │────────────────────>│                      │
  │                    │ <─── order_id ──────│                      │
  │                    │                     │                      │
  │                    │ INSERT DBOrder      │                      │
  │                    │ {strategy="trend",  │                      │
  │                    │  order_id, client_order_id}                 │
  │                    │────────────────────────────────────────────>│
  │                    │                     │                      │
  │                    │                     │ FILL event            │
  │                    │                     │─── webhook/poll ────>│
  │                    │                     │                      │
  │                    │ on fill: lookup strategy from DBOrder       │
  │                    │ INSERT DBManagedPosition {strategy="trend"} │
  │                    │ INSERT DBPosition    {strategy="trend"}     │
  │                    │ ...                                         │
```

### `client_order_id` format

```
{strategy}:{uuid}
```

Examples: `trend:8f3a1c-...`, `harvest:b2d4e5-...`, `penny:9c1f7-...`.

Alpaca's `client_order_id` is a free-form 128-character string preserved through all order events (fill, partial, cancel, replace) and returned in every response. This is the only reliable strategy-tag carrier across the broker boundary. The colon-prefix encoding is parseable both directions:

- Forward: agent → service builds the `client_order_id` from `strategy` and a generated UUID
- Reverse: service → DB extracts `strategy` from `client_order_id.split(':')[0]` if the DBOrder lookup misses (defensive fallback)

If a fill arrives for an order with no DBOrder record (e.g., the DB write failed but the broker accepted the order), the reverse-extraction from `client_order_id` is the recovery path. Without this, untagged fills become orphaned positions.

### Strategy whitelist

The service layer rejects orders with strategy values outside the registered set: `["v2", "harvest", "penny", "trend"]`. This prevents typos from creating untrackable positions and gives the universe a controlled vocabulary. A `models.StrategyName` enum or constant list is the source of truth.

## Schema changes

### `DBPosition`

Add a `Strategy` column. Change the unique index from `Symbol` to `(Symbol, Strategy)` so the same ticker can be held by multiple strategies (e.g., HARVEST has TLT condors while TREND is long TLT shares).

```go
type DBPosition struct {
    gorm.Model
    Symbol         string `gorm:"uniqueIndex:idx_symbol_strategy"`
    Strategy       string `gorm:"uniqueIndex:idx_symbol_strategy"`  // NEW
    Qty            float64
    AvgEntryPrice  float64
    // ... rest unchanged
}
```

Migration: existing rows backfill `Strategy = ""` (empty), which is treated as "untagged / pre-shared-account era." A subsequent migration assigns historical positions to their best-guess strategy by cross-joining with `DBOrder.StrategyName` on the `OrderID` field. Positions that cannot be attributed are flagged for operator review and not auto-tagged.

### `DBOrder`

Add a `ClientOrderID` column to make the reverse-extraction path explicit. Already populated from Alpaca; just persist it.

```go
type DBOrder struct {
    // ... existing fields
    ClientOrderID string `gorm:"uniqueIndex"`  // NEW
}
```

### `DBSegmentPnL` (new table)

Materialized daily P&L per strategy, written by an EOD job. Used by segment-scoped circuit breakers and the daily reconciliation script.

```go
type DBSegmentPnL struct {
    gorm.Model
    Strategy        string    `gorm:"index:idx_strategy_date"`
    Date            time.Time `gorm:"index:idx_strategy_date"`
    RealizedPnL     float64
    UnrealizedPnL   float64
    DeployedPercent float64
    PositionCount   int
    PortfolioValue  float64  // snapshot at EOD
}
```

This avoids recomputing per-strategy P&L on every heartbeat (which would be expensive for V2's frequent beats).

## API additions

### `GET /api/v1/positions?strategy=:strategy`

Returns broker-reconciled positions filtered by strategy. Response shape mirrors the existing `GET /api/v1/positions` but with the additional filter.

```json
{
  "strategy": "trend",
  "count": 2,
  "positions": [
    { "symbol": "TLT", "qty": 105, "avg_entry_price": 95.20, "unrealized_pl": 23.10, ... },
    { "symbol": "GLD", "qty": 41, "avg_entry_price": 215.40, "unrealized_pl": -12.40, ... }
  ]
}
```

If `strategy` is omitted, returns all positions (backward-compatible). If `strategy` is set to a value not in the whitelist, returns 400.

### `GET /api/v1/segment-pnl/:strategy?since=<date>`

Returns realized + unrealized P&L for the strategy over the given window. The agent's segment-scoped circuit breaker calls this once per heartbeat to decide whether the segment has tripped.

```json
{
  "strategy": "penny",
  "as_of": "2026-05-07T16:00:00-04:00",
  "session_realized_pnl": -350.00,
  "session_unrealized_pnl": -180.00,
  "session_total_pnl": -530.00,
  "portfolio_value_at_session_open": 110000.00,
  "session_pnl_percent": -0.0048,
  "circuit_breaker_threshold_percent": -0.05,
  "circuit_breaker_active": false
}
```

`session_total_pnl` is what the agent compares to its threshold. `circuit_breaker_active` is computed server-side as a convenience but the agent should still apply its own rule logic — the threshold lives in the rules file, not the service.

### `POST /api/v1/circuit-breaker/:strategy/trip` and `/reset`

Used by the agent to flag a tripped state for the rest of the session and by the operator (or a session-rollover job) to reset. Persists to DB so circuit-breaker state survives a harness restart.

```json
{ "strategy": "penny", "tripped_at": "...", "reason": "session_pnl below -5%" }
```

## Segment-scoped P&L computation

Realized P&L for a segment over a window:

```sql
SELECT SUM(pnl) FROM trades
WHERE strategy_name = ? AND exit_time >= ? AND exit_time <= ?
```

Unrealized P&L for a segment:

```sql
SELECT SUM(unrealized_pl) FROM positions
WHERE strategy = ?
```

Session-scoped P&L is the union: realized today + unrealized now. The "session start" timestamp is the most recent session boundary (typically 4:00 AM ET on the current trading day, or the previous trading day's close if the session has not yet started).

The EOD job that writes `DBSegmentPnL` runs at 4:30 PM ET (after market close, before TrendProphet's 5:00 PM heartbeat). Each agent's segment-scoped circuit-breaker check during the day reads from the live position/trade tables; the EOD snapshot is a historical record, not the live source of truth.

## Reconciliation

### Startup reconciliation (per agent)

When an agent starts (or restarts), its harness calls a reconciliation endpoint:

```
GET /api/v1/reconcile/:strategy
```

Response:

```json
{
  "strategy": "trend",
  "status": "ok" | "drift" | "fail",
  "broker_positions": [...],
  "ledger_positions": [...],
  "drift_details": [
    { "symbol": "TLT", "broker_qty": 105, "ledger_qty": 100, "delta": 5, "severity": "warn" }
  ]
}
```

Drift severity:
- `none` — broker and ledger agree exactly. Agent proceeds.
- `warn` — symbol counts match, quantities differ by ≤ 1 share. Agent proceeds with a logged warning. Causes: fractional-share rounding, late fill not yet ledgered.
- `error` — quantities differ by > 1 share or symbol set differs. Agent halts, logs `startup reconciliation failed`, awaits operator.

The `error` case is the hard stop specified in each agent's TRADING_RULES file. The `warn` case is new and necessary because race conditions between fill webhooks and ledger writes can produce small transient discrepancies.

### Daily reconciliation script

Runs at 6:00 PM ET, after all agents (including TrendProphet) have completed their EOD heartbeats. Cross-checks four invariants:

1. **Position truth-test:** for every strategy, ledger positions match broker positions (within fractional-share tolerance). Sum across all strategies matches the broker's total positions exactly.
2. **P&L truth-test:** sum of all `DBSegmentPnL.RealizedPnL` for the day matches broker-reported realized P&L. Any divergence > $1 is logged.
3. **Tag completeness:** every order placed during the day has a non-empty `StrategyName` and a parseable `ClientOrderID`. Untagged orders are an emergency.
4. **Capital cap conformance:** for every strategy, `DeployedPercent ≤ segment_cap`. Any breach is logged with the timestamp it occurred and the agent that placed the offending order.

The script is implemented as a Go command at `cmd/reconcile/main.go` and invoked by cron (or the orchestrator's after-hours scheduler). Output is a structured log entry plus a Markdown summary in `data/reconciliation/YYYY-MM-DD.md` for operator review.

Failures do not pause the agents automatically. They are logged with severity `critical` and the operator decides whether to halt. The reasoning: a reconciliation failure usually reveals an existing bug, not an actively dangerous state — pausing all agents on a stale data record would create more disruption than the bug itself.

## Race conditions and concurrency

Four agents calling `get_account` and `place_order` near-simultaneously is the primary new concurrency surface. Three specific failure modes:

**1. Cap-check TOCTOU.** Agent A reads `deployed_pct = 35`, decides to deploy 8% (would land at 43). Agent B reads `deployed_pct = 35` at the same time, also decides to deploy. Both submit. Total = 51%, exceeding A's 40% cap.

Mitigation: cap checks are server-side, not client-side. The `place_order` endpoint computes `current_deployed_pct + this_order_pct` against the strategy cap *inside a transaction* and rejects with 409 Conflict if it would breach. Agents must handle 409 as "skip this entry, log, continue."

**2. Ledger-write race.** Order fills from broker arrive faster than the service can process them; two fills for the same position update the same row.

Mitigation: position updates use optimistic locking via the `gorm.Model.UpdatedAt` column. Conflicting writes retry; persistent conflicts (>3 retries) escalate to operator.

**3. Reconciliation race.** Daily reconciliation runs while one agent is mid-heartbeat and modifying state.

Mitigation: reconciliation reads from a transaction snapshot at start; mid-flight changes are reflected in the next day's run. Acceptable since reconciliation is a sanity check, not real-time enforcement.

## Migration plan

The work is sequenced across roughly three phases. Each phase is independently shippable and the system remains functional throughout.

### Phase 1: Schema + tag flow (1 week)

- Add `Strategy` to `DBPosition`, `ClientOrderID` to `DBOrder`. Migrations.
- Wire `client_order_id` encoding into the Alpaca order placement path.
- On fill, look up `DBOrder` by `OrderID` and propagate `StrategyName` to `DBPosition` and `DBManagedPosition`.
- Reverse-extraction fallback for untagged fills.
- Implement `GET /api/v1/positions?strategy=X`.

After this phase, agents can still run on separate paper accounts. The infrastructure exists but is not yet exercised.

### Phase 2: Segment P&L + circuit breakers (1 week)

- Add `DBSegmentPnL` table and the EOD writer.
- Implement `GET /api/v1/segment-pnl/:strategy`.
- Implement `POST /api/v1/circuit-breaker/:strategy/trip` and `/reset`.
- Update each agent's rules file (and harness call sites) to use `segment-pnl` instead of computing locally — already the case in TRADING_RULES_TREND.md and TRADING_RULES_PENNY.md; verify HARVEST and V2 align.

After this phase, segment-scoped circuit breakers work correctly even in shared accounts.

### Phase 3: Reconciliation + shared-paper migration (1-2 weeks)

- Implement `GET /api/v1/reconcile/:strategy` and the startup-reconciliation flow in the harness.
- Implement the `cmd/reconcile/main.go` daily script.
- Migrate V2 + HARVEST + (later) TREND into a shared paper account. PENNY joins when ready.
- Active stress testing: cap contention, circuit-breaker isolation, restart reconciliation.
- 30-day soak with zero violations before going live.

## What this spec does NOT cover

- **Multi-broker support.** The design assumes Alpaca. A future spec is needed if IBKR or another broker is added.
- **Real-time position streaming.** Currently positions are reconciled by polling. A streaming solution (Alpaca's account events websocket) is a separate optimization.
- **Cross-strategy hedging.** If V2 buys puts and TREND buys long-dated calls on the same underlying, this spec treats them as independent positions. A net-exposure view is a future analytics concern.
- **Margin and buying-power arbitration.** Each strategy's segment cap is in *portfolio-percent*, but options margin and PDT rules consume buying power non-linearly. If the four agents combined exceed available buying power, the broker rejects orders. The agent rules treat this as a soft skip ("insufficient buying power"). A formal buying-power coordinator is out of scope for v1.
- **Operator dashboard for cross-strategy view.** The `agent/server.js` UI is per-sandbox today. A unified dashboard showing all four agents' segment caps, P&L, and reconciliation status is needed for production but is UI work, not backend.

## Open questions

1. **Alpaca paper-account behavior on tagged orders.** Confirm that paper Alpaca preserves `client_order_id` through fills exactly as live Alpaca does. If paper-mode strips it, the entire shared-paper validation phase tests something different from production.

2. **Single account vs. sub-accounts.** Alpaca has limited sub-account support. If sub-accounts are usable, each strategy could have its own sub-account with shared margin — eliminating most race conditions. Cost: more API surface to manage. Worth investigating before committing to the single-account design.

3. **Strategy name evolution.** If V2 forks into V2a and V2b, the strategy whitelist needs to grow. The migration story for renaming a strategy (preserving historical attribution) needs design. Defer until forced by an actual fork.

4. **Reconciliation alerting.** The spec says "log critical, operator decides." What is the alert mechanism? Slack webhook? Email? File-based with a watcher? The agent server emits SSE events; the simplest path is to surface reconciliation failures as a high-severity event on the existing channel and let the operator's dashboard surface them.

5. **EOD job placement.** The daily reconciliation runs at 6:00 PM ET. If the orchestrator handles it, it runs as part of the after-hours phase. If it's a standalone cron, it survives orchestrator restarts. Recommendation: standalone Go binary triggered by Windows Task Scheduler / systemd, with the result published to the agent server via HTTP for dashboard surfacing.

6. **What lives where on Windows.** The user runs Windows. The Go bot is built per-sandbox; the reconciliation script needs to run once across all sandboxes. Either it's a separate binary that reads all sandbox DBs, or it's a shared service that each sandbox reports to. Recommendation: separate binary, since the data is already in per-sandbox SQLite files and a read-only cross-DB pass is straightforward.
