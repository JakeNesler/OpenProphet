// Persistent configuration store for accounts, sandboxes, agents, strategies, and prompts
// Uses a JSON file for simplicity - no extra DB dependencies
import fs from 'fs/promises';
import path from 'path';
import { fileURLToPath } from 'url';
import crypto from 'crypto';
import { execSync } from 'child_process';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const CONFIG_PATH = path.join(__dirname, '..', 'data', 'agent-config.json');

const DEFAULT_HEARTBEAT = {
  pre_market: 900,
  market_open: 120,
  midday: 600,
  market_close: 120,
  after_hours: 1800,
  closed: 14400,
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
  penny_stock: {
    label: 'Penny Stock',
    description: 'Active intraday monitoring for fast-moving penny stocks — closes all day-trades by market close',
    phases: { pre_market: 180, market_open: 60, midday: 90, market_close: 60, after_hours: 1800, closed: 3600 },
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
      model: 'anthropic/claude-sonnet-4-6',
      heartbeatOverrides: {},
      customSystemPrompt: '',
      createdAt: new Date().toISOString(),
    },
    {
      id: 'conservative',
      name: 'Guardian',
      description: 'Conservative swing trader focused on capital preservation',
      systemPromptTemplate: 'custom',
      customSystemPrompt: `You are Guardian, a conservative AI trading agent. You prioritize capital preservation above all else.

## Rules
- Only take high-conviction setups with clear risk/reward > 3:1
- Maximum 5% of portfolio per position
- Maximum 30% deployed at any time (70%+ cash always)
- Only swing trades: 30-90 DTE, delta 0.40-0.60
- No scalping, no 0DTE, no earnings plays
- Stop loss at -10%, take profit at +30%
- Maximum 5 positions at once`,
      strategyId: null,
      model: 'anthropic/claude-sonnet-4-6',
      heartbeatOverrides: {
        pre_market: 1800,
        market_open: 300,
        midday: 900,
        market_close: 300,
        after_hours: 3600,
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
    console.log('[config-store] Could not load models from opencode, using defaults:', err.message);
  }
  
  return [
    { id: 'anthropic/claude-sonnet-4-6', name: 'Claude Sonnet 4.6', description: 'Best speed + intelligence, $3/$15 per MTok' },
    { id: 'anthropic/claude-opus-4-6', name: 'Claude Opus 4.6', description: 'Most intelligent, best for agents, $5/$25 per MTok' },
    { id: 'anthropic/claude-haiku-4-5', name: 'Claude Haiku 4.5', description: 'Fastest, near-frontier, $1/$5 per MTok' },
    { id: 'anthropic/claude-sonnet-4-5', name: 'Claude Sonnet 4.5 (Legacy)', description: 'Previous gen Sonnet, $3/$15 per MTok' },
    { id: 'anthropic/claude-opus-4-5', name: 'Claude Opus 4.5 (Legacy)', description: 'Previous gen Opus, $5/$25 per MTok' },
    { id: 'anthropic/claude-sonnet-4-0', name: 'Claude Sonnet 4 (Legacy)', description: 'Original Sonnet 4, $3/$15 per MTok' },
    { id: 'anthropic/claude-opus-4-0', name: 'Claude Opus 4 (Legacy)', description: 'Original Opus 4, $15/$75 per MTok' },
  ];
}

function createSandbox(account, overrides = {}) {
  const sandboxId = overrides.id || `sbx_${account.id}`;
  return {
    id: sandboxId,
    accountId: account.id,
    name: overrides.name || account.name || `Sandbox ${account.id}`,
    agent: {
      activeAgentId: overrides.agent?.activeAgentId || overrides.activeAgentId || 'default',
      model: overrides.agent?.model || overrides.activeModel || 'anthropic/claude-sonnet-4-6',
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
    schemaVersion: 3,
    activeAccountId: null,
    activeSandboxId: null,

    // Legacy compatibility aliases. Keep mirrored during migration.
    activeAgentId: 'default',
    activeModel: 'anthropic/claude-sonnet-4-6',
    heartbeat: { ...DEFAULT_HEARTBEAT },
    permissions: { ...DEFAULT_PERMISSIONS },
    plugins: mergePlugins(),

    accounts: [],
    sandboxes: {},
    agents: defaultAgents(),
    strategies: defaultStrategies(),
    manager: {
      model: 'anthropic/claude-sonnet-4-6',
      customPrompt: '',
    },
    models: defaultModels(),
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

function mergeSandbox(sandbox, fallback = {}) {
  return {
    ...sandbox,
    agent: {
      activeAgentId: sandbox?.agent?.activeAgentId || fallback.activeAgentId || 'default',
      model: sandbox?.agent?.model || fallback.activeModel || 'anthropic/claude-sonnet-4-6',
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
  const rawSchemaVersion = Number(raw.schemaVersion) || 0;
  const defaults = createDefaultConfig();
  const config = {
    ...defaults,
    ...raw,
    heartbeat: { ...DEFAULT_HEARTBEAT, ...(raw.heartbeat || {}) },
    permissions: { ...DEFAULT_PERMISSIONS, ...(raw.permissions || {}) },
    plugins: mergePlugins(raw.plugins || {}),
    accounts: raw.accounts || [],
    sandboxes: raw.sandboxes || {},
    agents: raw.agents || defaults.agents,
    strategies: raw.strategies || defaults.strategies,
    models: raw.models || defaults.models,
  };

  for (const [sandboxId, sandbox] of Object.entries(config.sandboxes)) {
    config.sandboxes[sandboxId] = mergeSandbox({ id: sandboxId, ...sandbox }, config);
  }

  return migrateLegacyConfig(config, rawSchemaVersion);
}

function migrateLegacyConfig(config, rawSchemaVersion = 0) {
  // v2 → v3: bump default `closed` heartbeat from 3600s to 14400s.
  // Only matches the OLD default exactly so user customizations (e.g. 7200, 10800) are preserved.
  if (rawSchemaVersion < 3) {
    if (config.heartbeat?.closed === 3600) config.heartbeat.closed = 14400;
    for (const sandbox of Object.values(config.sandboxes || {})) {
      if (sandbox?.heartbeat?.closed === 3600) sandbox.heartbeat.closed = 14400;
    }
  }
  config.schemaVersion = 3;
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

  const envPk = process.env.ALPACA_PUBLIC_KEY || process.env.ALPACA_API_KEY;
  const envSk = process.env.ALPACA_SECRET_KEY;
  const envBaseUrl = process.env.ALPACA_BASE_URL || process.env.ALPACA_ENDPOINT || '';

  if (_config.accounts.length === 0) {
    if (envPk && envSk) {
      const isPaper = envBaseUrl.includes('paper') || process.env.ALPACA_PAPER === 'true';
      const id = crypto.randomUUID().slice(0, 8);
      const account = {
        id,
        name: isPaper ? 'Paper (from .env)' : 'Live (from .env)',
        publicKey: envPk,
        secretKey: envSk,
        baseUrl: envBaseUrl || (isPaper ? 'https://paper-api.alpaca.markets' : 'https://api.alpaca.markets'),
        paper: isPaper,
        createdAt: new Date().toISOString(),
      };
      _config.accounts.push(account);
      _config.sandboxes[`sbx_${id}`] = createSandbox(account);
      _config.activeAccountId = id;
      _config.activeSandboxId = `sbx_${id}`;
      console.log(`  Auto-imported Alpaca account from .env (${isPaper ? 'paper' : 'live'})`);
    }
  } else if (envBaseUrl) {
    const envAccount = _config.accounts.find(a => a.name.includes('(from .env)'));
    if (envAccount && envAccount.baseUrl !== envBaseUrl) {
      envAccount.baseUrl = envBaseUrl;
      console.log(`  Synced baseUrl for "${envAccount.name}" from .env`);
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
    baseUrl: baseUrl || (paper ? 'https://paper-api.alpaca.markets' : 'https://api.alpaca.markets'),
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

export async function updateAccount(id, { name, baseUrl, paper }) {
  const account = _config.accounts.find(a => a.id === id);
  if (!account) throw new Error('Account not found');
  if (name !== undefined && name.trim()) account.name = name.trim();
  if (baseUrl !== undefined) account.baseUrl = baseUrl.trim() || (account.paper ? 'https://paper-api.alpaca.markets' : 'https://api.alpaca.markets');
  if (paper !== undefined) account.paper = paper;
  // Keep sandbox name in sync when account name changes
  const sandbox = _config.sandboxes[`sbx_${id}`];
  if (sandbox && name !== undefined && name.trim()) sandbox.name = name.trim();
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

export async function updatePlugin(pluginName, pluginConfig) {
  updateSandbox(_config.activeAccountId, sandbox => ({
    ...sandbox,
    plugins: {
      ...(sandbox.plugins || {}),
      [pluginName]: { ...((sandbox.plugins || {})[pluginName] || {}), ...pluginConfig },
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
        ...pluginConfig,
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
