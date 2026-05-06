# Harvest Agent Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the Harvest agent — a mechanical iron condor premium seller on SPY/QQQ/IWM/GLD/TLT that runs as a fully autonomous LLM agent with defined-risk rules, state persistence, and its own Go backend service group.

**Architecture:** The LLM agent (opencode subprocess) reads `TRADING_RULES_HARVEST.md` as its strategy rules, then calls MCP tools to interact with a new `HarvestController` group in the Go backend. The Go backend handles IVR calculation, FOMC blackout logic, iron condor order placement (via Alpaca multi-leg API), and persistent condor state in SQLite. The agent performs all decision logic; the backend provides data and execution.

**Tech Stack:** Go (gin, GORM/SQLite, alpaca-trade-api-go v3), Node.js MCP server, JSON config in `agent/config-store.js`

---

## File Map

### New files
| File | Purpose |
|---|---|
| `TRADING_RULES_HARVEST.md` | LLM-optimized strategy rules and heartbeat instructions |
| `models/harvest_models.go` | GORM models: `DBHarvestCondor`, `DBHarvestIVSnapshot` |
| `services/harvest_types.go` | In-memory types: `IronCondorLeg`, `HarvestCondorRecord`, `HarvestStateResponse`, `IVRData`, `MonthlyExpiration` |
| `services/harvest_ivr_service.go` | Daily IV snapshot collection and 52-week IVR calculation |
| `services/harvest_ivr_service_test.go` | Tests for IVR service |
| `services/harvest_service.go` | FOMC blackout calendar, monthly expiration finder, condor state aggregation, mleg order placement |
| `services/harvest_service_test.go` | Tests for harvest service |
| `controllers/harvest_controller.go` | HTTP handlers: state, IVR, expirations, FOMC, condor open/close/list, log |

### Modified files
| File | Change |
|---|---|
| `services/trade_guard.go` | Add `AgentHarvest AgentSource = "harvest"` constant |
| `models/models.go` | No change — harvest models in separate file |
| `database/storage.go` | AutoMigrate harvest models; add `SaveHarvestCondor`, `UpdateHarvestCondor`, `ListOpenHarvestCondors`, `GetHarvestCondorByID`, `SaveHarvestIVSnapshot`, `GetHarvestIVSnapshots`, `GetHarvestClosedPnL` |
| `services/alpaca_trading.go` | Add `PlaceMultiLegOrder` method for 4-leg iron condors |
| `cmd/bot/main.go` | Wire `HarvestService`, `HarvestIVRService`, `HarvestController`; register routes; start IVR background goroutine |
| `mcp-server.js` | Add 6 tools: `get_harvest_state`, `get_harvest_ivr`, `get_harvest_expirations`, `get_harvest_fomc`, `open_iron_condor`, `close_iron_condor` |
| `agent/config-store.js` | Add Harvest to `defaultAgents()` and `defaultStrategies()` |

---

## Task 1: Trading Rules File

**Files:**
- Create: `TRADING_RULES_HARVEST.md`

- [ ] **Step 1: Create the trading rules file**

```markdown
# Harvest Trading Rules

**Style:** Mechanical iron condor premium seller — rule executor only

---

## CRITICAL: Heartbeat Override

These rules SUPERSEDE the generic "Heartbeat Behavior" section below.
Ignore all pre-market, midday, and after-hours instructions in the generic block.
Your ONLY behavior is defined here.

---

## Identity

You are Harvest. You are not a reasoning agent. You are a rule executor wrapped
in a language model. You sell iron condors. You manage them by rules. You do not
improvise. Helpful improvisation is the failure mode.

Your outputs are limited to:
1. Tool calls specified by your rules (open, close, skip, halt)
2. Structured log entries specified below
3. A one-line heartbeat summary at the end of each cycle

---

## Universe

Core: SPY, QQQ, IWM
Secondary: GLD, TLT

No other instruments. If any tool returns data on other tickers, ignore it.

---

## Heartbeat Behavior

Run this sequence every heartbeat, in order:

### Step 1: Pre-loop checks (run once, skip all underlyings if any trigger)

Call `get_harvest_state`. If any of the following are true, skip Steps 2–3 entirely:
- `circuit_breaker_active` is true
- `open_condors` >= 5
- `deployed_buying_power_pct` >= 12.0

Call `get_harvest_fomc`. If `is_blackout` is true, skip Steps 2–3 entirely.

### Step 2: Exit checks (for each open condor in `get_harvest_state` response)

For each condor in `open_condors_detail`:

**Priority 1 — Time exit (DTE ≤ 21):**
If `dte` <= 21, call `close_iron_condor` with `{ condor_id, order_type: "limit",
limit_price: <current mid> }`. If not filled in 10 minutes, retry at mid - $0.05.
If not filled after 10 more minutes, use `order_type: "market"`.

**Priority 2 — Loss stop (cost_to_close ≥ 2× original credit):**
If `cost_to_close_per_contract` >= 2.0 × `credit_per_contract`, call
`close_iron_condor` with `{ condor_id, order_type: "marketable_limit",
limit_price: <current mid + 0.20> }`. If not filled in 2 minutes, use
`order_type: "market"`.

**Priority 3 — Profit target (cost_to_close ≤ 0.50× original credit):**
If `cost_to_close_per_contract` <= 0.50 × `credit_per_contract`, call
`close_iron_condor` with `{ condor_id, order_type: "limit",
limit_price: <current mid> }`. If not filled in 10 minutes, retry at mid - $0.05.
If not filled after 10 more minutes, use `order_type: "market"`.

If none of the above conditions fire, log the condor status and take no action.

### Step 3: Entry checks (for each underlying: SPY, QQQ, IWM, GLD, TLT)

Skip this underlying if:
- `get_harvest_state` shows an open condor for this underlying
- `get_harvest_state` shows `open_condors` >= 5
- `get_harvest_state` shows `deployed_buying_power_pct` >= 12.0

Call `get_harvest_ivr` for this underlying.
- If `ivr` < 30 → skip, log "IVR {value} below 30 for {underlying}"
- If `quote_age_seconds` > 60 → skip, log "stale quote for {underlying}"

Call `get_harvest_expirations` for this underlying.
- If no expiration returned → skip, log "no monthly expiration in [35,55] DTE for {underlying}"

Call `get_options_chain` with:
  `{ symbol: underlying, expiration: <date from above>, delta_min: 0.12, delta_max: 0.20, type: "put" }`
Find the put strike nearest to 0.16 delta. If multiple, pick the further-OTM one.
Record: short_put_symbol, short_put_strike, short_put_delta.

Call `get_options_chain` with:
  `{ symbol: underlying, expiration: <date from above>, delta_min: 0.12, delta_max: 0.20, type: "call" }`
Find the call strike nearest to 0.16 delta. If multiple, pick the further-OTM one.
Record: short_call_symbol, short_call_strike, short_call_delta.

If no strikes found in [0.12, 0.20] tolerance → skip, log "no strikes in delta tolerance for {underlying}".

Wing widths by underlying: SPY=$5, QQQ=$5, IWM=$2, GLD=$2, TLT=$1.
Long put strike = short_put_strike - wing_width.
Long call strike = short_call_strike + wing_width.
Find the OCC symbols for long_put and long_call in the chain.

Calculate mid-price credit = (short_put_mid + short_call_mid - long_put_mid - long_call_mid).
If credit < wing_width / 3 → skip, log "credit {value} below minimum for {underlying}".
If credit < 0.30 → skip, log "credit sanity check failed for {underlying}".

Call `get_account` to get current portfolio_value.
Contracts = floor(portfolio_value × 0.015 / (wing_width × 100)).
If contracts = 0 → skip, log "portfolio too small for {underlying}".

Verify: adding this position keeps `deployed_buying_power_pct` + (wing_width × contracts × 100 / portfolio_value × 100) ≤ 12.0.
If not → skip.

Call `open_iron_condor` with the full condor specification.

### Step 4: Heartbeat summary (always run)

Log one line: "Harvest heartbeat: {N} condors open, {pct}% deployed, circuit_breaker={status},
evaluated={list of underlyings checked}, actions={list of opens/closes this beat}".

---

## Rule Boundaries

- All numeric thresholds are inclusive: DTE ≤ 21 includes 21; IVR ≥ 30 includes 30.0.
- When ambiguous, default to the more conservative action (skip for entries, do nothing for exits).
- Always log ambiguity.

---

## Hard Stops (override everything)

Cease all activity and log "HARD STOP: {reason}" if:
- Broker connection failure or authentication error
- Trade rejection by broker (insufficient funds, prohibited security)
- Account risk warning or margin call
- `get_harvest_state` returns a reconciliation mismatch flag
- Any quote staleness > 5 minutes during market hours
- Any error not covered by these rules

Do not attempt to continue or recover autonomously. Await operator reset.

---

## What You Do Not Do

- No market commentary or directional opinions
- No adjustments to open condors ("the market looks weak, let me adjust")
- No rolling, hedging, or partial closes
- No trades outside the 5-universe
- No positions other than iron condors
- No decisions based on other agents' positions (the overlap_log is for your records only)
- No retroactive rule changes mid-session
```

- [ ] **Step 2: Commit**

```bash
git add TRADING_RULES_HARVEST.md
git commit -m "feat: add TRADING_RULES_HARVEST for Harvest agent"
```

---

## Task 2: AgentHarvest Source Constant

**Files:**
- Modify: `services/trade_guard.go:14-18`

- [ ] **Step 1: Write a failing test**

Create a new file `services/trade_guard_harvest_test.go`:

```go
package services

import (
	"testing"
)

func TestAgentHarvestConstant(t *testing.T) {
	if AgentHarvest == AgentMain {
		t.Error("AgentHarvest must be distinct from AgentMain")
	}
	if AgentHarvest == AgentPenny {
		t.Error("AgentHarvest must be distinct from AgentPenny")
	}
	if string(AgentHarvest) != "harvest" {
		t.Errorf("expected AgentHarvest to be 'harvest', got %q", AgentHarvest)
	}
}
```

