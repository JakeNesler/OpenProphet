// Persistent configuration store for accounts, sandboxes, agents, strategies, and prompts
// Uses a JSON file for simplicity - no extra DB dependencies
import fs from 'fs/promises';
import path from 'path';
import { fileURLToPath } from 'url';
import crypto from 'crypto';
import { execSync } from 'child_process';
import { DEFAULT_AGENT_MODEL, alpacaTradingUrl } from './defaults.js';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const CONFIG_PATH = process.env.OPENPROPHET_CONFIG_PATH || path.join(__dirname, '..', 'data', 'agent-config.json');

const DEFAULT_HEARTBEAT = {
  pre_market: 900,
  market_open: 120,
  midday: 600,
  market_close: 120,
  after_hours: 1800,
  closed: 3600,
};

export const HEARTBEAT_PROFILES = {
  active: {
    label: 'Active Trading',
    description: 'High-frequency monitoring during market hours',
    phases: { pre_market: 300, market_open: 60, midday: 300, market_close: 60, after_hours: 600, closed: 1800 },
  },
  passive: {
    label: 'Passive Monitoring',
    description: 'Low-frequency check-ins, hands-off approach',
    phases: { pre_market: 1800, market_open: 600, midday: 900, market_close: 600, after_hours: 3600, closed: 7200 },
  },
  long_horizon: {
    label: 'Long Horizon',
    description: 'Weekly/monthly style check-ins for position management',
    phases: { pre_market: 7200, market_open: 3600, midday: 3600, market_close: 3600, after_hours: 7200, closed: 14400 },
  },
  earnings_season: {
    label: 'Earnings Season',
    description: 'Heightened vigilance during earnings periods',
    phases: { pre_market: 180, market_open: 30, midday: 120, market_close: 30, after_hours: 300, closed: 1800 },
  },
  overnight: {
    label: 'Overnight Hold',
    description: 'Set and forget with minimal overnight checks',
    phases: { pre_market: 900, market_open: 120, midday: 300, market_close: 120, after_hours: 7200, closed: 10800 },
  },
  scalp: {
    label: 'Scalp Mode',
    description: 'Rapid-fire execution for day trading',
    phases: { pre_market: 60, market_open: 15, midday: 30, market_close: 15, after_hours: 120, closed: 600 },
  },
};

export const PHASE_TIME_RANGES = {
  pre_market: { label: 'Pre-Market', start: 240, end: 570 },
  market_open: { label: 'Market Open', start: 570, end: 630 },
  midday: { label: 'Midday', start: 630, end: 900 },
  market_close: { label: 'Market Close', start: 900, end: 960 },
  after_hours: { label: 'After Hours', start: 960, end: 1200 },
  closed: { label: 'Markets Closed', start: null, end: null },
};

const DEFAULT_PERMISSIONS = {
  allowLiveTrading: true,
  maxPositionPct: 15,
  maxDeployedPct: 80,
  maxDailyLoss: 5,
  maxOpenPositions: 10,
  maxOrderValue: 0,
  allowedTools: [],
  blockedTools: [],
  allowOptions: true,
  allowStocks: true,
  allow0DTE: false,
  requireConfirmation: false,
  maxToolRoundsPerBeat: 25,
};

const DEFAULT_PLUGINS = {
  slack: {
    enabled: false,
    webhookUrl: '',
    channel: '',
    notifyOn: {
      tradeExecuted: true,
      agentStartStop: true,
      errors: true,
      dailySummary: true,
      positionOpened: true,
      positionClosed: true,
      heartbeat: false,
    },
  },
};

const DEFAULT_AGENT_OVERRIDES = {
  name: null,
  description: null,
  systemPromptTemplate: null,
  customSystemPrompt: null,
  strategyId: undefined,
  customStrategyRules: null,
  heartbeatOverrides: {},
  sessionMode: 'continuous', // 'continuous' or 'fresh' - 'fresh' starts new session each beat
};

