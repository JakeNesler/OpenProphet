# CLAUDE.md — OpenProphet IBKR Fork

> **Start every session by reading `PROGRESS.md`** — it tracks exactly where we are.
> Find the first ⬜ step. That's the current task. Do only that step, test it, then stop.

## Project Overview

This is a fork of [OpenProphet](https://github.com/natslash/OpenProphet), an autonomous AI trading agent. We are making three changes:

1. **Replace OpenCode CLI with Claude Code** (`claude -p` headless mode)
2. **Replace Alpaca broker with IBKR** via a custom Go TWS API wrapper (from scratch, no third-party library)
3. **Instrument-agnostic design** — OESX index options, US/EU stocks, futures, all through one broker interface

The Go backend (Gin, port 4534) is the execution engine. The Node.js agent server, MCP server, and dashboard are largely unchanged.

---

## Branch & Repo

- **Branch:** `feature/ibkr-porting`
- **Base:** `main` (from natslash/OpenProphet fork)
- **Paper trading first:** IB Gateway port 4002 (paper), 4001 (live)
- **TWS API version:** 10.37.02 (matches the Java jar already installed locally)

---

## Architecture

```
Dashboard (Node.js, port 3737)
    │
    ├── Agent Harness (harness.js) ─── claude -p subprocess (was: opencode run)
    │
    └── MCP Server (mcp-server.js, stdio) ─── 45+ tools
            │
            └── HTTP ──► Go Backend (Gin, port 4534)
                            │
                            ├── controllers/     ← HTTP handlers (instrument-agnostic)
                            ├── services/        ← Business logic
                            │     ├── broker.go          ← BrokerService interface
                            │     ├── market_data.go     ← MarketDataService interface
                            │     ├── ibkr_broker.go     ← IBKR implementation
                            │     ├── ibkr_market_data.go
                            │     ├── position_manager.go
                            │     ├── news_service.go    ← European sources
                            │     └── technical_analysis.go
                            ├── tws/             ← NEW: Go TWS API wrapper (from scratch)
                            │     ├── client.go          ← TCP connection, handshake, message I/O
                            │     ├── decoder.go         ← Inbound message parser
                            │     ├── encoder.go         ← Outbound message builder
                            │     ├── contract.go        ← Contract type (maps to Instrument)
                            │     ├── order.go           ← Order type
                            │     ├── wrapper.go         ← EWrapper callback interface
                            │     ├── dispatcher.go      ← reqId→channel registry
                            │     └── constants.go       ← Message IDs, tick types
                            ├── interfaces/      ← Go type definitions
                            ├── models/          ← Database models
                            ├── database/        ← SQLite
                            └── config/          ← Environment config
```

---

## Critical Design Decisions

### 1. TWS Wire Protocol (from scratch in Go)

The TWS API uses a **length-prefixed, null-delimited text protocol** over TCP:
- **Handshake:** Send `API\0` then `v100..180\0` (version range)
- **Messages:** `<4-byte big-endian length><message body>`
- **Message body:** Null-delimited (`\0`) text fields parsed sequentially
- **No formal spec exists** — reverse-engineer from Java source (`EClient.java`, `EDecoder.java`)

Reference: TWS API 10.37.02 Java source at `~/IBJts/source/JavaClient/`

### 2. Callback → Channel Pattern (Go Dispatcher)

TWS API is callback-driven (EWrapper). In Go, we translate this to **channels**:

```go
// dispatcher.go — maps reqId to response channels
type Dispatcher struct {
    mu       sync.RWMutex
    pending  map[int64]chan any  // reqId → response channel
    nextId   int64              // seeded from nextValidId callback
}

func (d *Dispatcher) Register(reqId int64) <-chan any {
    ch := make(chan any, 16) // buffered for multi-message responses
    d.mu.Lock()
    d.pending[reqId] = ch
    d.mu.Unlock()
    return ch
}

func (d *Dispatcher) Dispatch(reqId int64, msg any) {
    d.mu.RLock()
    ch, ok := d.pending[reqId]
    d.mu.RUnlock()
    if ok {
        ch <- msg
    }
}

func (d *Dispatcher) Complete(reqId int64) {
    d.mu.Lock()
    if ch, ok := d.pending[reqId]; ok {
        close(ch)
        delete(d.pending, reqId)
    }
    d.mu.Unlock()
}
```

This is the same pattern as the Java `IbkrDispatcher` with `ConcurrentHashMap<reqId, CompletableFuture>`, translated to Go idioms.

### 3. Order ID Management

```go
// Order IDs are seeded from nextValidId callback on connect
// Must be monotonically increasing, unique per account lifetime
// Use atomic increment after initial seed

type OrderIdManager struct {
    nextId atomic.Int64
}

func (m *OrderIdManager) Seed(id int64) { m.nextId.Store(id) }
func (m *OrderIdManager) Next() int64    { return m.nextId.Add(1) - 1 }
```

### 4. Known TWS API 10.37.02 Quirks

- `cancelOrder` requires `OrderCancel` struct (not just orderId) — changed in recent versions
- Use `reqAllOpenOrders()` for order status polling, not `reqOrderStatus()`
- Streaming market data (reqMktData) preferred over snapshots for Greeks
- `nextValidId` callback fires on connect — must wait for it before placing orders
- Pacing: max 50 messages/second to TWS, historical data has 60-req-per-10-min limit

---

## Go Interface Contracts

### Instrument (universal representation)

```go
// interfaces/instrument.go
package interfaces

type InstrumentType string

const (
    InstrumentStock       InstrumentType = "STK"
    InstrumentOption      InstrumentType = "OPT"
    InstrumentFuture      InstrumentType = "FUT"
    InstrumentIndexOption InstrumentType = "IND_OPT" // maps to FOP or OPT on index
    InstrumentForex       InstrumentType = "CASH"
    InstrumentIndex       InstrumentType = "IND"
)

type OptionRight string

const (
    Call OptionRight = "C"
    Put  OptionRight = "P"
)

type Instrument struct {
    Symbol        string         `json:"symbol"`
    Type          InstrumentType `json:"type"`
    Exchange      string         `json:"exchange"`       // EUREX, SMART, NYSE, etc.
    Currency      string         `json:"currency"`       // EUR, USD, GBP
    Expiry        string         `json:"expiry"`         // YYYYMMDD for derivatives
    Strike        float64        `json:"strike"`         // for options
    Right         OptionRight    `json:"right"`          // C or P
    Multiplier    float64        `json:"multiplier"`     // 10 for OESX, 100 for US options
    LocalSymbol   string         `json:"localSymbol"`    // exchange-specific symbol
    ConId         int64          `json:"conId"`          // IBKR contract ID (0 if unknown)
    TradingClass  string         `json:"tradingClass"`   // OESX, SPX, etc.
    PrimaryExch   string         `json:"primaryExch"`    // disambiguation for SMART routing
}

// No convenience constructors here — this is the interface layer.
// Product-specific presets (OESX defaults, US option defaults, etc.)
// belong in config, seed data, or a separate presets/ package.
// Callers construct Instrument directly:
//
//   inst := Instrument{
//       Symbol: "ESTX50", Type: InstrumentIndexOption,
//       Exchange: "EUREX", Currency: "EUR",
//       Expiry: "20260620", Strike: 5200, Right: Call,
//       Multiplier: 10, TradingClass: "OESX",
//   }
```

### BrokerService (order execution)

```go
// interfaces/broker.go
package interfaces

import "context"

type OrderSide string
type OrderType string
type TimeInForce string

const (
    Buy  OrderSide = "BUY"
    Sell OrderSide = "SELL"

    Market     OrderType = "MKT"
    Limit      OrderType = "LMT"
    Stop       OrderType = "STP"
    StopLimit  OrderType = "STP LMT"
    TrailingStop OrderType = "TRAIL"

    Day TimeInForce = "DAY"
    GTC TimeInForce = "GTC"
    IOC TimeInForce = "IOC"
    GTD TimeInForce = "GTD"
)

type OrderRequest struct {
    Instrument  Instrument  `json:"instrument"`
    Side        OrderSide   `json:"side"`
    Quantity    float64     `json:"quantity"`
    Type        OrderType   `json:"orderType"`
    LimitPrice  float64     `json:"limitPrice,omitempty"`
    StopPrice   float64     `json:"stopPrice,omitempty"`
    TIF         TimeInForce `json:"tif"`
    OutsideRTH  bool        `json:"outsideRth,omitempty"`
}

type OrderStatus struct {
    OrderId     int64       `json:"orderId"`
    Instrument  Instrument  `json:"instrument"`
    Side        OrderSide   `json:"side"`
    Quantity    float64     `json:"quantity"`
    Filled      float64     `json:"filled"`
    AvgPrice    float64     `json:"avgFillPrice"`
    Status      string      `json:"status"`      // Submitted, Filled, Cancelled, etc.
    Type        OrderType   `json:"orderType"`
    LimitPrice  float64     `json:"limitPrice"`
    ParentId    int64       `json:"parentId,omitempty"`
    CreateTime  string      `json:"createTime"`
}

type Position struct {
    Instrument    Instrument `json:"instrument"`
    Quantity      float64    `json:"quantity"`
    AvgCost       float64    `json:"avgCost"`
    MarketValue   float64    `json:"marketValue"`
    UnrealizedPnL float64    `json:"unrealizedPnl"`
    RealizedPnL   float64    `json:"realizedPnl"`
    Account       string     `json:"account"`
}

type AccountSummary struct {
    AccountId       string  `json:"accountId"`
    NetLiquidation  float64 `json:"netLiquidation"`
    TotalCash       float64 `json:"totalCash"`
    BuyingPower     float64 `json:"buyingPower"`
    GrossPosition   float64 `json:"grossPosition"`
    MaintMargin     float64 `json:"maintMargin"`
    InitMargin      float64 `json:"initMargin"`
    UnrealizedPnL   float64 `json:"unrealizedPnl"`
    RealizedPnL     float64 `json:"realizedPnl"`
    Currency        string  `json:"currency"`
}

type BrokerService interface {
    // Connection
    Connect(ctx context.Context) error
    Disconnect() error
    IsConnected() bool

    // Account
    GetAccount(ctx context.Context) (*AccountSummary, error)
    GetPositions(ctx context.Context) ([]Position, error)

    // Orders
    PlaceOrder(ctx context.Context, req OrderRequest) (*OrderStatus, error)
    CancelOrder(ctx context.Context, orderId int64) error
    GetOrders(ctx context.Context) ([]OrderStatus, error)
    GetOpenOrders(ctx context.Context) ([]OrderStatus, error)

    // Contract search
    SearchContracts(ctx context.Context, instrument Instrument) ([]Instrument, error)
}
```

### MarketDataService (quotes, bars, options chains)

```go
// interfaces/market_data.go
package interfaces

import "context"

type Quote struct {
    Instrument Instrument `json:"instrument"`
    Bid        float64    `json:"bid"`
    Ask        float64    `json:"ask"`
    Last       float64    `json:"last"`
    Volume     int64      `json:"volume"`
    High       float64    `json:"high"`
    Low        float64    `json:"low"`
    Open       float64    `json:"open"`
    Close      float64    `json:"close"`
    Timestamp  string     `json:"timestamp"`
}

type OptionGreeks struct {
    Delta     float64 `json:"delta"`
    Gamma     float64 `json:"gamma"`
    Theta     float64 `json:"theta"`
    Vega      float64 `json:"vega"`
    ImpliedVol float64 `json:"impliedVol"`
    OptPrice  float64 `json:"optPrice"`
    Underlying float64 `json:"undPrice"`
}

type OptionChainEntry struct {
    Instrument Instrument   `json:"instrument"`
    Bid        float64      `json:"bid"`
    Ask        float64      `json:"ask"`
    Last       float64      `json:"last"`
    Volume     int64        `json:"volume"`
    OpenInt    int64        `json:"openInterest"`
    Greeks     OptionGreeks `json:"greeks"`
}

type OptionChain struct {
    Underlying  string             `json:"underlying"`
    Expiry      string             `json:"expiry"`
    Calls       []OptionChainEntry `json:"calls"`
    Puts        []OptionChainEntry `json:"puts"`
}

type Bar struct {
    Time   string  `json:"time"`
    Open   float64 `json:"open"`
    High   float64 `json:"high"`
    Low    float64 `json:"low"`
    Close  float64 `json:"close"`
    Volume int64   `json:"volume"`
}

type BarTimeframe string

const (
    Bar1Min  BarTimeframe = "1 min"
    Bar5Min  BarTimeframe = "5 mins"
    Bar15Min BarTimeframe = "15 mins"
    Bar1Hour BarTimeframe = "1 hour"
    Bar1Day  BarTimeframe = "1 day"
)

type MarketDataService interface {
    // Real-time
    GetQuote(ctx context.Context, instrument Instrument) (*Quote, error)
    GetOptionChain(ctx context.Context, underlying Instrument, expiry string) (*OptionChain, error)
    GetOptionSnapshot(ctx context.Context, option Instrument) (*OptionChainEntry, error)

    // Historical
    GetHistoricalBars(ctx context.Context, instrument Instrument, timeframe BarTimeframe, duration string) ([]Bar, error)
    GetLatestBar(ctx context.Context, instrument Instrument) (*Bar, error)

    // Discovery
    GetOptionExpiries(ctx context.Context, underlying Instrument) ([]string, error)
    GetOptionStrikes(ctx context.Context, underlying Instrument, expiry string) ([]float64, error)
}
```

### NewsService (European sources)

```go
// interfaces/news.go
package interfaces

import "context"

type NewsItem struct {
    Title     string `json:"title"`
    Source    string `json:"source"`
    URL       string `json:"url"`
    Published string `json:"published"`
    Summary   string `json:"summary,omitempty"`
    Sentiment string `json:"sentiment,omitempty"` // bullish, bearish, neutral
}

type NewsService interface {
    GetMarketNews(ctx context.Context) ([]NewsItem, error)
    SearchNews(ctx context.Context, query string) ([]NewsItem, error)
    GetECBNews(ctx context.Context) ([]NewsItem, error)
    GetEurexBulletins(ctx context.Context) ([]NewsItem, error)
    CleanNewsForTrading(ctx context.Context, items []NewsItem) ([]NewsItem, error)
}
```

---

## TWS Go Wrapper — Implementation Guide

### Package: `tws/`

This is a from-scratch Go implementation of the TWS API TCP socket protocol. Reference the Java source at `~/IBJts/source/JavaClient/` for message formats.

### File-by-file spec

**`tws/constants.go`** — Message type IDs (incoming/outgoing), tick type IDs
- Copy the numeric constants from Java's `EClient.java` (outgoing) and `EDecoder.java` (incoming)
- Tick types from `TickType.java`

**`tws/contract.go`** — TWS Contract struct
- Maps to/from our `interfaces.Instrument`
- Fields: ConId, Symbol, SecType, Exchange, Currency, Expiry (LastTradeDateOrContractMonth), Strike, Right, Multiplier, LocalSymbol, TradingClass, PrimaryExch

**`tws/order.go`** — TWS Order struct
- OrderId, Action (BUY/SELL), TotalQuantity, OrderType, LmtPrice, AuxPrice, Tif
- Maps to/from our `interfaces.OrderRequest`
- **IMPORTANT:** Include `OrderCancel` struct for TWS 10.37.02 `cancelOrder` signature

**`tws/client.go`** — TCP connection and message I/O
```go
type Client struct {
    conn       net.Conn
    host       string
    port       int
    clientId   int
    serverVer  int
    connected  bool
    mu         sync.Mutex  // protects writes
    wrapper    Wrapper     // callback receiver
    dispatcher *Dispatcher
    orderMgr   *OrderIdManager
}

func (c *Client) Connect(host string, port, clientId int) error
    // 1. TCP dial
    // 2. Send "API\0" + "v100..180"
    // 3. Read server version + connection time
    // 4. Send startApi message (clientId, optCapabilities)
    // 5. Start reader goroutine
    // 6. Wait for nextValidId callback

func (c *Client) sendMsg(fields ...any) error
    // Encode fields as null-delimited string
    // Prepend 4-byte big-endian length
    // Write to conn (under mu lock)

func (c *Client) reader()
    // Loop: read 4-byte length, read body, pass to decoder
```

**`tws/encoder.go`** — Builds outbound messages
- Each request is a function: `ReqMktData`, `PlaceOrder`, `CancelOrder`, `ReqAccountSummary`, `ReqPositions`, `ReqContractDetails`, `ReqHistoricalData`, `ReqSecDefOptParams`, `ReqAllOpenOrders`
- Each builds a `[]any` slice of fields and calls `client.sendMsg()`

**`tws/decoder.go`** — Parses inbound messages
- Reads message ID from first field
- Dispatches to appropriate parse function
- Each parse function reads fields sequentially, constructs response struct, calls `wrapper.OnXxx()`
- Key decoders: `tickPrice`, `tickSize`, `tickOptionComputation`, `orderStatus`, `openOrder`, `position`, `accountSummary`, `contractDetails`, `historicalData`, `nextValidId`, `error`

**`tws/wrapper.go`** — Callback interface (EWrapper equivalent)
```go
type Wrapper interface {
    NextValidId(orderId int64)
    Error(reqId int64, code int64, msg string, advancedOrderReject string)
    TickPrice(reqId int64, tickType int64, price float64, attribs TickAttrib)
    TickSize(reqId int64, tickType int64, size int64)
    TickOptionComputation(reqId int64, tickType int64, tickAttrib int64,
        impliedVol, delta, optPrice, pvDividend, gamma, vega, theta, undPrice float64)
    OrderStatus(orderId int64, status string, filled, remaining, avgFillPrice float64,
        permId int64, parentId int64, lastFillPrice float64, clientId int64, whyHeld string, mktCapPrice float64)
    OpenOrder(orderId int64, contract Contract, order Order, orderState OrderState)
    Position(account string, contract Contract, pos float64, avgCost float64)
    AccountSummary(reqId int64, account, tag, value, currency string)
    ContractDetails(reqId int64, details ContractDetails)
    HistoricalData(reqId int64, bar HistoricalBar)
    HistoricalDataEnd(reqId int64, startDate, endDate string)
    SecurityDefinitionOptionParameter(reqId int64, exchange string, underlyingConId int64,
        tradingClass, multiplier string, expirations, strikes []string)
    ConnectionClosed()
}
```

**`tws/dispatcher.go`** — reqId→channel registry (see design above)

### Build & test order

1. `tws/constants.go` + `tws/contract.go` + `tws/order.go` — pure data types, no I/O
2. `tws/encoder.go` — message builders (unit testable with byte comparison)
3. `tws/client.go` — TCP connect + handshake (integration test against IB Gateway paper)
4. `tws/decoder.go` — message parser (unit test with captured byte sequences)
5. `tws/wrapper.go` + `tws/dispatcher.go` — callback→channel bridge
6. `services/ibkr_broker.go` — implements `BrokerService` using `tws.Client`
7. `services/ibkr_market_data.go` — implements `MarketDataService`
8. Wire into controllers + MCP tools

---

## Phase 1: Claude Code CLI Swap

### Changes in `agent/harness.js`

Replace the OpenCode subprocess spawn:

```javascript
// BEFORE (OpenCode)
const child = spawn('opencode', [
    'run',
    '--format', 'json',
    '--model', model,
    '--max-turns', String(maxTurns),
    '--session', sessionId
]);

// AFTER (Claude Code)
const child = spawn('claude', [
    '-p',                               // headless/print mode
    '--output-format', 'stream-json',   // structured streaming
    '--model', model.replace('anthropic/', ''),  // 'sonnet' not 'anthropic/claude-sonnet-4-6'
    '--max-turns', String(maxTurns),
    '--resume', sessionId,              // was --session
    '--allowedTools', 'mcp__prophet__*' // allow all MCP tools
]);

// System prompt: pipe via stdin (same as before)
child.stdin.write(systemPrompt);
child.stdin.end();
```

### Changes in `agent/server.js`

Update SSE event parsing. Claude Code `stream-json` emits NDJSON:
```json
{"type": "assistant", "message": {"content": [{"type": "text", "text": "..."}]}}
{"type": "assistant", "message": {"content": [{"type": "tool_use", "name": "...", "input": {...}}]}}
{"type": "result", "result": "...", "session_id": "..."}
```

Map these to the existing SSE event types the dashboard expects.

### Session ID capture

```javascript
// Capture session_id from first result for subsequent beats
let sessionId = null;
child.stdout.on('data', (chunk) => {
    const lines = chunk.toString().split('\n').filter(Boolean);
    for (const line of lines) {
        const event = JSON.parse(line);
        if (event.session_id && !sessionId) {
            sessionId = event.session_id;
        }
    }
});
```

### MCP config

Replace `opencode.jsonc` with `.mcp.json`:
```json
{
    "mcpServers": {
        "prophet": {
            "command": "node",
            "args": ["./mcp-server.js"],
            "type": "stdio"
        }
    }
}
```

---

## Phase 2: IBKR TWS Adapter

Build order documented above in "TWS Go Wrapper — Implementation Guide".

### IB Gateway paper trading config

```env
IBKR_HOST=127.0.0.1
IBKR_PORT=4002          # paper trading (4001 = live)
IBKR_CLIENT_ID=1
```

### Test sequence on paper account

1. Connect → verify nextValidId received
2. reqAccountSummary → verify account data
3. reqPositions → verify positions (initially empty)
4. reqContractDetails for ESTX50 → verify OESX contracts found
5. reqMktData for OESX option → verify tick callbacks (price, size, greeks)
6. reqHistoricalData for SXE (Euro Stoxx 50 index) → verify bars
7. reqSecDefOptParams for ESTX50 → verify expiries/strikes
8. placeOrder (limit, far from market) → verify order callbacks
9. cancelOrder → verify cancellation
10. Full round-trip: search chain → pick contract → place order → verify fill on paper

---

## Phase 3: European News Sources

Replace Alpaca-centric US news with European sources:

- **ECB press releases & speeches** — RSS/API from ecb.europa.eu
- **Eurex circulars & bulletins** — Eurex exchange notices
- **Reuters Europe** — via Google News RSS filtered to European markets
- **Financial Times** — headlines via RSS
- **European macro calendar** — ECB rate decisions, PMI, CPI releases

Keep the Gemini AI cleaning layer — swap the prompt to European market context.

---

## File Change Summary

| File | Change | Priority |
|------|--------|----------|
| `agent/harness.js` | OpenCode → `claude -p` subprocess | Phase 1 |
| `agent/server.js` | SSE event parser for Claude Code JSON | Phase 1 |
| `.mcp.json` | New file (replaces `opencode.jsonc`) | Phase 1 |
| `tws/*.go` | NEW: entire TWS API wrapper package | Phase 2 |
| `interfaces/instrument.go` | NEW: universal Instrument type | Phase 2 |
| `interfaces/broker.go` | NEW: BrokerService interface | Phase 2 |
| `interfaces/market_data.go` | NEW: MarketDataService interface | Phase 2 |
| `services/ibkr_broker.go` | NEW: BrokerService implementation | Phase 2 |
| `services/ibkr_market_data.go` | NEW: MarketDataService implementation | Phase 2 |
| `services/alpaca_*.go` | DELETE (3 files) | Phase 2 |
| `controllers/*.go` | Rewire to use BrokerService/MarketDataService | Phase 2 |
| `config/config.go` | Add IBKR env vars | Phase 2 |
| `cmd/bot/main.go` | Wire IBKR services instead of Alpaca | Phase 2 |
| `TRADING_RULES.md` | Adapt to OESX / European markets | Phase 2 |
| `services/news_service.go` | European sources | Phase 3 |
| `services/gemini_service.go` | Adapt prompts to European context | Phase 3 |

---

## Do NOT

- Do not use any third-party Go TWS API library (hadrianl/ibapi, scmhub/ibapi, etc.)
- Do not use Lombok or code generation — explicit code only
- Do not use the IBKR Client Portal REST API (we chose TWS API for full autonomous operation)
- Do not change the MCP tool names or schemas (mcp-server.js stays compatible)
- Do not modify the dashboard (agent/public/index.html) in Phase 1 or 2
- Do not use CGO — pure Go only

## Do

- Write table-driven unit tests for encoder/decoder
- Test every TWS interaction against IB Gateway paper (port 4002) before proceeding
- Keep the Go wrapper minimal — only implement message types we actually need
- Log all TWS message traffic at debug level for troubleshooting
- Handle TWS reconnection gracefully (IB Gateway can restart daily)