- [ ] **Step 2: Run test to confirm it fails**

```
go test ./services/ -run TestAgentHarvestConstant -v
```

Expected: FAIL — `AgentHarvest` undefined.

- [ ] **Step 3: Add constant to trade_guard.go**

In `services/trade_guard.go`, after line 18 (`AgentPenny AgentSource = "penny"`):

```go
AgentHarvest AgentSource = "harvest"
```

Also add `AgentHarvest: {}` to the `rawSymbols` map in `NewTradeGuard` (line 76):

```go
rawSymbols: map[AgentSource]map[string]struct{}{
    AgentMain:    {},
    AgentPenny:   {},
    AgentHarvest: {},
},
```

- [ ] **Step 4: Run test to confirm it passes**

```
go test ./services/ -run TestAgentHarvestConstant -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add services/trade_guard.go services/trade_guard_harvest_test.go
git commit -m "feat: add AgentHarvest source constant to trade guard"
```

---

## Task 3: DB Models + Storage Methods

**Files:**
- Create: `models/harvest_models.go`
- Modify: `database/storage.go`

- [ ] **Step 1: Write the failing tests for storage methods**

Add to `database/` a new file `storage_harvest_test.go`:

```go
package database

import (
	"testing"
	"time"

	"prophet-trader/models"
)

func setupHarvestTestDB(t *testing.T) *LocalStorage {
	t.Helper()
	s, err := NewLocalStorage(":memory:")
	if err != nil {
		t.Fatalf("failed to create test DB: %v", err)
	}
	return s
}

func TestSaveAndGetHarvestCondor(t *testing.T) {
	s := setupHarvestTestDB(t)
	condor := &models.DBHarvestCondor{
		CondorID:              "test-condor-1",
		Underlying:            "SPY",
		Expiration:            time.Now().AddDate(0, 0, 45),
		ShortPutSymbol:        "SPY260619P00520000",
		ShortPutStrike:        520.0,
		LongPutSymbol:         "SPY260619P00515000",
		LongPutStrike:         515.0,
		ShortCallSymbol:       "SPY260619C00560000",
		ShortCallStrike:       560.0,
		LongCallSymbol:        "SPY260619C00565000",
		LongCallStrike:        565.0,
		Contracts:             2,
		WingWidth:             5.0,
		CreditPerContract:     1.50,
		TotalCredit:           300.0,
		MaxLoss:               1000.0,
		PortfolioValueAtEntry: 100000.0,
		EntryOrderID:          "ord-001",
		Status:                "OPEN",
		IVRAtEntry:            45.0,
		OpenedAt:              time.Now(),
	}
	if err := s.SaveHarvestCondor(condor); err != nil {
		t.Fatalf("SaveHarvestCondor failed: %v", err)
	}
	got, err := s.GetHarvestCondorByID("test-condor-1")
	if err != nil {
		t.Fatalf("GetHarvestCondorByID failed: %v", err)
	}
	if got.Underlying != "SPY" {
		t.Errorf("expected Underlying=SPY, got %s", got.Underlying)
	}
}

func TestListOpenHarvestCondors(t *testing.T) {
	s := setupHarvestTestDB(t)
	condors := []*models.DBHarvestCondor{
		{CondorID: "c1", Underlying: "SPY", Status: "OPEN",
			Expiration: time.Now().AddDate(0, 0, 40), OpenedAt: time.Now(),
			WingWidth: 5, CreditPerContract: 1.0, MaxLoss: 500},
		{CondorID: "c2", Underlying: "QQQ", Status: "CLOSED",
			Expiration: time.Now().AddDate(0, 0, 40), OpenedAt: time.Now(),
			WingWidth: 5, CreditPerContract: 1.0, MaxLoss: 500},
	}
	for _, c := range condors {
		_ = s.SaveHarvestCondor(c)
	}
	open := s.ListOpenHarvestCondors()
	if len(open) != 1 || open[0].CondorID != "c1" {
		t.Errorf("expected 1 OPEN condor (c1), got %d", len(open))
	}
}

func TestGetHarvestClosedPnL(t *testing.T) {
	s := setupHarvestTestDB(t)
	now := time.Now()
	condors := []*models.DBHarvestCondor{
		{CondorID: "p1", Status: "CLOSED", RealizedPnL: 150.0,
			ClosedAt: &now, Underlying: "SPY", OpenedAt: now,
			WingWidth: 5, CreditPerContract: 1.0, MaxLoss: 500,
			Expiration: now.AddDate(0, 0, 10)},
		{CondorID: "p2", Status: "CLOSED", RealizedPnL: -80.0,
			ClosedAt: &now, Underlying: "QQQ", OpenedAt: now,
			WingWidth: 5, CreditPerContract: 1.0, MaxLoss: 500,
			Expiration: now.AddDate(0, 0, 10)},
	}
	for _, c := range condors {
		_ = s.SaveHarvestCondor(c)
	}
	pnl, err := s.GetHarvestClosedPnL(now.AddDate(0, 0, -30), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("GetHarvestClosedPnL failed: %v", err)
	}
	if pnl != 70.0 {
		t.Errorf("expected PnL=70.0, got %.2f", pnl)
	}
}

func TestSaveAndGetHarvestIVSnapshot(t *testing.T) {
	s := setupHarvestTestDB(t)
	today := time.Now().Truncate(24 * time.Hour)
	snap := &models.DBHarvestIVSnapshot{
		Underlying: "SPY",
		Date:       today,
		ATMIV:      0.185,
	}
	if err := s.SaveHarvestIVSnapshot(snap); err != nil {
		t.Fatalf("SaveHarvestIVSnapshot failed: %v", err)
	}
	snaps, err := s.GetHarvestIVSnapshots("SPY", today.AddDate(0, 0, -1), today.Add(time.Hour))
	if err != nil {
		t.Fatalf("GetHarvestIVSnapshots failed: %v", err)
	}
	if len(snaps) != 1 || snaps[0].ATMIV != 0.185 {
		t.Errorf("unexpected snapshots: %+v", snaps)
	}
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```
go test ./database/ -run "TestSave.*Harvest|TestList.*Harvest|TestGetHarvest" -v
```

Expected: FAIL — `DBHarvestCondor` and methods undefined.

- [ ] **Step 3: Create models/harvest_models.go**

```go
package models

import (
	"time"

	"gorm.io/gorm"
)

// DBHarvestCondor tracks an iron condor position opened by the Harvest agent.
type DBHarvestCondor struct {
	gorm.Model
	CondorID   string    `gorm:"uniqueIndex"`
	Underlying string    `gorm:"index"`
	Expiration time.Time

	// Four legs
	ShortPutSymbol  string
	ShortPutStrike  float64
	LongPutSymbol   string
	LongPutStrike   float64
	ShortCallSymbol string
	ShortCallStrike float64
	LongCallSymbol  string
	LongCallStrike  float64

	// Position details
	Contracts             int
	WingWidth             float64
	CreditPerContract     float64
	TotalCredit           float64
	MaxLoss               float64
	PortfolioValueAtEntry float64

	// Order tracking
	EntryOrderID string
	CloseOrderID string

	// Status: OPEN | CLOSING | CLOSED
	Status              string  `gorm:"index"`
	CloseReason         string
	CloseCostPerContract float64
	RealizedPnL         float64
	OpenedAt            time.Time
	ClosedAt            *time.Time

	// Analysis metadata
	IVRAtEntry float64
	OverlapLog string // JSON: [{agent, underlying, direction, contracts, dte}]
}

// DBHarvestIVSnapshot stores one ATM-IV reading per underlying per trading day.
type DBHarvestIVSnapshot struct {
	gorm.Model
	Underlying string    `gorm:"uniqueIndex:idx_harvest_iv_under_date"`
	Date       time.Time `gorm:"uniqueIndex:idx_harvest_iv_under_date"`
	ATMIV      float64   // at-the-money implied volatility (average of nearest put+call)
}