function defaultAgents() {
  return [
    {
      id: 'default',
      name: 'Prophet',
      description: 'Aggressive discretionary options trader with scalping overlay',
      systemPromptTemplate: 'default',
      strategyId: 'default',
      model: DEFAULT_AGENT_MODEL,
      heartbeatOverrides: {},
      customSystemPrompt: '',
      createdAt: new Date().toISOString(),
    },
    {
      id: 'conservative',
      name: 'Guardian',
      description: 'Conservative swing trader focused on capital preservation',
      systemPromptTemplate: 'custom',
      customSystemPrompt: 'You are Guardian, a conservative AI trading agent. You prioritize capital preservation above all else, trading only when the setup is clear and the risk is well-defined. You would rather sit in cash than force a trade.',
      strategyId: 'capital-preservation',
      model: DEFAULT_AGENT_MODEL,
      heartbeatOverrides: {
        pre_market: 1800,
        market_open: 300,
        midday: 900,
        market_close: 300,
        after_hours: 3600,
      },
      createdAt: new Date().toISOString(),
    },
    {
      id: 'momentum',
      name: 'Surge',
      description: 'Momentum trader riding liquid large-cap breakouts and trend continuation',
      systemPromptTemplate: 'custom',
      customSystemPrompt: 'You are Surge, a momentum trading agent. You hunt for liquid large-cap stocks and ETFs breaking out on strong volume and ride the trend while it is confirmed by price and volume, cutting away the moment momentum stalls. You chase strength, never a falling price.',
      strategyId: 'equity-momentum',
      model: DEFAULT_AGENT_MODEL,
      heartbeatOverrides: {
        pre_market: 600,
        market_open: 60,
        midday: 240,
        market_close: 60,
        after_hours: 1800,
      },
      createdAt: new Date().toISOString(),
    },
    {
      id: 'mean-reversion',
      name: 'Pendulum',
      description: 'Mean-reversion trader fading short-term overextension in liquid ETFs',
      systemPromptTemplate: 'custom',
      customSystemPrompt: 'You are Pendulum, a mean-reversion trading agent. You trade liquid, broad-based ETFs that have stretched too far too fast from their recent average and are showing early signs of snapping back, taking profit as price returns toward the mean rather than chasing a trend.',
      strategyId: 'etf-mean-reversion',
      model: DEFAULT_AGENT_MODEL,
      heartbeatOverrides: {
        pre_market: 1200,
        market_open: 180,
        midday: 600,
        market_close: 180,
        after_hours: 3600,
      },
      createdAt: new Date().toISOString(),
    },
    {
      id: 'macro-rotation',
      name: 'Compass',
      description: 'Macro rotation across liquid index and sector ETFs by market regime',
      systemPromptTemplate: 'custom',
      customSystemPrompt: 'You are Compass, a macro rotation agent. You read the broad market regime — rates, breadth, relative sector strength — and rotate a small number of liquid index and sector ETF positions to align with the prevailing macro trend, rebalancing deliberately rather than reacting to daily noise.',
      strategyId: 'macro-rotation',
      model: DEFAULT_AGENT_MODEL,
      heartbeatOverrides: {
        pre_market: 3600,
        market_open: 1800,
        midday: 1800,
        market_close: 1800,
        after_hours: 7200,
      },
      createdAt: new Date().toISOString(),
    },
    {
      id: 'trend-follower',
      name: 'Anchor',
      description: 'Long-horizon trend follower holding durable uptrends for months',
      systemPromptTemplate: 'custom',
      customSystemPrompt: 'You are Anchor, a long-horizon trend-following agent. You build and hold positions in liquid large-cap stocks and ETFs that are in a durable, established uptrend over many months, adding patience where others add activity, and only step aside when the underlying trend itself breaks.',
      strategyId: 'long-horizon-trend',
      model: DEFAULT_AGENT_MODEL,
      heartbeatOverrides: {
        pre_market: 7200,
        market_open: 3600,
        midday: 3600,
        market_close: 3600,
        after_hours: 10800,
      },
      createdAt: new Date().toISOString(),
    },
    {
      id: 'catalyst',
      name: 'Herald',
      description: 'Catalyst and news-reaction trader working confirmed, already-public events',
      systemPromptTemplate: 'custom',
      customSystemPrompt: 'You are Herald, a catalyst-driven trading agent. You trade the confirmed market reaction to specific, already-public news and events — earnings prints, guidance changes, major headlines — never the rumor or the guess beforehand, entering only after the market has shown its hand and exiting once the initial reaction has played out.',
      strategyId: 'catalyst-news',
      model: DEFAULT_AGENT_MODEL,
      heartbeatOverrides: {
        pre_market: 300,
        market_open: 30,
        midday: 120,
        market_close: 30,
        after_hours: 300,
      },
      createdAt: new Date().toISOString(),
    },
    {
      id: 'long-vol',
      name: 'Vega',
      description: 'Long-premium volatility trader buying outright calls and puts',
      systemPromptTemplate: 'custom',
      customSystemPrompt: "You are Vega, a long-premium volatility agent. You buy options outright — calls or puts, never written or spread — to express a view on a stock's expected move around a specific volatility catalyst, treating the premium paid as your entire, pre-defined risk on every trade.",
      strategyId: 'long-premium-volatility',
      model: DEFAULT_AGENT_MODEL,
      heartbeatOverrides: {
        pre_market: 900,
        market_open: 120,
        midday: 300,
        market_close: 120,
        after_hours: 1800,
      },
      createdAt: new Date().toISOString(),
    },
  ];
}

