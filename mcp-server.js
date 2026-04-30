#!/usr/bin/env node

import { Server } from '@modelcontextprotocol/sdk/server/index.js';
import { StdioServerTransport } from '@modelcontextprotocol/sdk/server/stdio.js';
import {
  CallToolRequestSchema,
  ListToolsRequestSchema,
} from '@modelcontextprotocol/sdk/types.js';
import { GoogleGenerativeAI } from '@google/generative-ai';
import axios from 'axios';
import { spawnSync, spawn as spawnBg } from 'child_process';
import fs from 'fs/promises';
import path from 'path';

const PYTHON_BIN = process.platform === 'win32' ? 'python' : 'python3';
const REPORTS_DIR = path.join(process.cwd(), 'data', 'reports');
import { storeTrade, findSimilarTrades, getTradeStats, getEmbeddingCount } from './vectorDB.js';

// Configuration
const TRADING_BOT_URL = process.env.TRADING_BOT_URL || 'http://localhost:4534';
const GEMINI_API_KEY = process.env.GEMINI_API_KEY;
const OPENPROPHET_ACCOUNT_ID = process.env.OPENPROPHET_ACCOUNT_ID || 'default';
const OPENPROPHET_SANDBOX_ID = process.env.OPENPROPHET_SANDBOX_ID || `sbx_${OPENPROPHET_ACCOUNT_ID}`;

// Per-agent tool allowlist. When set (comma-separated tool names), the ListTools
// response is filtered to only those tools — saves prompt tokens by not exposing
// schemas for tools this agent never uses. Unset/empty = all tools (default).
const TOOL_ALLOWLIST = new Set(
  (process.env.OPENPROPHET_TOOL_ALLOWLIST || '')
    .split(',')
    .map(s => s.trim())
    .filter(Boolean)
);
const SANDBOX_DATA_DIR = path.join(process.cwd(), 'data', 'sandboxes', OPENPROPHET_ACCOUNT_ID);
const SUMMARIES_DIR = path.join(SANDBOX_DATA_DIR, 'news_summaries');
const DECISIONS_DIR = path.join(SANDBOX_DATA_DIR, 'decisive_actions');

// Initialize Gemini
const genAI = new GoogleGenerativeAI(GEMINI_API_KEY);
const model = genAI.getGenerativeModel({ model: 'gemini-2.0-flash-exp' });

// Ensure directories exist
await fs.mkdir(SUMMARIES_DIR, { recursive: true });
await fs.mkdir(DECISIONS_DIR, { recursive: true });

// ── News deduplication (entity + event bucketing) ─────────────────────────────
// Prevents the same story from appearing across multiple news tool calls in one
// heartbeat. Buckets by (ticker, event_type) so "Google surges on earnings" and
// "Alphabet jumps after Q1 results" both map to GOOG:earnings and the second is dropped.

const NEWS_DEDUP_TTL_MS = 30 * 60 * 1000; // reset every 30 min (one heartbeat window)

const newsDedup = {
  seen: new Set(),
  resetAt: Date.now() + NEWS_DEDUP_TTL_MS,
  checkAndReset() {
    if (Date.now() > this.resetAt) {
      this.seen.clear();
      this.resetAt = Date.now() + NEWS_DEDUP_TTL_MS;
    }
  },
};

// Longer aliases first so "goldman sachs" matches before "goldman".
// Each alias is compiled to a word-boundary regex to avoid substring false matches
// (e.g. "intel" matching "intelligence", "meta" matching "metals").
const TICKER_ALIASES = Object.entries({
  'goldman sachs': 'GS', 'j.p. morgan': 'JPM', 'bank of america': 'BAC',
  'morgan stanley': 'MS', 'microstrategy': 'MSTR', 'marathon digital': 'MARA',
  alphabet: 'GOOG', google: 'GOOG',
  facebook: 'META', meta: 'META',
  microsoft: 'MSFT', apple: 'AAPL', amazon: 'AMZN', tesla: 'TSLA',
  nvidia: 'NVDA', netflix: 'NFLX', intel: 'INTC', palantir: 'PLTR',
  coinbase: 'COIN', marathon: 'MARA', walmart: 'WMT', disney: 'DIS',
  salesforce: 'CRM', berkshire: 'BRK', jpmorgan: 'JPM', goldman: 'GS',
})
  .sort((a, b) => b[0].length - a[0].length)
  .map(([alias, ticker]) => [new RegExp(`\\b${alias.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}\\b`), ticker]);

const EVENT_KEYWORDS = {
  earnings:  ['earnings', 'revenue', 'profit', 'eps', ' q1 ', ' q2 ', ' q3 ', ' q4 ', 'quarterly', 'quarter results', 'better-than-expected', 'beat', 'miss', 'guidance', 'forecast', 'outlook'],
  analyst:   ['upgrade', 'downgrade', 'price target', 'overweight', 'underweight', 'outperform', 'underperform', 'buy rating', 'sell rating', 'raises target', 'cuts target'],
  ma:        ['merger', 'acquisition', 'acquires', 'buyout', 'takeover', 'to buy ', 'to acquire'],
  fda:       ['fda ', 'clinical trial', 'drug approval'],
  layoff:    ['layoff', 'layoffs', 'job cuts', 'restructur', 'downsiz', 'workforce reduction'],
  ipo:       ['ipo', 'goes public', 'public debut'],
  fed:       ['fomc', 'federal reserve', 'interest rate', 'rate hike', 'rate cut', 'powell', 'fed funds', 'basis point'],
  cpi:       ['consumer price index', 'cpi report', 'pce', 'ppi report', 'inflation data', 'inflation report'],
  jobs:      ['nfp', 'jobs report', 'payrolls', 'unemployment rate', 'jobless claims', 'labor market'],
  gdp:       ['gdp', 'gross domestic product'],
  tariff:    ['tariff', 'trade war', 'export ban', 'import duty'],
  legal:     ['lawsuit', 'sec charges', 'antitrust', 'fraud charges'],
};

// Common ALL-CAPS words in financial headlines that are not ticker symbols.
// Intentionally broad to prevent false dedup keys from words like RALLY, CHINA, OPEC.
const NON_TICKERS = new Set([
  'CEO', 'CFO', 'COO', 'CTO', 'IPO', 'ETF', 'SEC', 'FDA', 'DOJ', 'FED',
  'GDP', 'CPI', 'NFP', 'PCE', 'PPI', 'AI', 'US', 'UK', 'EU', 'NY', 'DC',
  'AM', 'PM', 'ET', 'EST', 'PST',
  // Common English words and abbreviations that appear ALL-CAPS in headlines
  'FOR', 'THE', 'AND', 'NOT', 'BUT', 'NEW', 'TOP', 'BIG', 'ALL', 'OUT',
  'UP', 'ON', 'IN', 'AT', 'BY', 'TO', 'AS', 'OR', 'AN',
  // Countries / regions
  'CHINA', 'JAPAN', 'INDIA', 'OPEC', 'NATO', 'BRICS', 'ECB', 'BOJ', 'BOE', 'IMF',
  // News-ese
  'BREAKING', 'UPDATE', 'WATCH', 'LIVE', 'NEWS', 'ALERT', 'REPORT', 'SAYS', 'SEES',
  'RALLY', 'BEATS', 'JUMPS', 'FALLS', 'RISES', 'SLUMP', 'CRASH', 'STOCK', 'BONDS',
  'CLOSE', 'OPENS', 'HIGHS', 'LOWER', 'MIXED', 'WORLD', 'FIRST', 'AFTER',
  // Regulators / agencies not already listed
  'FBI', 'CIA', 'IRS', 'EPA', 'FAA', 'TSA', 'WHO', 'CDC', 'GOP', 'DNC',
  // Media outlets (appear in "via CNBC" style suffixes)
  'WSJ', 'NYT', 'CNBC', 'BBC', 'WSJ', 'ESG', 'UAW',
  // Finance jargon all-caps
  'WTI', 'OIL', 'GAS', 'USD', 'EUR', 'YEN', 'GBP',
]);

function extractEntity(title) {
  const lower = title.toLowerCase();
  // Word-boundary regex per alias prevents "intel" → "intelligence" false matches
  for (const [re, ticker] of TICKER_ALIASES) {
    if (re.test(lower)) return ticker;
  }
  // Fall back to $AAPL cashtag or standalone 2-5 char ALL-CAPS words
  const matches = title.match(/\$([A-Z]{1,5})\b|\b([A-Z]{2,5})\b/g) || [];
  for (const m of matches) {
    const clean = m.replace('$', '');
    if (!NON_TICKERS.has(clean)) return clean;
  }
  return null;
}

function extractEvent(title) {
  const lower = ` ${title.toLowerCase()} `;
  for (const [event, keywords] of Object.entries(EVENT_KEYWORDS)) {
    if (keywords.some(kw => lower.includes(kw))) return event;
  }
  return 'general';
}

function deduplicateArticles(articles) {
  newsDedup.checkAndReset();
  const kept = [];
  for (const article of articles) {
    const title = article.title || article.headline || article.summary || '';
    if (!title) { kept.push(article); continue; }
    const entity = extractEntity(title);
    const event  = extractEvent(title);
    // If we can't identify either a specific entity OR a specific event, the story is
    // too ambiguous to bucket safely — keep it rather than risk collapsing unrelated articles
    // into a single "macro:general" slot.
    if (!entity && event === 'general') {
      kept.push(article);
      continue;
    }
    const key = entity ? `${entity}:${event}` : `macro:${event}`;
    if (!newsDedup.seen.has(key)) {
      newsDedup.seen.add(key);
      kept.push(article);
    }
  }
  return kept;
}

// Applies dedup to whichever array field a news response uses
function applyNewsDedup(data) {
  const fields = ['news', 'articles', 'bulletins', 'stories', 'items', 'results'];
  if (Array.isArray(data)) {
    const filtered = deduplicateArticles(data);
    return filtered;
  }
  for (const field of fields) {
    if (Array.isArray(data[field])) {
      const before = data[field].length;
      const filtered = deduplicateArticles(data[field]);
      return { ...data, [field]: filtered, _dedup_dropped: before - filtered.length };
    }
  }
  return data; // unknown shape — return as-is
}

// Helper to call trading bot API - resolves correct port per sandbox
async function getTradingBotUrl() {
  try {
    const resp = await agentAxios.get(`${AGENT_URL}/api/health`, { timeout: 3000 });
    const sandboxes = resp.data.sandboxes || [];
    const sandbox = sandboxes.find(s => s.sandboxId === OPENPROPHET_SANDBOX_ID);
    if (sandbox && sandbox.port) {
      return `http://localhost:${sandbox.port}`;
    }
  } catch {}
  return TRADING_BOT_URL;
}

let _tradingBotUrl = TRADING_BOT_URL;
let _lastPortCheck = 0;

async function callTradingBot(endpoint, method = 'GET', data = null) {
  try {
    // Refresh port every 30 seconds
    const now = Date.now();
    if (now - _lastPortCheck > 30000) {
      _tradingBotUrl = await getTradingBotUrl();
      _lastPortCheck = now;
    }
    const config = {
      method,
      url: `${_tradingBotUrl}/api/v1${endpoint}`,
      headers: { 'Content-Type': 'application/json' },
    };
    if (data) {
      config.data = data;
    }
    const response = await axios(config);
    return response.data;
  } catch (error) {
    throw new Error(`Trading bot error: ${error.message}`);
  }
}

// Create MCP server
const server = new Server(
  {
    name: 'openprophet',
    version: '1.0.0',
  },
  {
    capabilities: {
      tools: {},
    },
  }
);

