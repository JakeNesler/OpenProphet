# ClaudeProphetAndFriends

**Autonomous AI trading agent with a web dashboard, MCP tools, and a Go trading backend**

---

## **[Premium Setup Guide & Templates at openprophet.io](https://openprophet.io)**

**Not comfortable with Git or self-hosting?** A paid service is available at **[openprophet.io](https://openprophet.io)** that handles the full setup for you. It also includes:

- **Step-by-step setup guides** for every skill level
- **Agent templates** -- pre-built personas with tuned prompts and configurations
- **Strategy templates** -- ready-to-use trading strategies with rules and risk parameters

---

> **WARNING:** This is an experimental AI-powered trading system. Options trading involves significant risk of loss. Use paper trading only. The author assumes no responsibility for financial losses.

<p align="center">
  <img src="https://freeecomapi.us-east-1.linodeobjects.com/openprophet%2FIMG_2512.jpeg" width="180" />
  <img src="https://freeecomapi.us-east-1.linodeobjects.com/openprophet%2FIMG_2513.jpeg" width="180" />
  <img src="https://freeecomapi.us-east-1.linodeobjects.com/openprophet%2FIMG_2514.jpeg" width="180" />
  <img src="https://freeecomapi.us-east-1.linodeobjects.com/openprophet%2FIMG_2515.jpeg" width="180" />
  <img src="https://freeecomapi.us-east-1.linodeobjects.com/openprophet%2FIMG_2516.jpeg" width="180" />
</p>

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
    |   OpenCode CLI      |             |   Go Trading Backend  |
    |   (AI subprocess)   |             |   (Gin, port 4534)    |
    |   Claude models     |             |   Alpaca API client   |
    +---------------------+             |   News aggregation    |
              |                         |   Technical analysis  |
    +---------v-----------+             +-----------+-----------+
    |   MCP Server        |                         |
    |   (Node.js)         |             +-----------v-----------+
    |   45+ trading tools |             |   Alpaca Markets API  |
    |   Permission gates  |             |   (paper / live)      |
    +---------------------+             +-----------------------+
```

### The Loop

1. Agent wakes up on heartbeat (interval varies by market phase)
2. OpenCode subprocess spawns with Claude model + MCP tools
3. Agent calls tools: check account, scan news, analyze setups, place orders
4. Results stream to the web dashboard via SSE
5. Agent sleeps until next heartbeat

The agent controls its own heartbeat interval via the `set_heartbeat` MCP tool — it can speed up during volatile periods or slow down when markets are calm.

---

## Architecture

```
OpenProphet
├── agent/                        # Autonomous agent system (Node.js)
│   ├── server.js                 # Express web server, SSE, Go lifecycle, auth
│   ├── harness.js                # Heartbeat loop, OpenCode subprocess, session mgmt
│   ├── analysis-scheduler.js     # Automatic pre-market and weekly analysis jobs
│   ├── config-store.js           # Persistent JSON config with write locking
│   └── public/index.html         # Single-page dashboard (paper aesthetic)
├── mcp-server.js                 # MCP tool server (45+ tools, permission enforcement)
├── cmd/bot/main.go               # Go backend entry point
├── controllers/                  # HTTP handlers (48 functions)
│   ├── order_controller.go       # Buy/sell/options/managed positions
│   ├── intelligence_controller.go # AI news analysis
│   ├── news_controller.go        # News aggregation (Google, MarketWatch)
│   ├── activity_controller.go    # Activity logging
│   └── position_controller.go    # Position management
├── services/                     # Business logic (63 functions)
│   ├── alpaca_trading.go         # Order execution via Alpaca API
│   ├── alpaca_data.go            # Market data (quotes, bars, IEX feed)
│   ├── alpaca_options_data.go    # Options chains and snapshots
│   ├── position_manager.go       # Automated stop-loss / take-profit
│   ├── gemini_service.go         # Gemini AI for news cleaning
│   ├── news_service.go           # Multi-source news aggregation
│   ├── stock_analysis_service.go # Stock analysis
│   ├── technical_analysis.go     # RSI, MACD, momentum indicators
│   └── activity_logger.go       # Trade journaling
├── interfaces/                   # Go type definitions (80 types)
├── models/                       # Database models (7 types)
├── database/                     # SQLite storage layer
├── config/                       # Environment configuration
├── vectorDB.js                   # Semantic trade search (sqlite-vec)
├── TRADING_RULES.md              # Strategy rules (injected into agent prompt)
├── opencode.jsonc                # OpenCode MCP configuration
└── data/
    ├── agent-config.json          # Runtime config (accounts, agents, permissions)
    └── prophet_trader.db          # SQLite database
```

---

## Features

### Automated Analysis Scheduler
- **Pre-market briefing** — Runs automatically at 6:00 AM ET on weekdays. Calls `run_market_briefing`, `run_ftd_check`, `run_economic_calendar`, and `run_earnings_calendar`, then saves `data/reports/daily_brief_YYYYMMDD.json`
- **Weekly screeners** — Runs automatically at 6:00 PM ET on Sundays. Calls `run_market_briefing`, `run_market_top_check`, `run_vcp_screener`, and `run_pead_screener`, then saves `data/reports/weekly_regime_YYYYMMDD.json`
- **Prophet auto-reads results** — On each pre-market heartbeat Prophet calls `read_latest_report("daily_brief")` before making any trading decisions. On Mondays it also reads `read_latest_report("weekly_regime")` for the Sunday watchlist
- **Manual trigger** — `POST /api/scheduler/trigger` lets you run either job on demand (see Dashboard API)
- **SSE streaming** — Job start/end events stream to the dashboard terminal in real time

### Autonomous Agent
- **Phased heartbeat** — Pre-market (15m), market open (2m), midday (10m), close (2m), after hours (30m), closed (1h)
- **Session persistence** — OpenCode `--session` flag maintains context across beats
- **System prompt optimization** — Only sent on first beat, saving ~2,000 tokens/beat
- **User interrupts** — Send messages mid-beat; kills current subprocess, resumes on same session
- **Agent self-modification** — Tools to update its own prompt, strategy rules, permissions, and heartbeat

### Web Dashboard
- **Paper aesthetic** — Crimson Pro headings, Source Sans 3 body, IBM Plex Mono for data, warm `#faf9f6` background with SVG fractal noise texture
- **8 tabs** — Terminal, Trades, Portfolio, Agents, Strategies, Accounts, Plugins, Settings
- **Real-time SSE streaming** — Agent text, tool calls, tool results, beat lifecycle, trade events
- **Terminal search/filter** — Search logs by text, filter by level (text, tools, errors, beats)
- **Chat input** — Send messages to the agent, interrupt running beats
- **Mobile-first** — Responsive layout, touch-friendly, tab-based navigation
- **Tab visibility optimization** — Pauses SSE and polling when tab is hidden

### Security & Guardrails
- **Token-based auth** — Set `AGENT_AUTH_TOKEN` env var to require Bearer token on all API routes
- **Secret stripping** — `safeConfig()` masks secret keys in all SSE broadcasts and API responses
- **MCP permission enforcement** — `enforcePermissions()` checks before every tool execution:
  - `blockedTools` — Reject calls to specific tools
  - `allowLiveTrading` — Block all order tools when disabled
  - `allowOptions` / `allowStocks` — Asset class gates
  - `allow0DTE` — Parses OCC option symbols to check expiration date
  - `maxOrderValue` — Rejects orders exceeding dollar limit
  - `requireConfirmation` — Blocks orders with descriptive error
- **Daily loss circuit breaker** — Auto-pauses agent when P&L exceeds `maxDailyLoss`%
- **Max tool rounds per beat** — Passed as `--max-turns` to OpenCode CLI
- **Path traversal protection** — `get_news_summary` sanitizes filenames

### Multi-Account
- **Multiple Alpaca accounts** — Add paper/live accounts via dashboard
- **Hot-swap** — Activating a different account kills the Go backend and restarts with new credentials
- **Go backend auto-restart** — 5-second delay restart on unexpected crashes

### Plugins
- **Slack notifications** — Trade executed, agent start/stop, errors, position opened/closed, daily summary, heartbeat
- **Daily summary** — Scheduled at 4:30 PM ET with P&L, portfolio value, and beat/trade/error counts

### AI Intelligence
- **Gemini news cleaning** — Transforms noisy RSS feeds into structured trading intelligence
- **Multi-source aggregation** — Google News, MarketWatch (top stories, real-time, bulletins, market pulse)
- **Technical analysis** — RSI, MACD, momentum indicators via Go backend
- **Vector similarity search** — Semantic search over past trades using local embeddings (384-dim, sqlite-vec)

---

## Quick Start

### Prerequisites

- **Go 1.22+** — For the trading backend
- **Node.js 18+** — For the agent server and MCP tools
- **[OpenCode CLI](https://opencode.ai)** — The AI harness that drives the autonomous agent
- **Alpaca account** — [alpaca.markets](https://alpaca.markets) (paper trading is free)

### 1. Clone and Install

```bash
git clone https://github.com/NeverLucky2/ClaudeProphetAndFriends
cd ClaudeProphetAndFriends
npm install
go build -o prophet_bot.exe ./cmd/bot
cp opencode.example.jsonc opencode.jsonc
```

### 2. Configure Environment

```bash
cp .env.example .env
```

Edit `.env`:
```bash
# Required
ALPACA_PUBLIC_KEY=your_alpaca_public_key
ALPACA_SECRET_KEY=your_alpaca_secret_key
ALPACA_ENDPOINT=https://paper-api.alpaca.markets

# Optional
GEMINI_API_KEY=your_gemini_key        # AI news cleaning
AGENT_AUTH_TOKEN=your_secret_token    # Protect dashboard API
AGENT_PORT=3737                       # Dashboard port
FMP_API_KEY=your_fmp_key              # Analysis scheduler + screener skills (free tier: 250 calls/day)
```

### 3. Install and Authenticate OpenCode

OpenProphet uses [OpenCode](https://opencode.ai) as its AI runtime. OpenCode is an open-source CLI that connects to Claude (and other models) with full MCP tool support. The agent harness spawns `opencode run` as a subprocess on each heartbeat.

```bash
# Install OpenCode globally
npm install -g opencode

# Authenticate with Anthropic (opens browser for OAuth)
opencode auth login
```

After login, verify it worked:

```bash
opencode auth list
# Should show "Anthropic" with "oauth" credential
```

#### OpenCode Configuration

OpenProphet requires an `opencode.jsonc` file in the project root to register the MCP trading tools. This file is **not included in the repo** (it's gitignored) since it may contain personal MCP servers or API keys. Create your own from the provided example:

```bash
cp opencode.example.jsonc opencode.jsonc
```

The example config registers the Prophet MCP tools server, which is all you need:

```jsonc
// opencode.jsonc
{
  "mcp": {
    "prophet": {
      "type": "local",
      "command": ["node", "./mcp-server.js"],
      "enabled": true
    }
  }
}
```

You can add any additional MCP servers you use (Playwright, Cartogopher, etc.) to your local `opencode.jsonc` -- it won't be committed.

#### How the Agent Uses OpenCode

Each heartbeat, the harness spawns:

```bash
opencode run \
  --format json \
  --model anthropic/claude-sonnet-4-6 \
  --max-turns 25 \
  --session <session-id>
```

- `--format json` — Streams structured events (text, tool_use, step_finish) that the dashboard parses
- `--model` — Set from the dashboard Settings tab (any Anthropic model)
- `--max-turns` — Maps to `maxToolRoundsPerBeat` in permissions config
- `--session` — Continues the same conversation across beats, preserving context

The system prompt is piped via stdin (too large for CLI args). On the first beat it includes the full system prompt + trading rules. Subsequent beats on the same session skip the system prompt to save ~2,000 tokens/beat.

#### Using OpenCode Interactively (Optional)

You can also use OpenCode directly for manual trading with the same MCP tools:

```bash
# Start the Go backend first
.\prophet_bot.exe

# Then run OpenCode interactively with the trading tools
opencode
```

OpenCode will pick up the `opencode.jsonc` config and give you access to all 45+ trading tools in an interactive chat session. This is useful for manual trading sessions or testing tools before enabling the autonomous agent.

### 4. Start the Dashboard

```bash
npm run agent
```

This starts the Express server on port 3737, which automatically launches the Go backend. Open `http://localhost:3737` (or your network IP) and press **Start**.

You can also authenticate OpenCode from the dashboard's **Settings** tab if you haven't done it from the CLI.

### 5. (Alternative) MCP-Only Mode

If you just want the MCP tools without the autonomous agent — for use with Claude Code, Cursor, or any MCP-compatible client:

```bash
# Start Go backend
.\prophet_bot.exe

# Option A: Use with OpenCode interactively
opencode

# Option B: Configure in Claude Code's .mcp.json
# Option C: Point any MCP client at: node /path/to/mcp-server.js
```

The MCP server is a standalone stdio server that works with any MCP-compatible client. It connects to the Go backend on port 4534.

---

## Strategy Improvement Loop

Prophet logs every decision — buy, sell, hold — to `data/sandboxes/<id>/decisive_actions/` with full reasoning. Four Claude Code skills in `.claude/skills/` close the feedback loop, turning those logs into updated strategy rules.

Open a Claude Code session in this project directory and type the slash command to run any skill.

### Daily — `/agent-health`

**Run:** Every trading day. Also run any time the agent behaves unexpectedly.

Confirms the correct strategy is loaded, scans the last 24 hours of decisions for rule violations, and checks whether loss-review thresholds (3.5% / 5% intraday drawdown) were triggered and honored. Returns a single-line `HEALTHY / WATCH / ALERT` status with a reason.

```
/agent-health
```

---

### Daily (pre-market) — `/scenario-analysis`

**Run:** Every trading day before the open. Also run before any macro-driven position entry over $20K, or any time a major macro event (FOMC decision, tariff announcement, inflation data, geopolitical shock) could shift the thesis on an existing position.

Scans today's top financial headlines, selects the most macro-significant story, and runs a two-phase analysis pipeline. The **Scenario Analyst** builds 18-month Base/Bull/Bear scenarios with 1st/2nd/3rd-order sector impacts and 3–5 positive and negative US stock picks. The **Strategy Reviewer** then independently critiques the output for coverage gaps, probability errors, bias, and missing alternative scenarios. Both reports are saved to `data/reports/`.

```
/scenario-analysis                                    # auto — finds today's top macro headline
/scenario-analysis "Fed signals no cuts until 2027"   # manual — provide a specific headline
/scenario-analysis tariffs China semiconductors       # manual — provide a topic
```

---

### Weekly — `/review-performance`

**Run:** Every Monday pre-market. Also run after any stretch of 3+ consecutive losing trades.

Reads the last 5 activity logs and 40 decisive actions. Computes win rate, profit factor, largest win/loss, and rule violation count. Surfaces behavioral patterns — hesitation, revenge trading, thesis drift. Ends with specific recommended rule changes. Does **not** apply them; use `/adapt-strategy` for that.

```
/review-performance
```

---

### After Any Notable Loss — `/postmortem [symbol or date]`

**Run:** Immediately after a significant losing trade, a winner that reverses into a loss, or any time the same mistake seems to be repeating.

Reconstructs the full trade timeline, identifies the single root cause (bad entry, stop discipline failure, no partial exit, etc.), and writes the exact rule text that would have prevented it. Also scans history to check whether the same mistake has appeared before.

```
/postmortem QQQ              # deep-dive on a specific symbol
/postmortem 2026-04-23       # deep-dive on a specific session
/postmortem                  # auto-selects most recent losing trade
```

---

### Weekly or After Any Bad Stretch — `/adapt-strategy`

**Run:** After `/review-performance` surfaces patterns, or after `/postmortem` identifies a recurring mistake. Also run after any 3+ consecutive losing days.

Compares the last 30 days of actual decisions against the current `Aggressive Options v2` rules, proposes specific edits with quoted evidence from the decision log, and applies the ones you approve. Changes are written to `data/agent-config.json` and take effect on Prophet's **next heartbeat** — no restart needed.

```
/adapt-strategy
```

---

### Recommended Schedule

| Frequency | Skill | Trigger |
|---|---|---|
| Daily (pre-market) | `/scenario-analysis` | Every trading day before open; before any macro-driven entry >$20K |
| Daily | `/agent-health` | Every trading day |
| Weekly | `/review-performance` | Every Monday pre-market |
| On demand | `/postmortem [symbol]` | After any significant loss |
| Weekly / on demand | `/adapt-strategy` | After review or 3+ losing days |

The full weekly cycle:

```
Daily pre-market:    /scenario-analysis     ← macro briefing; auto-finds today's headline
                     /agent-health          ← operational status check

Monday pre-market:   /review-performance
                     /adapt-strategy        ← apply approved findings

Any loss:            /postmortem [symbol]
                     /adapt-strategy        ← apply if pattern is confirmed

Before any macro     /scenario-analysis "headline or topic"
trade >$20K:
```

Each `/adapt-strategy` run produces rules that reflect what the agent actually did — not aspirational guidelines written in a vacuum. Over time the gap between stated rules and actual behavior narrows, and the strategy compounds on itself.

---

## Research & Analysis Skills

Twenty-one additional Claude Code skills are available in `.claude/skills/`. These are manual research tools — run them from a Claude Code session in the project directory.

> **FMP API key required** for screener and detector skills. Set `FMP_API_KEY` in your shell environment (free tier: 250 calls/day). Skills marked **No API** use public data only.

---

### Market Intelligence (No API)

| Skill | When to run | What it does |
|-------|-------------|--------------|
| `/market-environment-analysis` | Pre-market or any time you need global context | Scans US, European, and Asian markets; forex, commodities, bonds, and volatility. Returns a structured multi-market overview with risk-on/risk-off bias. |
| `/market-news-analyst` | After any news-driven move | Gathers and synthesizes the past 10 days of market-moving news. Maps each story to sector impact (positive/negative/neutral) and identifies persistence vs. one-day noise. |
| `/sector-analyst` | Weekly or when rotation appears | Fetches TraderMonty sector uptrend CSV data (no API key) and produces a sector rotation heat map. Identifies leading and lagging sectors and their phase in the market cycle. |
| `/uptrend-analyzer` | Weekly or before sizing up exposure | Reads TraderMonty's Uptrend Ratio Dashboard (no API key) and produces a 0–100 composite breadth score. Score bands: 80+ (bull), 50–79 (mixed), below 50 (bearish). |
| `/breadth-chart-analyst` | Weekly or on breadth divergences | Parses TraderMonty S&P 500 Breadth Index and US Stock Market Uptrend Ratio CSVs. Optionally accepts chart image paths for visual analysis. Outputs current reading, trend, and regime verdict. |
| `/us-market-bubble-detector` | Monthly or when valuations concern you | Scores the market on the Minsky/Kindleberger bubble framework using 5 quantitative indicators (valuation, leverage, narrative momentum, smart money flow, policy accommodation). |
| `/us-stock-analysis [TICKER]` | Before entering a new position | Full fundamental + technical analysis for any US-listed stock: financial metrics, business quality, valuation, technical indicators, catalysts, and a risk/reward summary. |

---

### Market Regime & Timing (FMP API)

| Skill | When to run | What it does |
|-------|-------------|--------------|
| `/macro-regime-detector` | Monthly or on regime-change signals | Cross-asset analysis: RSP/SPY concentration, yield curve, credit conditions, sector rotation, and size factor. Returns one of four regimes: Risk-On / Risk-Off / Stagflation / Deflation. |
| `/market-top-detector` | Weekly or on high-distribution signals | Counts O'Neil distribution days on SPY/QQQ, tracks Minervini leading stock deterioration, and monitors Monty defensive sector rotation. Returns a 0–100 market-top probability score. |
| `/ftd-detector` | After a 10%+ drawdown or during correction | Identifies Follow-Through Days (FTD) on S&P 500 and Nasdaq using O'Neil's methodology (Day 4+, +1.7%+, volume above prior). Also monitors post-FTD health. |
| `/economic-calendar-fetcher` | Monday pre-market | Fetches the upcoming week's FOMC decisions, CPI/PPI prints, NFP report, GDP releases, and other tier-1 macro events. Flags the highest-impact dates. |
| `/earnings-calendar` | Monday pre-market | Fetches mid-cap+ earnings announcements for the coming week via FMP. Highlights names most likely to move the broader market narrative. |

---

### Screeners & Trade Setup (FMP API)

| Skill | When to run | What it does |
|-------|-------------|--------------|
| `/vcp-screener` | Weekly or when building a watchlist | Screens S&P 500 for Minervini Volatility Contraction Patterns. Three-phase pipeline: pre-filter → Stage 2 Trend Template → VCP detection and scoring. Returns Pre-breakout / Breakout candidates with pivot price and stop-loss. |
| `/pead-screener` | Weekly (Friday evening or Sunday) | Screens for Post-Earnings Announcement Drift setups: gap-up stocks showing red-candle weekly pullbacks near breakout levels. Scores each on 5 factors and returns actionable candidates. |
| `/earnings-trade-analyzer` | After earnings season each week | Scores recent post-earnings movers on 5 factors: Gap Size, Pre-Earnings Trend, Volume Trend, MA200 Position, MA50 Position. Identifies the strongest post-earnings drift candidates. |
| `/theme-detector` | Weekly or on sector momentum signals | Detects trending market themes (e.g. AI infrastructure, GLP-1, defense) using FINVIZ public performance data. Scores each theme on lifecycle stage (Emerging/Accelerating/Maturing/Rotating). No API key required for base run; FMP optional for deeper analysis. |
| `/finviz-screener [description]` | Any time you need a quick screen | Translates natural language into a FinViz screener URL and opens it in your browser. Example: `/finviz-screener small cap momentum stocks near 52-week high with high volume`. |

---

### Trade Execution Support

| Skill | When to run | What it does |
|-------|-------------|--------------|
| `/position-sizer` | Before every new entry | Calculates risk-based position size using fixed fractional, ATR-based, or Kelly criterion. Pass account size, risk %, entry price, and stop-loss price. Returns shares and dollar allocation. |
| `/breakout-trade-planner` | After `/vcp-screener` identifies candidates | Generates a full Minervini-style breakout trade plan from VCP screener JSON output: entry pivot, stop-loss (below last contraction low), R-multiple targets, portfolio heat check, and ready-to-paste Alpaca order templates. |

---

### Meta-Synthesizers

These skills aggregate output from the other skills above into a single high-level verdict. Run the upstream skills first and pass their output (or the `data/reports/` path) to the synthesizer.

| Skill | Upstream inputs | What it produces |
|-------|----------------|-----------------|
| `/exposure-coach` | `/breadth-chart-analyst`, `/uptrend-analyzer`, `/macro-regime-detector`, `/market-top-detector`, `/ftd-detector` | One-page Market Posture: **NEW_ENTRY_ALLOWED** / **REDUCE_ONLY** / **CASH_PRIORITY** with a net exposure ceiling (0–100%), growth-vs-value bias, and participation breadth verdict. |
| `/stanley-druckenmiller-investment` | 8 upstream skill JSON outputs (market breadth, uptrend, market top, macro regime, sector, theme, scenario, bubble detector) | Druckenmiller-style 0–100 conviction score, 4-pattern classification (Momentum Bull / Defensive Rotation / Bear Market / Crash Risk), and position sizing multiplier recommendation. |

---

### Extended Schedule

| Frequency | Skill | Trigger |
|-----------|-------|---------|
| Daily (pre-market) | `/scenario-analysis` | Every trading day; before any macro entry >$20K |
| Daily (pre-market) | `/agent-health` | Every trading day |
| Daily (pre-market) | `/economic-calendar-fetcher` | Check for tier-1 macro events this week |
| Daily | `/market-environment-analysis` | Any time you need global risk-on/off read |
| Weekly | `/review-performance` | Every Monday pre-market |
| Weekly | `/adapt-strategy` | After review or 3+ losing days |
| Weekly | `/earnings-calendar` | Monday pre-market — flag earnings landmines |
| Weekly | `/sector-analyst` + `/uptrend-analyzer` | Sunday or Monday — breadth check |
| Weekly | `/market-top-detector` | Sunday — distribution day count |
| Weekly | `/ftd-detector` | During/after corrections — confirm or deny recovery |
| Weekly | `/vcp-screener` + `/pead-screener` | Sunday evening — build watchlist |
| Weekly | `/exposure-coach` | After breadth/regime runs — set exposure ceiling |
| Monthly | `/macro-regime-detector` | Regime shifts or cross-asset anomalies |
| Monthly | `/us-market-bubble-detector` | Valuation concerns or late-cycle signals |
| On demand | `/postmortem [symbol]` | After any significant loss |
| On demand | `/us-stock-analysis [TICKER]` | Before entering any new position |
| On demand | `/finviz-screener [description]` | Quick screen for any criteria |
| On demand | `/position-sizer` | Before every new entry |
| On demand | `/market-news-analyst` | After news-driven moves |
| On demand | `/earnings-trade-analyzer` | After earnings reports |
| On demand | `/theme-detector` | When a sector starts leading/lagging |
| On demand | `/breakout-trade-planner` | After VCP screener finds candidates |
| On demand | `/stanley-druckenmiller-investment` | Before sizing into a high-conviction macro trade |

---

## Automated Analysis Scheduler

The scheduler runs two recurring jobs automatically whenever the server is running — no manual invocation needed. Results are saved to `data/reports/` and Prophet reads them automatically at the start of each pre-market heartbeat.

### Jobs

| Job | Trigger | Output | Notes |
|-----|---------|--------|-------|
| `daily_briefing` | 6:00 AM ET weekdays · startup if file missing | `data/reports/daily_brief_YYYYMMDD.json` | FMP optional |
| `weekly_screeners` | 6:00 PM ET Sundays | `data/reports/weekly_regime_YYYYMMDD.json` | FMP required for screeners |
| `scenario_analysis` | Startup if no `scenario_*_YYYYMMDD.md` today | `data/reports/scenario_*.md` + `review_*.md` | Web search required |
| `review_performance` | 6:05 AM ET Mondays · startup if not run this week | Terminal output only | Reads last 5 sessions + 40 decisions |
| `postmortem` | 4:30 PM ET if session P&L ≤ −5% · startup if yesterday was ≤ −5% | Terminal output only | Passes loss date as argument |
| `adapt_strategy` | After `review_performance` or `postmortem` · after 3 consecutive losing days | Edits `data/agent-config.json` | Auto-applies all proposed rule changes |

**State persistence:** `review_performance`, `postmortem`, and `adapt_strategy` don't produce output files, so the scheduler tracks whether they've already run in `data/scheduler-state.json`. This survives server restarts — if you ran the weekly review and then restart the server, it won't re-run until next week.

**Significant loss threshold:** A session P&L of −5% or worse triggers `postmortem`. This matches the agent's own stop-trading threshold — if the agent halted trading for the day, the session is worth analyzing. Sessions with zero trades are ignored.

### Daily briefing JSON structure

```json
{
  "date": "2026-04-23",
  "generated_at": "2026-04-23T10:00:00Z",
  "market_posture": "BULLISH",
  "breadth_score": 74,
  "uptrend_ratio": 68.5,
  "ftd_status": "active_ftd",
  "tier1_macro_events": [{ "date": "2026-04-29", "event": "FOMC Decision", "impact": "high" }],
  "key_earnings_this_week": [{ "date": "2026-04-24", "ticker": "META", "timing": "after_close" }],
  "exposure_ceiling_pct": 100,
  "summary": "Market breadth is improving with 74/100 composite score..."
}
```

`market_posture` bands: **BULLISH** (breadth ≥ 70) / **NEUTRAL** (40–69) / **BEARISH** (< 40). `exposure_ceiling_pct` is automatically reduced when `ftd_status` is `correction` or a tier-1 macro event falls today.

### Weekly regime JSON structure

```json
{
  "date": "2026-04-20",
  "generated_at": "2026-04-20T22:00:00Z",
  "breadth_score": 71,
  "uptrend_ratio": 65.2,
  "market_top_score": 32,
  "distribution_days": 2,
  "market_posture": "BULLISH",
  "vcp_candidates": [
    { "ticker": "NVDA", "score": 88, "execution_state": "Pre-breakout", "pivot_price": 875.0, "stop_loss": 840.0 }
  ],
  "pead_candidates": [
    { "ticker": "AXON", "score": 79, "entry_zone": "265-270", "stop_loss": 258.0 }
  ],
  "weekly_thesis": "Breadth expanding with low distribution. Favor momentum longs..."
}
```

`market_posture` options: **BULLISH** / **NEUTRAL** / **BEARISH** / **CORRECTION** / **TOP_RISK**. Up to 5 candidates each, sorted by score descending.

### How Prophet uses the reports

On every pre-market heartbeat Prophet calls `read_latest_report("daily_brief")` first. On Mondays it additionally calls `read_latest_report("weekly_regime")`. The reports inform:

- Whether to enter new positions (`exposure_ceiling_pct`)
- Which watchlist candidates to prioritize (`vcp_candidates`, `pead_candidates`)
- Whether a macro event today warrants reducing size
- Whether an FTD is active (supports adding exposure) or a correction is in progress (supports cutting exposure)

### Triggering jobs manually

Use the REST API to run any job immediately without waiting for the schedule:

```bash
# Pre-market briefing
curl -X POST http://localhost:3737/api/scheduler/trigger \
  -H "Content-Type: application/json" \
  -d '{"job": "daily_briefing"}'

# Weekly screeners
curl -X POST http://localhost:3737/api/scheduler/trigger \
  -H "Content-Type: application/json" \
  -d '{"job": "weekly_screeners"}'

# Scenario analysis
curl -X POST http://localhost:3737/api/scheduler/trigger \
  -H "Content-Type: application/json" \
  -d '{"job": "scenario_analysis"}'

# Weekly performance review
curl -X POST http://localhost:3737/api/scheduler/trigger \
  -H "Content-Type: application/json" \
  -d '{"job": "review_performance"}'

# Postmortem for a specific date or symbol
curl -X POST http://localhost:3737/api/scheduler/trigger \
  -H "Content-Type: application/json" \
  -d '{"job": "postmortem", "date": "2026-04-22"}'

# Adapt strategy (auto-applies all proposed rule changes)
curl -X POST http://localhost:3737/api/scheduler/trigger \
  -H "Content-Type: application/json" \
  -d '{"job": "adapt_strategy"}'
```

If `AGENT_AUTH_TOKEN` is set, add `-H "Authorization: Bearer <token>"` to the request.

Only one job runs at a time. If a job is already running, the API returns `400 { "error": "Job already running: <name>" }`.

### Checking scheduler status

The scheduler status is included in the health endpoint:

```bash
curl http://localhost:3737/api/health
```

```json
{
  "scheduler": {
    "running": true,
    "activeJob": null,
    "lastDailyBriefDate": "2026-04-23",
    "lastWeeklyScreenDate": "2026-04-20",
    "lastScenarioDate": "2026-04-23",
    "lastReviewWeek": "2026-W17",
    "lastPostmortemDate": "2026-04-22",
    "lastAdaptDate": "2026-04-22"
  }
}
```

---

## MCP Tools Reference

### Trading (order execution)

| Tool | Description |
|------|-------------|
| `place_options_order` | Buy/sell options with limit orders |
| `place_managed_position` | Position with automated stop-loss / take-profit |
| `close_managed_position` | Close managed position at market |
| `place_buy_order` | Buy stock shares |
| `place_sell_order` | Sell stock shares |
| `cancel_order` | Cancel a pending order |

### Market Data

| Tool | Description |
|------|-------------|
| `get_account` | Portfolio value, cash, buying power, equity |
| `get_positions` | All open stock positions |
| `get_options_positions` | All open options positions |
| `get_options_position` | Single option position by symbol |
| `get_options_chain` | Options chain with strike/expiry filtering |
| `get_orders` | Order history |
| `get_quote` | Real-time stock quote |
| `get_latest_bar` | Latest OHLCV bar |
| `get_historical_bars` | Historical price bars |
| `get_managed_positions` | Managed positions with stop/target status |

### News & Intelligence

| Tool | Description |
|------|-------------|
| `get_quick_market_intelligence` | AI-cleaned MarketWatch summary |
| `analyze_stocks` | Technical analysis + news + recommendations |
| `get_cleaned_news` | Multi-source aggregated intelligence |
| `search_news` | Google News keyword search |
| `get_news` | Latest Google News |
| `get_news_by_topic` | News by topic (business, technology, etc.) |
| `get_market_news` | Market-specific news feed |
| `aggregate_and_summarize_news` | Custom aggregation with AI summary |
| `list_news_summaries` / `get_news_summary` | Cached news summaries |
| `get_marketwatch_topstories` | MarketWatch top stories |
| `get_marketwatch_realtime` | Real-time headlines |
| `get_marketwatch_bulletins` | Breaking news |
| `get_marketwatch_marketpulse` | Quick market pulse |
| `get_marketwatch_all` | All MarketWatch feeds combined |

### Vector Search (AI Memory)

| Tool | Description |
|------|-------------|
| `find_similar_setups` | Semantic search over past trades |
| `store_trade_setup` | Store a trade for future pattern matching |
| `get_trade_stats` | Win rate, profit factor by symbol/strategy |

### Agent Self-Modification

| Tool | Description |
|------|-------------|
| `update_agent_prompt` | Update the active agent's system prompt |
| `update_strategy_rules` | Update trading strategy rules |
| `get_agent_config` | Read current agent config and permissions |
| `set_heartbeat` | Override heartbeat interval dynamically |
| `update_permissions` | Modify permission guardrails |

### Analysis & Screeners

These tools are called automatically by the scheduler but can also be called by Prophet directly on any heartbeat.

| Tool | Requires | Description |
|------|----------|-------------|
| `run_market_briefing` | — | Fetch market breadth and uptrend ratio from TraderMonty public CSVs. Returns composite breadth score (0–100) and uptrend ratio. |
| `run_ftd_check` | FMP_API_KEY | Detect Follow-Through Day signals on S&P 500 and Nasdaq. Returns `active_ftd`, `rally_attempt`, `no_signal`, or `correction`. |
| `run_economic_calendar` | FMP_API_KEY | Fetch tier-1 macro events for the next 14 days (FOMC, CPI, NFP, GDP). |
| `run_earnings_calendar` | FMP_API_KEY | Fetch mid-cap+ earnings announcements for the current week. |
| `run_market_top_check` | FMP_API_KEY | Count O'Neil distribution days and return a 0–100 market-top probability score. Runs synchronously (~90 seconds). |
| `run_vcp_screener` | FMP_API_KEY | Screen S&P 500 for Minervini VCP breakout candidates. Launches as a background job. Call `wait(180)` then `read_latest_report("vcp")` for results. |
| `run_pead_screener` | FMP_API_KEY | Screen for PEAD setups (gap-up stocks showing red-candle pullbacks). Background job — same pattern as VCP. |
| `read_latest_report` | — | Read the most recent JSON report of a given type from `data/reports/`. Valid types: `vcp`, `pead`, `market_top`, `ftd`, `daily_brief`, `weekly_regime`, `uptrend`, `scenario`, `review`. |

### Utilities

| Tool | Description |
|------|-------------|
| `log_decision` | Log a trading decision with reasoning |
| `log_activity` | Log activity to daily journal |
| `get_activity_log` | Retrieve today's activity log |
| `wait` | Pause execution (max 300 seconds) |
| `get_datetime` | Current time in US Eastern timezone |

---

## Dashboard API

The agent server exposes 40 REST endpoints under `/api/`:

### Agent Control
| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/agent/start` | Start the autonomous agent |
| POST | `/api/agent/stop` | Stop the agent (kills subprocess) |
| POST | `/api/agent/pause` | Pause heartbeat loop |
| POST | `/api/agent/resume` | Resume heartbeat loop |
| POST | `/api/agent/message` | Send message to agent (interrupts if busy) |
| POST | `/api/agent/heartbeat` | Override heartbeat interval |
| GET | `/api/agent/state` | Current agent state |
| GET | `/api/agent/prompt-preview` | Preview active system prompt |
| GET | `/api/events` | SSE event stream |

### Configuration CRUD
| Method | Endpoint | Description |
|--------|----------|-------------|
| GET/POST | `/api/accounts` | List / add Alpaca accounts |
| DELETE | `/api/accounts/:id` | Remove account |
| POST | `/api/accounts/:id/activate` | Switch active account (restarts Go backend) |
| GET/POST | `/api/agents` | List / add agent personas |
| PUT | `/api/agents/:id` | Update agent |
| POST | `/api/agents/:id/activate` | Switch active agent |
| GET/POST | `/api/strategies` | List / add strategies |
| PUT | `/api/strategies/:id` | Update strategy |
| GET/PUT | `/api/permissions` | Get / update guardrails |
| GET/PUT | `/api/heartbeat` | Get / update phase intervals |
| GET/PUT | `/api/plugins/:name` | Get / update plugin config |
| POST | `/api/models/activate` | Switch Claude model |

### Analysis Scheduler
| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/scheduler/trigger` | Manually trigger a job: `{"job": "daily_briefing"}` or `{"job": "weekly_screeners"}`. Optional `"date": "YYYY-MM-DD"`. Returns `400` if a job is already running. |
| GET | `/api/health` | Includes `scheduler` field: `{ running, activeJob, lastDailyBriefDate, lastWeeklyScreenDate }` |

### Portfolio Proxy
| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/portfolio/account` | Proxied account info from Go backend |
| GET | `/api/portfolio/positions` | Proxied positions |
| GET | `/api/portfolio/orders` | Proxied orders |

---

## Configuration

All runtime config is stored in `data/agent-config.json`. The dashboard provides a UI for everything, but the structure is:

```jsonc
{
  "activeAccountId": "abc123",
  "activeAgentId": "default",
  "activeModel": "anthropic/claude-sonnet-4-6",

  "heartbeat": {
    "pre_market": 900,     // seconds
    "market_open": 120,
    "midday": 600,
    "market_close": 120,
    "after_hours": 1800,
    "closed": 3600
  },

  "permissions": {
    "allowLiveTrading": true,
    "allowOptions": true,
    "allowStocks": true,
    "allow0DTE": false,
    "requireConfirmation": false,
    "maxPositionPct": 15,
    "maxDeployedPct": 80,
    "maxDailyLoss": 5,
    "maxOpenPositions": 10,
    "maxOrderValue": 0,        // 0 = unlimited
    "maxToolRoundsPerBeat": 25,
    "blockedTools": []
  },

  "accounts": [{ "id": "...", "name": "Paper", "publicKey": "...", "secretKey": "...", "paper": true }],
  "agents": [{ "id": "default", "name": "Prophet", "strategyId": "v2-options", "model": "..." }],
  "strategies": [{ "id": "v2-options", "name": "Aggressive Options v2", "rulesFile": "TRADING_RULES_V2.md" }],

  "plugins": {
    "slack": {
      "enabled": false,
      "webhookUrl": "",
      "notifyOn": { "tradeExecuted": true, "agentStartStop": true, "errors": true, "dailySummary": true }
    }
  }
}
```

### Available Models

| Model | Cost (input/output per MTok) |
|-------|-----|
| `anthropic/claude-sonnet-4-6` | $3 / $15 |
| `anthropic/claude-opus-4-6` | $5 / $25 |
| `anthropic/claude-haiku-4-5` | $1 / $5 |

---

## Go Backend Services

| Service | Purpose | Key Functions |
|---------|---------|---------------|
| `AlpacaTradingService` | Order execution | PlaceOrder, CancelOrder, GetPositions, GetAccount |
| `AlpacaDataService` | Market data (IEX feed) | GetHistoricalBars, GetLatestQuote, GetLatestBar |
| `AlpacaOptionsDataService` | Options data | GetOptionChain, GetOptionSnapshot, FindOptionsNearDTE |
| `PositionManager` | Automation | MonitorPositions, CloseManagedPosition |
| `StockAnalysisService` | Analysis | AnalyzeStock |
| `TechnicalAnalysisService` | Indicators | CalculateRSI, CalculateMACD |
| `NewsService` | Intelligence | GetGoogleNews, GetMarketWatchTopStories, AggregateAndSummarize |
| `GeminiService` | AI processing | CleanNewsForTrading |
| `ActivityLogger` | Journaling | LogDecision, LogActivity, LogPositionOpened/Closed |

---

## Development

### Adding a New MCP Tool

1. Add the endpoint in Go (`controllers/` + route in `cmd/bot/main.go`)
2. Add the tool definition in `mcp-server.js` (name, description, input schema)
3. Add the handler in the `switch` block in `mcp-server.js`
4. If it's an order tool, add permission checks in `enforcePermissions()`

### Project Scripts

```bash
npm run agent    # Start dashboard + agent server (port 3737)
npm start        # Start MCP server only (for Claude Code integration)
```

---

## Disclaimer

THIS SOFTWARE IS PROVIDED "AS IS" WITHOUT WARRANTY OF ANY KIND. The author strongly recommends against using this system with real money. Options trading carries substantial risk of loss. Past performance does not guarantee future results. You are solely responsible for your own trading decisions.

---

## License

[CC BY-NC 4.0](https://creativecommons.org/licenses/by-nc/4.0/) — Free for personal and non-commercial use. See [LICENSE](LICENSE) for details.

Copyright (c) 2025 Jake Nesler