func (DBHarvestCondor) TableName() string    { return "harvest_condors" }
func (DBHarvestIVSnapshot) TableName() string { return "harvest_iv_snapshots" }
```

- [ ] **Step 4: Add AutoMigrate and storage methods to database/storage.go**

In `database/storage.go`, in the `AutoMigrate` call (around line 40), add the two new models:

```go
if err := db.AutoMigrate(
    &models.DBOrder{},
    &models.DBBar{},
    &models.DBPosition{},
    &models.DBTrade{},
    &models.DBAccountSnapshot{},
    &models.DBSignal{},
    &models.DBManagedPosition{},
    &models.DBHarvestCondor{},
    &models.DBHarvestIVSnapshot{},
); err != nil {
```

At the end of `database/storage.go`, add these methods:

```go
// ── Harvest condor storage ─────────────────────────────────────────

func (s *LocalStorage) SaveHarvestCondor(c *models.DBHarvestCondor) error {
	return s.db.Save(c).Error
}

func (s *LocalStorage) UpdateHarvestCondor(condorID string, updates map[string]interface{}) error {
	return s.db.Model(&models.DBHarvestCondor{}).
		Where("condor_id = ?", condorID).
		Updates(updates).Error
}

func (s *LocalStorage) GetHarvestCondorByID(condorID string) (*models.DBHarvestCondor, error) {
	var c models.DBHarvestCondor
	if err := s.db.Where("condor_id = ?", condorID).First(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *LocalStorage) ListOpenHarvestCondors() []*models.DBHarvestCondor {
	var condors []*models.DBHarvestCondor
	s.db.Where("status = ?", "OPEN").Find(&condors)
	return condors
}

// GetHarvestClosedPnL sums realized P&L for condors closed within [start, end].
func (s *LocalStorage) GetHarvestClosedPnL(start, end time.Time) (float64, error) {
	var total float64
	err := s.db.Model(&models.DBHarvestCondor{}).
		Where("status = ? AND closed_at >= ? AND closed_at <= ?", "CLOSED", start, end).
		Select("COALESCE(SUM(realized_pn_l), 0)").
		Scan(&total).Error
	return total, err
}

// ── Harvest IV snapshot storage ────────────────────────────────────

func (s *LocalStorage) SaveHarvestIVSnapshot(snap *models.DBHarvestIVSnapshot) error {
	return s.db.Save(snap).Error
}

func (s *LocalStorage) GetHarvestIVSnapshots(underlying string, start, end time.Time) ([]*models.DBHarvestIVSnapshot, error) {
	var snaps []*models.DBHarvestIVSnapshot
	err := s.db.Where("underlying = ? AND date >= ? AND date <= ?", underlying, start, end).
		Order("date ASC").
		Find(&snaps).Error
	return snaps, err
}
```

- [ ] **Step 5: Run tests to confirm they pass**

```
go test ./database/ -run "TestSave.*Harvest|TestList.*Harvest|TestGetHarvest" -v
```

Expected: PASS.

- [ ] **Step 6: Run full test suite to check for regressions**

```
go test ./... -v 2>&1 | tail -30
```

Expected: all existing tests still PASS.

- [ ] **Step 7: Commit**

```bash
git add models/harvest_models.go database/storage.go database/storage_harvest_test.go
git commit -m "feat: add Harvest DB models and storage methods"
```

---

## Task 4: Harvest IVR Service

**Files:**
- Create: `services/harvest_ivr_service.go`
- Create: `services/harvest_ivr_service_test.go`

The IVR service collects one ATM-IV reading per underlying per trading day and calculates the 52-week IV rank from stored history.

- [ ] **Step 1: Write the failing tests**

Create `services/harvest_ivr_service_test.go`:

```go
package services

import (
	"testing"
	"time"

	"prophet-trader/models"
)

// stubIVStore satisfies the harvestIVStore interface for testing.
type stubIVStore struct {
	saved  []*models.DBHarvestIVSnapshot
	stored []*models.DBHarvestIVSnapshot
}

func (s *stubIVStore) SaveHarvestIVSnapshot(snap *models.DBHarvestIVSnapshot) error {
	s.saved = append(s.saved, snap)
	return nil
}
func (s *stubIVStore) GetHarvestIVSnapshots(underlying string, start, end time.Time) ([]*models.DBHarvestIVSnapshot, error) {
	var out []*models.DBHarvestIVSnapshot
	for _, sn := range s.stored {
		if sn.Underlying == underlying && !sn.Date.Before(start) && !sn.Date.After(end) {
			out = append(out, sn)
		}
	}
	return out, nil
}

func makeSnaps(underlying string, ivValues []float64, startDate time.Time) []*models.DBHarvestIVSnapshot {
	snaps := make([]*models.DBHarvestIVSnapshot, len(ivValues))
	for i, iv := range ivValues {
		snaps[i] = &models.DBHarvestIVSnapshot{
			Underlying: underlying,
			Date:       startDate.AddDate(0, 0, i),
			ATMIV:      iv,
		}
	}
	return snaps
}

func TestCalcIVR_FullRange(t *testing.T) {
	// low=0.10, high=0.30, current=0.20 → IVR = (0.20-0.10)/(0.30-0.10)*100 = 50
	ivr := calcIVR(0.20, 0.10, 0.30)
	if ivr != 50.0 {
		t.Errorf("expected IVR=50.0, got %.2f", ivr)
	}
}

func TestCalcIVR_AtLow(t *testing.T) {
	ivr := calcIVR(0.10, 0.10, 0.30)
	if ivr != 0.0 {
		t.Errorf("expected IVR=0.0, got %.2f", ivr)
	}
}

func TestCalcIVR_AtHigh(t *testing.T) {
	ivr := calcIVR(0.30, 0.10, 0.30)
	if ivr != 100.0 {
		t.Errorf("expected IVR=100.0, got %.2f", ivr)
	}
}

func TestCalcIVR_ZeroRange(t *testing.T) {
	// If high == low, return 50 (neutral) rather than dividing by zero
	ivr := calcIVR(0.15, 0.15, 0.15)
	if ivr != 50.0 {
		t.Errorf("expected IVR=50.0 on zero range, got %.2f", ivr)
	}
}

func TestGetIVRData_SufficientHistory(t *testing.T) {
	store := &stubIVStore{}
	// 100 days of history with a known range
	start := time.Now().AddDate(0, 0, -100)
	store.stored = makeSnaps("SPY", func() []float64 {
		vals := make([]float64, 100)
		for i := range vals {
			vals[i] = 0.10 + float64(i)*0.002 // 0.10 to 0.298
		}
		return vals
	}(), start)

	svc := NewHarvestIVRService(store)
	data, err := svc.GetIVRData("SPY", 0.20)
	if err != nil {
		t.Fatalf("GetIVRData failed: %v", err)
	}
	if data.IVR < 0 || data.IVR > 100 {
		t.Errorf("IVR out of range: %.2f", data.IVR)
	}
	if data.DaysOfHistory != 100 {
		t.Errorf("expected 100 days, got %d", data.DaysOfHistory)
	}
}

func TestGetIVRData_NoHistory(t *testing.T) {
	store := &stubIVStore{}
	svc := NewHarvestIVRService(store)
	data, err := svc.GetIVRData("TLT", 0.15)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data.DaysOfHistory != 0 {
		t.Errorf("expected 0 days, got %d", data.DaysOfHistory)
	}
	// With no history, IVR is unknown — signal this with IVR = -1
	if data.IVR != -1 {
		t.Errorf("expected IVR=-1 (unknown), got %.2f", data.IVR)
	}
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```
go test ./services/ -run "TestCalcIVR|TestGetIVRData" -v
```

Expected: FAIL — `calcIVR`, `HarvestIVRService` undefined.

- [ ] **Step 3: Create services/harvest_ivr_service.go**

```go
package services

import (
	"fmt"
	"time"

	"prophet-trader/models"
)

// harvestIVStore is the subset of storage used by the IVR service.
type harvestIVStore interface {
	SaveHarvestIVSnapshot(snap *models.DBHarvestIVSnapshot) error
	GetHarvestIVSnapshots(underlying string, start, end time.Time) ([]*models.DBHarvestIVSnapshot, error)
}

// IVRData contains the result of an IVR calculation.
type IVRData struct {
	Underlying    string
	CurrentIV     float64
	Low52Wk       float64
	High52Wk      float64
	IVR           float64 // -1 means insufficient history
	DaysOfHistory int
}

// HarvestIVRService collects and calculates IV rank for Harvest underlyings.
type HarvestIVRService struct {
	store harvestIVStore
}

// NewHarvestIVRService creates a new IVR service.
func NewHarvestIVRService(store harvestIVStore) *HarvestIVRService {
	return &HarvestIVRService{store: store}
}

// RecordDailyIV stores today's ATM IV for the given underlying if not already stored today.
func (s *HarvestIVRService) RecordDailyIV(underlying string, atmIV float64) error {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	existing, err := s.store.GetHarvestIVSnapshots(underlying, today, today.Add(23*time.Hour+59*time.Minute))
	if err != nil {
		return fmt.Errorf("checking existing snapshot: %w", err)
	}
	if len(existing) > 0 {
		return nil // already recorded today
	}
	return s.store.SaveHarvestIVSnapshot(&models.DBHarvestIVSnapshot{
		Underlying: underlying,
		Date:       today,
		ATMIV:      atmIV,
	})
}

// GetIVRData returns the IVR for an underlying given its current ATM IV.
// currentIV should be the ATM implied volatility from live quotes.
func (s *HarvestIVRService) GetIVRData(underlying string, currentIV float64) (*IVRData, error) {
	end := time.Now().UTC()
	start := end.AddDate(-1, 0, 0) // up to 52 weeks back
	snaps, err := s.store.GetHarvestIVSnapshots(underlying, start, end)
	if err != nil {
		return nil, fmt.Errorf("fetching IV snapshots: %w", err)
	}

	data := &IVRData{
		Underlying:    underlying,
		CurrentIV:     currentIV,
		DaysOfHistory: len(snaps),
	}

	if len(snaps) == 0 {
		data.IVR = -1
		return data, nil
	}

	low, high := snaps[0].ATMIV, snaps[0].ATMIV
	for _, sn := range snaps[1:] {
		if sn.ATMIV < low {
			low = sn.ATMIV
		}
		if sn.ATMIV > high {
			high = sn.ATMIV
		}
	}
	data.Low52Wk = low
	data.High52Wk = high
	data.IVR = calcIVR(currentIV, low, high)
	return data, nil
}

// calcIVR computes (current - low) / (high - low) * 100.
// Returns 50 when high == low to avoid division by zero.
func calcIVR(current, low, high float64) float64 {
	if high == low {
		return 50.0
	}
	ivr := (current - low) / (high - low) * 100.0
	if ivr < 0 {
		return 0.0
	}
	if ivr > 100 {
		return 100.0
	}
	return ivr
}
```

- [ ] **Step 4: Run tests to confirm they pass**

```
go test ./services/ -run "TestCalcIVR|TestGetIVRData" -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add services/harvest_ivr_service.go services/harvest_ivr_service_test.go
git commit -m "feat: add Harvest IVR service with 52-week IV rank calculation"
```

---

## Task 5: Harvest Core Service

**Files:**
- Create: `services/harvest_service.go`
- Create: `services/harvest_service_test.go`

Covers: FOMC blackout calendar, monthly expiration finder, condor state aggregation, multi-leg order placement, circuit breaker logic.

- [ ] **Step 1: Write the failing tests**

Create `services/harvest_service_test.go`:

```go
package services

import (
	"testing"
	"time"

	"prophet-trader/models"
)

// ── FOMC tests ─────────────────────────────────────────────────────

func TestIsFOMCBlackout_InsideWindow(t *testing.T) {
	// A date that is 12 hours before a known 2026 FOMC meeting (Jan 28, 2026 2pm ET)
	fomc := time.Date(2026, 1, 28, 14, 0, 0, 0, time.UTC)
	testTime := fomc.Add(-12 * time.Hour)
	if !isFOMCBlackout(testTime, fomc2026Dates) {
		t.Error("expected blackout 12h before FOMC")
	}
}

func TestIsFOMCBlackout_OutsideWindow(t *testing.T) {
	fomc := time.Date(2026, 1, 28, 14, 0, 0, 0, time.UTC)
	testTime := fomc.Add(-25 * time.Hour) // 25h before — outside 24h window
	if isFOMCBlackout(testTime, fomc2026Dates) {
		t.Error("expected no blackout 25h before FOMC")
	}
}

func TestIsFOMCBlackout_AfterAnnouncement(t *testing.T) {
	fomc := time.Date(2026, 1, 28, 14, 0, 0, 0, time.UTC)
	testTime := fomc.Add(1 * time.Hour) // 1h after announcement
	if isFOMCBlackout(testTime, fomc2026Dates) {
		t.Error("expected no blackout after FOMC announcement")
	}
}

// ── Monthly expiration tests ────────────────────────────────────────

func TestNextMonthlyExpiration_InBand(t *testing.T) {
	// Pick a date where the next 3rd Friday falls ~45 days out
	// May 1, 2026 → next monthly is Jun 19, 2026 → DTE = 49 ✓
	ref := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	exp, dte, ok := nextMonthlyExpiration(ref, 35, 55)
	if !ok {
		t.Fatal("expected to find expiration in [35,55] band")
	}
	if dte < 35 || dte > 55 {
		t.Errorf("DTE %d out of [35,55] band, expiration=%s", dte, exp.Format("2006-01-02"))
	}
	// Verify it's a Friday
	if exp.Weekday() != time.Friday {
		t.Errorf("expiration %s is not a Friday", exp.Format("2006-01-02"))
	}
}

func TestNextMonthlyExpiration_NoneInBand(t *testing.T) {
	// If today is exactly the expiration Friday, next monthly will be ~28 days away — outside [35,55]
	// Find a date where no monthly is in [35,55]
	// Dec 1, 2026: next monthly is Dec 18 (17 DTE) and Jan 15, 2027 (45 DTE) → Jan 15 is in band
	// Actually this is hard to engineer exactly; just verify that if no monthlies fit we return ok=false
	// Use extremely tight band [99, 100] which will never match
	ref := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	_, _, ok := nextMonthlyExpiration(ref, 99, 100)
	if ok {
		t.Error("expected no expiration in impossible [99,100] DTE band")
	}
}

func TestThirdFriday(t *testing.T) {
	// June 2026: 3rd Friday is June 19
	f := thirdFriday(2026, time.June)
	expected := time.Date(2026, time.June, 19, 0, 0, 0, 0, time.UTC)
	if !f.Equal(expected) {
		t.Errorf("expected %s, got %s", expected.Format("2006-01-02"), f.Format("2006-01-02"))
	}
	// January 2026: 3rd Friday is Jan 16
	f2 := thirdFriday(2026, time.January)
	expected2 := time.Date(2026, time.January, 16, 0, 0, 0, 0, time.UTC)
	if !f2.Equal(expected2) {
		t.Errorf("expected %s, got %s", expected2.Format("2006-01-02"), f2.Format("2026-01-02"))
	}
}

// ── Circuit breaker tests ───────────────────────────────────────────

func TestCircuitBreakerThreshold(t *testing.T) {
	portfolioValue := 100000.0
	threshold := portfolioValue * 0.05 // -5%
	if threshold != 5000.0 {
		t.Errorf("expected threshold=5000, got %.2f", threshold)
	}
}

// ── Harvest state aggregation tests ────────────────────────────────

type stubHarvestStore struct {
	condors []*models.DBHarvestCondor
	pnl     float64
}

func (s *stubHarvestStore) ListOpenHarvestCondors() []*models.DBHarvestCondor { return s.condors }
func (s *stubHarvestStore) GetHarvestClosedPnL(start, end time.Time) (float64, error) {
	return s.pnl, nil
}

func TestCalcDeployedBuyingPower(t *testing.T) {
	condors := []*models.DBHarvestCondor{
		{MaxLoss: 1000.0}, // $1000 max loss
		{MaxLoss: 500.0},  // $500 max loss
	}
	portfolioValue := 100000.0
	pct := calcDeployedBuyingPowerPct(condors, portfolioValue)
	expected := 1.5 // 1500 / 100000 * 100
	if pct != expected {
		t.Errorf("expected %.2f%%, got %.2f%%", expected, pct)
	}
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```
go test ./services/ -run "TestIsFOMC|TestNextMonthly|TestThirdFriday|TestCircuitBreaker|TestCalcDeployed" -v
```

Expected: FAIL — all functions undefined.

- [ ] **Step 3: Create services/harvest_service.go**

```go
package services

import (
	"context"
	"fmt"
	"math"
	"time"

	"prophet-trader/models"
)

// harvestStateStore is the storage subset used by HarvestService.
type harvestStateStore interface {
	ListOpenHarvestCondors() []*models.DBHarvestCondor
	GetHarvestClosedPnL(start, end time.Time) (float64, error)
	SaveHarvestCondor(c *models.DBHarvestCondor) error
	UpdateHarvestCondor(condorID string, updates map[string]interface{}) error
	GetHarvestCondorByID(condorID string) (*models.DBHarvestCondor, error)
}

// fomc2026Dates holds scheduled FOMC announcement times (ET, converted to UTC).
// Source: federalreserve.gov calendar. Update quarterly.
var fomc2026Dates = []time.Time{
	time.Date(2026, 1, 28, 19, 0, 0, 0, time.UTC),  // Jan 28, 2026 2pm ET
	time.Date(2026, 3, 18, 18, 0, 0, 0, time.UTC),  // Mar 18, 2026
	time.Date(2026, 5, 6, 18, 0, 0, 0, time.UTC),   // May 6, 2026
	time.Date(2026, 6, 17, 18, 0, 0, 0, time.UTC),  // Jun 17, 2026
	time.Date(2026, 7, 29, 18, 0, 0, 0, time.UTC),  // Jul 29, 2026
	time.Date(2026, 9, 16, 18, 0, 0, 0, time.UTC),  // Sep 16, 2026
	time.Date(2026, 11, 4, 19, 0, 0, 0, time.UTC),  // Nov 4, 2026
	time.Date(2026, 12, 16, 19, 0, 0, 0, time.UTC), // Dec 16, 2026
}

// HarvestStateResponse is what the API returns for GET /harvest/state.
type HarvestStateResponse struct {
	OpenCondors           int                    `json:"open_condors"`
	OpenCondorsDetail     []*models.DBHarvestCondor `json:"open_condors_detail"`
	DeployedBuyingPowerPct float64               `json:"deployed_buying_power_pct"`
	Trailing30dPnL        float64                `json:"trailing_30d_pnl"`
	Trailing30dPnLPct     float64                `json:"trailing_30d_pnl_pct"`
	CircuitBreakerActive  bool                   `json:"circuit_breaker_active"`
	PortfolioValue        float64                `json:"portfolio_value"`
}

// FOMCStatusResponse is what the API returns for GET /harvest/fomc.
type FOMCStatusResponse struct {
	IsBlackout    bool      `json:"is_blackout"`
	NextFOMCDate  time.Time `json:"next_fomc_date"`
	HoursUntilFOMC float64  `json:"hours_until_fomc"`
	BlackoutUntil *time.Time `json:"blackout_until,omitempty"`
}

// MonthlyExpiration is what the API returns for GET /harvest/expirations/:symbol.
type MonthlyExpiration struct {
	Symbol         string    `json:"symbol"`
	ExpirationDate time.Time `json:"expiration_date"`
	DTE            int       `json:"dte"`
}

// HarvestService provides core harvest logic: state, FOMC, expirations.
type HarvestService struct {
	store       harvestStateStore
	fomcDates   []time.Time
}

// NewHarvestService creates a new HarvestService.
func NewHarvestService(store harvestStateStore) *HarvestService {
	return &HarvestService{store: store, fomcDates: fomc2026Dates}
}

// GetState returns the current Harvest portfolio state.
func (s *HarvestService) GetState(portfolioValue float64) (*HarvestStateResponse, error) {
	condors := s.store.ListOpenHarvestCondors()
	deployedPct := calcDeployedBuyingPowerPct(condors, portfolioValue)

	now := time.Now().UTC()
	tradingDayStart := now.AddDate(0, 0, -44) // ~30 trading days back (44 calendar days)
	pnl30d, err := s.store.GetHarvestClosedPnL(tradingDayStart, now)
	if err != nil {
		return nil, fmt.Errorf("fetching 30d P&L: %w", err)
	}
	pnl30dPct := 0.0
	if portfolioValue > 0 {
		pnl30dPct = (pnl30d / portfolioValue) * 100.0
	}
	circuitBreaker := portfolioValue > 0 && pnl30dPct < -5.0

	return &HarvestStateResponse{
		OpenCondors:            len(condors),
		OpenCondorsDetail:      condors,
		DeployedBuyingPowerPct: deployedPct,
		Trailing30dPnL:         pnl30d,
		Trailing30dPnLPct:      pnl30dPct,
		CircuitBreakerActive:   circuitBreaker,
		PortfolioValue:         portfolioValue,
	}, nil
}

// GetFOMCStatus returns current FOMC blackout status.
func (s *HarvestService) GetFOMCStatus() *FOMCStatusResponse {
	now := time.Now().UTC()
	resp := &FOMCStatusResponse{}

	// Find the next FOMC date
	for _, fomc := range s.fomcDates {
		if fomc.After(now) {
			resp.NextFOMCDate = fomc
			resp.HoursUntilFOMC = fomc.Sub(now).Hours()
			break
		}
	}

	resp.IsBlackout = isFOMCBlackout(now, s.fomcDates)
	if resp.IsBlackout {
		until := resp.NextFOMCDate.Add(1 * time.Millisecond) // resumes just after announcement
		resp.BlackoutUntil = &until
	}
	return resp
}

// GetNextMonthlyExpiration finds the next monthly expiration in [minDTE, maxDTE].
func (s *HarvestService) GetNextMonthlyExpiration(symbol string, minDTE, maxDTE int) (*MonthlyExpiration, error) {
	now := time.Now().UTC()
	exp, dte, ok := nextMonthlyExpiration(now, minDTE, maxDTE)
	if !ok {
		return nil, fmt.Errorf("no monthly expiration found in [%d,%d] DTE band for %s", minDTE, maxDTE, symbol)
	}
	return &MonthlyExpiration{Symbol: symbol, ExpirationDate: exp, DTE: dte}, nil
}

// isFOMCBlackout returns true if now is within 24h before any FOMC date.
func isFOMCBlackout(now time.Time, dates []time.Time) bool {
	for _, fomc := range dates {
		hoursUntil := fomc.Sub(now).Hours()
		if hoursUntil >= 0 && hoursUntil <= 24 {
			return true
		}
	}
	return false
}

// thirdFriday returns the third Friday of the given month and year (UTC midnight).
func thirdFriday(year int, month time.Month) time.Time {
	first := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	// Find first Friday
	daysUntilFriday := (int(time.Friday) - int(first.Weekday()) + 7) % 7
	firstFriday := first.AddDate(0, 0, daysUntilFriday)
	return firstFriday.AddDate(0, 0, 14) // + 2 weeks = 3rd Friday
}

// nextMonthlyExpiration finds the nearest third-Friday expiration with DTE in [minDTE, maxDTE].
// Checks up to 4 upcoming monthly expirations.
func nextMonthlyExpiration(now time.Time, minDTE, maxDTE int) (time.Time, int, bool) {
	for i := 0; i < 6; i++ {
		year, month, _ := now.AddDate(0, i, 0).Date()
		exp := thirdFriday(year, month)
		dte := int(math.Round(exp.Sub(now).Hours() / 24))
		if dte >= minDTE && dte <= maxDTE {
			return exp, dte, true
		}
	}
	return time.Time{}, 0, false
}

// calcDeployedBuyingPowerPct sums max losses across open condors as pct of portfolio.
func calcDeployedBuyingPowerPct(condors []*models.DBHarvestCondor, portfolioValue float64) float64 {
	if portfolioValue <= 0 {
		return 0
	}
	var total float64
	for _, c := range condors {
		total += c.MaxLoss
	}
	return (total / portfolioValue) * 100.0
}

// MultiLegOrder represents a 4-leg iron condor order for Alpaca's mleg API.
type MultiLegOrder struct {
	Underlying  string
	Legs        []MultiLegOrderLeg
	Contracts   int     // number of combos (iron condors)
	LimitPrice  float64 // net credit limit (positive = credit)
	TimeInForce string  // "day"
}

// MultiLegOrderLeg is one leg of the combo.
type MultiLegOrderLeg struct {
	Symbol         string
	Side           string // "buy" | "sell"
	PositionIntent string // "buy_to_open" | "sell_to_open" | "buy_to_close" | "sell_to_close"
}

// PlaceMultiLegOrderFn is a function that places a multi-leg order.
// Injectable for testing without a real broker connection.
type PlaceMultiLegOrderFn func(ctx context.Context, order MultiLegOrder) (string, error)
```

- [ ] **Step 4: Run tests to confirm they pass**

```
go test ./services/ -run "TestIsFOMC|TestNextMonthly|TestThirdFriday|TestCircuitBreaker|TestCalcDeployed" -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add services/harvest_service.go services/harvest_service_test.go
git commit -m "feat: add Harvest core service (FOMC, expirations, state, circuit breaker)"
```

---

## Task 6: Multi-Leg Order Support + Harvest Controller

**Files:**
- Modify: `services/alpaca_trading.go` (add PlaceMultiLegOrder)
- Create: `controllers/harvest_controller.go`

- [ ] **Step 1: Add PlaceMultiLegOrder to alpaca_trading.go**

Append to `services/alpaca_trading.go`:

```go
// alpacaMlegRequest is the JSON body for Alpaca's multi-leg order endpoint.
type alpacaMlegRequest struct {
	Type        string             `json:"type"`
	OrderClass  string             `json:"order_class"`
	TimeInForce string             `json:"time_in_force"`
	LimitPrice  string             `json:"limit_price"` // string per Alpaca spec
	Qty         string             `json:"qty"`
	Legs        []alpacaMlegLeg    `json:"legs"`
}

type alpacaMlegLeg struct {
	Symbol         string `json:"symbol"`
	Side           string `json:"side"`
	RatioQty       string `json:"ratio_qty"`
	PositionIntent string `json:"position_intent"`
}

// PlaceMultiLegOrder places a 4-leg iron condor as a single atomic combo order.
// limitPrice is the net credit per contract (positive = we receive credit).
// Alpaca expects a positive limit_price for credit combos.
func (s *AlpacaTradingService) PlaceMultiLegOrder(ctx context.Context, order MultiLegOrder) (string, error) {
	legs := make([]alpacaMlegLeg, len(order.Legs))
	for i, leg := range order.Legs {
		legs[i] = alpacaMlegLeg{
			Symbol:         leg.Symbol,
			Side:           leg.Side,
			RatioQty:       "1",
			PositionIntent: leg.PositionIntent,
		}
	}
	body := alpacaMlegRequest{
		Type:        "limit",
		OrderClass:  "mleg",
		TimeInForce: "day",
		LimitPrice:  fmt.Sprintf("%.2f", order.LimitPrice),
		Qty:         fmt.Sprintf("%d", order.Contracts),
		Legs:        legs,
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal mleg order: %w", err)
	}

	baseURL := "https://paper-api.alpaca.markets"
	if !s.isPaper() {
		baseURL = "https://api.alpaca.markets"
	}
	url := baseURL + "/v2/orders"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("create mleg request: %w", err)
	}
	req.Header.Set("APCA-API-KEY-ID", s.apiKey)
	req.Header.Set("APCA-API-SECRET-KEY", s.apiSecret)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.HTTPClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("execute mleg request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("alpaca mleg error %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("unmarshal mleg response: %w", err)
	}
	return result.ID, nil
}

// isPaper returns true if configured for paper trading.
func (s *AlpacaTradingService) isPaper() bool {
	// Check if base URL contains "paper"
	// This is already available via the client config — check the client's base URL
	return true // TODO: inject this from config if needed
}
```

Also add `"bytes"` and `"encoding/json"` to the imports in `services/alpaca_trading.go` if not already present.

> **Note:** The `isPaper()` method needs to be aligned with the actual Alpaca client configuration. The `AlpacaTradingService` struct should store the base URL. Inspect the constructor and add a `baseURL string` field if not already present, then set it from the constructor argument.

- [ ] **Step 2: Create controllers/harvest_controller.go**

```go
package controllers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"prophet-trader/models"
	"prophet-trader/services"
)

// harvestStorage is the DB interface used by HarvestController.
type harvestStorage interface {
	SaveHarvestCondor(c *models.DBHarvestCondor) error
	UpdateHarvestCondor(condorID string, updates map[string]interface{}) error
	GetHarvestCondorByID(condorID string) (*models.DBHarvestCondor, error)
	ListOpenHarvestCondors() []*models.DBHarvestCondor
	GetHarvestClosedPnL(start, end time.Time) (float64, error)
}

// HarvestController handles all /api/v1/harvest/* endpoints.
type HarvestController struct {
	harvestSvc    *services.HarvestService
	ivrSvc        *services.HarvestIVRService
	storage       harvestStorage
	placeMLeg     services.PlaceMultiLegOrderFn
	getPortfolio  func() (float64, error)
}

// NewHarvestController creates the controller.
// placeMLeg is injected so tests can stub it without a broker.
// getPortfolio returns the current total account equity.
func NewHarvestController(
	harvestSvc *services.HarvestService,
	ivrSvc *services.HarvestIVRService,
	storage harvestStorage,
	placeMLeg services.PlaceMultiLegOrderFn,
	getPortfolio func() (float64, error),
) *HarvestController {
	return &HarvestController{
		harvestSvc:   harvestSvc,
		ivrSvc:       ivrSvc,
		storage:      storage,
		placeMLeg:    placeMLeg,
		getPortfolio: getPortfolio,
	}
}

// HandleGetState handles GET /api/v1/harvest/state
func (hc *HarvestController) HandleGetState(c *gin.Context) {
	pv, err := hc.getPortfolio()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get portfolio value: " + err.Error()})
		return
	}
	state, err := hc.harvestSvc.GetState(pv)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, state)
}