// List available tools
server.setRequestHandler(ListToolsRequestSchema, async () => {
  const allTools = [
      {
        name: 'get_account',
        description: 'Get trading account information including cash, buying power, and portfolio value',
        inputSchema: {
          type: 'object',
          properties: {},
        },
      },
      {
        name: 'get_positions',
        description: 'Get all open positions in the trading account',
        inputSchema: {
          type: 'object',
          properties: {},
        },
      },
      {
        name: 'get_orders',
        description: 'Get all orders (open, filled, cancelled)',
        inputSchema: {
          type: 'object',
          properties: {},
        },
      },
      {
        name: 'place_buy_order',
        description: 'Place a buy order for a stock or option',
        inputSchema: {
          type: 'object',
          properties: {
            symbol: {
              type: 'string',
              description: 'Stock symbol (e.g., AAPL, TSLA)',
            },
            quantity: {
              type: 'number',
              description: 'Number of shares to buy',
            },
            order_type: {
              type: 'string',
              description: 'Order type (market, limit)',
              enum: ['market', 'limit'],
            },
            limit_price: {
              type: 'number',
              description: 'Limit price (required for limit orders)',
            },
          },
          required: ['symbol', 'quantity', 'order_type'],
        },
      },
      {
        name: 'place_sell_order',
        description: 'Place a sell order for a stock or option',
        inputSchema: {
          type: 'object',
          properties: {
            symbol: {
              type: 'string',
              description: 'Stock symbol (e.g., AAPL, TSLA)',
            },
            quantity: {
              type: 'number',
              description: 'Number of shares to sell',
            },
            order_type: {
              type: 'string',
              description: 'Order type (market, limit)',
              enum: ['market', 'limit'],
            },
            limit_price: {
              type: 'number',
              description: 'Limit price (required for limit orders)',
            },
          },
          required: ['symbol', 'quantity', 'order_type'],
        },
      },
      {
        name: 'place_managed_position',
        description: 'Open a managed position with automatic stop loss, take profit, and optional partial exits. Perfect for active swing trading.',
        inputSchema: {
          type: 'object',
          properties: {
            symbol: {
              type: 'string',
              description: 'Stock symbol (e.g., BE, NXT, GOOGL)',
            },
            side: {
              type: 'string',
              description: 'Position side',
              enum: ['buy', 'sell'],
            },
            strategy: {
              type: 'string',
              description: 'Trading strategy type',
              enum: ['SWING_TRADE', 'LONG_TERM', 'DAY_TRADE'],
            },
            allocation_dollars: {
              type: 'number',
              description: 'Dollar amount to allocate to this position',
            },
            entry_strategy: {
              type: 'string',
              description: 'Entry order type',
              enum: ['market', 'limit'],
            },
            entry_price: {
              type: 'number',
              description: 'Entry price (required for limit orders)',
            },
            stop_loss_percent: {
              type: 'number',
              description: 'Stop loss as % from entry (e.g., 15 for -15%)',
            },
            stop_loss_price: {
              type: 'number',
              description: 'Absolute stop loss price',
            },
            take_profit_percent: {
              type: 'number',
              description: 'Take profit as % from entry (e.g., 25 for +25%)',
            },
            take_profit_price: {
              type: 'number',
              description: 'Absolute take profit price',
            },
            trailing_stop: {
              type: 'boolean',
              description: 'Enable trailing stop loss',
            },
            trailing_percent: {
              type: 'number',
              description: 'Trailing stop percentage',
            },
            partial_exit: {
              type: 'object',
              description: 'Partial profit taking configuration',
              properties: {
                enabled: {
                  type: 'boolean',
                  description: 'Enable partial exits',
                },
                percent: {
                  type: 'number',
                  description: 'Percentage of position to exit (e.g., 50 for 50%)',
                },
                target_percent: {
                  type: 'number',
                  description: 'Profit % to trigger partial exit (e.g., 20 for +20%)',
                },
              },
            },
            notes: {
              type: 'string',
              description: 'Notes about this position',
            },
            tags: {
              type: 'array',
              description: 'Tags for categorization',
              items: {
                type: 'string',
              },
            },
          },
          required: ['symbol', 'side', 'allocation_dollars'],
        },
      },
      {
        name: 'get_managed_positions',
        description: 'List managed positions with optional status filter. By default, returns only ACTIVE positions for token efficiency. Use status="" or status="ALL" to get all positions.',
        inputSchema: {
          type: 'object',
          properties: {
            status: {
              type: 'string',
              description: 'Filter by status. Leave empty or use "ALL" for all positions. Use PENDING, ACTIVE, PARTIAL, CLOSED, or STOPPED_OUT for specific statuses. Defaults to ACTIVE only.',
              enum: ['PENDING', 'ACTIVE', 'PARTIAL', 'CLOSED', 'STOPPED_OUT', 'ALL', ''],
            },
          },
        },
      },
      {
        name: 'get_managed_position',
        description: 'Get details of a specific managed position by ID',
        inputSchema: {
          type: 'object',
          properties: {
            position_id: {
              type: 'string',
              description: 'Position ID',
            },
          },
          required: ['position_id'],
        },
      },
      {
        name: 'close_managed_position',
        description: 'Manually close a managed position (cancels all orders and exits at market)',
        inputSchema: {
          type: 'object',
          properties: {
            position_id: {
              type: 'string',
              description: 'Position ID to close',
            },
          },
          required: ['position_id'],
        },
      },
      {
        name: 'cancel_order',
        description: 'Cancel an open order by ID',
        inputSchema: {
          type: 'object',
          properties: {
            order_id: {
              type: 'string',
              description: 'Order ID to cancel',
            },
          },
          required: ['order_id'],
        },
      },
      {
        name: 'get_quote',
        description: 'Get real-time quote data (bid/ask prices) for a stock symbol',
        inputSchema: {
          type: 'object',
          properties: {
            symbol: {
              type: 'string',
              description: 'Stock symbol (e.g., AAPL, GOOGL, TSLA)',
            },
          },
          required: ['symbol'],
        },
      },
      {
        name: 'get_latest_bar',
        description: 'Get the latest price bar (OHLCV data) for a stock symbol',
        inputSchema: {
          type: 'object',
          properties: {
            symbol: {
              type: 'string',
              description: 'Stock symbol (e.g., AAPL, GOOGL, TSLA)',
            },
          },
          required: ['symbol'],
        },
      },
      {
        name: 'get_historical_bars',
        description: 'Get historical price bars for technical analysis. Returns OHLCV data for the specified date range and timeframe.',
        inputSchema: {
          type: 'object',
          properties: {
            symbol: {
              type: 'string',
              description: 'Stock symbol (e.g., AAPL, GOOGL, TSLA)',
            },
            start_date: {
              type: 'string',
              description: 'Start date in YYYY-MM-DD format (default: 30 days ago)',
            },
            end_date: {
              type: 'string',
              description: 'End date in YYYY-MM-DD format (default: today)',
            },
            timeframe: {
              type: 'string',
              description: 'Bar timeframe: 1Min, 5Min, 15Min, 1Hour, 1Day (default: 1Day)',
              enum: ['1Min', '5Min', '15Min', '1Hour', '1Day'],
            },
          },
          required: ['symbol'],
        },
      },
      {
        name: 'get_news',
        description: 'Get latest news from Google News RSS feed',
        inputSchema: {
          type: 'object',
          properties: {
            limit: {
              type: 'number',
              description: 'Number of news items to return (default: 20)',
            },
          },
        },
      },
      {
        name: 'get_news_by_topic',
        description: 'Get news for a specific topic (WORLD, NATION, BUSINESS, TECHNOLOGY, ENTERTAINMENT, SPORTS, SCIENCE, HEALTH)',
        inputSchema: {
          type: 'object',
          properties: {
            topic: {
              type: 'string',
              description: 'News topic',
              enum: ['WORLD', 'NATION', 'BUSINESS', 'TECHNOLOGY', 'ENTERTAINMENT', 'SPORTS', 'SCIENCE', 'HEALTH'],
            },
          },
          required: ['topic'],
        },
      },
      {
        name: 'search_news',
        description: 'Search for news by keyword or stock symbol',
        inputSchema: {
          type: 'object',
          properties: {
            query: {
              type: 'string',
              description: 'Search query (e.g., Tesla, NVDA, Federal Reserve)',
            },
            limit: {
              type: 'number',
              description: 'Number of results (default: 20)',
            },
          },
          required: ['query'],
        },
      },
      {
        name: 'get_market_news',
        description: 'Get market news, optionally filtered by stock symbols',
        inputSchema: {
          type: 'object',
          properties: {
            symbols: {
              type: 'string',
              description: 'Comma-separated stock symbols (e.g., TSLA,NVDA,AAPL)',
            },
          },
        },
      },
      {
        name: 'aggregate_and_summarize_news',
        description: 'Aggregate news from multiple sources and create an AI-powered summary using Gemini. Saves summary to a file.',
        inputSchema: {
          type: 'object',
          properties: {
            topics: {
              type: 'array',
              items: { type: 'string' },
              description: 'News topics to aggregate (BUSINESS, TECHNOLOGY, etc.)',
            },
            symbols: {
              type: 'array',
              items: { type: 'string' },
              description: 'Stock symbols to search for (e.g., ["TSLA", "NVDA"])',
            },
            max_articles: {
              type: 'number',
              description: 'Maximum articles per source (default: 10)',
            },
          },
        },
      },
      {
        name: 'list_news_summaries',
        description: 'List all saved news summaries',
        inputSchema: {
          type: 'object',
          properties: {},
        },
      },
      {
        name: 'get_news_summary',
        description: 'Get a specific news summary by filename',
        inputSchema: {
          type: 'object',
          properties: {
            filename: {
              type: 'string',
              description: 'Summary filename',
            },
          },
          required: ['filename'],
        },
      },
      {
        name: 'get_marketwatch_topstories',
        description: 'Get MarketWatch top stories',
        inputSchema: {
          type: 'object',
          properties: {},
        },
      },
      {
        name: 'get_marketwatch_realtime',
        description: 'Get MarketWatch real-time headlines',
        inputSchema: {
          type: 'object',
          properties: {},
        },
      },
      {
        name: 'get_marketwatch_bulletins',
        description: 'Get MarketWatch breaking news bulletins',
        inputSchema: {
          type: 'object',
          properties: {},
        },
      },
      {
        name: 'get_marketwatch_marketpulse',
        description: 'Get MarketWatch market pulse (brief up-to-the-minute market updates)',
        inputSchema: {
          type: 'object',
          properties: {},
        },
      },
      {
        name: 'get_marketwatch_all',
        description: 'Get all MarketWatch news feeds aggregated',
        inputSchema: {
          type: 'object',
          properties: {},
        },
      },
      // ── Economic Intelligence Feeds (free, no API key) ──────────────────
      {
        name: 'get_treasury_data',
        description: 'Get US Treasury data: national debt levels and average interest rates on government securities. No API key required. Updated daily.',
        inputSchema: { type: 'object', properties: {} },
      },
      {
        name: 'get_global_events',
        description: 'Get global news events from GDELT (Global Database of Events, Language, and Tone). Searches 100+ languages, updates every 15 minutes. No API key required.',
        inputSchema: {
          type: 'object',
          properties: {
            query: { type: 'string', description: 'Search query (e.g., "tariff china", "federal reserve"). Leave empty for broad market coverage.' },
          },
        },
      },
      {
        name: 'get_economic_indicators',
        description: 'Get key economic indicators from BLS: CPI, Core CPI, Unemployment Rate, Nonfarm Payrolls, PPI. No API key required.',
        inputSchema: { type: 'object', properties: {} },
      },
      {
        name: 'get_market_snapshot',
        description: 'Get broad market snapshot from Yahoo Finance: indexes (SPY, QQQ, DIA, IWM), bonds, commodities (Gold, Oil), crypto (BTC, ETH), VIX. Includes 5-day history. No API key required.',
        inputSchema: { type: 'object', properties: {} },
      },
      {
        name: 'get_defense_contracts',
        description: 'Get recent US defense/military contracts from USAspending.gov. Useful for defense sector signals. No API key required.',
        inputSchema: { type: 'object', properties: {} },
      },
      {
        name: 'get_global_trade_flows',
        description: 'Get global trade flow data from UN Comtrade for strategic commodities: crude, gas, gold, semiconductors. No API key required. Data lags 1-2 months.',
        inputSchema: { type: 'object', properties: {} },
      },
      {
        name: 'get_quick_market_intelligence',
        description: 'Get AI-powered quick market intelligence (Gemini-cleaned news from MarketWatch - 15 articles max, very fast)',
        inputSchema: {
          type: 'object',
          properties: {},
        },
      },
      {
        name: 'analyze_stocks',
        description: 'Analyze multiple stocks with comprehensive technical indicators, news, and AI-powered recommendations. Returns RSI, trend, volatility, support/resistance, catalysts, and trade recommendations for each stock.',
        inputSchema: {
          type: 'object',
          properties: {
            symbols: {
              type: 'array',
              items: { type: 'string' },
              description: 'Array of stock symbols to analyze (e.g., ["CLRB", "PLUG", "BE", "NVDA"])',
            },
          },
          required: ['symbols'],
        },
      },
      {
        name: 'get_cleaned_news',
        description: 'Get AI-powered cleaned and aggregated news from multiple sources (Google News + MarketWatch)',
        inputSchema: {
          type: 'object',
          properties: {
            include_google: {
              type: 'boolean',
              description: 'Include Google News feeds',
            },
            include_marketwatch: {
              type: 'boolean',
              description: 'Include MarketWatch feeds',
            },
            google_topics: {
              type: 'array',
              items: { type: 'string' },
              description: 'Google News topics to include (BUSINESS, TECHNOLOGY, etc.)',
            },
            symbols: {
              type: 'array',
              items: { type: 'string' },
              description: 'Stock symbols to search for',
            },
            max_articles_per_source: {
              type: 'number',
              description: 'Maximum articles per source (default 10)',
            },
          },
        },
      },
      {
        name: 'log_decision',
        description: 'Log a trading decision with reasoning to decisive_actions/ folder',
        inputSchema: {
          type: 'object',
          properties: {
            action: {
              type: 'string',
              description: 'The action taken (BUY, SELL, HOLD, PASS)',
            },
            symbol: {
              type: 'string',
              description: 'Stock symbol (optional)',
            },
            reasoning: {
              type: 'string',
              description: 'Detailed reasoning for the decision',
            },
            market_data: {
              type: 'object',
              description: 'Relevant market data that influenced the decision',
            },
          },
          required: ['action', 'reasoning'],
        },
      },
      {
        name: 'log_activity',
        description: 'Log AI trading activity to the daily activity log (positions, intelligence, decisions)',
        inputSchema: {
          type: 'object',
          properties: {
            type: {
              type: 'string',
              description: 'Activity type: ANALYSIS, INTELLIGENCE, DECISION, POSITION_CHECK',
            },
            action: {
              type: 'string',
              description: 'Action description (e.g., "Analyzed 10 stocks", "Gathered market intelligence")',
            },
            symbol: {
              type: 'string',
              description: 'Stock symbol if applicable',
            },
            reasoning: {
              type: 'string',
              description: 'Reasoning or notes for this activity',
            },
            details: {
              type: 'object',
              description: 'Additional details as key-value pairs',
            },
          },
          required: ['type', 'action'],
        },
      },
      {
        name: 'get_activity_log',
        description: 'Get the current day\'s activity log showing all AI trading activities',
        inputSchema: {
          type: 'object',
          properties: {},
        },
      },
      {
        name: 'place_options_order',
        description: 'Place an options order (calls or puts)',
        inputSchema: {
          type: 'object',
          properties: {
            symbol: {
              type: 'string',
              description: 'Options symbol in OCC format (e.g., TSLA251219C00400000 for TSLA Dec 19 2025 $400 Call)',
            },
            underlying: {
              type: 'string',
              description: 'Underlying stock symbol (e.g., TSLA)',
            },
            quantity: {
              type: 'number',
              description: 'Number of contracts to trade',
            },
            side: {
              type: 'string',
              description: 'Order side',
              enum: ['buy', 'sell'],
            },
            position_intent: {
              type: 'string',
              description: 'Position intent (optional, defaults based on side)',
              enum: ['buy_to_open', 'buy_to_close', 'sell_to_open', 'sell_to_close'],
            },
            order_type: {
              type: 'string',
              description: 'Order type',
              enum: ['market', 'limit'],
            },
            limit_price: {
              type: 'number',
              description: 'Limit price per contract (required for limit orders)',
            },
          },
          required: ['symbol', 'quantity', 'side', 'order_type'],
        },
      },
      {
        name: 'get_options_positions',
        description: 'Get all open options positions',
        inputSchema: {
          type: 'object',
          properties: {},
        },
      },
      {
        name: 'get_options_position',
        description: 'Get a specific options position by symbol',
        inputSchema: {
          type: 'object',
          properties: {
            symbol: {
              type: 'string',
              description: 'Options symbol in OCC format',
            },
          },
          required: ['symbol'],
        },
      },
      {
        name: 'get_options_chain',
        description: 'Get available options contracts for an underlying symbol with optional filtering. Use filters to reduce token usage. Use this to find valid option symbols before placing orders.',
        inputSchema: {
          type: 'object',
          properties: {
            symbol: {
              type: 'string',
              description: 'Underlying stock symbol (e.g., SPY, TSLA, AAPL)',
            },
            expiration: {
              type: 'string',
              description: 'Expiration date in YYYY-MM-DD format (optional, defaults to next Friday)',
            },
            delta_min: {
              type: 'number',
              description: 'Minimum delta (absolute value, e.g., 0.4 for ATM options)',
            },
            delta_max: {
              type: 'number',
              description: 'Maximum delta (absolute value, e.g., 0.6 for ATM options)',
            },
            min_bid: {
              type: 'number',
              description: 'Minimum bid price to filter out illiquid options (e.g., 0.1)',
            },
            type: {
              type: 'string',
              description: 'Filter by option type: "call" or "put"',
              enum: ['call', 'put'],
            },
          },
          required: ['symbol'],
        },
      },
      {
        name: 'wait',
        description: 'Wait for a specified duration in seconds. Useful for AI to pause between trading actions without blocking the user. Maximum 300 seconds (5 minutes).',
        inputSchema: {
          type: 'object',
          properties: {
            seconds: {
              type: 'number',
              description: 'Number of seconds to wait (1-300)',
            },
            reason: {
              type: 'string',
              description: 'Optional reason for waiting (e.g., "Monitoring position momentum")',
            },
          },
          required: ['seconds'],
        },
      },
      {
        name: 'get_datetime',
        description: 'Get the current date and time in a specified timezone. Defaults to America/New_York (US Eastern). Returns time, date, day of week, market status, and whether markets are likely open.',
        inputSchema: {
          type: 'object',
          properties: {
            timezone: {
              type: 'string',
              description: 'IANA timezone (e.g., "America/New_York", "America/Los_Angeles", "UTC"). Defaults to America/New_York.',
            },
          },
        },
      },
      {
        name: 'find_similar_setups',
        description: 'Find historically similar trading setups using AI vector similarity search. Query with natural language (e.g., "SPY gap up scalp") to find past trades with similar setups, reasoning, and market context. Returns similar trades with results, reasoning, and similarity scores.',
        inputSchema: {
          type: 'object',
          properties: {
            query: {
              type: 'string',
              description: 'Natural language query describing the setup (e.g., "SPY gap up momentum scalp", "NVDA earnings breakout swing")',
            },
            limit: {
              type: 'number',
              description: 'Number of similar trades to return (default: 5)',
            },
            symbol: {
              type: 'string',
              description: 'Optional: Filter by symbol (e.g., "SPY", "NVDA")',
            },
            strategy: {
              type: 'string',
              description: 'Optional: Filter by strategy ("SCALP", "SWING", "HOLD")',
            },
            action: {
              type: 'string',
              description: 'Optional: Filter by action (e.g., "BUY", "SELL")',
            },
          },
          required: ['query'],
        },
      },
      {
        name: 'store_trade_setup',
        description: 'Store a completed trade with AI embeddings for future similarity search. Use this after closing a trade to add it to the historical database with reasoning and market context.',
        inputSchema: {
          type: 'object',
          properties: {
            symbol: {
              type: 'string',
              description: 'Stock symbol (e.g., "SPY", "NVDA")',
            },
            action: {
              type: 'string',
              description: 'Trade action (e.g., "BUY", "SELL", "HOLD")',
            },
            strategy: {
              type: 'string',
              description: 'Strategy type ("SCALP", "SWING", "HOLD")',
            },
            result_pct: {
              type: 'number',
              description: 'Result percentage (e.g., 26.5 for +26.5%, -15.6 for -15.6%)',
            },
            result_dollars: {
              type: 'number',
              description: 'Result in dollars (e.g., 1920 for +$1920, -960 for -$960)',
            },
            reasoning: {
              type: 'string',
              description: 'Detailed trade reasoning and thesis',
            },
            market_context: {
              type: 'string',
              description: 'Market conditions, catalysts, and context',
            },
          },
          required: ['symbol', 'action', 'strategy', 'reasoning', 'market_context'],
        },
      },
      {
        name: 'get_trade_stats',
        description: 'Get statistics for trades matching filters (win rate, profit factor, avg result, best/worst). Useful for analyzing performance by symbol, strategy, or action.',
        inputSchema: {
          type: 'object',
          properties: {
            symbol: {
              type: 'string',
              description: 'Optional: Filter by symbol (e.g., "SPY")',
            },
            strategy: {
              type: 'string',
              description: 'Optional: Filter by strategy ("SCALP", "SWING")',
            },
            action: {
              type: 'string',
              description: 'Optional: Filter by action (e.g., "BUY")',
            },
          },
        },
      },
      // ── Agent Self-Modification Tools ──────────────────────────
      {
        name: 'update_agent_prompt',
        description: 'Update the active agent\'s custom system prompt. Use this when the user asks you to change your trading behavior, persona, or rules. The new prompt replaces the existing custom prompt.',
        inputSchema: {
          type: 'object',
          properties: {
            prompt: {
              type: 'string',
              description: 'The new system prompt text',
            },
          },
          required: ['prompt'],
        },
      },
      {
        name: 'update_strategy_rules',
        description: 'Create a new trading strategy with the given rules and assign it to the current agent. Existing strategies are NEVER modified — a new one is always created so the operator can review it on the Agents page. ONLY use this when the user EXPLICITLY asks you to change trading rules.',
        inputSchema: {
          type: 'object',
          properties: {
            name: { type: 'string', description: 'Name for the new strategy (e.g., "Conservative Options v2")' },
            rules: { type: 'string', description: 'The trading rules in markdown format' },
          },
          required: ['name', 'rules'],
        },
      },
      {
        name: 'get_agent_config',
        description: 'Get the current agent configuration including active agent, strategy, model, heartbeat settings, and permissions. Useful for understanding your current setup before making changes.',
        inputSchema: {
          type: 'object',
          properties: {},
        },
      },
      {
        name: 'set_heartbeat',
        description: 'Override the agent heartbeat interval. Use this to speed up or slow down your own heartbeat cycle based on market conditions or workload. For example, set to 60s during volatile markets or 600s when nothing is happening.',
        inputSchema: {
          type: 'object',
          properties: {
            seconds: { type: 'number', description: 'New heartbeat interval in seconds (30-3600)' },
            reason: { type: 'string', description: 'Reason for the override (logged to terminal)' },
          },
          required: ['seconds'],
        },
      },
      {
        name: 'update_permissions',
        description: 'Update agent trading permissions/guardrails. Use this when the user asks to change risk limits, enable/disable trading types, etc.',
        inputSchema: {
          type: 'object',
          properties: {
            allowLiveTrading: { type: 'boolean', description: 'Allow placing live orders' },
            allowOptions: { type: 'boolean', description: 'Allow options trading' },
            allowStocks: { type: 'boolean', description: 'Allow stock trading' },
            allow0DTE: { type: 'boolean', description: 'Allow 0DTE options' },
            maxPositionPct: { type: 'number', description: 'Max position size as % of portfolio' },
            maxDeployedPct: { type: 'number', description: 'Max total deployed capital %' },
            maxDailyLoss: { type: 'number', description: 'Max daily loss % before auto-pause' },
            maxOpenPositions: { type: 'number', description: 'Max simultaneous positions' },
          },
        },
      },
      {
        name: 'set_session_mode',
        description: 'Set session mode: "continuous" keeps conversation context across heartbeats (default), "fresh" starts a new session each heartbeat (better for long_horizon mode). Use fresh for long horizon strategies where each beat should be independent.',
        inputSchema: {
          type: 'object',
          properties: {
            mode: { type: 'string', description: 'Session mode: "continuous" or "fresh"' },
          },
          required: ['mode'],
        },
      },
      {
        name: 'get_heartbeat_profiles',
        description: 'List available heartbeat profiles/skills. These are predefined heartbeat configurations for different trading styles: active (high-frequency), passive (low-frequency), long_horizon (weekly/monthly check-ins), earnings_season (heightened vigilance), overnight (minimal overnight checks), scalp (rapid-fire).',
        inputSchema: {
          type: 'object',
          properties: {},
        },
      },
      {
        name: 'apply_heartbeat_profile',
        description: 'Apply a heartbeat profile to change your heartbeat intervals based on trading style. Use get_heartbeat_profiles to see available options.',
        inputSchema: {
          type: 'object',
          properties: {
            profile: { 
              type: 'string', 
              description: 'Profile key: active, passive, long_horizon, earnings_season, overnight, scalp',
            },
          },
          required: ['profile'],
        },
      },
      {
        name: 'get_heartbeat_phases',
        description: 'Get the current heartbeat phase time ranges (in minutes from midnight ET). This shows when each phase (pre_market, market_open, midday, market_close, after_hours, closed) is active.',
        inputSchema: {
          type: 'object',
          properties: {},
        },
      },
      {
        name: 'update_heartbeat_phase',
        description: 'Update the time range for a heartbeat phase. Use get_heartbeat_phases to see current ranges.',
        inputSchema: {
          type: 'object',
          properties: {
            phase: { 
              type: 'string', 
              description: 'Phase name: pre_market, market_open, midday, market_close, after_hours',
            },
            start: { 
              type: 'number', 
              description: 'Start minute from midnight ET (e.g., 240 = 4:00 AM ET)',
            },
            end: { 
              type: 'number', 
              description: 'End minute from midnight ET (e.g., 570 = 9:30 AM ET)',
            },
          },
          required: ['phase'],
        },
      },
      {
        name: 'create_agent',
        description: 'Create a new agent persona. The agent will appear in the UI and can be assigned to any sandbox/account. Returns the new agent ID.',
        inputSchema: {
          type: 'object',
          properties: {
            name: { type: 'string', description: 'Agent name (e.g., "BluechipTrader")' },
            description: { type: 'string', description: 'Short description of the agent personality' },
            model: { type: 'string', description: 'Model ID (e.g., "anthropic/claude-sonnet-4-6")' },
            strategyId: { type: 'string', description: 'Strategy ID to use (optional, can assign later)' },
            customSystemPrompt: { type: 'string', description: 'Custom system prompt for this agent' },
          },
          required: ['name'],
        },
      },
      {
        name: 'create_strategy',
        description: 'Create a new trading strategy with rules. The strategy will appear in the UI and can be assigned to any agent. Returns the new strategy ID.',
        inputSchema: {
          type: 'object',
          properties: {
            name: { type: 'string', description: 'Strategy name (e.g., "BluechipSteady")' },
            description: { type: 'string', description: 'Short description of the strategy' },
            customRules: { type: 'string', description: 'The trading rules in markdown format' },
          },
          required: ['name', 'customRules'],
        },
      },
      {
        name: 'assign_agent_to_sandbox',
        description: 'Assign an agent to a specific sandbox/account. Use after creating an agent to activate it on an account.',
        inputSchema: {
          type: 'object',
          properties: {
            agentId: { type: 'string', description: 'Agent ID to assign' },
            sandboxId: { type: 'string', description: 'Sandbox ID (e.g., "sbx_6edbf348"). If not provided, uses current sandbox.' },
          },
          required: ['agentId'],
        },
      },

      // ── Analysis Tools ─────────────────────────────────────────────
      {
        name: 'run_market_briefing',
        description: 'Fetch market breadth and uptrend ratio data from TraderMonty CSV sources (no API key needed). Returns composite uptrend score 0-100, breadth index, and sector data. Takes ~20 seconds.',
        inputSchema: { type: 'object', properties: {} },
      },
      {
        name: 'run_vcp_screener',
        description: 'Screen S&P 500 for Minervini VCP breakout candidates using FMP API. Launches a background job (~2-3 min). Returns immediately with job status. Call wait(180) then read_latest_report("vcp") to retrieve results. Requires FMP_API_KEY env var.',
        inputSchema: {
          type: 'object',
          properties: {
            strict: { type: 'boolean', description: 'Strict mode: only Pre-breakout/Breakout execution states (default: false)' },
          },
        },
      },
      {
        name: 'run_pead_screener',
        description: 'Screen for Post-Earnings Announcement Drift candidates using FMP API. Launches a background job (~2 min). Returns immediately. Call wait(120) then read_latest_report("pead") to retrieve results. Requires FMP_API_KEY env var.',
        inputSchema: { type: 'object', properties: {} },
      },
      {
        name: 'run_market_top_check',
        description: "Run O'Neil distribution day count + Minervini leading stock deterioration + Monty defensive rotation. Returns market top probability 0-100. Synchronous, ~90 seconds. Requires FMP_API_KEY env var.",
        inputSchema: { type: 'object', properties: {} },
      },
      {
        name: 'run_ftd_check',
        description: "Detect Follow-Through Day signals on S&P 500 and Nasdaq using O'Neil methodology. Synchronous, ~60 seconds. Requires FMP_API_KEY env var.",
        inputSchema: { type: 'object', properties: {} },
      },
      {
        name: 'run_economic_calendar',
        description: 'Fetch upcoming economic events (FOMC, CPI, PPI, NFP, GDP) for the next 14 days via FMP API. Synchronous, ~15 seconds. Requires FMP_API_KEY env var.',
        inputSchema: { type: 'object', properties: {} },
      },
      {
        name: 'run_earnings_calendar',
        description: 'Fetch mid-cap+ earnings announcements for the current week via FMP API. Synchronous, ~15 seconds. Requires FMP_API_KEY env var.',
        inputSchema: { type: 'object', properties: {} },
      },
      {
        name: 'read_latest_report',
        description: "Read the most recently generated analysis report from data/reports/. Use after background screeners finish, or to load the daily briefing and weekly regime report at the start of each pre-market beat. Use type='market_alert' to read the latest intra-day breaking news alert written by the mid-session scanner.",
        inputSchema: {
          type: 'object',
          properties: {
            type: {
              type: 'string',
              enum: ['vcp', 'pead', 'market_top', 'ftd', 'daily_brief', 'weekly_regime', 'uptrend', 'scenario', 'review', 'market_alert'],
              description: 'Report type to read',
            },
          },
          required: ['type'],
        },
      },
      {
        name: 'get_penny_candidates',
        description: 'Get penny stock candidates scored above a threshold by the real-time signal pipeline. Returns ticker, scores (composite, technical, regulatory, social), and dominant_signal type (technical/regulatory/social). Per-candidate context strings are omitted by default to keep the list compact — call get_penny_signal_detail for full context on the specific tickers you intend to trade. Use min_score=60 for tradeable signals.',
        inputSchema: {
          type: 'object',
          properties: {
            min_score: {
              type: 'number',
              description: 'Minimum composite score (0–100). Default: 60. Scores 60–79 → 2–3% position size; 80–100 → 5–7% position size.',
            },
            detail: {
              type: 'boolean',
              description: 'When true, includes full context strings (technical_context, regulatory_event, social_context) inline for every candidate. Default false — prefer fetching context per-ticker via get_penny_signal_detail to save tokens.',
            },
          },
        },
      },
      {
        name: 'get_penny_signal_detail',
        description: 'Get the full signal breakdown for a specific ticker: technical score, regulatory score, social score, dominant signal type, event descriptions, and last update time.',
        inputSchema: {
          type: 'object',
          properties: {
            ticker: {
              type: 'string',
              description: 'Stock ticker symbol (e.g. ACMR, MFIN)',
            },
          },
          required: ['ticker'],
        },
      },
      {
        name: 'get_penny_universe',
        description: 'Get the current monitored penny stock universe: all symbols passing the $2–$10 price, $50M–$500M market cap, $300K+ ADV, exchange-listed filter.',
        inputSchema: {
          type: 'object',
          properties: {},
        },
      },
      {
        name: 'scan_penny_universe_now',
        description: 'Trigger an out-of-cycle universe refresh. Use after market open to ensure the latest symbols are loaded. Returns {status: "refreshing"}.',
        inputSchema: {
          type: 'object',
          properties: {},
        },
      },
    ];

  const tools = TOOL_ALLOWLIST.size > 0
    ? allTools.filter(t => TOOL_ALLOWLIST.has(t.name))
    : allTools;
  return { tools };
});