function defaultStrategies() {
  return [
    {
      id: 'default',
      name: 'Aggressive Options',
      description: 'Multi-timeframe options with scalping overlay',
      rulesFile: 'TRADING_RULES.md',
      customRules: null,
      createdAt: new Date().toISOString(),
    },
    {
      id: 'capital-preservation',
      name: 'Capital Preservation Swing',
      description: 'Conservative discretionary swing trading with capital preservation as the primary objective',
      rulesFile: null,
      customRules: `# Capital Preservation Swing

## Universe & Liquidity
- Liquid large-cap stocks and their options only (tight bid-ask spreads, high open interest)
- Avoid thinly traded names, illiquid strikes, and anything with a spread >5% of mid-price

## Entry Confirmation
- Only take high-conviction setups with a clear, stated thesis and risk/reward of at least 3:1
- Require confluence: trend direction, a defined support/resistance level, and a catalyst or technical trigger
- No entries on unconfirmed rumor, hype, or FOMO

## Position Sizing
- Maximum 5% of portfolio per position
- Maximum 30% of portfolio deployed at any time — hold 70%+ in cash by default
- Maximum 5 open positions at once

## Exits
- Stop loss at -10% from entry, no exceptions
- Take profit at +30%, or sooner on a clear technical breakdown of the thesis
- Move the stop to breakeven once a position is up 15%+

## Time Horizon
- Swing trades only: 30-90 days to expiration for options, delta 0.40-0.60
- No day trades, no scalps, no 0DTE

## No-Trade Conditions
- No new positions in the 2 trading days before a company's earnings release
- No trading during unusually thin, holiday-shortened sessions
- If no setup meets every entry criterion, do nothing that heartbeat

## Hard Risk Discipline
- Stop opening new positions for the day if daily loss reaches the account's configured limit
- Never average down on a losing thesis
- Cash is a position — sitting out is always an acceptable outcome`,
      createdAt: new Date().toISOString(),
    },
    {
      id: 'equity-momentum',
      name: 'Liquid Equity Momentum',
      description: 'Trend-following momentum trading in liquid, large-cap equities and broad ETFs',
      rulesFile: null,
      customRules: `# Liquid Equity Momentum

## Universe & Liquidity
- Large-cap and mega-cap stocks and broad-market ETFs with average daily volume well above 1M shares
- Avoid low-float, low-volume, or hard-to-borrow names

## Entry Confirmation
- Enter only on a confirmed breakout above a well-defined resistance level or multi-day range, on volume meaningfully above the recent average
- Require the broader market/sector to not be working directly against the trade
- Skip extended moves — do not chase a name already far beyond its breakout level

## Position Sizing
- Standard position: up to the account's configured max position size
- Scale in only after the initial entry is confirmed by follow-through price action, never before
- Reduce size in higher-volatility or lower-liquidity names

## Exits
- Initial stop below the breakout level or most recent swing low, whichever is tighter
- Trail the stop as the position moves in your favor; give it room to run but never remove the stop
- Exit fully on a close back below the trailing stop or a clear loss of momentum (volume drying up, failed follow-through)

## Time Horizon
- Multi-day to multi-week swing holds; not a scalp strategy
- Reassess any position that has not shown continuation within 5-10 trading days of entry

## No-Trade Conditions
- No entries against the prevailing broad-market trend
- No entries in the final 30 minutes of a session chasing a late-day spike
- No new entries into a name already extended 2+ standard deviations above its short-term average

## Hard Risk Discipline
- Cut a position immediately if the stop is hit — no hoping, no widening the stop after entry
- Respect the account's configured max open positions and max deployed capital at all times
- Stop opening new positions for the day if the daily loss limit is reached`,
      createdAt: new Date().toISOString(),
    },
    {
      id: 'etf-mean-reversion',
      name: 'Liquid ETF Mean Reversion',
      description: 'Short-horizon mean reversion trading in broad, highly liquid index and sector ETFs',
      rulesFile: null,
      customRules: `# Liquid ETF Mean Reversion

## Universe & Liquidity
- Broad index and major sector ETFs only (deep options chains, tight spreads, heavy daily volume)
- No single-name equities and no thinly-traded sector or thematic ETFs

## Entry Confirmation
- Enter only after a sharp, statistically stretched move away from a short-term average (e.g. well below a 10-20 day moving average or a clear oversold reading) inside an otherwise range-bound or uptrending backdrop
- Require an early reversal signal (a stall in selling pressure, a bullish intraday reversal candle) before entering — never catch a falling price with no confirmation
- Do not fade a strong, news-driven directional trend

## Position Sizing
- Smaller-than-standard size per trade, since this strategy trades more frequently
- Cap aggregate mean-reversion exposure well within the account's configured max deployed capital
- Never add to a position that has moved further against the reversion thesis

## Exits
- Target a return to the recent average (e.g. the 10-20 day moving average or prior consolidation zone); take profit there rather than waiting for a full round-trip
- Stop loss just beyond the recent extreme low/high that defined the setup
- Time-based exit: close the trade if the expected reversion has not begun within a few trading sessions

## Time Horizon
- Short-horizon swings, typically 1-10 trading days
- Not a buy-and-hold approach — flatten and look for the next setup once the reversion plays out

## No-Trade Conditions
- No entries during a confirmed strong trend with no sign of exhaustion
- No entries within a day of a major scheduled macro release (rate decisions, inflation prints) that could invalidate the setup
- If the "stretch" is not statistically unusual relative to recent history, skip the trade

## Hard Risk Discipline
- Exit immediately if price makes a new extreme beyond the stop — do not average into a worsening reversion trade
- Respect the account's configured max position size and max open positions
- Stop trading for the day if the daily loss limit is reached`,
      createdAt: new Date().toISOString(),
    },
    {
      id: 'macro-rotation',
      name: 'Index & Macro Rotation',
      description: 'Slow-moving, regime-driven rotation across a small basket of liquid index and sector ETFs',
      rulesFile: null,
      customRules: `# Index & Macro Rotation

## Universe & Liquidity
- Broad market index ETFs and major sector/style ETFs only (highest liquidity tier)
- No single-name equities, no leveraged or inverse products

## Entry Confirmation
- Rotate into a sector or index only when multiple macro signals agree on the regime (e.g. relative strength trend, breadth, rate direction) — a single signal is not sufficient confirmation
- Prefer adding to a position in the direction of an already-established regime over predicting a turn early

## Position Sizing
- Hold a small number of core positions (typically 2-5) sized to the account's configured max position size
- Rebalance gradually — resize an existing position rather than fully exiting and re-entering on minor regime wobbles

## Exits
- Reduce or exit a position when the macro thesis that justified it is invalidated by the signals that originally confirmed it, not on ordinary daily noise
- Use a wide, regime-level stop (well below normal swing-trade stops) consistent with the multi-week/multi-month holding period

## Time Horizon
- Weeks to months per rotation; this strategy is not for short-term trading
- Reassess the full basket on a regular cadence rather than reacting to every session

## No-Trade Conditions
- No rotation on a single day's price action alone
- No new positions immediately ahead of a major scheduled macro event without an already-established thesis
- If macro signals are mixed or conflicting, hold the existing allocation rather than forcing a rotation

## Hard Risk Discipline
- Never let a single sector/index position grow past the account's configured max position size through drift without rebalancing
- Respect the account's configured max deployed capital across the whole basket
- Stop all new rotations for the day if the daily loss limit is reached`,
      createdAt: new Date().toISOString(),
    },
    {
      id: 'long-horizon-trend',
      name: 'Long-Horizon Trend',
      description: 'Patient, multi-month trend following in liquid large-cap equities and index ETFs',
      rulesFile: null,
      customRules: `# Long-Horizon Trend Following

## Universe & Liquidity
- Liquid large-cap stocks and broad index ETFs with an established, multi-month price history
- Avoid recent IPOs, illiquid names, and anything without a meaningful trading history

## Entry Confirmation
- Enter only when price is in a durable uptrend confirmed across multiple timeframes (e.g. price above a rising long-term moving average, higher highs and higher lows over months)
- Prefer adding on confirmed pullbacks within the trend over chasing new highs

## Position Sizing
- Standard position sized to the account's configured max position size, built gradually rather than all at once
- Diversify across a handful of uncorrelated trends rather than concentrating in one theme

## Exits
- Exit or trim only when the long-term trend itself breaks (e.g. a sustained close below the long-term trend average), not on short-term pullbacks
- Use a wide trailing stop appropriate to a multi-month holding period — this strategy is designed to sit through normal volatility

## Time Horizon
- Multi-month to multi-quarter holds; this is the slowest-moving strategy in the catalog
- Review core positions on a weekly, not daily, cadence

## No-Trade Conditions
- No new entries into a name that has already round-tripped through the same range multiple times without a clean breakout
- No entries against the dominant long-term trend
- If no name in the universe shows a durable trend, hold cash rather than force a position

## Hard Risk Discipline
- Never add to a position after the long-term trend has broken
- Respect the account's configured max open positions and max deployed capital
- Stop opening new positions for the day if the daily loss limit is reached`,
      createdAt: new Date().toISOString(),
    },
    {
      id: 'catalyst-news',
      name: 'Catalyst & News Reaction',
      description: 'Short-hold trading of the confirmed market reaction to specific, already-public news and events',
      rulesFile: null,
      customRules: `# Catalyst & News Reaction

## Universe & Liquidity
- Liquid large-cap stocks and their options with an active, tradable reaction to a specific, verifiable catalyst (earnings release, guidance update, major confirmed headline)
- Confirm the news through a tool call before acting — never trade on an assumed or unverified headline

## Entry Confirmation
- Enter only after the catalyst is public and the market's initial directional reaction is visible in price and volume — never position ahead of a scheduled release on a guess
- Require the move to be liquid enough to enter and exit cleanly (tight spread, active volume in the relevant options or shares)

## Position Sizing
- Smaller-than-standard size given the elevated volatility around catalyst events
- One position per catalyst; do not stack multiple catalyst trades in the same name

## Exits
- Take profit as the initial reaction move matures — this strategy captures the reaction, not any multi-week trend that may follow
- Hard stop if price reverses back through the pre-reaction level, invalidating the catalyst thesis
- Exit by end of the session if the reaction has not developed as expected

## Time Horizon
- Intraday to a few trading days per position — this is a short-hold, event-driven strategy
- Do not convert a failed catalyst trade into a longer-term hold to avoid taking the loss

## No-Trade Conditions
- No pre-positioning ahead of a scheduled earnings release or known event based on speculation
- No trading a headline that has not been confirmed through a tool call
- No trading illiquid options chains just because the underlying had news

## Hard Risk Discipline
- Exit immediately if the reaction thesis is invalidated — do not wait for it to "come back"
- Respect the account's configured max position size and max open positions
- Stop opening new catalyst positions for the day if the daily loss limit is reached`,
      createdAt: new Date().toISOString(),
    },
    {
      id: 'long-premium-volatility',
      name: 'Long-Premium Volatility',
      description: 'Directional and volatility-expansion trading using outright long options only',
      rulesFile: null,
      customRules: `# Long-Premium Volatility

## Universe & Liquidity
- Liquid, large-cap optionable names and index/ETF options with tight spreads and meaningful open interest
- Avoid illiquid strikes and wide-spread chains where the premium paid is eaten by slippage

## Entry Confirmation
- Enter only on a specific, statable thesis for an expected move — a directional view, an anticipated volatility expansion, or a confirmed catalyst ahead
- Only long calls or long puts, bought to open — never a written, sold-to-open, or multi-leg spread position
- Prefer strikes and expirations liquid enough to exit at any time without forcing a fill

## Position Sizing
- Size each position so the full premium paid — the maximum possible loss on the trade — stays within the account's configured max position size
- Because every long option can go to zero, never treat one long-premium trade as a substitute for a diversified allocation

## Exits
- Take profit systematically as the position captures its intended move — long premium erodes with time, so do not hold for a "better" exit once the thesis has played out
- Exit or roll before the position enters the steep theta-decay window into expiration if the thesis has not yet resolved
- Cut the position if the thesis is invalidated, even before a stop-loss percentage is hit — a broken thesis on a decaying asset only gets more expensive to hold

## Time Horizon
- Days to a few weeks per position, sized to the expected timeline of the move being traded
- Not a buy-and-forget strategy — decay makes inactivity itself a risk

## No-Trade Conditions
- No selling options to open (no covered calls, no cash-secured puts, no naked or spread positions of any kind)
- No buying options so close to expiration that theta decay dominates the trade before the thesis can play out, unless the strategy explicitly calls for a short-dated catalyst trade
- If the implied volatility already prices in the expected move at a level that makes the risk/reward unfavorable, skip the trade

## Hard Risk Discipline
- Never let total premium at risk across open long-option positions exceed the account's configured max deployed capital
- Respect the account's configured max open positions
- Stop opening new positions for the day if the daily loss limit is reached`,
      createdAt: new Date().toISOString(),
    },
  ];
}