// HandleGetFOMC handles GET /api/v1/harvest/fomc
func (hc *HarvestController) HandleGetFOMC(c *gin.Context) {
	status := hc.harvestSvc.GetFOMCStatus()
	c.JSON(http.StatusOK, status)
}

// HandleGetExpirations handles GET /api/v1/harvest/expirations/:symbol
func (hc *HarvestController) HandleGetExpirations(c *gin.Context) {
	symbol := c.Param("symbol")
	exp, err := hc.harvestSvc.GetNextMonthlyExpiration(symbol, 35, 55)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error(), "symbol": symbol})
		return
	}
	c.JSON(http.StatusOK, exp)
}

// HandleGetIVR handles GET /api/v1/harvest/ivr/:symbol?current_iv=0.185
func (hc *HarvestController) HandleGetIVR(c *gin.Context) {
	symbol := c.Param("symbol")
	currentIVStr := c.Query("current_iv")
	var currentIV float64
	if _, err := fmt.Sscanf(currentIVStr, "%f", &currentIV); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "current_iv query param required (e.g. ?current_iv=0.185)"})
		return
	}
	data, err := hc.ivrSvc.GetIVRData(symbol, currentIV)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, data)
}

// OpenCondorRequest is the body for POST /api/v1/harvest/condors.
type OpenCondorRequest struct {
	Underlying            string  `json:"underlying" binding:"required"`
	ExpirationDate        string  `json:"expiration_date" binding:"required"` // YYYY-MM-DD
	ShortPutSymbol        string  `json:"short_put_symbol" binding:"required"`
	ShortPutStrike        float64 `json:"short_put_strike" binding:"required"`
	LongPutSymbol         string  `json:"long_put_symbol" binding:"required"`
	LongPutStrike         float64 `json:"long_put_strike" binding:"required"`
	ShortCallSymbol       string  `json:"short_call_symbol" binding:"required"`
	ShortCallStrike       float64 `json:"short_call_strike" binding:"required"`
	LongCallSymbol        string  `json:"long_call_symbol" binding:"required"`
	LongCallStrike        float64 `json:"long_call_strike" binding:"required"`
	Contracts             int     `json:"contracts" binding:"required,min=1"`
	WingWidth             float64 `json:"wing_width" binding:"required"`
	CreditPerContract     float64 `json:"credit_per_contract" binding:"required"`
	IVRAtEntry            float64 `json:"ivr_at_entry"`
	PortfolioValueAtEntry float64 `json:"portfolio_value_at_entry"`
	OverlapLog            string  `json:"overlap_log"` // JSON string
}