// ── Permission Enforcement ──────────────────────────────────────────
const AGENT_URL = process.env.AGENT_URL || 'http://localhost:3737';
const AGENT_AUTH_TOKEN = process.env.AGENT_AUTH_TOKEN || '';
const AGENT_QUERY = { sandboxId: OPENPROPHET_SANDBOX_ID };
const agentAxios = axios.create({
  headers: AGENT_AUTH_TOKEN ? { Authorization: `Bearer ${AGENT_AUTH_TOKEN}` } : {},
});
const ORDER_TOOLS = ['place_buy_order', 'place_sell_order', 'place_options_order', 'place_managed_position', 'close_managed_position'];

async function enforcePermissions(toolName, args) {
  let perms;
  try {
    const resp = await agentAxios.get(`${AGENT_URL}/api/permissions`, { timeout: 3000, params: AGENT_QUERY });
    perms = resp.data;
  } catch {
    // If agent server unreachable, allow (fail open for non-order tools, fail closed for orders)
    if (ORDER_TOOLS.includes(toolName)) throw new Error('Cannot verify permissions — agent server unreachable. Order blocked for safety.');
    return;
  }

  // Blocked tools
  if (perms.blockedTools?.length && perms.blockedTools.includes(toolName)) {
    throw new Error(`Tool "${toolName}" is blocked by permissions. Blocked tools: ${perms.blockedTools.join(', ')}`);
  }

  // Order-specific enforcement
  if (ORDER_TOOLS.includes(toolName)) {
    // Live trading disabled
    if (!perms.allowLiveTrading) {
      throw new Error('Live trading is DISABLED (read-only mode). Cannot place orders. Change permissions to enable.');
    }
    // Options check
    if (!perms.allowOptions && (toolName === 'place_options_order' || (args.symbol && args.symbol.length > 10))) {
      throw new Error('Options trading is DISABLED by permissions.');
    }
    // Stock check
    if (!perms.allowStocks && (toolName === 'place_buy_order' || toolName === 'place_sell_order')) {
      throw new Error('Stock trading is DISABLED by permissions.');
    }
    // 0DTE check for options
    if (!perms.allow0DTE && toolName === 'place_options_order' && args.symbol) {
      // OCC format: SYMBOL + YYMMDD + C/P + price — extract expiration
      const match = args.symbol.match(/(\d{6})[CP]/);
      if (match) {
        const expStr = match[1]; // YYMMDD
        const expDate = new Date(`20${expStr.slice(0,2)}-${expStr.slice(2,4)}-${expStr.slice(4,6)}`);
        const today = new Date();
        today.setHours(0,0,0,0);
        expDate.setHours(0,0,0,0);
        if (expDate.getTime() === today.getTime()) {
          throw new Error('0DTE options are NOT allowed by permissions.');
        }
      }
    }
    // Require confirmation
    if (perms.requireConfirmation) {
      throw new Error(`Order requires operator confirmation (requireConfirmation is enabled). Tell the operator what you want to do and wait for them to disable this setting or approve via the dashboard.`);
    }
    // Max order value
    if (perms.maxOrderValue > 0) {
      const orderValue = (args.limit_price || args.entry_price || 0) * (args.quantity || args.qty || 0);
      const allocValue = args.allocation_dollars || 0;
      const checkValue = allocValue || orderValue;
      if (checkValue > perms.maxOrderValue) {
        throw new Error(`Order value $${checkValue.toFixed(2)} exceeds max allowed $${perms.maxOrderValue}. Reduce size or change permissions.`);
      }
    }
  }
}