function defaultModels() {
  try {
    const out = execSync('opencode models 2>&1', { encoding: 'utf-8', timeout: 10000 });
    const lines = out.trim().split('\n').filter(l => l && l.includes('/'));
    const models = [];
    const seen = new Set();
    
    for (const line of lines) {
      const id = line.trim();
      if (!id || seen.has(id)) continue;
      seen.add(id);
      
      let name = id;
      let description = '';
      
      if (id.startsWith('anthropic/')) {
        const model = id.replace('anthropic/', '');
        if (model.includes('opus')) {
          name = `Claude Opus ${model.replace(/[^\d.]/g, '')}`;
          description = 'Anthropic Opus model';
        } else if (model.includes('sonnet')) {
          name = `Claude Sonnet ${model.replace(/[^\d.]/g, '')}`;
          description = 'Anthropic Sonnet model';
        } else if (model.includes('haiku')) {
          name = `Claude Haiku ${model.replace(/[^\d.]/g, '')}`;
          description = 'Anthropic Haiku model';
        }
      } else if (id.startsWith('openai/')) {
        name = id.replace('openai/', '').replace(/-/g, ' ').replace(/\b\w/g, c => c.toUpperCase());
        description = 'OpenAI model';
      } else if (id.startsWith('google/')) {
        name = 'Gemini ' + id.replace('google/', '').replace(/-/g, ' ');
        description = 'Google model';
      } else if (id.startsWith('openrouter/')) {
        name = id.replace('openrouter/', '').replace(/:/g, ' ').replace(/-/g, ' ');
        description = 'OpenRouter model';
      } else if (id.startsWith('opencode/')) {
        name = id.replace('opencode/', '').replace(/-/g, ' ').replace(/\b\w/g, c => c.toUpperCase());
        description = 'OpenCode provider model';
      } else {
        name = id.split('/').pop().replace(/-/g, ' ').replace(/\b\w/g, c => c.toUpperCase());
        description = 'Available model';
      }
      
      models.push({ id, name, description });
    }
    
    if (models.length > 0) {
      console.log(`[config-store] Loaded ${models.length} models from opencode`);
      return models;
    }
  } catch (err) {
    console.log('[config-store] Could not load models from opencode:', err.message);
  }

  return null; // signal "discovery failed" so the registry can fall back to last-good / default
}

// Live, TTL-cached model registry. The catalog AUTO-UPDATES from `opencode models` rather than
// being a frozen snapshot baked into config, so newly-available models appear without a code
// change. On discovery failure it returns the last good list, then a minimal honest default.
let _modelCache = { models: null, fetchedAt: 0, ttl: 0 };
const MODEL_CACHE_TTL_MS = 10 * 60 * 1000; // refresh a good list at most every 10 min
const MODEL_FAIL_TTL_MS = 30 * 1000;       // after a failure, retry sooner but don't hammer opencode
const FALLBACK_MODELS = [
  { id: DEFAULT_AGENT_MODEL, name: 'Default model', description: 'Model list unavailable — check `opencode auth`. Any valid provider/model id can still be entered.' },
];