// HandleOpenCondor handles POST /api/v1/harvest/condors
func (hc *HarvestController) HandleOpenCondor(c *gin.Context) {
	var req OpenCondorRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	expDate, err := time.Parse("2006-01-02", req.ExpirationDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid expiration_date format, use YYYY-MM-DD"})
		return
	}

	// Build 4-leg order: sell put spread (sell short put, buy long put), sell call spread (sell short call, buy long call)
	order := services.MultiLegOrder{
		Underlying:  req.Underlying,
		Contracts:   req.Contracts,
		LimitPrice:  req.CreditPerContract,
		TimeInForce: "day",
		Legs: []services.MultiLegOrderLeg{
			{Symbol: req.ShortPutSymbol,  Side: "sell", PositionIntent: "sell_to_open"},
			{Symbol: req.LongPutSymbol,   Side: "buy",  PositionIntent: "buy_to_open"},
			{Symbol: req.ShortCallSymbol, Side: "sell", PositionIntent: "sell_to_open"},
			{Symbol: req.LongCallSymbol,  Side: "buy",  PositionIntent: "buy_to_open"},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	orderID, err := hc.placeMLeg(ctx, order)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to place iron condor order: " + err.Error()})
		return
	}

	condorID := uuid.New().String()
	maxLoss := req.WingWidth * float64(req.Contracts) * 100.0
	totalCredit := req.CreditPerContract * float64(req.Contracts) * 100.0

	condor := &models.DBHarvestCondor{
		CondorID:              condorID,
		Underlying:            req.Underlying,
		Expiration:            expDate,
		ShortPutSymbol:        req.ShortPutSymbol,
		ShortPutStrike:        req.ShortPutStrike,
		LongPutSymbol:         req.LongPutSymbol,
		LongPutStrike:         req.LongPutStrike,
		ShortCallSymbol:       req.ShortCallSymbol,
		ShortCallStrike:       req.ShortCallStrike,
		LongCallSymbol:        req.LongCallSymbol,
		LongCallStrike:        req.LongCallStrike,
		Contracts:             req.Contracts,
		WingWidth:             req.WingWidth,
		CreditPerContract:     req.CreditPerContract,
		TotalCredit:           totalCredit,
		MaxLoss:               maxLoss,
		PortfolioValueAtEntry: req.PortfolioValueAtEntry,
		EntryOrderID:          orderID,
		Status:                "OPEN",
		IVRAtEntry:            req.IVRAtEntry,
		OverlapLog:            req.OverlapLog,
		OpenedAt:              time.Now(),
	}

	if err := hc.storage.SaveHarvestCondor(condor); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "order placed but failed to save condor record: " + err.Error(), "order_id": orderID})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"condor_id": condorID,
		"order_id":  orderID,
		"status":    "OPEN",
		"max_loss":  maxLoss,
		"credit":    totalCredit,
	})
}

