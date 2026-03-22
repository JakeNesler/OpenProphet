# OpenProphet — IBKR Edition

**Autonomous AI trading agent with web dashboard, MCP tools, and Go trading backend — adapted for Interactive Brokers**

> Forked from [JakeNesler/OpenProphet](https://github.com/JakeNesler/OpenProphet). This fork replaces Alpaca with IBKR (TWS API), OpenCode with Claude Code, and targets instrument-agnostic European + US trading.

---

> **WARNING:** This is an experimental AI-powered trading system. Options trading involves significant risk of loss. Use paper trading only. The author assumes no responsibility for financial losses.

---

## What Changed from Upstream

| Component | Original (Alpaca) | This Fork (IBKR) |
|---|---|---|
| **Broker** | Alpaca REST API | IBKR TWS API via IB Gateway (custom Go wrapper) |
| **AI Runtime** | OpenCode CLI | Claude Code (`claude -p` headless mode) |
| **Markets** | US equities & options only | Instrument-agnostic: OESX index options, US/EU stocks, futures |
| **News** | Google News + MarketWatch (US focus) | ECB, Eurex, Reuters Europe + Google News |
| **Connection** | REST request-response | TCP socket, callback→channel pattern |
| **Auth** | Alpaca API key pair | IB Gateway (auto-restart, no daily re-auth) |

---

## What Is This?

OpenProphet is a fully autonomous trading harness that runs an AI agent on a heartbeat loop. The agent wakes up on a schedule, assesses market conditions, manages positions, and executes trades — all without human intervention. A mobile-friendly web dashboard at `http://localhost:3737` streams everything in real time.

```
                        +---------------------+
                        |   Web Dashboard     |
                        |   (port 3737)       |
                        |   SSE streaming     |
                        +--------+------------+
                                 |
                        +--------v------------+
                        |   Agent Server      |
                        |   (Node.js/Express)  |
                        |   Heartbeat loop    |
                        |   Config store      |
                        +--------+------------+
                                 |
              +------------------+------------------+
              |                                     |
    +---------v-----------+             +-----------v-----------+
    |   Claude Code CLI   |             |   Go Trading Backend  |
    |   (claude -p)       |             |   (Gin, port 4534)    |
    |   Headless mode     |             |   TWS API client      |
    +---------------------+             |   News aggregation    |
              |                         |   Technical analysis  |
    +---------v-----------+             +-----------+-----------+
    |   MCP Server        |                         |
    |   (Node.js)         |             +-----------v-----------+
    |   45+ trading tools |             |   IB Gateway          |
    |   Permission gates  |             |   (paper: 4002)       |
    +---------------------+             |   (live:  4001)       |
                                        +-----------------------+
```

### The Loop

1. Agent wakes up on heartbeat (interval varies by market phase)
2. Claude Code subprocess spawns with `claude -p` + MCP tools
3. Agent calls tools: check account, scan news, analyze setups, place orders
4. Results stream to the web dashboard via SSE
5. Agent sleeps until next heartbeat

The agent controls its own heartbeat interval via the `set_heartbeat` MCP tool — it can speed up during volatile periods or slow down when markets are calm.

---

## Architecture

```
OpenProphet (IBKR Edition)
├── agent/                        # Autonomous agent system (Node.js)
│   ├── server.js                 # Express web server, SSE, Go lifecycle, auth
│   ├── harness.js                # Heartbeat loop, Claude Code subprocess, session mgmt
│   ├── config-store.js           # Persistent JSON config with write locking
│   └── public/index.html         # Single-page dashboard (paper aesthetic)
├── mcp-server.js                 # MCP tool server (45+ tools, permission enforcement)
├── .mcp.json                     # Claude Code MCP server registration
├── cmd/bot/main.go               # Go backend entry point
├── tws/                          # ★ Custom Go TWS API wrapper (from scratch)
│   ├── client.go                 # TCP connection, handshake, message I/O
│   ├── decoder.go                # Inbound message parser
│   ├── encoder.go                # Outbound message builder
│   ├── contract.go               # TWS Contract type
│   ├── order.go                  # TWS Order + OrderCancel types
│   ├── wrapper.go                # EWrapper callback interface (Go)
│   ├── dispatcher.go             # reqId → channel registry
│   └── constants.go              # Message IDs, tick types
├── interfaces/                   # Go type definitions
│   ├── instrument.go             # ★ Universal Instrument type
│   ├── broker.go                 # ★ BrokerService interface
│   ├── market_data.go            # ★ MarketDataService interface
│   └── news.go                   # ★ NewsService interface
├── controllers/                  # HTTP handlers (instrument-agnostic)
│   ├── order_controller.go       # Buy/sell/options/managed positions
│   ├── intelligence_controller.go # AI news analysis
│   ├── news_controller.go        # News aggregation (European sources)
│   ├── activity_controller.go    # Activity logging
│   └── position_controller.go    # Position management
├── services/                     # Business logic
│   ├── ibkr_broker.go            # ★ BrokerService implementation (TWS API)
│   ├── ibkr_market_data.go       # ★ MarketDataService implementation (TWS API)
│   ├── position_manager.go       # Automated stop-loss / take-profit
│   ├── gemini_service.go         # Gemini AI for news cleaning (European context)
│   ├── news_service.go           # Multi-source news (ECB, Eurex, Reuters Europe)
│   ├── stock_analysis_service.go # Stock analysis
│   ├── technical_analysis.go     # RSI, MACD, momentum indicators
│   └── activity_logger.go        # Trade journaling
├── models/                       # Database models
├── database/                     # SQLite storage layer
├── config/                       # Environment configuration
├── vectorDB.js                   # Semantic trade search (sqlite-vec)
├── TRADING_RULES.md              # Strategy rules (injected into agent prompt)
├── CLAUDE.md                     # ★ Project context for Claude Code sessions
└── data/
    ├── agent-config.json          # Runtime config
    └── prophet_trader.db          # SQLite database
```

---

## Key Differences from Upstream

### Custom Go TWS API Wrapper (`tws/`)

We wrote the TWS API client from scratch in pure Go — no third-party library, no CGO. The wrapper implements the TWS TCP socket protocol directly:

- **Wire protocol:** Length-prefixed, null-delimited text fields over TCP
- **Handshake:** `API\0` → version negotiation → `startApi` → wait for `nextValidId`
- **Callback→Channel pattern:** TWS callbacks (EWrapper) are translated to Go channels via a `Dispatcher` that maps `reqId` to buffered channels
- **Order ID management:** Seeded from `nextValidId` callback, atomic increment thereafter
- **Minimal surface:** Only implements the ~15 message types we actually need

### Instrument-Agnostic Design

A universal `Instrument` type replaces Alpaca's symbol strings:

```go
// OESX index option on Eurex
inst := interfaces.Instrument{
    Symbol: "ESTX50", Type: interfaces.InstrumentIndexOption,
    Exchange: "EUREX", Currency: "EUR",
    Expiry: "20260620", Strike: 5200, Right: interfaces.Call,
    Multiplier: 10, TradingClass: "OESX",
}

// US equity
inst := interfaces.Instrument{
    Symbol: "AAPL", Type: interfaces.InstrumentStock,
    Exchange: "SMART", Currency: "USD",
}

// Futures contract
inst := interfaces.Instrument{
    Symbol: "ES", Type: interfaces.InstrumentFuture,
    Exchange: "CME", Currency: "USD",
    Expiry: "20260620", Multiplier: 50,
}
```

The `Instrument` struct is a plain data container with no product-specific logic. All field values come from configuration, user input, or contract search results — the interface layer never assumes what you're trading.

### Claude Code Integration

The agent harness spawns Claude Code in headless mode:

```bash
claude -p \
  --output-format stream-json \
  --model sonnet \
  --max-turns 25 \
  --resume <session-id>
```

Session continuity works via `--resume` with a captured session ID. The system prompt (including TRADING_RULES.md) is piped via stdin on the first beat only. MCP tools are registered via `.mcp.json` at the project root.

---

## Features

### Autonomous Agent

- **Phased heartbeat** — Pre-market (15m), market open (2m), midday (10m), close (2m), after hours (30m), closed (1h)
- **Session persistence** — `claude -p --resume` maintains context across beats
- **System prompt optimization** — Only sent on first beat, saving ~2,000 tokens/beat
- **User interrupts** — Send messages mid-beat; kills current subprocess, resumes on same session
- **Agent self-modification** — Tools to update its own prompt, strategy rules, permissions, and heartbeat

### Web Dashboard

- **8 tabs** — Terminal, Trades, Portfolio, Agents, Strategies, Accounts, Plugins, Settings
- **Real-time SSE streaming** — Agent text, tool calls, tool results, beat lifecycle, trade events
- **Chat input** — Send messages to the agent, interrupt running beats
- **Mobile-first** — Responsive layout, touch-friendly

### Security & Guardrails

- **MCP permission enforcement** — Asset class gates, order value limits, 0DTE detection, tool blocking
- **Daily loss circuit breaker** — Auto-pauses agent when P&L exceeds `maxDailyLoss`%
- **IB Gateway auto-reconnect** — Handles daily restarts and connection drops
- **Token-based auth** — Set `AGENT_AUTH_TOKEN` for dashboard API protection

### AI Intelligence

- **Gemini news cleaning** — Transforms noisy feeds into structured European trading intelligence
- **European news sources** — ECB press releases, Eurex bulletins, Reuters Europe, FT headlines
- **Technical analysis** — RSI, MACD, momentum indicators via Go backend
- **Vector similarity search** — Semantic search over past trades using local embeddings

---

## Quick Start

### Prerequisites

- **Go 1.22+** — For the trading backend
- **Node.js 18+** — For the agent server and MCP tools
- **Claude Code** — `npm install -g @anthropic-ai/claude-code`
- **IB Gateway** — [Download](https://www.interactivebrokers.com/en/trading/ibgateway-stable.php) (paper trading is free)
- **IBKR Pro account** — [interactivebrokers.com](https://www.interactivebrokers.com)

### 1. Clone and Install

```bash
git clone https://github.com/natslash/OpenProphet.git
cd OpenProphet
git checkout feature/ibkr-porting
npm install
go build -o prophet_bot ./cmd/bot
```

### 2. Configure IB Gateway

Start IB Gateway and log in with your IBKR paper trading credentials:
- **Paper trading port:** 4002
- **API Settings:** Enable "Enable ActiveX and Socket Clients", set socket port to 4002
- **Trusted IPs:** Add 127.0.0.1
- **Read-only API:** Uncheck (we need to place orders)

### 3. Configure Environment

```bash
cp .env.example .env
```

Edit `.env`:

```env
# IBKR Configuration
IBKR_HOST=127.0.0.1
IBKR_PORT=4002                    # Paper: 4002, Live: 4001
IBKR_CLIENT_ID=1

# Gemini API (optional — for AI news cleaning)
GEMINI_API_KEY=your_gemini_key

# Agent Dashboard Configuration (optional)
AGENT_PORT=3737
AGENT_AUTH_TOKEN=
TRADING_BOT_PORT=4534
```

### 4. Authenticate Claude Code

```bash
npm install -g @anthropic-ai/claude-code
claude auth login
```

### 5. Start the Dashboard

```bash
npm run agent
```

Open `http://localhost:3737` and press **Start**.

### 6. (Alternative) MCP-Only Mode

```bash
./prophet_bot          # Start Go backend (connects to IB Gateway)
claude                 # Interactive Claude Code with trading tools
```

---

## Supported Instruments

| Instrument | Example | Exchange | Status |
|---|---|---|---|
| OESX Index Options | ESTX50 Jun 2026 5200 Call | EUREX | ✅ Primary |
| US Equities | AAPL, MSFT, TSLA | SMART/NYSE/NASDAQ | ✅ Supported |
| US Equity Options | SPY Jun 2026 580 Put | SMART | ✅ Supported |
| Futures | ES, NQ, FESX | CME, EUREX | ✅ Supported |
| Forex | EUR.USD, GBP.USD | IDEALPRO | 🔄 Planned |

Adding a new instrument type requires no changes to the agent, MCP tools, or dashboard — just a new `InstrumentType` constant and a TWS Contract mapping.

---

## MCP Tools Reference

### Trading

| Tool | Description |
|---|---|
| `place_options_order` | Buy/sell options with limit orders |
| `place_managed_position` | Position with automated stop-loss / take-profit |
| `close_managed_position` | Close managed position at market |
| `place_buy_order` | Buy shares/contracts |
| `place_sell_order` | Sell shares/contracts |
| `cancel_order` | Cancel a pending order |

### Market Data

| Tool | Description |
|---|---|
| `get_account` | Portfolio value, cash, buying power, margin |
| `get_positions` | All open positions |
| `get_options_positions` | All open options positions |
| `get_options_chain` | Options chain with strike/expiry filtering |
| `get_orders` | Order history |
| `get_quote` | Real-time quote |
| `get_latest_bar` | Latest OHLCV bar |
| `get_historical_bars` | Historical price bars |
| `get_managed_positions` | Managed positions with stop/target status |

### News & Intelligence

| Tool | Description |
|---|---|
| `get_quick_market_intelligence` | AI-cleaned European market summary |
| `analyze_stocks` | Technical analysis + news + recommendations |
| `get_cleaned_news` | Multi-source aggregated intelligence |
| `search_news` | Keyword search |
| `get_ecb_news` | ECB press releases and speeches |
| `get_eurex_bulletins` | Eurex exchange circulars |

### Vector Search (AI Memory)

| Tool | Description |
|---|---|
| `find_similar_setups` | Semantic search over past trades |
| `store_trade_setup` | Store a trade for future pattern matching |
| `get_trade_stats` | Win rate, profit factor by symbol/strategy |

### Agent Self-Modification

| Tool | Description |
|---|---|
| `update_agent_prompt` | Update the active agent's system prompt |
| `update_strategy_rules` | Update trading strategy rules |
| `get_agent_config` | Read current agent config and permissions |
| `set_heartbeat` | Override heartbeat interval dynamically |
| `update_permissions` | Modify permission guardrails |

---

## Configuration

All runtime config is stored in `data/agent-config.json`:

```json
{
  "activeAccountId": "DU1234567",
  "activeAgentId": "default",
  "activeModel": "sonnet",
  "heartbeat": {
    "pre_market": 900,
    "market_open": 120,
    "midday": 600,
    "market_close": 120,
    "after_hours": 1800,
    "closed": 3600
  },
  "permissions": {
    "allowLiveTrading": false,
    "allowOptions": true,
    "allowStocks": true,
    "allow0DTE": false,
    "requireConfirmation": false,
    "maxPositionPct": 15,
    "maxDeployedPct": 80,
    "maxDailyLoss": 5,
    "maxOpenPositions": 10,
    "maxOrderValue": 0,
    "maxToolRoundsPerBeat": 25,
    "blockedTools": []
  }
}
```

---

## Go Backend Services

| Service | Purpose | Key Functions |
|---|---|---|
| `IbkrBrokerService` | Order execution via TWS API | PlaceOrder, CancelOrder, GetPositions, GetAccount |
| `IbkrMarketDataService` | Market data via TWS API | GetQuote, GetHistoricalBars, GetOptionChain |
| `PositionManager` | Automation | MonitorPositions, CloseManagedPosition |
| `TechnicalAnalysisService` | Indicators | CalculateRSI, CalculateMACD |
| `NewsService` | European intelligence | GetECBNews, GetEurexBulletins, SearchNews |
| `GeminiService` | AI processing | CleanNewsForTrading |
| `ActivityLogger` | Journaling | LogDecision, LogActivity |

---

## Development

### Adding a New MCP Tool

1. Add the endpoint in Go (`controllers/` + route in `cmd/bot/main.go`)
2. Add the tool definition in `mcp-server.js`
3. Add the handler in the `switch` block in `mcp-server.js`
4. If it's an order tool, add permission checks in `enforcePermissions()`

### Adding a New Instrument Type

1. Add the `InstrumentType` constant in `interfaces/instrument.go` (e.g., `InstrumentBond`)
2. Add the TWS Contract mapping in `tws/contract.go` for the new SecType
3. No changes needed in controllers, MCP tools, or dashboard

### Running Tests

```bash
go test ./tws/... -v                              # Unit tests (no IB Gateway needed)
go test ./services/... -v -tags=integration        # Integration tests (IB Gateway paper)
```

---

## Migrating from Upstream (Alpaca) OpenProphet

1. Replace `.env` — Remove `ALPACA_*` vars, add `IBKR_*` vars
2. Replace `opencode.jsonc` with `.mcp.json`
3. Install Claude Code — `npm install -g @anthropic-ai/claude-code && claude auth login`
4. Start IB Gateway — Paper trading on port 4002
5. Rebuild — `go build -o prophet_bot ./cmd/bot`
6. Update `TRADING_RULES.md` — Adapt strategy to your target instruments

---

## Future: Java/Spring Boot Migration

The architecture is designed for a future migration to Java/Spring Boot + containers. Broker interfaces map directly to Java interfaces, the MCP server and dashboard talk over HTTP (invisible swap), and the SQLite schema is portable to JPA. Docker Compose can containerize the current stack now.

---

## Disclaimer

THIS SOFTWARE IS PROVIDED "AS IS" WITHOUT WARRANTY OF ANY KIND. The author strongly recommends against using this system with real money until thoroughly tested on paper trading. Options trading carries substantial risk of loss. You are solely responsible for your own trading decisions.

---

## License

[CC BY-NC 4.0](https://creativecommons.org/licenses/by-nc/4.0/) — Free for personal and non-commercial use. See [LICENSE](LICENSE) for details.

Original work copyright (c) 2025 Jake Nesler. Fork modifications by Shashi.