export function getAvailableModels({ force = false } = {}) {
  const now = Date.now();
  if (!force && _modelCache.models && (now - _modelCache.fetchedAt) < _modelCache.ttl) {
    return _modelCache.models;
  }
  const live = defaultModels(); // live `opencode models`, or null on failure
  if (live && live.length > 0) {
    _modelCache = { models: live, fetchedAt: now, ttl: MODEL_CACHE_TTL_MS };
    return live;
  }
  // Discovery failed: keep serving the last good list (or the minimal default), and retry soon.
  const fallback = _modelCache.models || FALLBACK_MODELS;
  _modelCache = { models: fallback, fetchedAt: now, ttl: MODEL_FAIL_TTL_MS };
  return fallback;
}

function createSandbox(account, overrides = {}) {
  const sandboxId = overrides.id || `sbx_${account.id}`;
  return {
    id: sandboxId,
    accountId: account.id,
    name: overrides.name || account.name || `Sandbox ${account.id}`,
    agent: {
      activeAgentId: overrides.agent?.activeAgentId || overrides.activeAgentId || 'default',
      model: overrides.agent?.model || overrides.activeModel || DEFAULT_AGENT_MODEL,
      overrides: {
        ...DEFAULT_AGENT_OVERRIDES,
        ...(overrides.agent?.overrides || {}),
      },
    },
    heartbeat: { ...DEFAULT_HEARTBEAT, ...(overrides.heartbeat || {}) },
    permissions: { ...DEFAULT_PERMISSIONS, ...(overrides.permissions || {}) },
    plugins: mergePlugins(overrides.plugins || {}),
    createdAt: overrides.createdAt || new Date().toISOString(),
    updatedAt: new Date().toISOString(),
  };
}

function createDefaultConfig() {
  return {
    schemaVersion: 2,
    activeAccountId: null,
    activeSandboxId: null,

    // Legacy compatibility aliases. Keep mirrored during migration.
    activeAgentId: 'default',
    activeModel: DEFAULT_AGENT_MODEL,
    heartbeat: { ...DEFAULT_HEARTBEAT },
    permissions: { ...DEFAULT_PERMISSIONS },
    plugins: mergePlugins(),

    accounts: [],
    sandboxes: {},
    agents: defaultAgents(),
    strategies: defaultStrategies(),
    manager: {
      model: DEFAULT_AGENT_MODEL,
      customPrompt: '',
    },
    models: getAvailableModels(),
  };
}

function mergePlugins(plugins = {}) {
  return {
    ...DEFAULT_PLUGINS,
    ...plugins,
    slack: {
      ...DEFAULT_PLUGINS.slack,
      ...(plugins.slack || {}),
      notifyOn: {
        ...DEFAULT_PLUGINS.slack.notifyOn,
        ...(plugins.slack?.notifyOn || {}),
      },
    },
  };
}

// Appends built-in catalog entries missing by id; never touches an id already present,
// so a user's edits to a built-in survive an upgrade that adds more built-ins.
function mergeBuiltinCatalog(existing, builtins) {
  const list = Array.isArray(existing) ? existing : [];
  const existingIds = new Set(list.map(item => item.id));
  const missing = builtins.filter(item => !existingIds.has(item.id));
  return missing.length > 0 ? [...list, ...missing] : list;
}

function mergeSandbox(sandbox, fallback = {}) {
  return {
    ...sandbox,
    agent: {
      activeAgentId: sandbox?.agent?.activeAgentId || fallback.activeAgentId || 'default',
      model: sandbox?.agent?.model || fallback.activeModel || DEFAULT_AGENT_MODEL,
      overrides: {
        ...DEFAULT_AGENT_OVERRIDES,
        ...(sandbox?.agent?.overrides || {}),
        heartbeatOverrides: {
          ...DEFAULT_AGENT_OVERRIDES.heartbeatOverrides,
          ...(sandbox?.agent?.overrides?.heartbeatOverrides || {}),
        },
      },
    },
    heartbeat: { ...DEFAULT_HEARTBEAT, ...(fallback.heartbeat || {}), ...(sandbox?.heartbeat || {}) },
    permissions: { ...DEFAULT_PERMISSIONS, ...(fallback.permissions || {}), ...(sandbox?.permissions || {}) },
    plugins: mergePlugins({ ...(fallback.plugins || {}), ...(sandbox?.plugins || {}) }),
  };
}

function normalizeConfig(raw = {}) {
  const defaults = createDefaultConfig();
  const config = {
    ...defaults,
    ...raw,
    heartbeat: { ...DEFAULT_HEARTBEAT, ...(raw.heartbeat || {}) },
    permissions: { ...DEFAULT_PERMISSIONS, ...(raw.permissions || {}) },
    plugins: mergePlugins(raw.plugins || {}),
    accounts: raw.accounts || [],
    sandboxes: raw.sandboxes || {},
    agents: mergeBuiltinCatalog(raw.agents, defaults.agents),
    strategies: mergeBuiltinCatalog(raw.strategies, defaults.strategies),
    models: raw.models || defaults.models,
  };

  for (const [sandboxId, sandbox] of Object.entries(config.sandboxes)) {
    config.sandboxes[sandboxId] = mergeSandbox({ id: sandboxId, ...sandbox }, config);
  }

  return migrateLegacyConfig(config);
}

function migrateLegacyConfig(config) {
  config.schemaVersion = 2;
  if (!config.sandboxes) config.sandboxes = {};

  for (const account of config.accounts || []) {
    const sandboxId = `sbx_${account.id}`;
    if (!config.sandboxes[sandboxId]) {
      config.sandboxes[sandboxId] = createSandbox(account, {
        id: sandboxId,
        name: account.name,
        activeAgentId: config.activeAgentId,
        activeModel: config.activeModel,
        heartbeat: config.heartbeat,
        permissions: config.permissions,
        plugins: config.plugins,
      });
    } else {
      config.sandboxes[sandboxId] = mergeSandbox({
        ...config.sandboxes[sandboxId],
        id: sandboxId,
        accountId: account.id,
        name: config.sandboxes[sandboxId].name || account.name,
      }, config);
    }
  }

  if (!config.activeAccountId) {
    config.activeAccountId = config.accounts[0]?.id || null;
  }
  if (!config.activeSandboxId && config.activeAccountId) {
    config.activeSandboxId = `sbx_${config.activeAccountId}`;
  }

  syncLegacyAliases(config);
  return config;
}