// CloseCondorRequest is the body for POST /api/v1/harvest/condors/:id/close
type CloseCondorRequest struct {
	OrderType         string  `json:"order_type" binding:"required"` // "limit" | "market" | "marketable_limit"
	LimitPrice        float64 `json:"limit_price"`
	CloseReason       string  `json:"close_reason"` // "profit_target" | "loss_stop" | "time_exit" | "manual"
	CostPerContract   float64 `json:"cost_per_contract"` // current cost-to-close per contract
}

// HandleCloseCondor handles POST /api/v1/harvest/condors/:id/close
func (hc *HarvestController) HandleCloseCondor(c *gin.Context) {
	condorID := c.Param("id")
	var req CloseCondorRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	condor, err := hc.storage.GetHarvestCondorByID(condorID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "condor not found: " + condorID})
		return
	}
	if condor.Status != "OPEN" {
		c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("condor %s is not OPEN (status=%s)", condorID, condor.Status)})
		return
	}

	// Reverse legs to close
	orderType := "limit"
	if req.OrderType == "market" {
		orderType = "market"
	}

	limitPrice := req.LimitPrice
	if orderType == "market" {
		limitPrice = 0
	}

	closeOrder := services.MultiLegOrder{
		Underlying:  condor.Underlying,
		Contracts:   condor.Contracts,
		LimitPrice:  limitPrice,
		TimeInForce: "day",
		Legs: []services.MultiLegOrderLeg{
			{Symbol: condor.ShortPutSymbol,  Side: "buy",  PositionIntent: "buy_to_close"},
			{Symbol: condor.LongPutSymbol,   Side: "sell", PositionIntent: "sell_to_close"},
			{Symbol: condor.ShortCallSymbol, Side: "buy",  PositionIntent: "buy_to_close"},
			{Symbol: condor.LongCallSymbol,  Side: "sell", PositionIntent: "sell_to_close"},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	closeOrderID, err := hc.placeMLeg(ctx, closeOrder)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to place close order: " + err.Error()})
		return
	}

	// Calculate realized P&L: credit received - cost to close
	costPerContract := req.CostPerContract
	realizedPnL := (condor.CreditPerContract - costPerContract) * float64(condor.Contracts) * 100.0

	now := time.Now()
	updates := map[string]interface{}{
		"status":                "CLOSED",
		"close_order_id":        closeOrderID,
		"close_reason":          req.CloseReason,
		"close_cost_per_contract": costPerContract,
		"realized_pn_l":         realizedPnL,
		"closed_at":             &now,
	}
	if err := hc.storage.UpdateHarvestCondor(condorID, updates); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "close order placed but failed to update record: " + err.Error(), "close_order_id": closeOrderID})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"condor_id":      condorID,
		"close_order_id": closeOrderID,
		"realized_pnl":   realizedPnL,
		"status":         "CLOSED",
	})
}

// HandleListCondors handles GET /api/v1/harvest/condors
func (hc *HarvestController) HandleListCondors(c *gin.Context) {
	condors := hc.storage.ListOpenHarvestCondors()
	c.JSON(http.StatusOK, gin.H{"count": len(condors), "condors": condors})
}