// Handle tool calls
server.setRequestHandler(CallToolRequestSchema, async (request) => {
  const { name, arguments: args } = request.params;

  try {
    // Enforce permissions before executing any tool
    await enforcePermissions(name, args);

    switch (name) {
      case 'get_account': {
        const data = await callTradingBot('/account');
        return {
          content: [
            {
              type: 'text',
              text: JSON.stringify(data, null, 2),
            },
          ],
        };
      }

      case 'get_positions': {
        const data = await callTradingBot('/positions');
        return {
          content: [
            {
              type: 'text',
              text: JSON.stringify(data, null, 2),
            },
          ],
        };
      }

      case 'get_orders': {
        const data = await callTradingBot('/orders');
        return {
          content: [
            {
              type: 'text',
              text: JSON.stringify(data, null, 2),
            },
          ],
        };
      }

      case 'place_buy_order': {
        // Transform quantity to qty for API compatibility
        const requestData = {
          symbol: args.symbol,
          qty: args.quantity,
          order_type: args.order_type,
          ...(args.limit_price && { limit_price: args.limit_price })
        };
        const data = await callTradingBot('/orders/buy', 'POST', requestData);
        return {
          content: [
            {
              type: 'text',
              text: JSON.stringify(data, null, 2),
            },
          ],
        };
      }

      case 'place_sell_order': {
        // Transform quantity to qty for API compatibility
        const requestData = {
          symbol: args.symbol,
          qty: args.quantity,
          order_type: args.order_type,
          ...(args.limit_price && { limit_price: args.limit_price })
        };
        const data = await callTradingBot('/orders/sell', 'POST', requestData);
        return {
          content: [
            {
              type: 'text',
              text: JSON.stringify(data, null, 2),
            },
          ],
        };
      }

      case 'place_managed_position': {
        const data = await callTradingBot('/positions/managed', 'POST', args);
        return {
          content: [
            {
              type: 'text',
              text: JSON.stringify(data, null, 2),
            },
          ],
        };
      }

      case 'get_managed_positions': {
        // Default to ACTIVE positions only for token efficiency
        // Use status="ALL" or status="" to get all positions
        let endpoint;
        if (args.status === 'ALL' || args.status === '') {
          endpoint = '/positions/managed';
        } else if (args.status) {
          endpoint = `/positions/managed?status=${encodeURIComponent(args.status)}`;
        } else {
          // Default: only ACTIVE positions
          endpoint = '/positions/managed?status=ACTIVE';
        }

        const data = await callTradingBot(endpoint);

        // Token-efficient summary format
        if (data.count === 0) {
          return {
            content: [{type: 'text', text: JSON.stringify({count: 0, positions: []})}],
          };
        }

        // For more than 10 positions, return compact summary
        if (data.count > 10) {
          const summary = {
            count: data.count,
            summary: `${data.count} positions found. Status breakdown: ` +
              `ACTIVE: ${data.positions.filter(p => p.status === 'ACTIVE').length}, ` +
              `PENDING: ${data.positions.filter(p => p.status === 'PENDING').length}, ` +
              `PARTIAL: ${data.positions.filter(p => p.status === 'PARTIAL').length}, ` +
              `CLOSED: ${data.positions.filter(p => p.status === 'CLOSED').length}`,
            note: 'Full position data available, use get_managed_position(id) for details'
          };
          return {
            content: [{type: 'text', text: JSON.stringify(summary, null, 2)}],
          };
        }

        // For <=10 positions, return full data
        return {
          content: [{type: 'text', text: JSON.stringify(data, null, 2)}],
        };
      }

      case 'get_managed_position': {
        const data = await callTradingBot(`/positions/managed/${args.position_id}`);
        return {
          content: [
            {
              type: 'text',
              text: JSON.stringify(data, null, 2),
            },
          ],
        };
      }

      case 'close_managed_position': {
        const data = await callTradingBot(`/positions/managed/${args.position_id}`, 'DELETE');
        return {
          content: [
            {
              type: 'text',
              text: JSON.stringify(data, null, 2),
            },
          ],
        };
      }

      case 'cancel_order': {
        const data = await callTradingBot(`/orders/${args.order_id}`, 'DELETE');
        return {
          content: [
            {
              type: 'text',
              text: JSON.stringify(data, null, 2),
            },
          ],
        };
      }

      case 'get_quote': {
        const data = await callTradingBot(`/market/quote/${args.symbol}`);
        return {
          content: [
            {
              type: 'text',
              text: JSON.stringify(data, null, 2),
            },
          ],
        };
      }

      case 'get_latest_bar': {
        const data = await callTradingBot(`/market/bar/${args.symbol}`);
        return {
          content: [
            {
              type: 'text',
              text: JSON.stringify(data, null, 2),
            },
          ],
        };
      }

      case 'get_historical_bars': {
        let endpoint = `/market/bars/${args.symbol}`;
        const params = new URLSearchParams();
        if (args.start_date) params.append('start', args.start_date);
        if (args.end_date) params.append('end', args.end_date);
        if (args.timeframe) params.append('timeframe', args.timeframe);
        if (params.toString()) endpoint += `?${params.toString()}`;

        const data = await callTradingBot(endpoint);
        return {
          content: [
            {
              type: 'text',
              text: JSON.stringify(data, null, 2),
            },
          ],
        };
      }

      case 'get_news': {
        const limit = args.limit || 20;
        const data = applyNewsDedup(await callTradingBot(`/news?limit=${limit}`));
        return {
          content: [
            {
              type: 'text',
              text: JSON.stringify(data, null, 2),
            },
          ],
        };
      }

      case 'get_news_by_topic': {
        // Use compact mode to reduce token usage
        const data = applyNewsDedup(await callTradingBot(`/news/topic/${args.topic}?compact=true`));
        return {
          content: [
            {
              type: 'text',
              text: JSON.stringify(data, null, 2),
            },
          ],
        };
      }

      case 'search_news': {
        const limit = args.limit || 20;
        // No dedup applied — this is a targeted query; the agent expects specific results.
        const data = await callTradingBot(`/news/search?q=${encodeURIComponent(args.query)}&limit=${limit}`);
        return {
          content: [
            {
              type: 'text',
              text: JSON.stringify(data, null, 2),
            },
          ],
        };
      }

      case 'get_market_news': {
        const endpoint = args.symbols
          ? `/news/market?symbols=${encodeURIComponent(args.symbols)}`
          : '/news/market';
        const data = applyNewsDedup(await callTradingBot(endpoint));
        return {
          content: [
            {
              type: 'text',
              text: JSON.stringify(data, null, 2),
            },
          ],
        };
      }

      case 'aggregate_and_summarize_news': {
        const { topics = [], symbols = [], max_articles = 10 } = args;
        const allNews = [];

        // Fetch news from topics
        for (const topic of topics) {
          try {
            const data = await callTradingBot(`/news/topic/${topic}`);
            const articles = data.news.slice(0, max_articles);
            allNews.push(...articles.map(a => ({ ...a, source_type: `topic:${topic}` })));
          } catch (error) {
            console.error(`Error fetching topic ${topic}:`, error.message);
          }
        }

        // Fetch news for symbols
        for (const symbol of symbols) {
          try {
            const data = await callTradingBot(`/news/search?q=${encodeURIComponent(symbol)}&limit=${max_articles}`);
            const articles = data.news.slice(0, max_articles);
            allNews.push(...articles.map(a => ({ ...a, source_type: `symbol:${symbol}` })));
          } catch (error) {
            console.error(`Error fetching symbol ${symbol}:`, error.message);
          }
        }

        if (allNews.length === 0) {
          return {
            content: [
              {
                type: 'text',
                text: 'No news articles found to summarize.',
              },
            ],
          };
        }

        // Prepare news for Gemini
        const newsText = allNews.map((article, i) =>
          `[${i + 1}] ${article.title}\nSource: ${article.source || 'Unknown'} (${article.source_type})\nPublished: ${article.pub_date}\nDescription: ${article.description?.replace(/<[^>]*>/g, '').substring(0, 200) || 'N/A'}\n`
        ).join('\n');

        // Generate summary with Gemini
        const prompt = `You are a financial news analyst. Below are ${allNews.length} news articles from various sources.

Please provide:
1. A concise executive summary (2-3 paragraphs)
2. Key market themes and trends identified
3. Notable stock mentions and sentiment
4. Any actionable insights for traders

News articles:
${newsText}

Provide a well-structured analysis that a trader could use to make informed decisions.`;

        const result = await model.generateContent(prompt);
        const summary = result.response.text();

        // Save summary to file
        const timestamp = new Date().toISOString().replace(/:/g, '-').split('.')[0];
        const filename = `news_summary_${timestamp}.md`;
        const filepath = path.join(SUMMARIES_DIR, filename);

        const fileContent = `# News Summary - ${new Date().toLocaleString()}

## Sources
- Topics: ${topics.join(', ') || 'None'}
- Symbols: ${symbols.join(', ') || 'None'}
- Total Articles: ${allNews.length}

---

${summary}

---

## Articles Analyzed

${allNews.map((article, i) =>
  `### [${i + 1}] ${article.title}
- **Source**: ${article.source || 'Unknown'} (${article.source_type})
- **Published**: ${article.pub_date}
- **Link**: ${article.link}
`).join('\n')}
`;

        await fs.writeFile(filepath, fileContent, 'utf-8');

        return {
          content: [
            {
              type: 'text',
              text: `Summary generated and saved to: ${filename}\n\n${summary}`,
            },
          ],
        };
      }

      case 'list_news_summaries': {
        const files = await fs.readdir(SUMMARIES_DIR);
        const summaryFiles = files.filter(f => f.endsWith('.md'));
        return {
          content: [
            {
              type: 'text',
              text: JSON.stringify({ summaries: summaryFiles, count: summaryFiles.length }, null, 2),
            },
          ],
        };
      }

      case 'get_news_summary': {
        // Sanitize filename — prevent path traversal
        const safeName = path.basename(args.filename);
        const filepath = path.join(SUMMARIES_DIR, safeName);
        const content = await fs.readFile(filepath, 'utf-8');
        return {
          content: [
            {
              type: 'text',
              text: content,
            },
          ],
        };
      }

      case 'get_marketwatch_topstories': {
        const data = applyNewsDedup(await callTradingBot('/news/marketwatch/topstories'));
        return {
          content: [
            {
              type: 'text',
              text: JSON.stringify(data, null, 2),
            },
          ],
        };
      }

      case 'get_marketwatch_realtime': {
        const data = applyNewsDedup(await callTradingBot('/news/marketwatch/realtime'));
        return {
          content: [
            {
              type: 'text',
              text: JSON.stringify(data, null, 2),
            },
          ],
        };
      }

      case 'get_marketwatch_bulletins': {
        const data = applyNewsDedup(await callTradingBot('/news/marketwatch/bulletins'));
        return {
          content: [
            {
              type: 'text',
              text: JSON.stringify(data, null, 2),
            },
          ],
        };
      }

      case 'get_marketwatch_marketpulse': {
        const data = applyNewsDedup(await callTradingBot('/news/marketwatch/marketpulse'));
        return {
          content: [
            {
              type: 'text',
              text: JSON.stringify(data, null, 2),
            },
          ],
        };
      }

      case 'get_marketwatch_all': {
        const data = applyNewsDedup(await callTradingBot('/news/marketwatch/all'));
        return {
          content: [
            {
              type: 'text',
              text: JSON.stringify(data, null, 2),
            },
          ],
        };
      }

      // ── Economic Intelligence Feeds ──────────────────────────────────────
      case 'get_treasury_data': {
        const data = await callTradingBot('/feeds/treasury');
        return { content: [{ type: 'text', text: JSON.stringify(data, null, 2) }] };
      }
      case 'get_global_events': {
        let endpoint = '/feeds/gdelt';
        if (args.query) endpoint += `?q=${encodeURIComponent(args.query)}`;
        const data = await callTradingBot(endpoint);
        return { content: [{ type: 'text', text: JSON.stringify(data, null, 2) }] };
      }
      case 'get_economic_indicators': {
        const data = await callTradingBot('/feeds/bls');
        return { content: [{ type: 'text', text: JSON.stringify(data, null, 2) }] };
      }
      case 'get_market_snapshot': {
        const data = await callTradingBot('/feeds/yfinance');
        return { content: [{ type: 'text', text: JSON.stringify(data, null, 2) }] };
      }
      case 'get_defense_contracts': {
        const data = await callTradingBot('/feeds/usaspending');
        return { content: [{ type: 'text', text: JSON.stringify(data, null, 2) }] };
      }
      case 'get_global_trade_flows': {
        const data = await callTradingBot('/feeds/comtrade');
        return { content: [{ type: 'text', text: JSON.stringify(data, null, 2) }] };
      }

      case 'get_quick_market_intelligence': {
        const data = await callTradingBot('/intelligence/quick-market');
        return {
          content: [
            {
              type: 'text',
              text: JSON.stringify(data, null, 2),
            },
          ],
        };
      }

      case 'analyze_stocks': {
        const data = await callTradingBot('/intelligence/analyze-multiple', 'POST', args);
        return {
          content: [
            {
              type: 'text',
              text: JSON.stringify(data, null, 2),
            },
          ],
        };
      }

      case 'get_cleaned_news': {
        const requestBody = {
          include_google: args.include_google,
          include_marketwatch: args.include_marketwatch,
          google_topics: args.google_topics || [],
          symbols: args.symbols || [],
          max_articles_per_source: args.max_articles_per_source || 10,
        };
        const data = applyNewsDedup(await callTradingBot('/intelligence/cleaned-news', 'POST', requestBody));
        return {
          content: [
            {
              type: 'text',
              text: JSON.stringify(data, null, 2),
            },
          ],
        };
      }

      case 'log_activity': {
        const data = await callTradingBot('/activity/log', 'POST', {
          type: args.type,
          action: args.action,
          symbol: args.symbol || '',
          reasoning: args.reasoning || '',
          details: args.details || {},
        });
        return {
          content: [
            {
              type: 'text',
              text: `Activity logged: ${args.action}`,
            },
          ],
        };
      }

      case 'get_activity_log': {
        const data = await callTradingBot('/activity/current');
        return {
          content: [
            {
              type: 'text',
              text: JSON.stringify(data, null, 2),
            },
          ],
        };
      }

      case 'log_decision': {
        const timestamp = new Date().toISOString().replace(/[:.]/g, '-');
        const filename = `${timestamp}_${args.action}${args.symbol ? '_' + args.symbol : ''}.json`;
        const filepath = path.join(DECISIONS_DIR, filename);

        const decision = {
          timestamp: new Date().toISOString(),
          sandbox_id: OPENPROPHET_SANDBOX_ID,
          account_id: OPENPROPHET_ACCOUNT_ID,
          action: args.action,
          symbol: args.symbol || null,
          reasoning: args.reasoning,
          market_data: args.market_data || {},
        };

        await fs.writeFile(filepath, JSON.stringify(decision, null, 2));

        return {
          content: [
            {
              type: 'text',
              text: `Decision logged to ${filename}`,
            },
          ],
        };
      }

      case 'place_options_order': {
        const requestData = {
          symbol: args.symbol,
          underlying: args.underlying,
          qty: args.quantity,
          side: args.side,
          type: args.order_type,
          ...(args.position_intent && { position_intent: args.position_intent }),
          ...(args.limit_price && { limit_price: args.limit_price })
        };
        const data = await callTradingBot('/options/order', 'POST', requestData);
        return {
          content: [
            {
              type: 'text',
              text: JSON.stringify(data, null, 2),
            },
          ],
        };
      }

      case 'get_options_positions': {
        const data = await callTradingBot('/options/positions');
        return {
          content: [
            {
              type: 'text',
              text: JSON.stringify(data, null, 2),
            },
          ],
        };
      }

      case 'get_options_position': {
        const data = await callTradingBot(`/options/position/${args.symbol}`);
        return {
          content: [
            {
              type: 'text',
              text: JSON.stringify(data, null, 2),
            },
          ],
        };
      }

      case 'get_options_chain': {
        let endpoint = `/options/chain/${args.symbol}`;
        const params = new URLSearchParams();

        if (args.expiration) params.append('expiration', args.expiration);
        if (args.delta_min !== undefined) params.append('delta_min', args.delta_min);
        if (args.delta_max !== undefined) params.append('delta_max', args.delta_max);
        if (args.min_bid !== undefined) params.append('min_bid', args.min_bid);
        if (args.type) params.append('type', args.type);

        if (params.toString()) endpoint += `?${params.toString()}`;

        const data = await callTradingBot(endpoint);
        return {
          content: [
            {
              type: 'text',
              text: JSON.stringify(data, null, 2),
            },
          ],
        };
      }

      case 'wait': {
        const seconds = Math.min(Math.max(args.seconds, 1), 300); // Clamp between 1-300 seconds
        const reason = args.reason || 'Waiting';

        const startTime = Date.now();
        await new Promise(resolve => setTimeout(resolve, seconds * 1000));
        const actualDuration = ((Date.now() - startTime) / 1000).toFixed(1);

        return {
          content: [
            {
              type: 'text',
              text: `Waited ${actualDuration} seconds${reason ? ` - ${reason}` : ''}`,
            },
          ],
        };
      }

      case 'get_datetime': {
        const timezone = args.timezone || 'America/New_York';
        const now = new Date();

        try {
          // Time formatting
          const timeString = now.toLocaleTimeString('en-US', {
            timeZone: timezone,
            hour: '2-digit',
            minute: '2-digit',
            second: '2-digit',
            hour12: true,
          });

          const time24 = now.toLocaleTimeString('en-US', {
            timeZone: timezone,
            hour: '2-digit',
            minute: '2-digit',
            second: '2-digit',
            hour12: false,
          });

          // Date formatting
          const dateString = now.toLocaleDateString('en-US', {
            timeZone: timezone,
            weekday: 'long',
            year: 'numeric',
            month: 'long',
            day: 'numeric',
          });

          const isoDate = now.toLocaleDateString('en-CA', { timeZone: timezone }); // YYYY-MM-DD format

          const dayOfWeek = now.toLocaleDateString('en-US', {
            timeZone: timezone,
            weekday: 'long',
          });

          // Check if within market hours (9:30 AM - 4:00 PM ET)
          const etTime = now.toLocaleTimeString('en-US', {
            timeZone: 'America/New_York',
            hour: '2-digit',
            minute: '2-digit',
            hour12: false,
          });
          const [hours, minutes] = etTime.split(':').map(Number);
          const marketMinutes = hours * 60 + minutes;
          const marketOpen = marketMinutes >= 570 && marketMinutes < 960; // 9:30 AM to 4:00 PM
          const preMarket = marketMinutes >= 240 && marketMinutes < 570; // 4:00 AM to 9:30 AM
          const afterHours = marketMinutes >= 960 && marketMinutes < 1200; // 4:00 PM to 8:00 PM

          // Check if it's a weekday
          const actualDay = now.getDay();
          const marketDay = actualDay >= 1 && actualDay <= 5;

          // US market holidays (NYSE observed) — 2025-2027
          const marketHolidays = [
            // 2025
            '2025-01-01', '2025-01-20', '2025-02-17', '2025-04-18',
            '2025-05-26', '2025-06-19', '2025-07-04', '2025-09-01',
            '2025-11-27', '2025-12-25',
            // 2026
            '2026-01-01', '2026-01-19', '2026-02-16', '2026-04-03',
            '2026-05-25', '2026-06-19', '2026-07-03', '2026-09-07',
            '2026-11-26', '2026-12-25',
            // 2027
            '2027-01-01', '2027-01-18', '2027-02-15', '2027-03-26',
            '2027-05-31', '2027-06-18', '2027-07-05', '2027-09-06',
            '2027-11-25', '2027-12-24',
          ];
          const isHoliday = marketHolidays.includes(isoDate);

          // Determine market status
          let marketStatus = 'CLOSED';
          if (marketDay && !isHoliday) {
            if (marketOpen) marketStatus = 'OPEN';
            else if (preMarket) marketStatus = 'PRE_MARKET';
            else if (afterHours) marketStatus = 'AFTER_HOURS';
          }

          return {
            content: [
              {
                type: 'text',
                text: JSON.stringify({
                  time: timeString,
                  time_24h: time24,
                  date: dateString,
                  iso_date: isoDate,
                  day_of_week: dayOfWeek,
                  timezone: timezone,
                  iso: now.toISOString(),
                  unix: Math.floor(now.getTime() / 1000),
                  is_weekday: marketDay,
                  is_market_holiday: isHoliday,
                  market_status: marketStatus,
                  markets_open_today: marketDay && !isHoliday,
                }, null, 2),
              },
            ],
          };
        } catch (error) {
          return {
            content: [{ type: 'text', text: `Error: Invalid timezone "${timezone}"` }],
            isError: true,
          };
        }
      }

      // Vector DB: Find similar trading setups
      case 'find_similar_setups': {
        const { query, limit = 5, symbol, strategy, action } = args;

        const filters = {};
        if (symbol) filters.symbol = symbol;
        if (strategy) filters.strategy = strategy;
        if (action) filters.action = action;

        const similarTrades = await findSimilarTrades(query, limit, filters);

        // Format results for display
        const formattedResults = similarTrades.map((trade, i) => {
          const resultStr = trade.result_pct !== null
            ? `${trade.result_pct > 0 ? '+' : ''}${trade.result_pct.toFixed(1)}% ($${trade.result_dollars > 0 ? '+' : ''}${trade.result_dollars})`
            : 'No result data';

          return `
${i + 1}. ${trade.symbol} ${trade.action} - ${trade.strategy}
   Date: ${trade.date}
   Result: ${resultStr}
   Similarity: ${(trade.similarity * 100).toFixed(1)}%

   Reasoning: ${trade.reasoning}

   Market Context: ${trade.market_context}
   `;
        }).join('\n---\n');

        const summary = `Found ${similarTrades.length} similar ${strategy ? strategy + ' ' : ''}trades${symbol ? ' for ' + symbol : ''}:\n\n${formattedResults}`;

        return {
          content: [{ type: 'text', text: summary }],
        };
      }

      // Vector DB: Store trade setup
      case 'store_trade_setup': {
        const { symbol, action, strategy, result_pct, result_dollars, reasoning, market_context } = args;

        const now = new Date();
        const dateStr = now.toISOString().split('T')[0]; // YYYY-MM-DD
        const id = `${dateStr}-${symbol}-${action}-${now.getTime()}`;
        const decision_file = `manual_${id}.json`;

        const trade = {
          id,
          decision_file,
          symbol,
          action,
          strategy,
          result_pct: result_pct || null,
          result_dollars: result_dollars || null,
          date: dateStr,
          reasoning,
          market_context,
        };

        await storeTrade(trade);

        const totalEmbeddings = getEmbeddingCount();

        return {
          content: [{
            type: 'text',
            text: `✅ Stored trade: ${symbol} ${action} (${strategy})
Result: ${result_pct !== null ? (result_pct > 0 ? '+' : '') + result_pct.toFixed(1) + '%' : 'pending'}
Total embeddings in database: ${totalEmbeddings}

You can now use find_similar_setups to find trades similar to this one.`,
          }],
        };
      }

      // Vector DB: Get trade statistics
      case 'get_trade_stats': {
        const { symbol, strategy, action } = args;

        const filters = {};
        if (symbol) filters.symbol = symbol;
        if (strategy) filters.strategy = strategy;
        if (action) filters.action = action;

        const stats = getTradeStats(filters);

        const filterDesc = [];
        if (symbol) filterDesc.push(`Symbol: ${symbol}`);
        if (strategy) filterDesc.push(`Strategy: ${strategy}`);
        if (action) filterDesc.push(`Action: ${action}`);

        const filterStr = filterDesc.length > 0 ? ` (${filterDesc.join(', ')})` : '';

        const statsText = `
📊 Trade Statistics${filterStr}

Total Trades: ${stats.count}
Winners: ${stats.winners} (${stats.win_rate.toFixed(1)}%)
Losers: ${stats.losers}

Average Result: ${stats.avg_result_pct >= 0 ? '+' : ''}${stats.avg_result_pct.toFixed(1)}% ($${stats.avg_result_dollars >= 0 ? '+' : ''}${stats.avg_result_dollars.toFixed(0)})

Best Trade: +${stats.best_result_pct.toFixed(1)}% ($${stats.best_result_dollars > 0 ? '+' : ''}${stats.best_result_dollars.toFixed(0)})
Worst Trade: ${stats.worst_result_pct.toFixed(1)}% ($${stats.worst_result_dollars.toFixed(0)})
`;

        return {
          content: [{ type: 'text', text: statsText }],
        };
      }

      // ── Agent Self-Modification Tools ──────────────────────────
      case 'update_agent_prompt': {
        const { prompt } = args;
        const configResp2 = await agentAxios.get(`${AGENT_URL}/api/sandboxes/${OPENPROPHET_SANDBOX_ID}/config`);
        const agentId = configResp2.data?.agent?.id || 'default';
        await agentAxios.put(`${AGENT_URL}/api/agents/${agentId}`, {
          systemPromptTemplate: 'custom',
          customSystemPrompt: prompt,
        });
        await agentAxios.put(`${AGENT_URL}/api/sandboxes/${OPENPROPHET_SANDBOX_ID}/agent/overrides`, {
          systemPromptTemplate: null,
          customSystemPrompt: null,
        });
        return {
          content: [{ type: 'text', text: `Updated agent "${agentId}" prompt (${prompt.length} chars). Visible on Agents page. Takes effect next heartbeat.` }],
        };
      }

      case 'update_strategy_rules': {
        const { name: strategyName, rules } = args;
        const createResp = await agentAxios.post(`${AGENT_URL}/api/strategies`, {
          name: strategyName || 'Agent-Created Strategy',
          description: `Created by agent at ${new Date().toISOString()}`,
          customRules: rules,
        });
        const newStrategy = createResp.data.strategy;
        const configResp3 = await agentAxios.get(`${AGENT_URL}/api/sandboxes/${OPENPROPHET_SANDBOX_ID}/config`);
        const agentId3 = configResp3.data?.agent?.id || 'default';
        await agentAxios.put(`${AGENT_URL}/api/agents/${agentId3}`, { strategyId: newStrategy.id });
        await agentAxios.put(`${AGENT_URL}/api/sandboxes/${OPENPROPHET_SANDBOX_ID}/strategy-rules`, { rules: '' });
        return {
          content: [{ type: 'text', text: `Created new strategy "${strategyName}" (ID: ${newStrategy.id}) and assigned to agent "${agentId3}". Visible on Agents page. Existing strategies not modified.` }],
        };
      }

      case 'get_agent_config': {
        const [configResp, permResp, hbResp, sandboxResp] = await Promise.all([
          agentAxios.get(`${AGENT_URL}/api/config`),
          agentAxios.get(`${AGENT_URL}/api/permissions`, { params: AGENT_QUERY }),
          agentAxios.get(`${AGENT_URL}/api/heartbeat`, { params: AGENT_QUERY }),
          agentAxios.get(`${AGENT_URL}/api/sandboxes/${OPENPROPHET_SANDBOX_ID}/config`),
        ]);
        const config = configResp.data;
        const sandbox = sandboxResp.data.sandbox || config.sandboxes?.[OPENPROPHET_SANDBOX_ID] || null;
        const activeAgent = sandboxResp.data.agent || null;
        const activeModel = activeAgent?.model || sandbox?.agent?.model || config.activeModel;
        const result = {
          activeAgent: activeAgent ? {
            id: activeAgent.id,
            name: activeAgent.name,
            model: activeAgent.model,
            promptType: activeAgent.systemPromptTemplate,
            strategyId: activeAgent.strategyId ?? null,
            customStrategyRules: Boolean(activeAgent.customStrategyRules),
          } : null,
          activeModel,
          permissions: permResp.data,
          heartbeat: hbResp.data,
          sandboxId: OPENPROPHET_SANDBOX_ID,
          accountId: OPENPROPHET_ACCOUNT_ID,
          accountCount: config.accounts?.length || 0,
          strategyCount: config.strategies?.length || 0,
        };
        return {
          content: [{ type: 'text', text: JSON.stringify(result, null, 2) }],
        };
      }

      case 'set_heartbeat': {
        const seconds = Math.min(Math.max(args.seconds, 30), 3600);
        await agentAxios.post(`${AGENT_URL}/api/agent/heartbeat`, {
          seconds,
          sandboxId: OPENPROPHET_SANDBOX_ID,
          reason: args.reason || `Agent override to ${seconds}s`,
        });
        return {
          content: [{ type: 'text', text: `Heartbeat interval set to ${seconds}s. ${args.reason || ''}` }],
        };
      }

      case 'update_permissions': {
        await agentAxios.put(`${AGENT_URL}/api/permissions`, {
          ...args,
          sandboxId: OPENPROPHET_SANDBOX_ID,
        });
        return {
          content: [{ type: 'text', text: `Updated permissions: ${Object.keys(args).join(', ')}. Changes take effect immediately.` }],
        };
      }

      case 'get_heartbeat_profiles': {
        const resp = await agentAxios.get(`${AGENT_URL}/api/heartbeat/profiles`);
        const profiles = resp.data.profiles || {};
        let msg = 'Available heartbeat profiles:\n';
        for (const [key, p] of Object.entries(profiles)) {
          msg += `\n${key}: ${p.label}\n  ${p.description}\n  Phases: ${JSON.stringify(p.phases)}\n`;
        }
        return { content: [{ type: 'text', text: msg }] };
      }

      case 'apply_heartbeat_profile': {
        const { profile } = args;
        await agentAxios.post(`${AGENT_URL}/api/heartbeat/apply-profile`, {
          profile,
          sandboxId: OPENPROPHET_SANDBOX_ID,
        });
        return {
          content: [{ type: 'text', text: `Applied heartbeat profile "${profile}". Changes take effect on next heartbeat.` }],
        };
      }

      case 'get_heartbeat_phases': {
        const resp = await agentAxios.get(`${AGENT_URL}/api/heartbeat/phases`);
        const phases = resp.data.phases || {};
        let msg = 'Heartbeat phase time ranges (minutes from midnight ET):\n';
        for (const [key, p] of Object.entries(phases)) {
          msg += `\n${key}: ${p.label}\n  ${p.start !== null ? `${p.start}-${p.end}` : 'N/A (closed)'}\n`;
        }
        return { content: [{ type: 'text', text: msg }] };
      }

      case 'update_heartbeat_phase': {
        const { phase, start, end } = args;
        await agentAxios.put(`${AGENT_URL}/api/heartbeat/phases`, {
          phase,
          start,
          end,
        });
        return {
          content: [{ type: 'text', text: `Updated phase "${phase}" time range. Changes take effect immediately.` }],
        };
      }

      case 'set_session_mode': {
        const { mode } = args;
        if (mode !== 'continuous' && mode !== 'fresh') {
          return { content: [{ type: 'text', text: 'Invalid mode. Use "continuous" or "fresh".' }], isError: true };
        }
        await agentAxios.put(`${AGENT_URL}/api/sandboxes/${OPENPROPHET_SANDBOX_ID}/agent/overrides`, {
          sessionMode: mode,
        });
        const msg = mode === 'fresh' 
          ? 'Session mode set to "fresh" - each heartbeat will start with a fresh context. Good for long_horizon strategies.'
          : 'Session mode set to "continuous" - conversation context persists across heartbeats.';
        return { content: [{ type: 'text', text: msg }] };
      }

      case 'create_agent': {
        const { name: agentName, description, model, strategyId, customSystemPrompt } = args;
        const body = {
          name: agentName,
          description: description || '',
          model: model || 'anthropic/claude-sonnet-4-6',
          strategyId: strategyId || undefined,
          systemPromptTemplate: customSystemPrompt ? 'custom' : 'default',
          customSystemPrompt: customSystemPrompt || '',
        };
        const resp = await agentAxios.post(`${AGENT_URL}/api/agents`, body);
        const agent = resp.data.agent;
        return {
          content: [{ type: 'text', text: `Created agent "${agentName}" (ID: ${agent.id}). You can now assign it to a sandbox with assign_agent_to_sandbox.` }],
        };
      }

      case 'create_strategy': {
        const { name: stratName, description, customRules } = args;
        const body = {
          name: stratName,
          description: description || '',
          customRules: customRules,
        };
        const resp = await agentAxios.post(`${AGENT_URL}/api/strategies`, body);
        const strategy = resp.data.strategy;
        return {
          content: [{ type: 'text', text: `Created strategy "${stratName}" (ID: ${strategy.id}). Assign it to an agent by updating the agent's strategyId, or use the UI.` }],
        };
      }

      case 'assign_agent_to_sandbox': {
        const { agentId, sandboxId } = args;
        const targetSandbox = sandboxId || OPENPROPHET_SANDBOX_ID;
        await agentAxios.put(`${AGENT_URL}/api/sandboxes/${targetSandbox}/agent`, {
          activeAgentId: agentId,
        });
        return {
          content: [{ type: 'text', text: `Assigned agent "${agentId}" to sandbox "${targetSandbox}". The agent will take over on the next heartbeat.` }],
        };
      }

      // ── Analysis Tool Handlers ──────────────────────────────────────

      case 'run_market_briefing': {
        await fs.mkdir(REPORTS_DIR, { recursive: true });

        const breadthResult = spawnSync(PYTHON_BIN, [
          path.join(process.cwd(), '.claude/skills/breadth-chart-analyst/scripts/fetch_breadth_csv.py'),
          '--json',
        ], { timeout: 30000, encoding: 'utf-8', env: process.env });

        const uptrendResult = spawnSync(PYTHON_BIN, [
          path.join(process.cwd(), '.claude/skills/uptrend-analyzer/scripts/uptrend_analyzer.py'),
          '--output-dir', REPORTS_DIR,
        ], { timeout: 90000, encoding: 'utf-8', env: process.env });

        let uptrendData = null;
        try {
          const files = (await fs.readdir(REPORTS_DIR))
            .filter(f => f.startsWith('uptrend_analysis_') && f.endsWith('.json'))
            .sort().reverse();
          if (files.length > 0) uptrendData = JSON.parse(await fs.readFile(path.join(REPORTS_DIR, files[0]), 'utf-8'));
        } catch {}

        let breadthData = null;
        try { breadthData = JSON.parse(breadthResult.stdout || 'null'); } catch {}

        const output = {
          breadth_csv: breadthData || { error: breadthResult.stderr?.slice(0, 300) || 'no output' },
          uptrend_analysis: uptrendData || { error: uptrendResult.stderr?.slice(0, 300) || 'no output file generated' },
        };
        return { content: [{ type: 'text', text: JSON.stringify(output, null, 2) }] };
      }

      case 'run_vcp_screener': {
        await fs.mkdir(REPORTS_DIR, { recursive: true });
        const fmpKey = process.env.FMP_API_KEY;
        if (!fmpKey) return { content: [{ type: 'text', text: 'Error: FMP_API_KEY environment variable not set. Cannot run VCP screener.' }], isError: true };

        const scriptArgs = [
          path.join(process.cwd(), '.claude/skills/vcp-screener/scripts/screen_vcp.py'),
          '--output-dir', REPORTS_DIR,
        ];
        if (args?.strict) scriptArgs.push('--strict');

        const proc = spawnBg(PYTHON_BIN, scriptArgs, {
          cwd: process.cwd(),
          env: { ...process.env, FMP_API_KEY: fmpKey },
          stdio: 'ignore',
          detached: false,
        });
        const pid = proc.pid;
        proc.unref();
        return { content: [{ type: 'text', text: `VCP screener launched (PID: ${pid}). Expected completion: 2-3 minutes.\n\nResults will appear in data/reports/vcp_screener_*.json\n\nRecommended: call wait(180) then read_latest_report("vcp")` }] };
      }

      case 'run_pead_screener': {
        await fs.mkdir(REPORTS_DIR, { recursive: true });
        const fmpKey = process.env.FMP_API_KEY;
        if (!fmpKey) return { content: [{ type: 'text', text: 'Error: FMP_API_KEY environment variable not set. Cannot run PEAD screener.' }], isError: true };

        const proc = spawnBg(PYTHON_BIN, [
          path.join(process.cwd(), '.claude/skills/pead-screener/scripts/screen_pead.py'),
          '--output-dir', REPORTS_DIR,
        ], {
          cwd: process.cwd(),
          env: { ...process.env, FMP_API_KEY: fmpKey },
          stdio: 'ignore',
          detached: false,
        });
        const pid = proc.pid;
        proc.unref();
        return { content: [{ type: 'text', text: `PEAD screener launched (PID: ${pid}). Expected completion: 1-2 minutes.\n\nResults will appear in data/reports/pead_screener_*.json\n\nRecommended: call wait(120) then read_latest_report("pead")` }] };
      }

      case 'run_market_top_check': {
        await fs.mkdir(REPORTS_DIR, { recursive: true });
        const fmpKey = process.env.FMP_API_KEY;
        if (!fmpKey) return { content: [{ type: 'text', text: 'Error: FMP_API_KEY environment variable not set.' }], isError: true };

        const result = spawnSync(PYTHON_BIN, [
          path.join(process.cwd(), '.claude/skills/market-top-detector/scripts/market_top_detector.py'),
          '--output-dir', REPORTS_DIR,
        ], { timeout: 120000, encoding: 'utf-8', env: { ...process.env, FMP_API_KEY: fmpKey } });

        if (result.error) return { content: [{ type: 'text', text: `Error: ${result.error.message}` }], isError: true };

        let reportData = null;
        try {
          const files = (await fs.readdir(REPORTS_DIR))
            .filter(f => f.startsWith('market_top_') && f.endsWith('.json'))
            .sort().reverse();
          if (files.length > 0) reportData = JSON.parse(await fs.readFile(path.join(REPORTS_DIR, files[0]), 'utf-8'));
        } catch {}

        const text = reportData ? JSON.stringify(reportData, null, 2) : (result.stdout || result.stderr || 'Completed with no output');
        return { content: [{ type: 'text', text: text.slice(0, 8000) }] };
      }

      case 'run_ftd_check': {
        await fs.mkdir(REPORTS_DIR, { recursive: true });
        const fmpKey = process.env.FMP_API_KEY;
        if (!fmpKey) return { content: [{ type: 'text', text: 'Error: FMP_API_KEY environment variable not set.' }], isError: true };

        const result = spawnSync(PYTHON_BIN, [
          path.join(process.cwd(), '.claude/skills/ftd-detector/scripts/ftd_detector.py'),
          '--output-dir', REPORTS_DIR,
        ], { timeout: 90000, encoding: 'utf-8', env: { ...process.env, FMP_API_KEY: fmpKey } });

        if (result.error) return { content: [{ type: 'text', text: `Error: ${result.error.message}` }], isError: true };

        let reportData = null;
        try {
          const files = (await fs.readdir(REPORTS_DIR))
            .filter(f => f.startsWith('ftd_detector_') && f.endsWith('.json'))
            .sort().reverse();
          if (files.length > 0) reportData = JSON.parse(await fs.readFile(path.join(REPORTS_DIR, files[0]), 'utf-8'));
        } catch {}

        const text = reportData ? JSON.stringify(reportData, null, 2) : (result.stdout || result.stderr || 'Completed with no output');
        return { content: [{ type: 'text', text: text.slice(0, 8000) }] };
      }

      case 'run_economic_calendar': {
        const fmpKey = process.env.FMP_API_KEY;
        if (!fmpKey) return { content: [{ type: 'text', text: 'Error: FMP_API_KEY environment variable not set.' }], isError: true };

        const today = new Date().toLocaleDateString('en-CA', { timeZone: 'America/New_York' });
        const endDate = new Date(Date.now() + 14 * 24 * 60 * 60 * 1000).toLocaleDateString('en-CA', { timeZone: 'America/New_York' });

        const result = spawnSync(PYTHON_BIN, [
          path.join(process.cwd(), '.claude/skills/economic-calendar-fetcher/scripts/get_economic_calendar.py'),
          '--from', today,
          '--to', endDate,
        ], { timeout: 30000, encoding: 'utf-8', env: { ...process.env, FMP_API_KEY: fmpKey } });

        if (result.error) return { content: [{ type: 'text', text: `Error: ${result.error.message}` }], isError: true };
        return { content: [{ type: 'text', text: result.stdout || result.stderr || 'No events found.' }] };
      }

      case 'run_earnings_calendar': {
        const fmpKey = process.env.FMP_API_KEY;
        if (!fmpKey) return { content: [{ type: 'text', text: 'Error: FMP_API_KEY environment variable not set.' }], isError: true };

        const etNow = new Date(new Date().toLocaleString('en-US', { timeZone: 'America/New_York' }));
        const day = etNow.getDay();
        const monday = new Date(etNow);
        monday.setDate(etNow.getDate() - (day === 0 ? 6 : day - 1));
        const friday = new Date(monday);
        friday.setDate(monday.getDate() + 4);
        const fmt = (d) => d.toLocaleDateString('en-CA');

        const result = spawnSync(PYTHON_BIN, [
          path.join(process.cwd(), '.claude/skills/earnings-calendar/scripts/fetch_earnings_fmp.py'),
          fmt(monday),
          fmt(friday),
        ], { timeout: 30000, encoding: 'utf-8', env: { ...process.env, FMP_API_KEY: fmpKey } });

        if (result.error) return { content: [{ type: 'text', text: `Error: ${result.error.message}` }], isError: true };
        return { content: [{ type: 'text', text: result.stdout || result.stderr || 'No earnings found.' }] };
      }

      case 'read_latest_report': {
        const { type: reportType } = args;
        const prefixMap = {
          vcp: 'vcp_screener_',
          pead: 'pead_screener_',
          market_top: 'market_top_',
          ftd: 'ftd_detector_',
          daily_brief: 'daily_brief_',
          weekly_regime: 'weekly_regime_',
          uptrend: 'uptrend_analysis_',
          scenario: 'scenario_',
          review: 'review_',
          market_alert: 'market_alert_',
        };
        const prefix = prefixMap[reportType];
        if (!prefix) return { content: [{ type: 'text', text: `Unknown report type: ${reportType}. Valid: ${Object.keys(prefixMap).join(', ')}` }], isError: true };

        let files;
        try {
          const all = await fs.readdir(REPORTS_DIR);
          files = all.filter(f => f.startsWith(prefix) && f.endsWith('.json')).sort().reverse();
        } catch {
          return { content: [{ type: 'text', text: 'data/reports/ not found. No reports generated yet.' }] };
        }

        if (files.length === 0) {
          // Check if the report is currently being generated (lock file present)
          const lockFiles = (await fs.readdir(REPORTS_DIR).catch(() => [])).filter(f => f.startsWith(prefix) && f.endsWith('.running'));
          if (lockFiles.length > 0) {
            return { content: [{ type: 'text', text: `BRIEFING_IN_PROGRESS: The ${reportType} report is currently being generated by another agent. Call wait(60) then retry read_latest_report("${reportType}").` }] };
          }
          return { content: [{ type: 'text', text: `No ${reportType} reports found in data/reports/. Run the corresponding screener first.` }] };
        }

        const content = await fs.readFile(path.join(REPORTS_DIR, files[0]), 'utf-8');
        const truncated = content.length > 8000 ? content.slice(0, 8000) + '\n... [truncated]' : content;
        return { content: [{ type: 'text', text: `Report: ${files[0]}\n\n${truncated}` }] };
      }

      case 'get_penny_candidates': {
        const min_score = args?.min_score ?? 60;
        const detail = args?.detail === true ? '&detail=true' : '';
        const data = await callTradingBot(`/penny/candidates?min_score=${min_score}${detail}`);
        return {
          content: [{ type: 'text', text: JSON.stringify(data, null, 2) }],
        };
      }

      case 'get_penny_signal_detail': {
        const data = await callTradingBot(`/penny/signal/${encodeURIComponent(args.ticker)}`);
        return {
          content: [{ type: 'text', text: JSON.stringify(data, null, 2) }],
        };
      }

      case 'get_penny_universe': {
        const data = await callTradingBot('/penny/universe');
        return {
          content: [{ type: 'text', text: JSON.stringify(data, null, 2) }],
        };
      }

      case 'scan_penny_universe_now': {
        const data = await callTradingBot('/penny/scan', 'POST');
        return {
          content: [{ type: 'text', text: JSON.stringify(data, null, 2) }],
        };
      }

      default:
        throw new Error(`Unknown tool: ${name}`);
    }
  } catch (error) {
    return {
      content: [
        {
          type: 'text',
          text: `Error: ${error.message}`,
        },
      ],
      isError: true,
    };
  }
});

// Start server
async function main() {
  const transport = new StdioServerTransport();
  await server.connect(transport);
  console.error('OpenProphet MCP Server running on stdio');
}

main().catch((error) => {
  console.error('Fatal error:', error);
  process.exit(1);
});