function syncLegacyAliases(config) {
  const sandbox = getActiveSandboxFromConfig(config);
  if (!sandbox) return;
  config.activeAccountId = sandbox.accountId;
  config.activeSandboxId = sandbox.id;
  config.activeAgentId = sandbox.agent.activeAgentId;
  config.activeModel = sandbox.agent.model;
  config.heartbeat = { ...sandbox.heartbeat };
  config.permissions = { ...sandbox.permissions };
  config.plugins = mergePlugins(sandbox.plugins || {});
}

function getActiveSandboxFromConfig(config) {
  if (!config) return null;
  if (config.activeSandboxId && config.sandboxes?.[config.activeSandboxId]) {
    return config.sandboxes[config.activeSandboxId];
  }
  if (config.activeAccountId) {
    return Object.values(config.sandboxes || {}).find(s => s.accountId === config.activeAccountId) || null;
  }
  return Object.values(config.sandboxes || {})[0] || null;
}

let _config = null;
let _writeLock = Promise.resolve();

export async function loadConfig() {
  try {
    const raw = await fs.readFile(CONFIG_PATH, 'utf-8');
    _config = normalizeConfig(JSON.parse(raw));
  } catch (err) {
    if (err.code !== 'ENOENT') console.error('Warning: Failed to parse config file:', err.message);
    _config = createDefaultConfig();
  }

  if (_config.accounts.length === 0) {
    const pk = process.env.ALPACA_PUBLIC_KEY || process.env.ALPACA_API_KEY;
    const sk = process.env.ALPACA_SECRET_KEY;
    if (pk && sk) {
      const baseUrl = process.env.ALPACA_BASE_URL || process.env.ALPACA_ENDPOINT || '';
      const isPaper = baseUrl.includes('paper') || process.env.ALPACA_PAPER === 'true';
      const id = crypto.randomUUID().slice(0, 8);
      const account = {
        id,
        name: isPaper ? 'Paper (from .env)' : 'Live (from .env)',
        publicKey: pk,
        secretKey: sk,
        baseUrl: alpacaTradingUrl(isPaper, baseUrl),
        paper: isPaper,
        createdAt: new Date().toISOString(),
      };
      _config.accounts.push(account);
      _config.sandboxes[`sbx_${id}`] = createSandbox(account);
      _config.activeAccountId = id;
      _config.activeSandboxId = `sbx_${id}`;
      console.log(`  Auto-imported Alpaca account from .env (${isPaper ? 'paper' : 'live'})`);
    }
  }

  syncLegacyAliases(_config);
  await saveConfig();
  return _config;
}

export async function saveConfig() {
  _writeLock = _writeLock.then(async () => {
    syncLegacyAliases(_config);
    await fs.mkdir(path.dirname(CONFIG_PATH), { recursive: true });
    await fs.writeFile(CONFIG_PATH, JSON.stringify(_config, null, 2));
  }).catch(err => console.error('Config save error:', err.message));
  return _writeLock;
}

export function getConfig() {
  if (!_config) throw new Error('Config not loaded. Call loadConfig() first.');
  return _config;
}

export function getSandboxes() {
  return Object.values(getConfig().sandboxes || {});
}

export function getSandbox(id) {
  return getConfig().sandboxes?.[id] || null;
}

export function getSandboxByAccountId(accountId) {
  return getSandboxes().find(s => s.accountId === accountId) || null;
}

export function getActiveSandbox() {
  return getActiveSandboxFromConfig(getConfig());
}

export async function setActiveSandbox(id) {
  const sandbox = getSandbox(id);
  if (!sandbox) throw new Error('Sandbox not found');
  _config.activeSandboxId = id;
  _config.activeAccountId = sandbox.accountId;
  syncLegacyAliases(_config);
  await saveConfig();
  return sandbox;
}

function updateSandbox(accountId, updater) {
  const sandbox = getSandboxByAccountId(accountId) || getActiveSandbox();
  if (!sandbox) throw new Error('Sandbox not found');
  const updated = updater({ ...sandbox });
  updated.updatedAt = new Date().toISOString();
  _config.sandboxes[sandbox.id] = mergeSandbox(updated, _config);
  if (_config.activeSandboxId === sandbox.id) syncLegacyAliases(_config);
  return _config.sandboxes[sandbox.id];
}

// ── Accounts ───────────────────────────────────────────────────────

export async function addAccount({ name, publicKey, secretKey, baseUrl, paper }) {
  const id = crypto.randomUUID().slice(0, 8);
  const account = {
    id,
    name: name || `Account ${_config.accounts.length + 1}`,
    publicKey,
    secretKey,
    baseUrl: alpacaTradingUrl(paper, baseUrl),
    paper: paper !== false,
    createdAt: new Date().toISOString(),
  };
  _config.accounts.push(account);
  _config.sandboxes[`sbx_${id}`] = createSandbox(account, {
    activeAgentId: _config.activeAgentId,
    activeModel: _config.activeModel,
    heartbeat: _config.heartbeat,
    permissions: _config.permissions,
    plugins: _config.plugins,
  });
  if (!_config.activeAccountId) {
    _config.activeAccountId = id;
    _config.activeSandboxId = `sbx_${id}`;
  }
  syncLegacyAliases(_config);
  await saveConfig();
  return account;
}

export async function removeAccount(id) {
  _config.accounts = _config.accounts.filter(a => a.id !== id);
  delete _config.sandboxes[`sbx_${id}`];
  if (_config.activeAccountId === id) {
    const next = _config.accounts[0]?.id || null;
    _config.activeAccountId = next;
    _config.activeSandboxId = next ? `sbx_${next}` : null;
  }
  syncLegacyAliases(_config);
  await saveConfig();
}

export async function setActiveAccount(id) {
  if (!_config.accounts.find(a => a.id === id)) throw new Error('Account not found');
  _config.activeAccountId = id;
  _config.activeSandboxId = `sbx_${id}`;
  syncLegacyAliases(_config);
  await saveConfig();
}

export function getActiveAccount() {
  return _config.accounts.find(a => a.id === _config.activeAccountId) || null;
}

export function getAccountById(id) {
  return _config.accounts.find(a => a.id === id) || null;
}

// ── Agents ─────────────────────────────────────────────────────────

