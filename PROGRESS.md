# PROGRESS.md — Implementation Tracker

> **Read this first every session.** Find the first unchecked item — that's where we are.
>
> **Rules:**
> - One step = one commit = one testable change
> - Mark ✅ only after the test passes
> - Never skip ahead
> - If a step fails, add a note and fix before moving on

---

## Phase 1: Claude Code CLI Swap

Replace OpenCode CLI with Claude Code (`claude -p` headless mode). All changes tested against the existing Alpaca backend — no broker changes yet.

| Step | Description | Status | Date | Commit |
|------|-------------|--------|------|--------|
| 1.1 | Create branch `feature/ibkr-porting`, add `CLAUDE.md` and `PROGRESS.md` to repo | ✅ | 2026-03-22 | 2149d18 |
| 1.2 | Create `.mcp.json` (replaces `opencode.jsonc`) | ⬜ | | |
| 1.3 | Update `harness.js` — spawn command only (`opencode run` → `claude -p`, argument mapping) | ⬜ | | |
| 1.4 | Update `harness.js` — session handling (`--session` → `--resume`, capture `session_id`) | ⬜ | | |
| 1.5 | Update `harness.js` — system prompt piping (stdin, first-beat-only optimization) | ⬜ | | |
| 1.6 | Update `server.js` — SSE event parser (OpenCode JSON → Claude Code `stream-json`) | ⬜ | | |
| 1.7 | End-to-end test: dashboard → Start → full beat cycle with Alpaca backend | ⬜ | | |

**Test criteria per step:**
- **1.1:** Branch exists, both `.md` files render on GitHub
- **1.2:** `claude` CLI picks up MCP server config (run `claude` interactively, verify tools listed)
- **1.3:** Subprocess spawns, connects to MCP server, exits cleanly (check process output)
- **1.4:** First beat returns `session_id`, second beat resumes same session (check logs)
- **1.5:** Agent receives system prompt on beat 1, beat 2 skips it (check token count in logs)
- **1.6:** Dashboard Terminal tab shows agent text, tool calls, and tool results streaming
- **1.7:** Full autonomous cycle: agent wakes → calls tools → results stream → sleeps → next beat

---

## Phase 2: IBKR TWS Adapter

Custom Go TWS API wrapper from scratch. Tested against IB Gateway paper (port 4002).

### Phase 2A: TWS Wire Protocol (pure data types, no I/O)

| Step | Description | Status | Date | Commit |
|------|-------------|--------|------|--------|
| 2A.1 | Create `tws/constants.go` — message IDs (incoming/outgoing), tick types | ⬜ | | |
| 2A.2 | Create `tws/contract.go` — TWS Contract struct + Instrument↔Contract mapping | ⬜ | | |
| 2A.3 | Create `tws/order.go` — TWS Order + OrderCancel structs | ⬜ | | |
| 2A.4 | Unit tests for Contract↔Instrument round-trip (all instrument types) | ⬜ | | |

### Phase 2B: Encoder (outbound messages)

| Step | Description | Status | Date | Commit |
|------|-------------|--------|------|--------|
| 2B.1 | Create `tws/encoder.go` — message framing (length prefix + null-delimited fields) | ⬜ | | |
| 2B.2 | Add `ReqMktData`, `CancelMktData` encoders | ⬜ | | |
| 2B.3 | Add `PlaceOrder`, `CancelOrder` encoders | ⬜ | | |
| 2B.4 | Add `ReqAccountSummary`, `ReqPositions` encoders | ⬜ | | |
| 2B.5 | Add `ReqContractDetails`, `ReqSecDefOptParams` encoders | ⬜ | | |
| 2B.6 | Add `ReqHistoricalData`, `ReqAllOpenOrders` encoders | ⬜ | | |
| 2B.7 | Unit tests for all encoders (byte-level comparison with expected output) | ⬜ | | |

### Phase 2C: Client + Decoder (TCP connection, inbound messages)

| Step | Description | Status | Date | Commit |
|------|-------------|--------|------|--------|
| 2C.1 | Create `tws/client.go` — TCP dial, handshake (`API\0` + version negotiation) | ⬜ | | |
| 2C.2 | Create `tws/decoder.go` — message reader + dispatch by message ID | ⬜ | | |
| 2C.3 | Create `tws/wrapper.go` — EWrapper callback interface | ⬜ | | |
| 2C.4 | Add decoders: `nextValidId`, `error`, `connectionClosed` | ⬜ | | |
| 2C.5 | Integration test: connect to IB Gateway paper, receive `nextValidId` | ⬜ | | |
| 2C.6 | Add decoders: `tickPrice`, `tickSize`, `tickOptionComputation` | ⬜ | | |
| 2C.7 | Integration test: `reqMktData` for any instrument, verify tick callbacks | ⬜ | | |
| 2C.8 | Add decoders: `orderStatus`, `openOrder`, `openOrderEnd` | ⬜ | | |
| 2C.9 | Add decoders: `position`, `positionEnd`, `accountSummary`, `accountSummaryEnd` | ⬜ | | |
| 2C.10 | Add decoders: `contractDetails`, `contractDetailsEnd` | ⬜ | | |
| 2C.11 | Add decoders: `historicalData`, `historicalDataEnd` | ⬜ | | |
| 2C.12 | Add decoders: `securityDefinitionOptionParameter`, `securityDefinitionOptionParameterEnd` | ⬜ | | |