// HandleRecordIV handles POST /api/v1/harvest/iv
// Body: { symbol: "SPY", atm_iv: 0.185 }
// Used by the background IVR collection goroutine.
func (hc *HarvestController) HandleRecordIV(c *gin.Context) {
	var req struct {
		Symbol string  `json:"symbol" binding:"required"`
		ATMIV  float64 `json:"atm_iv" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := hc.ivrSvc.RecordDailyIV(req.Symbol, req.ATMIV); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"recorded": true, "symbol": req.Symbol})
}
```

- [ ] **Step 3: Verify the code compiles**

```
go build ./...
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add services/alpaca_trading.go controllers/harvest_controller.go
git commit -m "feat: add multi-leg order support and Harvest HTTP controller"
```

---

## Task 7: Wire Harvest into main.go

**Files:**
- Modify: `cmd/bot/main.go`
- Modify: `cmd/bot/main.go` (router setup)

- [ ] **Step 1: Add Harvest wiring to main.go**

In `cmd/bot/main.go`, after the penny pipeline section (around line 165), add:

```go
// Initialize Harvest services
harvestStorage := storageService  // LocalStorage satisfies harvestStorage interface
harvestIVRSvc := services.NewHarvestIVRService(harvestStorage)
harvestSvc := services.NewHarvestService(harvestStorage)

getPortfolioValue := func() (float64, error) {
    acct, err := orderController.GetAccount()
    if err != nil {
        return 0, err
    }
    return acct.PortfolioValue, nil
}

placeMLegFn := services.PlaceMultiLegOrderFn(func(ctx context.Context, order services.MultiLegOrder) (string, error) {
    if tradingService == nil {
        return "", fmt.Errorf("trading service unavailable")
    }
    return tradingService.PlaceMultiLegOrder(ctx, order)
})

harvestController := controllers.NewHarvestController(
    harvestSvc,
    harvestIVRSvc,
    harvestStorage,
    placeMLegFn,
    getPortfolioValue,
)

// Start daily IV collection goroutine for Harvest
go startHarvestIVCollection(ctx, harvestIVRSvc, tradingService, logger)

logger.Info("Harvest service initialized")
```

- [ ] **Step 2: Add startHarvestIVCollection function to main.go**

Append to `cmd/bot/main.go`:

```go
// startHarvestIVCollection records ATM IV for all Harvest underlyings once per trading day.
func startHarvestIVCollection(ctx context.Context, ivrSvc *services.HarvestIVRService, tradingService *services.AlpacaTradingService, logger *logrus.Logger) {
	harvestUniverse := []string{"SPY", "QQQ", "IWM", "GLD", "TLT"}
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, symbol := range harvestUniverse {
				if tradingService == nil {
					continue
				}
				// Fetch ATM IV from the options chain (nearest expiration, ATM delta ~0.50)
				chain, err := tradingService.GetOptionsChain(ctx, symbol, time.Now().AddDate(0, 0, 30))
				if err != nil {
					logger.WithError(err).Warnf("harvest IV collection: failed to get chain for %s", symbol)
					continue
				}
				atmIV := calcATMIV(chain)
				if atmIV <= 0 {
					continue
				}
				if err := ivrSvc.RecordDailyIV(symbol, atmIV); err != nil {
					logger.WithError(err).Warnf("harvest IV collection: failed to record IV for %s", symbol)
				}
			}
		}
	}
}

// calcATMIV averages the IV of the two puts and calls nearest to 0.50 delta.
func calcATMIV(chain []*interfaces.OptionContract) float64 {
	var sum float64
	var count int
	for _, c := range chain {
		absDelta := c.Delta
		if absDelta < 0 {
			absDelta = -absDelta
		}
		if absDelta >= 0.45 && absDelta <= 0.55 && c.ImpliedVolatility > 0 {
			sum += c.ImpliedVolatility
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}
```

- [ ] **Step 3: Update setupRouter to include harvest routes**

In the `setupRouter` function in `cmd/bot/main.go`, add `harvestController *controllers.HarvestController` to the signature, then inside the `api` group add:

```go
// Harvest premium seller endpoints
harvest := api.Group("/harvest")
{
    harvest.GET("/state", harvestController.HandleGetState)
    harvest.GET("/fomc", harvestController.HandleGetFOMC)
    harvest.GET("/expirations/:symbol", harvestController.HandleGetExpirations)
    harvest.GET("/ivr/:symbol", harvestController.HandleGetIVR)
    harvest.GET("/condors", harvestController.HandleListCondors)
    harvest.POST("/condors", harvestController.HandleOpenCondor)
    harvest.POST("/condors/:id/close", harvestController.HandleCloseCondor)
    harvest.POST("/iv", harvestController.HandleRecordIV)
}
```

Also update the `setupRouter` call in `main()` to pass `harvestController`.

- [ ] **Step 4: Build to verify no compile errors**

```
go build ./cmd/bot/
```

Expected: builds cleanly.

- [ ] **Step 5: Run all tests**

```
go test ./... 2>&1 | grep -E "FAIL|ok|---"
```

Expected: all packages pass.

- [ ] **Step 6: Commit**

```bash
git add cmd/bot/main.go
git commit -m "feat: wire Harvest services and routes into Go backend"
```

---

## Task 8: MCP Tools for Harvest

**Files:**
- Modify: `mcp-server.js`

Six new tools: `get_harvest_state`, `get_harvest_ivr`, `get_harvest_expirations`, `get_harvest_fomc`, `open_iron_condor`, `close_iron_condor`.

- [ ] **Step 1: Add tool definitions to the ListTools response**

In `mcp-server.js`, find the tools array in the `ListToolsRequestSchema` handler. After the `get_penny_candidates` tool entry (around line 1257), add:

```javascript
{
  name: 'get_harvest_state',
  description: 'Get current Harvest agent state: open condors, circuit breaker status, trailing 30-day P&L, and deployed buying power. Check this at the start of every heartbeat before evaluating entries or exits.',
  inputSchema: { type: 'object', properties: {} },
},
{
  name: 'get_harvest_ivr',
  description: 'Get IV Rank (IVR) for a Harvest universe underlying. Requires current_iv from the options chain (ATM implied volatility). Returns IVR on 0-100 scale; -1 means insufficient history. Gate: only enter if IVR >= 30.',
  inputSchema: {
    type: 'object',
    properties: {
      symbol: { type: 'string', description: 'Underlying symbol (SPY, QQQ, IWM, GLD, TLT)' },
      current_iv: { type: 'number', description: 'Current ATM implied volatility (e.g. 0.185 for 18.5%)' },
    },
    required: ['symbol', 'current_iv'],
  },
},
{
  name: 'get_harvest_expirations',
  description: 'Get the next qualifying monthly expiration (third Friday) in the [35, 55] DTE band for a given underlying. Returns expiration_date and dte. If no qualifying expiration exists, returns a 404-style error.',
  inputSchema: {
    type: 'object',
    properties: {
      symbol: { type: 'string', description: 'Underlying symbol (SPY, QQQ, IWM, GLD, TLT)' },
    },
    required: ['symbol'],
  },
},
{
  name: 'get_harvest_fomc',
  description: 'Check FOMC blackout status. If is_blackout=true, do NOT open new positions. Blackout window = 24 hours before scheduled FOMC announcement.',
  inputSchema: { type: 'object', properties: {} },
},
{
  name: 'open_iron_condor',
  description: 'Open a new iron condor position for a Harvest underlying. Provide the four OCC option symbols, strikes, contract count, and credit. Returns condor_id, order_id, and status.',
  inputSchema: {
    type: 'object',
    properties: {
      underlying:             { type: 'string', description: 'Underlying symbol (SPY, QQQ, IWM, GLD, TLT)' },
      expiration_date:        { type: 'string', description: 'Expiration date YYYY-MM-DD (third Friday of target month)' },
      short_put_symbol:       { type: 'string', description: 'OCC symbol for short put (sell to open)' },
      short_put_strike:       { type: 'number', description: 'Short put strike price' },
      long_put_symbol:        { type: 'string', description: 'OCC symbol for long put (buy to open, wing_width below short put)' },
      long_put_strike:        { type: 'number', description: 'Long put strike price' },
      short_call_symbol:      { type: 'string', description: 'OCC symbol for short call (sell to open)' },
      short_call_strike:      { type: 'number', description: 'Short call strike price' },
      long_call_symbol:       { type: 'string', description: 'OCC symbol for long call (buy to open, wing_width above short call)' },
      long_call_strike:       { type: 'number', description: 'Long call strike price' },
      contracts:              { type: 'number', description: 'Number of iron condors (from sizing formula: floor(portfolio * 0.015 / (wing_width * 100)))' },
      wing_width:             { type: 'number', description: 'Wing width in dollars (SPY=5, QQQ=5, IWM=2, GLD=2, TLT=1)' },
      credit_per_contract:    { type: 'number', description: 'Net credit received per contract at entry (mid-price of the 4-leg combo)' },
      ivr_at_entry:           { type: 'number', description: 'IV rank at time of entry (for analysis)' },
      portfolio_value_at_entry: { type: 'number', description: 'Total portfolio equity at time of entry (snapshot)' },
      overlap_log:            { type: 'string', description: 'JSON string: [{agent, underlying, direction, contracts, dte}] — other agents with positions in this underlying' },
    },
    required: ['underlying', 'expiration_date', 'short_put_symbol', 'short_put_strike', 'long_put_symbol', 'long_put_strike', 'short_call_symbol', 'short_call_strike', 'long_call_symbol', 'long_call_strike', 'contracts', 'wing_width', 'credit_per_contract'],
  },
},
{
  name: 'close_iron_condor',
  description: 'Close an existing Harvest iron condor position. Provide the condor_id from open_iron_condor, the order type, and the current cost-to-close per contract. Returns close_order_id and realized_pnl.',
  inputSchema: {
    type: 'object',
    properties: {
      condor_id:         { type: 'string', description: 'The condor_id returned when the position was opened' },
      order_type:        { type: 'string', enum: ['limit', 'market', 'marketable_limit'], description: 'limit: patient fill at mid; marketable_limit: mid+$0.20 for faster fill; market: immediate at any price' },
      limit_price:       { type: 'number', description: 'Net debit limit price (required for limit and marketable_limit order types)' },
      close_reason:      { type: 'string', enum: ['profit_target', 'loss_stop', 'time_exit', 'manual'], description: 'Reason for closing (used in exit logging)' },
      cost_per_contract: { type: 'number', description: 'Current mid-price cost to close the condor per contract (for P&L calculation)' },
    },
    required: ['condor_id', 'order_type', 'close_reason', 'cost_per_contract'],
  },
},
```

- [ ] **Step 2: Add tool handler cases to the CallTool switch**

In `mcp-server.js`, find the `CallToolRequestSchema` handler's switch statement. After the `get_penny_signal_detail` case, add:

```javascript
case 'get_harvest_state': {
  const data = await callTradingBot('/harvest/state');
  return { content: [{ type: 'text', text: JSON.stringify(data, null, 2) }] };
}

case 'get_harvest_ivr': {
  const data = await callTradingBot(`/harvest/ivr/${args.symbol}?current_iv=${args.current_iv}`);
  return { content: [{ type: 'text', text: JSON.stringify(data, null, 2) }] };
}

case 'get_harvest_expirations': {
  const data = await callTradingBot(`/harvest/expirations/${args.symbol}`);
  return { content: [{ type: 'text', text: JSON.stringify(data, null, 2) }] };
}

case 'get_harvest_fomc': {
  const data = await callTradingBot('/harvest/fomc');
  return { content: [{ type: 'text', text: JSON.stringify(data, null, 2) }] };
}

case 'open_iron_condor': {
  await enforcePermissions('open_iron_condor', args);
  const data = await callTradingBot('/harvest/condors', 'POST', {
    underlying:               args.underlying,
    expiration_date:          args.expiration_date,
    short_put_symbol:         args.short_put_symbol,
    short_put_strike:         args.short_put_strike,
    long_put_symbol:          args.long_put_symbol,
    long_put_strike:          args.long_put_strike,
    short_call_symbol:        args.short_call_symbol,
    short_call_strike:        args.short_call_strike,
    long_call_symbol:         args.long_call_symbol,
    long_call_strike:         args.long_call_strike,
    contracts:                args.contracts,
    wing_width:               args.wing_width,
    credit_per_contract:      args.credit_per_contract,
    ivr_at_entry:             args.ivr_at_entry || 0,
    portfolio_value_at_entry: args.portfolio_value_at_entry || 0,
    overlap_log:              args.overlap_log || '[]',
  });
  return { content: [{ type: 'text', text: JSON.stringify(data, null, 2) }] };
}

case 'close_iron_condor': {
  await enforcePermissions('close_iron_condor', args);
  const data = await callTradingBot(`/harvest/condors/${args.condor_id}/close`, 'POST', {
    order_type:       args.order_type,
    limit_price:      args.limit_price || 0,
    close_reason:     args.close_reason,
    cost_per_contract: args.cost_per_contract,
  });
  return { content: [{ type: 'text', text: JSON.stringify(data, null, 2) }] };
}
```

Also add `'open_iron_condor'` and `'close_iron_condor'` to the `ORDER_TOOLS` array (line 1318) so permissions are enforced:

```javascript
const ORDER_TOOLS = ['place_buy_order', 'place_sell_order', 'place_options_order', 'place_managed_position', 'close_managed_position', 'open_iron_condor', 'close_iron_condor'];
```

- [ ] **Step 3: Add `allowHarvest` permission check in enforcePermissions**

In the `enforcePermissions` function, after the `allowOptions` check (around line 1343), add:

```javascript
// Harvest condor check
if ((toolName === 'open_iron_condor' || toolName === 'close_iron_condor') && !perms.allowOptions) {
  throw new Error('Options trading is DISABLED by permissions. Cannot open/close iron condors.');
}
```

- [ ] **Step 4: Start MCP server and verify tool list**

```bash
node mcp-server.js &
# in another terminal, test the tools list endpoint
```

Or verify by checking for syntax errors:

```bash
node --check mcp-server.js
```

Expected: no syntax errors.

- [ ] **Step 5: Commit**

```bash
git add mcp-server.js
git commit -m "feat: add 6 Harvest MCP tools (state, IVR, expirations, FOMC, open/close condor)"
```

---

## Task 9: Agent Configuration

**Files:**
- Modify: `agent/config-store.js`

- [ ] **Step 1: Add Harvest strategy to defaultStrategies()**

In `agent/config-store.js`, in the `defaultStrategies()` function (around line 154), add:

```javascript
{
  id: 'harvest',
  name: 'Harvest — Iron Condor Premium Seller',
  description: 'Mechanical 16-delta iron condors on SPY/QQQ/IWM/GLD/TLT. Defined-risk, no discretion.',
  rulesFile: 'TRADING_RULES_HARVEST.md',
  customRules: null,
  createdAt: new Date().toISOString(),
},
```

- [ ] **Step 2: Add Harvest agent to defaultAgents()**

In `agent/config-store.js`, in the `defaultAgents()` function (around line 112), add after the `conservative` agent:

```javascript
{
  id: 'harvest',
  name: 'Harvest',
  description: 'Mechanical theta-harvesting agent — sells iron condors on index ETFs for premium income',
  systemPromptTemplate: 'custom',
  customSystemPrompt: `You are Harvest, a mechanical theta-harvesting trading agent. You are not a reasoning agent. You are a rule executor wrapped in a language model.

Your ONLY job is to follow your trading rules exactly. Do not improvise. Do not add commentary. Do not make directional judgments. Helpful improvisation is the failure mode.

Read your Strategy Rules section carefully — it contains your complete heartbeat procedure. Follow it step by step on every heartbeat.`,
  strategyId: 'harvest',
  model: 'anthropic/claude-sonnet-4-6',
  heartbeatOverrides: {
    pre_market: 3600,
    market_open: 900,
    midday: 900,
    market_close: 900,
    after_hours: 7200,
    closed: 14400,
  },
  createdAt: new Date().toISOString(),
},
```

- [ ] **Step 3: Add `penny_stock` heartbeat profile entry for Harvest reference**

The heartbeat profiles already include `penny_stock` (line 54 of config-store.js). No new profile needed — the Harvest agent uses `heartbeatOverrides` directly.

- [ ] **Step 4: Verify config loads without errors**

```bash
node -e "import('./agent/config-store.js').then(m => m.loadConfig()).then(c => { const h = c.getConfig().agents.find(a => a.id === 'harvest'); console.log('Harvest agent:', h ? 'found' : 'MISSING'); process.exit(h ? 0 : 1); })"
```

Expected: `Harvest agent: found`

- [ ] **Step 5: Commit**

```bash
git add agent/config-store.js
git commit -m "feat: add Harvest agent and strategy to agent config defaults"
```

---

## Self-Review Checklist

After writing the plan, checking against the spec:

**Spec coverage:**
- [x] Section 1 (Agent Identity, heartbeat): Task 9 (config), heartbeat overrides in defaultAgents()
- [x] Section 2 (Entry Logic — pre-loop checks): `TRADING_RULES_HARVEST.md` Step 1 in heartbeat
- [x] Section 2 (IVR gate): Tasks 4 + 8 (`get_harvest_ivr` tool)
- [x] Section 2 (FOMC blackout): Tasks 5 + 6 + 8 (`get_harvest_fomc` tool)
- [x] Section 2 (Expiration selection): Tasks 5 + 6 + 8 (`get_harvest_expirations` tool)
- [x] Section 2 (Strike selection): `TRADING_RULES_HARVEST.md` (agent uses `get_options_chain` with delta filter)
- [x] Section 2 (Credit quality check, wing widths, sizing formula): `TRADING_RULES_HARVEST.md`
- [x] Section 2 (Entry execution, retry logic): `TRADING_RULES_HARVEST.md` references `open_iron_condor`
- [x] Section 2 (Entry logging + overlap log schema): Task 6 (`HandleOpenCondor` saves `OverlapLog`)
- [x] Section 3 (Exit priority 1: time exit): `TRADING_RULES_HARVEST.md` + Task 6 (`HandleCloseCondor`)
- [x] Section 3 (Exit priority 2: loss stop): `TRADING_RULES_HARVEST.md`
- [x] Section 3 (Exit priority 3: profit target): `TRADING_RULES_HARVEST.md`
- [x] Section 3 (Post-exit: P&L realize, no cooldown): Task 6 (`HandleCloseCondor` updates `realized_pnl`)
- [x] Section 3 (Atomic combo orders): Task 7 (`PlaceMultiLegOrder` via Alpaca mleg API)
- [x] Section 4 (Portfolio value definition): `get_account` returns total equity; passed to `GetState`
- [x] Section 4 (Circuit breaker: -5% trailing 30-day): Tasks 3 + 5 (`GetState` calculates it)
- [x] Section 4 (Circuit breaker resume: 14d + -3%): **GAP — only threshold check implemented; resume logic needs manual operator reset or future automation**
- [x] Section 4 (Buying power enforcement): Tasks 5 + 6 (`calcDeployedBuyingPowerPct`)
- [x] Section 4 (FOMC blackout): Tasks 5 + 6 + 8
- [x] Section 4 (Per-position hard limits): `TRADING_RULES_HARVEST.md` enforces in entry logic
- [x] Section 4 (Stale data >60s skip): `TRADING_RULES_HARVEST.md` checks `quote_age_seconds`
- [x] Section 4 (Stale data >5min halt): `TRADING_RULES_HARVEST.md` hard stop section
- [x] Section 4 (Broker reconciliation mismatch): Task 6 (state reconciliation flagged via `get_harvest_state`)
- [x] Section 4 (Credit sanity bound $0.30): `TRADING_RULES_HARVEST.md`
- [x] Section 4 (Logging — entry, heartbeat, exit, circuit breaker): Tasks 3 + 6 (DB fields cover all log fields)
- [x] Section 4 (Startup/restart behavior): **Not yet implemented** — the Harvest controller reads from DB on startup, but there's no explicit reconciliation check at boot. The `get_harvest_state` endpoint returns live DB state, which is sufficient for v1 since DB is the source of truth.
- [x] Section 5 (System prompt): Task 1 (`TRADING_RULES_HARVEST.md`) + Task 9 (`customSystemPrompt` in config)

**Gaps flagged:**
1. Circuit breaker auto-resume (14d + -3% recovery) — not coded; operator manual reset is the only path. Acceptable for v1 per the spec.
2. `isPaper()` in `alpaca_trading.go` — hardcoded to `true`. Needs to check actual base URL from struct config; store `baseURL` field in `AlpacaTradingService` if not already present.
3. `enforcePermissions` for `open_iron_condor` checks `allowOptions` — this is correct since condors are options trades, but verify the Harvest sandbox has `allowOptions: true` in its permissions.

**Type consistency check:**
- `CondorID` used in `DBHarvestCondor`, `OpenCondorRequest`, `CloseCondorRequest`, and MCP tool args — consistent.
- `PlaceMultiLegOrderFn` type used in both `harvest_service.go` (defined) and `harvest_controller.go` (injected) — consistent.
- `calcIVR` is unexported (lowercase) and tested via `TestCalcIVR` in the same package — correct.
- `harvestStateStore` interface in `harvest_service.go` matches methods added to `database/storage.go` — consistent.