export async function addAgent(agent) {
  const id = crypto.randomUUID().slice(0, 8);
  const newAgent = {
    id,
    name: agent.name || 'New Agent',
    description: agent.description || '',
    systemPromptTemplate: agent.systemPromptTemplate || 'custom',
    customSystemPrompt: agent.customSystemPrompt || '',
    strategyId: agent.strategyId || null,
    model: agent.model || _config.activeModel,
    heartbeatOverrides: agent.heartbeatOverrides || {},
    createdAt: new Date().toISOString(),
  };
  _config.agents.push(newAgent);
  await saveConfig();
  return newAgent;
}

export async function updateAgent(id, updates) {
  const idx = _config.agents.findIndex(a => a.id === id);
  if (idx === -1) throw new Error('Agent not found');
  const oldAgent = _config.agents[idx];
  _config.agents[idx] = { ...oldAgent, ...updates, updatedAt: new Date().toISOString() };

  // Propagate model/strategy changes to all sandboxes using this agent
  const modelChanged = updates.model && updates.model !== oldAgent.model;
  const strategyChanged = updates.strategyId !== undefined && updates.strategyId !== oldAgent.strategyId;

  if (modelChanged || strategyChanged) {
    for (const sandbox of getSandboxes()) {
      if (sandbox.agent.activeAgentId !== id) continue;
      if (modelChanged) {
        _config.sandboxes[sandbox.id].agent.model = updates.model;
      }
      if (strategyChanged) {
        if (_config.sandboxes[sandbox.id].agent.overrides) {
          _config.sandboxes[sandbox.id].agent.overrides.customStrategyRules = null;
        }
      }
    }
    syncLegacyAliases(_config);
  }

  await saveConfig();
  return _config.agents[idx];
}

export async function removeAgent(id) {
  if (id === 'default') throw new Error('Cannot remove default agent');
  _config.agents = _config.agents.filter(a => a.id !== id);
  for (const sandbox of getSandboxes()) {
    if (sandbox.agent.activeAgentId === id) {
      _config.sandboxes[sandbox.id].agent.activeAgentId = 'default';
    }
  }
  syncLegacyAliases(_config);
  await saveConfig();
}

export async function setActiveAgent(id) {
  if (!_config.agents.find(a => a.id === id)) throw new Error('Agent not found');
  await updateSandboxAgentSelection(_config.activeSandboxId, { activeAgentId: id });
}

export function getActiveAgent() {
  return getResolvedAgentForSandbox(_config.activeSandboxId) || _config.agents[0];
}

export function getAgentById(id) {
  return _config.agents.find(a => a.id === id) || null;
}

export function getAgentForSandbox(sandboxId) {
  const sandbox = getSandbox(sandboxId);
  if (!sandbox) return null;
  return getAgentById(sandbox.agent.activeAgentId) || null;
}

export function getResolvedAgentForSandbox(sandboxId) {
  const sandbox = getSandbox(sandboxId);
  if (!sandbox) return null;

  const baseAgent = getAgentById(sandbox.agent.activeAgentId) || null;
  const overrides = sandbox.agent?.overrides || {};
  const resolved = {
    ...(baseAgent || {}),
    id: sandbox.agent.activeAgentId,
    model: sandbox.agent?.model || baseAgent?.model || _config.activeModel,
    heartbeatOverrides: {
      ...(baseAgent?.heartbeatOverrides || {}),
      ...(overrides.heartbeatOverrides || {}),
    },
    sandboxId,
    accountId: sandbox.accountId,
    customStrategyRules: overrides.customStrategyRules ?? null,
  };

  if (overrides.name !== null) resolved.name = overrides.name;
  if (overrides.description !== null) resolved.description = overrides.description;
  if (overrides.systemPromptTemplate !== null) resolved.systemPromptTemplate = overrides.systemPromptTemplate;
  if (overrides.customSystemPrompt !== null) resolved.customSystemPrompt = overrides.customSystemPrompt;
  if (Object.prototype.hasOwnProperty.call(overrides, 'strategyId') && overrides.strategyId !== undefined) {
    resolved.strategyId = overrides.strategyId;
  }

  return resolved;
}

export async function updateSandboxAgentOverrides(sandboxId, overrides) {
  const sandbox = getSandbox(sandboxId);
  if (!sandbox) throw new Error('Sandbox not found');

  _config.sandboxes[sandboxId] = mergeSandbox({
    ...sandbox,
    agent: {
      ...sandbox.agent,
      overrides: {
        ...(sandbox.agent?.overrides || {}),
        ...overrides,
        heartbeatOverrides: {
          ...(sandbox.agent?.overrides?.heartbeatOverrides || {}),
          ...(overrides.heartbeatOverrides || {}),
        },
      },
    },
    updatedAt: new Date().toISOString(),
  }, _config);

  if (_config.activeSandboxId === sandboxId) syncLegacyAliases(_config);
  await saveConfig();
  return _config.sandboxes[sandboxId];
}

export async function updateSandboxAgentSelection(sandboxId, updates) {
  const sandbox = getSandbox(sandboxId);
  if (!sandbox) throw new Error('Sandbox not found');
  const nextActiveAgentId = updates.activeAgentId ?? sandbox.agent.activeAgentId;
  const newAgent = _config.agents.find(a => a.id === nextActiveAgentId);
  if (!newAgent) throw new Error('Agent not found');

  const agentChanged = nextActiveAgentId !== sandbox.agent.activeAgentId;
  let mergedOverrides = {
    ...(sandbox.agent?.overrides || {}),
    ...(updates.overrides || {}),
    heartbeatOverrides: {
      ...(sandbox.agent?.overrides?.heartbeatOverrides || {}),
      ...(updates.overrides?.heartbeatOverrides || {}),
    },
  };
  if (agentChanged) {
    mergedOverrides.customStrategyRules = null;
    mergedOverrides.customSystemPrompt = null;
    mergedOverrides.systemPromptTemplate = null;
  }

  _config.sandboxes[sandboxId] = mergeSandbox({
    ...sandbox,
    agent: {
      ...sandbox.agent,
      ...updates,
      activeAgentId: nextActiveAgentId,
      model: agentChanged ? (newAgent.model || sandbox.agent.model) : (updates.model || sandbox.agent.model),
      overrides: mergedOverrides,
    },
    updatedAt: new Date().toISOString(),
  }, _config);

  if (_config.activeSandboxId === sandboxId) syncLegacyAliases(_config);
  await saveConfig();
  return _config.sandboxes[sandboxId];
}

export async function updateSandboxStrategyRules(sandboxId, rules) {
  return updateSandboxAgentOverrides(sandboxId, { customStrategyRules: rules });
}