### Phase 2D: Dispatcher (reqId→channel bridge)

| Step | Description | Status | Date | Commit |
|------|-------------|--------|------|--------|
| 2D.1 | Create `tws/dispatcher.go` — Register, Dispatch, Complete | ⬜ | | |
| 2D.2 | Create `tws/order_id_manager.go` — Seed + atomic Next | ⬜ | | |
| 2D.3 | Unit tests for dispatcher (concurrent register/dispatch/complete) | ⬜ | | |
| 2D.4 | Wire dispatcher into client: wrapper callbacks → dispatcher.Dispatch | ⬜ | | |
| 2D.5 | Integration test: full request→channel→response for `reqContractDetails` | ⬜ | | |

### Phase 2E: Go Interface Implementations

| Step | Description | Status | Date | Commit |
|------|-------------|--------|------|--------|
| 2E.1 | Create `interfaces/instrument.go` — Instrument struct + type constants | ⬜ | | |
| 2E.2 | Create `interfaces/broker.go` — BrokerService interface | ⬜ | | |
| 2E.3 | Create `interfaces/market_data.go` — MarketDataService interface | ⬜ | | |
| 2E.4 | Create `services/ibkr_broker.go` — Connect, Disconnect, IsConnected, GetAccount | ⬜ | | |
| 2E.5 | Integration test: connect → GetAccount → verify account data from paper | ⬜ | | |
| 2E.6 | Add `services/ibkr_broker.go` — GetPositions, GetOrders, GetOpenOrders | ⬜ | | |
| 2E.7 | Add `services/ibkr_broker.go` — PlaceOrder, CancelOrder | ⬜ | | |
| 2E.8 | Integration test: place limit order far from market → verify → cancel → verify | ⬜ | | |
| 2E.9 | Add `services/ibkr_broker.go` — SearchContracts | ⬜ | | |
| 2E.10 | Create `services/ibkr_market_data.go` — GetQuote | ⬜ | | |
| 2E.11 | Integration test: GetQuote for ESTX50 index → verify bid/ask | ⬜ | | |
| 2E.12 | Add `services/ibkr_market_data.go` — GetOptionChain, GetOptionSnapshot | ⬜ | | |
| 2E.13 | Add `services/ibkr_market_data.go` — GetHistoricalBars, GetLatestBar | ⬜ | | |
| 2E.14 | Add `services/ibkr_market_data.go` — GetOptionExpiries, GetOptionStrikes | ⬜ | | |
| 2E.15 | Integration test: full option chain fetch for OESX → verify expiries, strikes, greeks | ⬜ | | |

### Phase 2F: Controller + MCP Wiring

| Step | Description | Status | Date | Commit |
|------|-------------|--------|------|--------|
| 2F.1 | Update `config/config.go` — add IBKR env vars, remove Alpaca vars | ⬜ | | |
| 2F.2 | Update `cmd/bot/main.go` — wire IbkrBrokerService + IbkrMarketDataService | ⬜ | | |
| 2F.3 | Update `controllers/order_controller.go` — use BrokerService interface | ⬜ | | |
| 2F.4 | Update `controllers/position_controller.go` — use BrokerService interface | ⬜ | | |
| 2F.5 | Update remaining controllers to use interfaces | ⬜ | | |
| 2F.6 | Delete `services/alpaca_trading.go`, `alpaca_data.go`, `alpaca_options_data.go` | ⬜ | | |
| 2F.7 | Update `.env.example` — IBKR vars | ⬜ | | |
| 2F.8 | End-to-end test: MCP tool `get_account` via Claude Code → IBKR paper data | ⬜ | | |
| 2F.9 | End-to-end test: MCP tool `get_options_chain` → OESX chain from IBKR | ⬜ | | |
| 2F.10 | End-to-end test: MCP tool `place_options_order` → limit order on paper | ⬜ | | |
| 2F.11 | Full autonomous beat test: dashboard → agent → IBKR paper | ⬜ | | |

### Phase 2G: Trading Rules

| Step | Description | Status | Date | Commit |
|------|-------------|--------|------|--------|
| 2G.1 | Update `TRADING_RULES.md` — adapt from US options to OESX / European strategy | ⬜ | | |

---

## Phase 3: European News Sources

| Step | Description | Status | Date | Commit |
|------|-------------|--------|------|--------|
| 3.1 | Create `interfaces/news.go` — NewsService interface | ⬜ | | |
| 3.2 | Update `services/news_service.go` — ECB RSS feed integration | ⬜ | | |
| 3.3 | Add Eurex bulletins/circulars feed | ⬜ | | |
| 3.4 | Add Reuters Europe / FT headlines via Google News RSS | ⬜ | | |
| 3.5 | Update `services/gemini_service.go` — European market context prompts | ⬜ | | |
| 3.6 | Update MCP tools: `get_ecb_news`, `get_eurex_bulletins` | ⬜ | | |
| 3.7 | End-to-end test: `get_quick_market_intelligence` returns European content | ⬜ | | |

---

## Notes

_Add dated notes here when something unexpected happens, a step needs rework, or a design decision changes._

```
YYYY-MM-DD | Step X.X | Note text here
```