// ── Strategies ─────────────────────────────────────────────────────

export async function addStrategy(strategy) {
  const id = crypto.randomUUID().slice(0, 8);
  const newStrategy = {
    id,
    name: strategy.name || 'New Strategy',
    description: strategy.description || '',
    rulesFile: null,
    customRules: strategy.customRules || '',
    createdAt: new Date().toISOString(),
  };
  _config.strategies.push(newStrategy);
  await saveConfig();
  return newStrategy;
}

export async function updateStrategy(id, updates) {
  const idx = _config.strategies.findIndex(s => s.id === id);
  if (idx === -1) throw new Error('Strategy not found');
  _config.strategies[idx] = { ..._config.strategies[idx], ...updates, updatedAt: new Date().toISOString() };
  await saveConfig();
  return _config.strategies[idx];
}

export async function removeStrategy(id) {
  if (id === 'default') throw new Error('Cannot remove default strategy');
  _config.strategies = _config.strategies.filter(s => s.id !== id);
  await saveConfig();
}

export function getStrategyById(id) {
  return _config.strategies.find(s => s.id === id) || null;
}

// ── Model ──────────────────────────────────────────────────────────

export async function setActiveModel(modelId) {
  await updateSandboxAgentSelection(_config.activeSandboxId, { model: modelId });
  _config.activeModel = modelId;
}

// ── Heartbeat ──────────────────────────────────────────────────────

export async function updateHeartbeat(phaseIntervals) {
  updateSandbox(_config.activeAccountId, sandbox => ({
    ...sandbox,
    heartbeat: { ...sandbox.heartbeat, ...phaseIntervals },
  }));
  await saveConfig();
}

export async function updateHeartbeatForSandbox(sandboxId, phaseIntervals) {
  const sandbox = getSandbox(sandboxId);
  if (!sandbox) throw new Error('Sandbox not found');
  _config.sandboxes[sandboxId] = mergeSandbox({
    ...sandbox,
    heartbeat: { ...sandbox.heartbeat, ...phaseIntervals },
    updatedAt: new Date().toISOString(),
  }, _config);
  if (_config.activeSandboxId === sandboxId) syncLegacyAliases(_config);
  await saveConfig();
}

export function getHeartbeatForPhase(phase) {
  const sandbox = getActiveSandbox();
  return sandbox?.heartbeat?.[phase] || _config.heartbeat?.[phase] || DEFAULT_HEARTBEAT[phase] || 600;
}

export function getHeartbeatForSandboxPhase(sandboxId, phase) {
  const sandbox = getSandbox(sandboxId);
  return sandbox?.heartbeat?.[phase] || DEFAULT_HEARTBEAT[phase] || 600;
}

export function getHeartbeatProfiles() {
  return HEARTBEAT_PROFILES;
}

export function getPhaseTimeRanges() {
  return PHASE_TIME_RANGES;
}

export async function applyHeartbeatProfile(sandboxId, profileKey) {
  const profile = HEARTBEAT_PROFILES[profileKey];
  if (!profile) throw new Error(`Unknown heartbeat profile: ${profileKey}`);
  await updateHeartbeatForSandbox(sandboxId, profile.phases);
}

export async function updatePhaseTimeRange(phase, range) {
  if (!PHASE_TIME_RANGES[phase]) throw new Error(`Unknown phase: ${phase}`);
  if (range.start !== undefined) PHASE_TIME_RANGES[phase].start = range.start;
  if (range.end !== undefined) PHASE_TIME_RANGES[phase].end = range.end;
}

// ── Permissions ───────────────────────────────────────────────────

export async function updatePermissions(perms) {
  updateSandbox(_config.activeAccountId, sandbox => ({
    ...sandbox,
    permissions: { ...sandbox.permissions, ...perms },
  }));
  await saveConfig();
}

export async function updatePermissionsForSandbox(sandboxId, perms) {
  const sandbox = getSandbox(sandboxId);
  if (!sandbox) throw new Error('Sandbox not found');
  _config.sandboxes[sandboxId] = mergeSandbox({
    ...sandbox,
    permissions: { ...sandbox.permissions, ...perms },
    updatedAt: new Date().toISOString(),
  }, _config);
  if (_config.activeSandboxId === sandboxId) syncLegacyAliases(_config);
  await saveConfig();
}

export function getPermissions() {
  const sandbox = getActiveSandbox();
  return sandbox?.permissions || _config.permissions || DEFAULT_PERMISSIONS;
}

export function getPermissionsForSandbox(sandboxId) {
  return getSandbox(sandboxId)?.permissions || DEFAULT_PERMISSIONS;
}

// ── Plugins ────────────────────────────────────────────────────────

const MASK_SENTINEL = /^\*{4}/;
function dropMaskedFields(obj) {
  if (!obj || typeof obj !== 'object') return obj;
  const out = {};
  for (const [k, v] of Object.entries(obj)) {
    if (typeof v === 'string' && MASK_SENTINEL.test(v)) continue;
    out[k] = v;
  }
  return out;
}

export async function updatePlugin(pluginName, pluginConfig) {
  updateSandbox(_config.activeAccountId, sandbox => ({
    ...sandbox,
    plugins: {
      ...(sandbox.plugins || {}),
      [pluginName]: { ...((sandbox.plugins || {})[pluginName] || {}), ...dropMaskedFields(pluginConfig) },
    },
  }));
  await saveConfig();
}

export async function updatePluginForSandbox(sandboxId, pluginName, pluginConfig) {
  const sandbox = getSandbox(sandboxId);
  if (!sandbox) throw new Error('Sandbox not found');
  _config.sandboxes[sandboxId] = mergeSandbox({
    ...sandbox,
    plugins: {
      ...(sandbox.plugins || {}),
      [pluginName]: {
        ...((sandbox.plugins || {})[pluginName] || {}),
        ...dropMaskedFields(pluginConfig),
      },
    },
    updatedAt: new Date().toISOString(),
  }, _config);
  if (_config.activeSandboxId === sandboxId) syncLegacyAliases(_config);
  await saveConfig();
}

export function getPlugin(pluginName) {
  const sandbox = getActiveSandbox();
  return sandbox?.plugins?.[pluginName] || _config.plugins?.[pluginName] || null;
}

export function getPluginForSandbox(sandboxId, pluginName) {
  return getSandbox(sandboxId)?.plugins?.[pluginName] || null;
}
