#!/usr/bin/env node

// Prophet Agent Web Server - SSE streaming dashboard + agent control
import 'dotenv/config';
import express from 'express';
import http from 'http';
import fs from 'fs/promises';
import path from 'path';
import { fileURLToPath } from 'url';
import { spawn, execSync } from 'child_process';

const OPENCODE_BIN = process.platform === 'win32' ? 'cmd.exe' : 'opencode';
const OPENCODE_WIN_PREFIX = process.platform === 'win32' ? ['/c', 'opencode.cmd'] : [];
import axios from 'axios';
import { AgentHarness, buildSystemPrompt } from './harness.js';
import { AnalysisScheduler } from './analysis-scheduler.js';
import ChatStore from './chat-store.js';
import AgentOrchestrator from './orchestrator.js';
import { migrateLegacyDataForAccount } from './data-migration.js';
import {
  loadConfig, getConfig, saveConfig,
  addAccount, updateAccount, removeAccount, setActiveAccount, setActiveSandbox, getActiveAccount, getAccountById,
  addAgent, updateAgent, removeAgent, setActiveAgent, getActiveAgent, getAgentById, getResolvedAgentForSandbox,
  addStrategy, updateStrategy, removeStrategy,
  setActiveModel, getStrategyById,
  updateSandboxAgentOverrides, updateSandboxAgentSelection, updateSandboxStrategyRules,
  updateHeartbeat, updateHeartbeatForSandbox, getHeartbeatForPhase,
  updatePermissions, updatePermissionsForSandbox, getPermissions, getPermissionsForSandbox,
  updatePlugin, updatePluginForSandbox, getPlugin, getPluginForSandbox,
  getActiveSandbox, getSandbox, getHeartbeatForSandboxPhase, getSandboxes,
  getHeartbeatProfiles, getPhaseTimeRanges, applyHeartbeatProfile, updatePhaseTimeRange,
} from './config-store.js';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const PROJECT_ROOT = path.join(__dirname, '..');
const PORT = process.env.AGENT_PORT || 3737;
const TRADING_BOT_PORT = process.env.TRADING_BOT_PORT || '4534';
const TRADING_BOT_URL = process.env.TRADING_BOT_URL || `http://localhost:${TRADING_BOT_PORT}`;

function getSandboxDbPathForAccount(accountId) {
  return path.join(PROJECT_ROOT, 'data', 'sandboxes', accountId, 'prophet_trader.db');
}

// Pooled HTTP agent for Go backend calls — reuses TCP connections
const goHttpAgent = new http.Agent({ keepAlive: true, maxSockets: 10, keepAliveMsecs: 30000 });
const goAxios = axios.create({ baseURL: TRADING_BOT_URL, httpAgent: goHttpAgent, timeout: 5000 });

const app = express();
app.use(express.json({ limit: '1mb' }));

// ── Auth Middleware ────────────────────────────────────────────────
// Token-based auth. Set AGENT_AUTH_TOKEN env var to enable.
// Without it, server is open (for local dev). With it, all API routes require the token.
const AUTH_TOKEN = process.env.AGENT_AUTH_TOKEN || '';
function authMiddleware(req, res, next) {
  if (!AUTH_TOKEN) return next(); // no token configured = open access
  // Allow health check unauthenticated
  if (req.path === '/api/health') return next();
  // Check Authorization header or query param
  const header = req.headers.authorization;
  const token = header?.startsWith('Bearer ') ? header.slice(7) : req.query.token;
  if (token === AUTH_TOKEN) return next();
  res.status(401).json({ error: 'Unauthorized. Set Authorization: Bearer <token> header.' });
}
app.use('/api', authMiddleware);

// ── Go Backend Manager ─────────────────────────────────────────────
// Manages the Go trading bot lifecycle, supports restarting with different Alpaca keys
let goProc = null;
let goReady = false;

async function startGoBackend(account) {
  // Kill existing if running
  await stopGoBackend();

  if (!account) {
    console.log('  No active account — Go backend not started');
    return false;
  }

  // Build binary if needed
  const binaryName = process.platform === 'win32' ? 'prophet_bot.exe' : 'prophet_bot';
  const binaryPath = path.join(PROJECT_ROOT, binaryName);
  try {
    const fs = await import('fs');
    if (!fs.existsSync(binaryPath)) {
      console.log('  Building Go binary...');
      execSync(`go build -o ${binaryName} ./cmd/bot`, { cwd: PROJECT_ROOT, timeout: 60000 });
    }
  } catch (err) {
    console.error('  Failed to build Go binary:', err.message);
    return false;
  }

  const env = {
    ...process.env,
    ALPACA_API_KEY: account.publicKey,
    ALPACA_SECRET_KEY: account.secretKey,
    ALPACA_BASE_URL: account.baseUrl || (account.paper ? 'https://paper-api.alpaca.markets' : 'https://api.alpaca.markets'),
    ALPACA_PAPER: account.paper ? 'true' : 'false',
    PORT: TRADING_BOT_PORT,
    DATABASE_PATH: getSandboxDbPathForAccount(account.id),
    ACTIVITY_LOG_DIR: path.join(PROJECT_ROOT, 'data', 'sandboxes', account.id, 'activity_logs'),
    OPENPROPHET_ACCOUNT_ID: account.id,
    OPENPROPHET_SANDBOX_ID: `sbx_${account.id}`,
  };

  await fs.mkdir(path.dirname(env.DATABASE_PATH), { recursive: true });

  console.log(`  Starting Go backend for account "${account.name}" (${account.paper ? 'paper' : 'live'})...`);

  goProc = spawn(binaryPath, [], {
    cwd: PROJECT_ROOT,
    env,
    stdio: ['ignore', 'pipe', 'pipe'],
  });

  goProc.stdout.on('data', (d) => {
    const msg = d.toString().trim();
    if (msg) console.log(`  [go] ${msg}`);
  });
  goProc.stderr.on('data', (d) => {
    const msg = d.toString().trim();
    if (msg) console.log(`  [go-err] ${msg}`);
  });
  goProc.on('exit', (code, signal) => {
    console.log(`  Go backend exited (code: ${code}, signal: ${signal})`);
    goReady = false;
    goProc = null;
    // Auto-restart on unexpected crash (not manual stop)
    if (code !== 0 && code !== null && signal !== 'SIGTERM') {
      console.log('  Go backend crashed — auto-restarting in 5s...');
      broadcast('agent_log', {
        message: 'Trading backend crashed — auto-restarting in 5s...',
        level: 'error',
        timestamp: new Date().toISOString(),
      });
      setTimeout(() => {
        const acc = getActiveAccount();
        if (acc) startGoBackend(acc);
      }, 5000);
    }
  });

  // Wait for health check
  goReady = false;
  for (let i = 0; i < 20; i++) {
    await new Promise(r => setTimeout(r, 500));
    try {
      await goAxios.get('/health', { timeout: 2000 });
      goReady = true;
      console.log(`  Go backend ready on port ${TRADING_BOT_PORT} (account: ${account.name})`);
      broadcast('agent_log', {
        message: `Trading backend started for account "${account.name}" (${account.paper ? 'paper' : 'live'})`,
        level: 'success',
        timestamp: new Date().toISOString(),
      });
      return true;
    } catch {}
  }

  console.error('  Go backend failed to start within 10s');
  broadcast('agent_log', {
    message: 'Trading backend failed to start. Check logs.',
    level: 'error',
    timestamp: new Date().toISOString(),
  });
  return false;
}

async function stopGoBackend() {
  if (goProc) {
    const pid = goProc.pid;
    goProc.kill('SIGTERM');
    await new Promise(r => setTimeout(r, 1500));
    // Check if still alive
    try { process.kill(pid, 0); goProc.kill('SIGKILL'); } catch {}
    goProc = null;
    goReady = false;
    await new Promise(r => setTimeout(r, 500));
  }
  // Kill any orphaned Go backend on the port (but NOT our own Node process)
  const myPid = process.pid;
  try {
    let pids = '';
    if (process.platform === 'win32') {
      const out = execSync(`netstat -ano -p TCP 2>NUL`, { encoding: 'utf-8' });
      for (const line of out.split('\n')) {
        if (line.includes(`:${TRADING_BOT_PORT}`) && line.includes('LISTENING')) {
          const pid = parseInt(line.trim().split(/\s+/).pop());
          if (pid && pid !== myPid) pids += pid + '\n';
        }
      }
    } else {
      pids = execSync(`lsof -t -i :${TRADING_BOT_PORT} -sTCP:LISTEN 2>/dev/null`, { encoding: 'utf-8' }).trim();
    }
    if (pids.trim()) {
      for (const pid of pids.trim().split('\n')) {
        const p = parseInt(pid);
        if (p && p !== myPid) {
          try { process.kill(p, 'SIGTERM'); } catch {}
        }
      }
      await new Promise(r => setTimeout(r, 500));
    }
  } catch {}
}

// ── Load Config ────────────────────────────────────────────────────
await loadConfig();
const initialActiveAccount = getActiveAccount();
if (initialActiveAccount?.id) {
  const migration = await migrateLegacyDataForAccount(initialActiveAccount.id);
  if (migration.migrated) {
    console.log(`  Migrated legacy data into sandbox for account ${initialActiveAccount.id}: ${migration.copied.join(', ')}`);
  }
}

// Seed second account from env vars if configured
{
  const pk2 = process.env.ALPACA_PUBLIC_KEY_2;
  const sk2 = process.env.ALPACA_SECRET_KEY_2;
  if (pk2 && sk2) {
    const cfg = getConfig();
    const alreadyExists = (cfg.accounts || []).some(a => a.publicKey === pk2);
    if (!alreadyExists) {
      const paper2 = process.env.ALPACA_PAPER_2 !== 'false';
      const name2 = process.env.ALPACA_NAME_2 || 'Account 2';
      const baseUrl2 = process.env.ALPACA_ENDPOINT_2 ||
        (paper2 ? 'https://paper-api.alpaca.markets' : 'https://api.alpaca.markets');
      await addAccount({ name: name2, publicKey: pk2, secretKey: sk2, baseUrl: baseUrl2, paper: paper2 });
      console.log(`  Seeded second account "${name2}" from env vars`);
    }
  }
}

// ── Agent Instance ─────────────────────────────────────────────────
const chatStore = new ChatStore();
const orchestrator = new AgentOrchestrator({
  chatStore,
  agentUrl: `http://localhost:${PORT}`,
  tradingBotBasePort: Number(TRADING_BOT_PORT),
});
let harness = createHarnessForActiveSandbox();
const sseClients = new Set();
const boundOperationalHarnesses = new WeakSet();
const dailySummaryTimers = new Map();

function createHarnessForActiveSandbox() {
  const sandbox = getActiveSandbox();
  return new AgentHarness({
    sandboxId: sandbox?.id || null,
    accountId: sandbox?.accountId || null,
    getSandbox,
    getAccount: getAccountById,
    getAgent: getAgentById,
    getResolvedAgent: getResolvedAgentForSandbox,
    getStrategyById,
    getHeartbeatForPhase: getHeartbeatForSandboxPhase,
    getPermissions: getPermissionsForSandbox,
    chatStore,
    opencodeEnv: {
      TRADING_BOT_URL,
      AGENT_URL: `http://localhost:${PORT}`,
      OPENPROPHET_SANDBOX_ID: sandbox?.id || '',
      OPENPROPHET_ACCOUNT_ID: sandbox?.accountId || '',
      DATABASE_PATH: sandbox?.accountId ? getSandboxDbPathForAccount(sandbox.accountId) : '',
    },
  });
}

function rebindHarness() {
  harness = createHarnessForActiveSandbox();
  bindHarnessEvents(harness);
  bindOperationalHooks(harness);
}

function getOrCreateSandboxRuntime(sandboxId) {
  if (!sandboxId || isActiveSandbox(sandboxId)) return null;
  const runtime = orchestrator.ensureRuntime(sandboxId);
  bindOperationalHooks(runtime.harness);
  return runtime;
}

function getHarnessForSandbox(sandboxId) {
  if (!sandboxId) return harness;
  if (harness?.sandboxId === sandboxId) return harness;
  return getOrCreateSandboxRuntime(sandboxId)?.harness || null;
}

function isActiveSandbox(sandboxId) {
  return sandboxId && sandboxId === getActiveSandbox()?.id;
}

function getGoClientForSandbox(sandboxId) {
  if (!sandboxId || sandboxId === getActiveSandbox()?.id) return goAxios;
  return getOrCreateSandboxRuntime(sandboxId)?.goAxios || null;
}

async function refreshHarnessConfigForSandbox(sandboxId, options = {}) {
  const targetHarness = getHarnessForSandbox(sandboxId);
  if (!targetHarness) return;
  await targetHarness.reloadConfig(options);
}

async function refreshAllHarnessConfigs(options = {}) {
  const tasks = [];
  if (harness) tasks.push(harness.reloadConfig(options));
  for (const runtime of orchestrator.runtimes.values()) {
    if (runtime.harness) tasks.push(runtime.harness.reloadConfig(options));
  }
  await Promise.allSettled(tasks);
}

function broadcast(event, data) {
  if (sseClients.size === 0) return; // skip serialization when no clients connected
  const msg = `event: ${event}\ndata: ${JSON.stringify(data)}\n\n`;
  for (const client of sseClients) {
    client.write(msg);
  }
}

const EVENTS = [
  'status', 'agent_log', 'agent_text', 'beat_start', 'beat_end',
  'tool_call', 'tool_result', 'heartbeat_change', 'schedule', 'trade',
];

function bindHarnessEvents(activeHarness) {
  for (const evt of EVENTS) {
    activeHarness.state.on(evt, (data) => {
      broadcast(evt, { ...data, sandboxId: activeHarness.sandboxId || getActiveSandbox()?.id || null, timestamp: new Date().toISOString() });
    });
  }
}

bindHarnessEvents(harness);
bindOperationalHooks(harness);
for (const evt of EVENTS) {
  orchestrator.on(evt, (data) => {
    broadcast(evt, { ...data, timestamp: new Date().toISOString() });
  });
}

// ── Analysis Scheduler ─────────────────────────────────────────────
const scheduler = new AnalysisScheduler({
  model: getConfig().activeModel || 'anthropic/claude-sonnet-4-6',
  onEmergencyWake: (reason) => {
    if (harness?.state?.running && !harness?.state?.paused) harness.emergencyWake(reason);
    orchestrator.triggerEmergencyHeartbeat(reason);
  },
});
scheduler.on('agent_log', (data) => broadcast('agent_log', data));
scheduler.on('scheduler_job_start', ({ job, date }) => broadcast('agent_log', {
  message: `[Scheduler] Job started: ${job} for ${date}`, level: 'info', timestamp: new Date().toISOString(),
}));
scheduler.on('scheduler_job_end', ({ job, date, output }) => broadcast('agent_log', {
  message: `[Scheduler] Job complete: ${job} → ${output}`, level: 'success', timestamp: new Date().toISOString(),
}));

// ── Slack Notification Dispatcher ──────────────────────────────────
async function notifySlack(text, sandboxId) {
  try {
    const slack = sandboxId ? getPluginForSandbox(sandboxId, 'slack') : getPlugin('slack');
    if (!slack?.enabled || !slack?.webhookUrl) return;
    await axios.post(slack.webhookUrl, {
      text,
      channel: slack.channel || undefined,
    }, { timeout: 5000 });
  } catch (err) {
    console.error('Slack notification failed:', err.message);
  }
}

function slackEnabled(event, sandboxId) {
  const slack = sandboxId ? getPluginForSandbox(sandboxId, 'slack') : getPlugin('slack');
  return slack?.enabled && slack?.webhookUrl && slack?.notifyOn?.[event];
}

// Daily summary — schedule at 4:30 PM ET
function scheduleDailySummaryForHarness(targetHarness) {
  const sandboxId = targetHarness.sandboxId;
  const existing = dailySummaryTimers.get(sandboxId);
  if (existing) clearTimeout(existing);
  const now = new Date();
  const et = new Date(now.toLocaleString('en-US', { timeZone: 'America/New_York' }));
  const target = new Date(et);
  target.setHours(16, 30, 0, 0);
  if (et >= target) target.setDate(target.getDate() + 1);
  const ms = target.getTime() - et.getTime();
  const timer = setTimeout(async () => {
    if (slackEnabled('dailySummary', sandboxId)) {
      try {
        const client = getGoClientForSandbox(sandboxId);
        if (!client) return;
        const { data: acc } = await client.get('/api/v1/account');
        const equity = Number(acc.PortfolioValue || acc.portfolio_value || acc.Equity || acc.equity || 0);
        const lastEquity = Number(acc.LastEquity || acc.last_equity || 0);
        const pnl = equity - lastEquity;
        const pnlPct = lastEquity ? ((pnl / lastEquity) * 100).toFixed(2) : '0.00';
        const emoji = pnl >= 0 ? ':chart_with_upwards_trend:' : ':chart_with_downwards_trend:';
        notifySlack(`${emoji} *Daily Summary*\nP&L: ${pnl >= 0 ? '+' : ''}$${pnl.toFixed(2)} (${pnlPct}%)\nPortfolio: $${equity.toFixed(2)}\nBeats: ${targetHarness.state.stats.totalBeats} | Trades: ${targetHarness.state.stats.trades} | Errors: ${targetHarness.state.stats.errors}`, sandboxId);
      } catch {}
    }
    scheduleDailySummaryForHarness(targetHarness);
  }, ms);
  dailySummaryTimers.set(sandboxId, timer);
}

function bindOperationalHooks(targetHarness) {
  if (!targetHarness || boundOperationalHarnesses.has(targetHarness)) return;
  boundOperationalHarnesses.add(targetHarness);

  targetHarness.state.on('status', (data) => {
    const sandboxId = targetHarness.sandboxId;
    if (!slackEnabled('agentStartStop', sandboxId)) return;
    if (data.status === 'started') {
      notifySlack(`:rocket: *Prophet Agent Started*\nAgent: ${data.agent || 'Unknown'}\nModel: ${data.model || 'Unknown'}\nAccount: ${data.account || 'N/A'}`, sandboxId);
    } else if (data.status === 'stopped') {
      notifySlack(`:octagonal_sign: *Prophet Agent Stopped*`, sandboxId);
    }
  });

  targetHarness.state.on('trade', (trade) => {
    const sandboxId = targetHarness.sandboxId;
    if (slackEnabled('tradeExecuted', sandboxId)) {
      const side = (trade.side || '').toUpperCase();
      const emoji = side === 'BUY' ? ':chart_with_upwards_trend:' : ':chart_with_downwards_trend:';
      notifySlack(`${emoji} *Trade Executed*\n${side} ${trade.quantity || '?'}x ${trade.symbol || '??'}${trade.price ? ' @ $' + trade.price : ''}\nTool: ${trade.tool || 'unknown'}`, sandboxId);
    }
    const sideLower = (trade.side || '').toLowerCase();
    if (sideLower === 'buy' && slackEnabled('positionOpened', sandboxId)) {
      notifySlack(`:new: *Position Opened*\n${trade.symbol || '??'} | ${trade.quantity || '?'} contracts${trade.price ? ' @ $' + trade.price : ''}`, sandboxId);
    }
    if (sideLower === 'sell' && slackEnabled('positionClosed', sandboxId)) {
      notifySlack(`:checkered_flag: *Position Closed*\n${trade.symbol || '??'} | ${trade.quantity || '?'} contracts${trade.price ? ' @ $' + trade.price : ''}`, sandboxId);
    }
  });

  targetHarness.state.on('agent_log', (data) => {
    const sandboxId = targetHarness.sandboxId;
    if (data.level !== 'error' || !slackEnabled('errors', sandboxId)) return;
    notifySlack(`:warning: *Prophet Error*\n${data.message}`, sandboxId);
  });

  targetHarness.state.on('beat_start', (data) => {
    const sandboxId = targetHarness.sandboxId;
    if (!slackEnabled('heartbeat', sandboxId)) return;
    notifySlack(`:heartbeat: Beat #${data.beat} | Phase: ${data.phase}`, sandboxId);
  });

  targetHarness.state.on('beat_end', async () => {
    try {
      const sandboxId = targetHarness.sandboxId;
      const perms = getPermissionsForSandbox(sandboxId);
      if (!perms.maxDailyLoss || perms.maxDailyLoss <= 0) return;
      const client = getGoClientForSandbox(sandboxId);
      if (!client) return;
      const { data: acc } = await client.get('/api/v1/account', { timeout: 3000 });
      const equity = Number(acc.PortfolioValue || acc.portfolio_value || acc.Equity || acc.equity || 0);
      const lastEquity = Number(acc.LastEquity || acc.last_equity || 0);
      if (!lastEquity) return;
      const dayLossPct = ((equity - lastEquity) / lastEquity) * 100;
      if (dayLossPct <= -perms.maxDailyLoss && !targetHarness.state.paused) {
        targetHarness.pause();
        const msg = `CIRCUIT BREAKER: Daily loss ${dayLossPct.toFixed(2)}% exceeds -${perms.maxDailyLoss}% limit. Agent auto-paused.`;
        broadcast('agent_log', { message: msg, level: 'error', sandboxId, timestamp: new Date().toISOString() });
        if (slackEnabled('errors', sandboxId)) notifySlack(`:rotating_light: ${msg}`, sandboxId);
      }
    } catch { /* silently skip if account unavailable */ }
  });

  scheduleDailySummaryForHarness(targetHarness);
}

// ── SSE Endpoint ───────────────────────────────────────────────────
app.get('/api/events', (req, res) => {
  res.writeHead(200, {
    'Content-Type': 'text/event-stream',
    'Cache-Control': 'no-cache',
    'Connection': 'keep-alive',
    'Access-Control-Allow-Origin': '*',
  });
  res.write(`event: state\ndata: ${JSON.stringify({ ...harness.state.toJSON(), sandboxId: getActiveSandbox()?.id || null })}\n\n`);
  res.write(`event: config\ndata: ${JSON.stringify(safeConfig())}\n\n`);
  sseClients.add(res);
  req.on('close', () => sseClients.delete(res));
});

// ── Agent Control ──────────────────────────────────────────────────
app.post('/api/agent/start', async (req, res) => {
  try {
    await harness.start();
    res.json({ ok: true, status: 'started' });
  } catch (err) { res.status(500).json({ error: err.message }); }
});

app.post('/api/agent/stop', async (req, res) => {
  await harness.stop();
  res.json({ ok: true, status: 'stopped' });
});

app.post('/api/agent/pause', (req, res) => {
  harness.pause();
  res.json({ ok: true, status: 'paused' });
});

app.post('/api/agent/resume', (req, res) => {
  harness.resume();
  res.json({ ok: true, status: 'resumed' });
});

// ── Manager Chat ───────────────────────────────────────────────────
let _managerSessionId = null;
let _managerProc = null;
const _managerSessions = []; // { id, startTime, messageCount }

app.get('/api/manager/config', (req, res) => {
  const config = getConfig();
  const mgr = config.manager || { model: config.activeModel, customPrompt: '' };
  res.json({ model: mgr.model, customPrompt: mgr.customPrompt || '', sessions: _managerSessions, activeSessionId: _managerSessionId });
});

app.put('/api/manager/config', async (req, res) => {
  try {
    const config = getConfig();
    if (!config.manager) config.manager = {};
    if (req.body.model !== undefined) config.manager.model = req.body.model;
    if (req.body.customPrompt !== undefined) config.manager.customPrompt = req.body.customPrompt;
    await saveConfig();
    res.json({ ok: true });
  } catch (err) { res.status(400).json({ error: err.message }); }
});

app.post('/api/manager/new-session', (req, res) => {
  if (_managerProc) { try { _managerProc.kill('SIGTERM'); } catch {} _managerProc = null; }
  _managerSessionId = null;
  res.json({ ok: true });
});

app.post('/api/manager/stop', (req, res) => {
  if (_managerProc) {
    try { _managerProc.kill('SIGTERM'); } catch {}
    _managerProc = null;
    broadcast('manager_done', {});
  }
  res.json({ ok: true });
});

app.get('/api/manager/sessions', (req, res) => {
  res.json({ sessions: _managerSessions, activeSessionId: _managerSessionId });
});

app.post('/api/manager/message', async (req, res) => {
  try {
    const { message } = req.body;
    if (!message?.trim()) return res.status(400).json({ error: 'Message is required' });

    const config = getConfig();
    const mgr = config.manager || {};
    const model = mgr.model || config.activeModel || 'anthropic/claude-sonnet-4-6';
    const ocModel = model.includes('/') ? model : `anthropic/${model}`;
    const customPromptAddition = mgr.customPrompt ? `\n\n## Custom Instructions\n${mgr.customPrompt}` : '';
    
    const managerPrompt = `You are the OpenProphet Manager — a configuration and research assistant.

## CRITICAL: You do NOT trade. You NEVER place orders, buy, or sell anything.

You help the user:
- Create and configure trading agents (their personality and model)
- Create and edit strategies (the rules agents follow)
- Assign agents and strategies to accounts
- Research markets, analyze stocks, gather news
- Configure heartbeats, permissions, and session modes

## Your Available Tools

**Configuration** (your primary tools):
- create_agent: Create a new agent with name, description, model, and optional custom identity prompt
- create_strategy: Create a new strategy with name, description, and trading rules (markdown)
- assign_agent_to_sandbox: Assign an agent to an account to activate it
- update_agent_prompt: Update the current account's agent identity prompt
- update_strategy_rules: Update the current account's strategy rules
- get_agent_config: View current configuration

**Research** (for helping users make informed decisions):
- analyze_stocks: Technical analysis with RSI, trend, support/resistance
- get_quote, get_latest_bar, get_historical_bars: Price data
- search_news, get_market_news, get_quick_market_intelligence: News
- find_similar_setups, get_trade_stats: Historical trade patterns

**System**:
- get_heartbeat_profiles, apply_heartbeat_profile, set_heartbeat: Heartbeat config
- update_permissions: Update trading permissions/guardrails
- get_datetime: Current time and market status

## How Agents and Strategies Work

An **Agent** is a personality — it has a name, description, model choice, and optionally a custom identity prompt that defines how it thinks and approaches trading.

A **Strategy** is a set of hard rules — position sizes, stop losses, what instruments to trade, risk limits, exit criteria. Written in markdown.

The final instructions sent to the AI = Agent Identity + Strategy Rules + System Tools/Heartbeat.

When creating an agent:
1. First create_strategy with the trading rules
2. Then create_agent with the personality, linking the strategy
3. Then assign_agent_to_sandbox to activate it on an account

## Instructions
- Be direct and actionable
- If the user describes an agent, create both the strategy and agent immediately
- Don't ask unnecessary questions — use reasonable defaults
- When creating strategies, write comprehensive markdown rules covering: what to trade, position sizing, risk management, entry/exit criteria, and any special instructions

## Current Time
${new Date().toLocaleString('en-US', { timeZone: 'America/New_York' })} ET

## User Message
${message.trim()}${customPromptAddition}`;

    const args = ['run', '--format', 'json', '--model', ocModel];
    if (_managerSessionId) args.push('--session', _managerSessionId);

    const isNewSession = !_managerSessionId;
    const fullPrompt = isNewSession 
      ? managerPrompt
      : `[Manager] User message:\n${message.trim()}`;
    
    // Track session
    if (isNewSession) {
      _managerSessions.push({ id: null, startTime: new Date().toISOString(), messageCount: 1, model: ocModel });
    } else {
      const last = _managerSessions[_managerSessions.length - 1];
      if (last) last.messageCount++;
    }

    // Kill any existing manager process
    if (_managerProc) { try { _managerProc.kill('SIGTERM'); } catch {} }

    // On Windows, cmd.exe cannot handle multiline prompts as CLI args — write to temp file
    let tempPromptFile = null;
    if (process.platform === 'win32') {
      const os = await import('os');
      tempPromptFile = path.join(os.tmpdir(), `prophet_mgr_${Date.now()}.txt`);
      await fs.writeFile(tempPromptFile, fullPrompt, 'utf-8');
      args.push('Process the full prompt from the attached file.', '--file', tempPromptFile);
    }

    const proc = spawn(OPENCODE_BIN, [...OPENCODE_WIN_PREFIX, ...args], {
      cwd: process.cwd(),
      env: {
        ...process.env,
        ANTHROPIC_API_KEY: process.env.CLAUDE_API_KEY || '',
      },
      stdio: ['pipe', 'pipe', 'pipe'],
    });
    _managerProc = proc;

    if (process.platform !== 'win32') {
      proc.stdin.write(fullPrompt);
    }
    proc.stdin.end();

    // Return immediately - streaming happens via SSE
    res.json({ ok: true, streaming: true, model: ocModel });

    let stdoutBuf = '';
    proc.stdout.on('data', (chunk) => {
      stdoutBuf += chunk.toString();
      const lines = stdoutBuf.split('\n');
      stdoutBuf = lines.pop();
      for (const line of lines) {
        if (!line.trim()) continue;
        try {
          const evt = JSON.parse(line);
          const part = evt.part || {};
          
          if (evt.type === 'text') {
            const text = part.text || evt.text || '';
            if (text) broadcast('manager_text', { text });
          } else if (evt.type === 'tool_call') {
            const name = part.name || part.tool || evt.name || '?';
            const args = part.args || part.input || {};
            broadcast('manager_tool', { name, args });
          } else if (evt.type === 'tool_result') {
            const name = part.name || '?';
            const result = String(part.result || part.output || '').substring(0, 200);
            broadcast('manager_tool_result', { name, result });
          }
          
          // Capture session ID
          if (evt.sessionID) {
            _managerSessionId = evt.sessionID;
          }
        } catch {}
      }
    });

    proc.stderr.on('data', () => {});
    proc.on('close', () => {
      if (_managerProc === proc) _managerProc = null;
      if (tempPromptFile) fs.unlink(tempPromptFile).catch(() => {});
      const last = _managerSessions[_managerSessions.length - 1];
      if (last && !last.id && _managerSessionId) last.id = _managerSessionId;
      broadcast('manager_done', {});
    });
  } catch (err) {
    res.status(400).json({ error: err.message });
  }
});

app.post('/api/agent/message', async (req, res) => {
  try {
    const { message, sandboxId } = req.body;
    if (!message?.trim()) return res.status(400).json({ error: 'Message is required' });

    // Check for commands
    const trimmed = message.trim();
    const config = getConfig();
    
    // /help - show available commands
    if (trimmed === '/help' || trimmed === '/?') {
      const helpText = `Available commands:

/newagent - Create a new agent
/editagent <id> - Edit an existing agent
/agents - List all agents
/sandboxes - List all sandboxes (portfolios)
/start <sandboxId> - Start agent on a sandbox
/stop <sandboxId> - Stop agent on a sandbox
/status - Show status of all portfolios
/portfolios - Show status of all portfolios

Models: ${(config.models || []).length} available
Providers: ${[...new Set((config.models || []).map(m => m.id.split('/')[0]))].join(', ')}

Use /newagent to open the agent builder!`;
      return res.json({ ok: true, text: helpText });
    }
    
    // /newagent - open agent builder
    if (trimmed === '/newagent' || trimmed.startsWith('/newagent ')) {
      const models = config.models || [];
      const strategies = config.strategies || [];
      broadcast('agent_builder', {
        mode: 'create',
        models,
        strategies,
        sandboxId: sandboxId || getActiveSandbox()?.id,
      });
      return res.json({ ok: true, builder: true });
    }
    
    // /editagent - open agent editor
    const editMatch = trimmed.match(/^\/editagent\s+(\S+)/);
    if (editMatch) {
      const agentId = editMatch[1];
      const agent = getAgentById(agentId);
      if (!agent) return res.status(404).json({ error: 'Agent not found' });
      const models = config.models || [];
      const strategies = config.strategies || [];
      broadcast('agent_builder', {
        mode: 'edit',
        agent,
        models,
        strategies,
        sandboxId: sandboxId || getActiveSandbox()?.id,
      });
      return res.json({ ok: true, builder: true });
    }
    
    // /agents - list agents
    if (trimmed === '/agents') {
      const agents = config.agents || [];
      let msg = 'Available agents:\n';
      for (const a of agents) {
        msg += `\n- ${a.name} (${a.id})\n  Model: ${a.model || 'default'}\n  Strategy: ${a.strategyId || 'none'}\n`;
      }
      msg += '\nUse /editagent <id> to edit an agent';
      return res.json({ ok: true, text: msg });
    }
    
    // /sandboxes - list sandboxes and their status
    if (trimmed === '/sandboxes') {
      const sandboxes = getSandboxes();
      let msg = 'Available sandboxes (portfolios):\n';
      for (const s of sandboxes) {
        const isActive = getActiveSandbox()?.id === s.id;
        const runtime = orchestrator.getSandboxRuntime(s.id);
        const state = isActive ? harness.state.running : (runtime ? runtime.harness.state.running : false);
        msg += `\n- ${s.name} (${s.id})\n  Account: ${s.accountId}\n  Status: ${state ? 'running' : 'stopped'}\n  Agent: ${s.agent?.activeAgentId || 'default'}\n`;
      }
      msg += '\nUse /start <sandboxId> or /stop <sandboxId> to control';
      return res.json({ ok: true, text: msg });
    }
    
    // /start <sandboxId> - start agent on a specific sandbox
    const startMatch = trimmed.match(/^\/start\s+(\S+)/);
    if (startMatch) {
      const sbxId = startMatch[1];
      const sandbox = getSandbox(sbxId);
      if (!sandbox) return res.status(404).json({ error: 'Sandbox not found' });
      const isActive = getActiveSandbox()?.id === sbxId;
      if (isActive) {
        if (!harness.state.running) { await harness.start(); }
      } else {
        await orchestrator.startSandbox(sbxId);
      }
      return res.json({ ok: true, text: `Started agent on sandbox ${sandbox.name}` });
    }
    
    // /stop <sandboxId> - stop agent on a specific sandbox
    const stopMatch = trimmed.match(/^\/stop\s+(\S+)/);
    if (stopMatch) {
      const sbxId = stopMatch[1];
      const sandbox = getSandbox(sbxId);
      if (!sandbox) return res.status(404).json({ error: 'Sandbox not found' });
      const isActive = getActiveSandbox()?.id === sbxId;
      if (isActive) {
        if (harness.state.running) { await harness.stop(); }
      } else {
        await orchestrator.stopSandbox(sbxId);
      }
      return res.json({ ok: true, text: `Stopped agent on sandbox ${sandbox.name}` });
    }
    
    // /status - show status of all sandboxes
    if (trimmed === '/status' || trimmed === '/portfolio' || trimmed === '/portfolios') {
      const sandboxes = getSandboxes();
      const account = getActiveAccount();
      let msg = 'Portfolio Status:\n';
      msg += `\nActive: ${account?.name || 'none'} (${account?.paper ? 'paper' : 'live'})\n`;
      msg += '\nSandbox Status:\n';
      for (const s of sandboxes) {
        const isActive = getActiveSandbox()?.id === s.id;
        const runtime = orchestrator.getSandboxRuntime(s.id);
        const state = isActive ? harness.state.toJSON() : (runtime ? runtime.harness.state.toJSON() : { running: false, beat: 0 });
        msg += `\n${s.name}: ${state.running ? 'running' : 'stopped'} (beat #${state.beat || 0})`;
      }
      return res.json({ ok: true, text: msg });
    }

    const result = await harness.sendMessage(trimmed);
    res.json({ ok: true, ...result });
  } catch (err) { res.status(400).json({ error: err.message }); }
});

app.get('/api/agent/state', (req, res) => {
  res.json(harness.state.toJSON());
});

// Multi-sandbox orchestration
app.get('/api/sandboxes', (req, res) => {
  const sandboxes = getSandboxes().map(sandbox => ({
    ...sandbox,
    runtime: isActiveSandbox(sandbox.id)
      ? harness.state.toJSON()
      : (orchestrator.getSandboxRuntime(sandbox.id) ? orchestrator.getState(sandbox.id) : null),
    isActive: getActiveSandbox()?.id === sandbox.id,
  }));
  res.json({ sandboxes });
});

app.get('/api/sandboxes/:id/state', (req, res) => {
  try {
    if (isActiveSandbox(req.params.id)) return res.json(harness.state.toJSON());
    res.json(orchestrator.getState(req.params.id));
  } catch (err) { res.status(404).json({ error: err.message }); }
});

app.post('/api/sandboxes/:id/start', async (req, res) => {
  try {
    if (isActiveSandbox(req.params.id)) {
      const account = getActiveAccount();
      if (!goReady && account) await startGoBackend(account);
      await harness.start();
    }
    // Always also start via orchestrator so both can run
    if (!isActiveSandbox(req.params.id)) {
      await orchestrator.startSandbox(req.params.id);
    }
    res.json({ ok: true, status: 'started', sandboxId: req.params.id });
  } catch (err) { res.status(400).json({ error: err.message }); }
});

app.post('/api/sandboxes/:id/stop', async (req, res) => {
  try {
    if (isActiveSandbox(req.params.id)) {
      await harness.stop();
      await stopGoBackend();
    } else {
      await orchestrator.stopSandbox(req.params.id);
    }
    res.json({ ok: true, status: 'stopped', sandboxId: req.params.id });
  } catch (err) { res.status(400).json({ error: err.message }); }
});

app.post('/api/sandboxes/:id/pause', (req, res) => {
  try {
    if (isActiveSandbox(req.params.id)) harness.pause();
    else orchestrator.pauseSandbox(req.params.id);
    res.json({ ok: true, status: 'paused', sandboxId: req.params.id });
  } catch (err) { res.status(400).json({ error: err.message }); }
});

app.post('/api/sandboxes/:id/resume', (req, res) => {
  try {
    if (isActiveSandbox(req.params.id)) harness.resume();
    else orchestrator.resumeSandbox(req.params.id);
    res.json({ ok: true, status: 'resumed', sandboxId: req.params.id });
  } catch (err) { res.status(400).json({ error: err.message }); }
});

app.post('/api/sandboxes/:id/message', async (req, res) => {
  try {
    const { message } = req.body;
    const sandboxId = req.params.id;
    if (!message?.trim()) return res.status(400).json({ error: 'Message is required' });

    const config = getConfig();
    const trimmed = message.trim();
    
    // /newagent command
    if (trimmed === '/newagent') {
      broadcast('agent_builder', {
        mode: 'create',
        models: config.models || [],
        strategies: config.strategies || [],
        sandboxId,
      });
      const providers = [...new Set((config.models || []).map(m => m.id.split('/')[0]))].join(', ');
      return res.json({ ok: true, builder: true, text: 
        'Agent Builder opened! You can also describe what you want here:\n\n' +
        '- What should it trade? (options, stocks, both)\n' +
        '- What trading style? (aggressive, conservative, scalping, swing, long-term)\n' +
        '- Any timeframe rules? (day trading, multi-day holds, weekly)\n' +
        '- Risk tolerance? (max position size, stop loss %)\n' +
        '- Which model? (' + providers + ')\n' +
        '- Any specific rules?\n\n' +
        'Example: "Create a conservative tech options agent with 30-day holds, max 10% per position, using claude-sonnet-4-6"'
      });
    }
    
    // /editagent command
    const editMatch = trimmed.match(/^\/editagent\s+(\S+)/);
    if (editMatch) {
      const agent = getAgentById(editMatch[1]);
      if (!agent) return res.status(404).json({ error: 'Agent not found' });
      broadcast('agent_builder', {
        mode: 'edit',
        agent,
        models: config.models || [],
        strategies: config.strategies || [],
        sandboxId,
      });
      return res.json({ ok: true, builder: true });
    }
    
    // /agents command
    if (trimmed === '/agents') {
      const agents = config.agents || [];
      let msg = 'Available agents:\n' + agents.map(a => `- ${a.name} (${a.id})`).join('\n');
      return res.json({ ok: true, text: msg });
    }

    const result = isActiveSandbox(sandboxId)
      ? await harness.sendMessage(trimmed)
      : await orchestrator.sendMessage(sandboxId, trimmed);
    res.json({ ok: true, sandboxId, ...result });
  } catch (err) { res.status(400).json({ error: err.message }); }
});

app.get('/api/sandboxes/:id/config', (req, res) => {
  try {
    const sandbox = getSandbox(req.params.id);
    if (!sandbox) return res.status(404).json({ error: 'Sandbox not found' });
    const agent = getResolvedAgentForSandbox(req.params.id);
    res.json({ sandbox, agent });
  } catch (err) { res.status(400).json({ error: err.message }); }
});

app.get('/api/sandboxes/:id/dashboard', (req, res) => {
  try {
    const sandbox = getSandbox(req.params.id);
    if (!sandbox) return res.status(404).json({ error: 'Sandbox not found' });

    const agent = getResolvedAgentForSandbox(req.params.id);
    const heartbeat = getSandbox(req.params.id)?.heartbeat || {};
    const permissions = getPermissionsForSandbox(req.params.id);
    const slack = getPluginForSandbox(req.params.id, 'slack');
    const isActive = isActiveSandbox(req.params.id);
    let state;
    if (isActive) {
      state = harness.state.toJSON();
    } else {
      const runtime = orchestrator.getSandboxRuntime(req.params.id);
      state = runtime ? runtime.harness.state.toJSON() : { running: false, status: 'stopped', beat: 0 };
    }

    const config = getConfig();
    const providers = [...new Set((config.models || []).map(m => m.id.split('/')[0]))];

    res.json({
      sandbox,
      agent,
      models: config.models,
      providers,
      heartbeat,
      heartbeatProfiles: getHeartbeatProfiles(),
      heartbeatPhases: getPhaseTimeRanges(),
      permissions,
      slack: slack || {},
      state,
    });
  } catch (err) { res.status(400).json({ error: err.message }); }
});

app.post('/api/sandboxes/:id/activate', async (req, res) => {
  try {
    const sandbox = getSandbox(req.params.id);
    if (!sandbox) return res.status(404).json({ error: 'Sandbox not found' });

    const wasRunning = harness.state.running;
    if (wasRunning) await harness.stop();
    if (orchestrator.getSandboxRuntime(req.params.id)) {
      await orchestrator.stopSandbox(req.params.id);
    }

    await setActiveSandbox(req.params.id);
    rebindHarness();
    const account = getActiveAccount();
    if (account) {
      await migrateLegacyDataForAccount(account.id);
      await startGoBackend(account);
      if (wasRunning) await harness.start();
    }
    broadcast('config', safeConfig());
    res.json({ ok: true, sandboxId: req.params.id });
  } catch (err) { res.status(400).json({ error: err.message }); }
});

app.put('/api/sandboxes/:id/agent', async (req, res) => {
  try {
    const { activeAgentId, model, overrides = {} } = req.body || {};
    const updates = {};
    if (activeAgentId !== undefined) updates.activeAgentId = activeAgentId;
    if (model !== undefined) updates.model = model;
    if (Object.keys(overrides).length) updates.overrides = overrides;
    const sandbox = await updateSandboxAgentSelection(req.params.id, updates);
    // Apply the agent's default heartbeat profile when the agent changes
    if (activeAgentId !== undefined) {
      const agent = getAgentById(activeAgentId);
      if (agent?.defaultHeartbeatProfile) {
        await applyHeartbeatProfile(req.params.id, agent.defaultHeartbeatProfile).catch(() => {});
        const targetHarness = getHarnessForSandbox(req.params.id);
        if (targetHarness?.state.running) targetHarness._scheduleNext();
      }
    }
    await refreshHarnessConfigForSandbox(req.params.id, { resetSession: true });
    broadcast('config', safeConfig());
    res.json({ ok: true, sandbox, agent: getResolvedAgentForSandbox(req.params.id) });
  } catch (err) { res.status(400).json({ error: err.message }); }
});

app.put('/api/sandboxes/:id/agent/overrides', async (req, res) => {
  try {
    const sandbox = await updateSandboxAgentOverrides(req.params.id, req.body || {});
    await refreshHarnessConfigForSandbox(req.params.id, { resetSession: true });
    broadcast('config', safeConfig());
    res.json({ ok: true, sandbox, agent: getResolvedAgentForSandbox(req.params.id) });
  } catch (err) { res.status(400).json({ error: err.message }); }
});

app.put('/api/sandboxes/:id/strategy-rules', async (req, res) => {
  try {
    if (typeof req.body?.rules !== 'string') {
      return res.status(400).json({ error: 'rules is required' });
    }
    const sandbox = await updateSandboxStrategyRules(req.params.id, req.body.rules);
    await refreshHarnessConfigForSandbox(req.params.id, { resetSession: true });
    broadcast('config', safeConfig());
    res.json({ ok: true, sandbox, agent: getResolvedAgentForSandbox(req.params.id) });
  } catch (err) { res.status(400).json({ error: err.message }); }
});

// ── Order Confirmation ─────────────────────────────────────────────
// When requireConfirmation is enabled, the MCP server checks /api/permissions
// and returns an error asking the agent to wait. The operator must approve via UI.
// This is enforced at the MCP permission layer (enforcePermissions function).
// The UI can show a confirmation prompt — for now, requireConfirmation
// makes the MCP server reject orders with a "requires confirmation" error.
// The agent will see this error and should report it to the operator.

app.post('/api/agent/heartbeat', (req, res) => {
  const { seconds, reason, sandboxId } = req.body;
  if (!seconds || seconds < 30 || seconds > 3600) return res.status(400).json({ error: 'seconds must be 30-3600' });
  const targetHarness = getHarnessForSandbox(sandboxId);
  if (!targetHarness) return res.status(404).json({ error: 'Sandbox harness not found' });
  targetHarness.state.heartbeatOverride = { seconds, reason: reason || 'Manual override', oneTime: false };
  targetHarness.state.emit('heartbeat_change', { seconds, reason: reason || 'Manual override from UI', sandboxId: sandboxId || targetHarness.sandboxId });
  res.json({ ok: true, seconds });
});

// ── Safe Config (strip secrets) ────────────────────────────────────
function safeConfig() {
  const cfg = { ...getConfig() };
  // Strip secret keys from accounts
  cfg.accounts = (cfg.accounts || []).map(a => ({ ...a, secretKey: a.secretKey ? '****' + a.secretKey.slice(-4) : '****' }));
  return cfg;
}

// ── Config CRUD ────────────────────────────────────────────────────
app.get('/api/config', (req, res) => {
  res.json(safeConfig());
});

// System prompt preview
app.get('/api/agent/prompt-preview', async (req, res) => {
  try {
    const sandboxId = req.query.sandboxId || getActiveSandbox()?.id;
    const agentConfig = sandboxId ? getResolvedAgentForSandbox(sandboxId) : getActiveAgent();
    const prompt = await buildSystemPrompt(agentConfig, { getStrategyById });
    res.json({ prompt, agentName: agentConfig.name, sandboxId });
  } catch (err) { res.status(500).json({ error: err.message }); }
});

// Chat history
app.get('/api/chats', async (req, res) => {
  try {
    const accountId = req.query.accountId || getActiveAccount()?.id;
    if (!accountId) return res.json({ sessions: [] });
    const limit = Number(req.query.limit || 50);
    const sessions = await chatStore.listSessions(accountId, limit);
    res.json({ accountId, sessions });
  } catch (err) { res.status(500).json({ error: err.message }); }
});

app.get('/api/chats/all', async (req, res) => {
  try {
    const limit = Number(req.query.limit || 100);
    const sessions = await chatStore.listAllSessions(limit);
    res.json({ sessions });
  } catch (err) { res.status(500).json({ error: err.message }); }
});

app.get('/api/chats/:sessionId', async (req, res) => {
  try {
    const accountId = req.query.accountId || getActiveAccount()?.id;
    if (!accountId) return res.status(400).json({ error: 'No active account' });
    const session = await chatStore.getSession(accountId, req.params.sessionId);
    const messages = await chatStore.getSessionMessages(accountId, req.params.sessionId, {
      offset: Number(req.query.offset || 0),
      limit: Number(req.query.limit || 500),
    });
    res.json({ accountId, session, messages });
  } catch (err) { res.status(500).json({ error: err.message }); }
});

app.delete('/api/chats/:sessionId', async (req, res) => {
  try {
    const accountId = req.query.accountId || getActiveAccount()?.id;
    if (!accountId) return res.status(400).json({ error: 'No active account' });
    await chatStore.deleteSession(accountId, req.params.sessionId);
    res.json({ ok: true });
  } catch (err) { res.status(500).json({ error: err.message }); }
});

// Accounts
app.get('/api/accounts', (req, res) => {
  const config = getConfig();
  // Don't expose secret keys to frontend
  const safe = config.accounts.map(a => ({ ...a, secretKey: '****' + a.secretKey.slice(-4) }));
  res.json({ accounts: safe, activeId: config.activeAccountId });
});

app.post('/api/accounts', async (req, res) => {
  try {
    const account = await addAccount(req.body);
    broadcast('config', safeConfig());
    res.json({ ok: true, account: { ...account, secretKey: '****' } });
  } catch (err) { res.status(400).json({ error: err.message }); }
});

app.put('/api/accounts/:id', async (req, res) => {
  try {
    const account = await updateAccount(req.params.id, req.body);
    broadcast('config', safeConfig());
    res.json({ ok: true, account: { ...account, secretKey: '****' + account.secretKey.slice(-4) } });
  } catch (err) { res.status(400).json({ error: err.message }); }
});

app.delete('/api/accounts/:id', async (req, res) => {
  try {
    await removeAccount(req.params.id);
    broadcast('config', safeConfig());
    res.json({ ok: true });
  } catch (err) { res.status(400).json({ error: err.message }); }
});

app.post('/api/accounts/:id/activate', async (req, res) => {
  try {
    const nextSandboxId = `sbx_${req.params.id}`;
    const wasRunning = harness.state.running;
    if (wasRunning) await harness.stop();
    if (orchestrator.getSandboxRuntime(nextSandboxId)) {
      await orchestrator.stopSandbox(nextSandboxId);
    }
    await setActiveAccount(req.params.id);
    const account = getActiveAccount();
    rebindHarness();
    broadcast('config', safeConfig());
    // Restart Go backend with new account credentials
    if (account) {
      await migrateLegacyDataForAccount(account.id);
      broadcast('agent_log', {
        message: `Switching to account "${account.name}"... restarting trading backend.`,
        level: 'info',
        timestamp: new Date().toISOString(),
      });
      await startGoBackend(account);
      if (wasRunning) await harness.start();
    }
    res.json({ ok: true });
  } catch (err) { res.status(400).json({ error: err.message }); }
});

app.post('/api/accounts/:id/clone', async (req, res) => {
  try {
    const source = getAccountById(req.params.id);
    if (!source) return res.status(404).json({ error: 'Account not found' });
    const { name } = req.body;
    if (!name?.trim()) return res.status(400).json({ error: 'Name is required' });
    const account = await addAccount({
      name: name.trim(),
      publicKey: source.publicKey,
      secretKey: source.secretKey,
      baseUrl: source.baseUrl,
      paper: source.paper,
    });
    broadcast('config', safeConfig());
    res.json({ ok: true, sandboxId: `sbx_${account.id}`, account: { ...account, secretKey: '****' + account.secretKey.slice(-4) } });
  } catch (err) { res.status(400).json({ error: err.message }); }
});

// Agents
app.get('/api/agents', (req, res) => {
  const config = getConfig();
  res.json({ agents: config.agents, activeId: config.activeAgentId });
});

app.post('/api/agents', async (req, res) => {
  try {
    const agent = await addAgent(req.body);
    broadcast('config', safeConfig());
    res.json({ ok: true, agent });
  } catch (err) { res.status(400).json({ error: err.message }); }
});

app.put('/api/agents/:id', async (req, res) => {
  try {
    const agent = await updateAgent(req.params.id, req.body);
    await refreshAllHarnessConfigs({ resetSession: true });
    broadcast('config', safeConfig());
    res.json({ ok: true, agent });
  } catch (err) { res.status(400).json({ error: err.message }); }
});

app.delete('/api/agents/:id', async (req, res) => {
  try {
    await removeAgent(req.params.id);
    await refreshAllHarnessConfigs({ resetSession: true });
    broadcast('config', safeConfig());
    res.json({ ok: true });
  } catch (err) { res.status(400).json({ error: err.message }); }
});

app.post('/api/agents/:id/activate', async (req, res) => {
  try {
    await setActiveAgent(req.params.id);
    await refreshHarnessConfigForSandbox(getActiveSandbox()?.id, { resetSession: true });
    broadcast('config', safeConfig());
    res.json({ ok: true });
  } catch (err) { res.status(400).json({ error: err.message }); }
});

// Strategies
app.get('/api/strategies', (req, res) => {
  const config = getConfig();
  res.json({ strategies: config.strategies });
});

app.post('/api/strategies', async (req, res) => {
  try {
    const strategy = await addStrategy(req.body);
    broadcast('config', safeConfig());
    res.json({ ok: true, strategy });
  } catch (err) { res.status(400).json({ error: err.message }); }
});

app.put('/api/strategies/:id', async (req, res) => {
  try {
    const strategy = await updateStrategy(req.params.id, req.body);
    await refreshAllHarnessConfigs({ resetSession: true });
    broadcast('config', safeConfig());
    res.json({ ok: true, strategy });
  } catch (err) { res.status(400).json({ error: err.message }); }
});

app.delete('/api/strategies/:id', async (req, res) => {
  try {
    await removeStrategy(req.params.id);
    await refreshAllHarnessConfigs({ resetSession: true });
    broadcast('config', safeConfig());
    res.json({ ok: true });
  } catch (err) { res.status(400).json({ error: err.message }); }
});

// Model selection
app.get('/api/models', (req, res) => {
  const config = getConfig();
  const allModels = config.models || [];
  const provider = req.query.provider;
  const models = provider ? allModels.filter(m => m.id.startsWith(provider + '/')) : allModels;
  const allProviders = [...new Set(allModels.map(m => m.id.split('/')[0]))];
  const filteredProviders = provider ? [provider] : allProviders;
  res.json({ models, activeModel: config.activeModel, providers: filteredProviders, allProviders });
});

app.post('/api/models/activate', async (req, res) => {
  try {
    await setActiveModel(req.body.model);
    await refreshHarnessConfigForSandbox(getActiveSandbox()?.id, { resetSession: true });
    broadcast('config', safeConfig());
    res.json({ ok: true });
  } catch (err) { res.status(400).json({ error: err.message }); }
});

app.post('/api/models/refresh', async (req, res) => {
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
    
    const config = getConfig();
    config.models = models;
    await saveConfig();
    broadcast('config', safeConfig());
    res.json({ ok: true, count: models.length });
  } catch (err) { res.status(400).json({ error: err.message }); }
});

// ── Heartbeat Config ───────────────────────────────────────────────
app.get('/api/heartbeat', (req, res) => {
  const sandboxId = req.query.sandboxId;
  if (sandboxId) {
    return res.json(getSandbox(sandboxId)?.heartbeat || {});
  }
  const config = getConfig();
  res.json(config.heartbeat || {});
});

app.put('/api/heartbeat', async (req, res) => {
  try {
    const { sandboxId, ...heartbeatBody } = req.body || {};
    if (sandboxId) {
      await updateHeartbeatForSandbox(sandboxId, heartbeatBody);
    } else {
      await updateHeartbeat(heartbeatBody);
    }
    broadcast('config', safeConfig());
    res.json({ ok: true });
  } catch (err) { res.status(400).json({ error: err.message }); }
});

app.get('/api/heartbeat/profiles', (req, res) => {
  res.json({ profiles: getHeartbeatProfiles() });
});

app.post('/api/heartbeat/apply-profile', async (req, res) => {
  try {
    const { sandboxId, profile } = req.body || {};
    const targetSandbox = sandboxId || getActiveSandbox()?.id;
    if (!targetSandbox) throw new Error('No active sandbox');
    await applyHeartbeatProfile(targetSandbox, profile);
    // Reschedule the live harness immediately so the new interval takes effect now
    // rather than waiting for the current timer to expire naturally.
    const targetHarness = getHarnessForSandbox(targetSandbox);
    if (targetHarness?.state.running) targetHarness._scheduleNext();
    broadcast('config', safeConfig());
    res.json({ ok: true, profile, sandboxId: targetSandbox });
  } catch (err) { res.status(400).json({ error: err.message }); }
});

app.get('/api/heartbeat/phases', (req, res) => {
  res.json({ phases: getPhaseTimeRanges() });
});

app.put('/api/heartbeat/phases', async (req, res) => {
  try {
    const { phase, start, end } = req.body || {};
    if (!phase) throw new Error('Phase is required');
    await updatePhaseTimeRange(phase, { start, end });
    broadcast('config', safeConfig());
    res.json({ ok: true, phases: getPhaseTimeRanges() });
  } catch (err) { res.status(400).json({ error: err.message }); }
});

// ── Permissions / Guardrails ───────────────────────────────────────
app.get('/api/permissions', (req, res) => {
  const sandboxId = req.query.sandboxId;
  if (sandboxId) return res.json(getPermissionsForSandbox(sandboxId));
  res.json(getPermissions());
});

app.put('/api/permissions', async (req, res) => {
  try {
    const { sandboxId, ...permBody } = req.body || {};
    if (sandboxId) {
      await updatePermissionsForSandbox(sandboxId, permBody);
    } else {
      await updatePermissions(permBody);
    }
    broadcast('config', safeConfig());
    res.json({ ok: true });
  } catch (err) { res.status(400).json({ error: err.message }); }
});

// ── Plugins ────────────────────────────────────────────────────────
app.get('/api/plugins', (req, res) => {
  const config = getConfig();
  res.json(config.plugins || {});
});

app.get('/api/plugins/:name', (req, res) => {
  const sandboxId = req.query.sandboxId;
  const plugin = sandboxId ? getPluginForSandbox(sandboxId, req.params.name) : getPlugin(req.params.name);
  res.json(plugin || {});
});

app.put('/api/plugins/:name', async (req, res) => {
  try {
    const { sandboxId, ...pluginBody } = req.body || {};
    if (sandboxId) await updatePluginForSandbox(sandboxId, req.params.name, pluginBody);
    else await updatePlugin(req.params.name, pluginBody);
    broadcast('config', safeConfig());
    res.json({ ok: true });
  } catch (err) { res.status(400).json({ error: err.message }); }
});

app.post('/api/plugins/slack/test', async (req, res) => {
  try {
    const sandboxId = req.body?.sandboxId || req.query.sandboxId;
    const slack = sandboxId ? getPluginForSandbox(sandboxId, 'slack') : getPlugin('slack');
    if (!slack?.webhookUrl) return res.status(400).json({ error: 'No Slack webhook URL configured' });
    const { default: axios } = await import('axios');
    await axios.post(slack.webhookUrl, {
      text: ':robot_face: *Prophet Agent* - Test notification\nSlack integration is working!',
      channel: slack.channel || undefined,
    }, { timeout: 5000 });
    res.json({ ok: true });
  } catch (err) { res.status(500).json({ error: 'Failed to send test message: ' + err.message }); }
});

// ── Portfolio Proxy ────────────────────────────────────────────────
app.get('/api/portfolio/account', async (req, res) => {
  try {
    const client = getGoClientForSandbox(req.query.sandboxId);
    if (!client) return res.status(404).json({ error: 'Sandbox trading backend unavailable' });
    const { data } = await client.get('/api/v1/account');
    res.json(data);
  } catch { res.status(502).json({ error: 'Trading bot unavailable' }); }
});

app.get('/api/portfolio/positions', async (req, res) => {
  try {
    const client = getGoClientForSandbox(req.query.sandboxId);
    if (!client) return res.status(404).json({ error: 'Sandbox trading backend unavailable' });
    const { data } = await client.get('/api/v1/options/positions');
    res.json(data);
  } catch { res.status(502).json({ error: 'Trading bot unavailable' }); }
});

app.get('/api/portfolio/orders', async (req, res) => {
  try {
    const client = getGoClientForSandbox(req.query.sandboxId);
    if (!client) return res.status(404).json({ error: 'Sandbox trading backend unavailable' });
    const { data } = await client.get('/api/v1/orders');
    res.json(data);
  } catch { res.status(502).json({ error: 'Trading bot unavailable' }); }
});

// ── Auth (OpenCode) ────────────────────────────────────────────────
app.get('/api/auth/status', (req, res) => {
  // API key in env is the fastest check
  if (process.env.CLAUDE_API_KEY) {
    return res.json({
      loggedIn: true,
      authMethod: 'api_key',
      provider: 'opencode',
      raw: 'CLAUDE_API_KEY set in environment',
    });
  }
  try {
    const out = execSync('opencode auth list 2>&1', { timeout: 5000, encoding: 'utf-8' });
    // Parse the table output - look for "Anthropic" with "oauth" or any credential
    const hasAnthropicAuth = out.includes('Anthropic') && (out.includes('oauth') || out.includes('api-key'));
    res.json({
      loggedIn: hasAnthropicAuth,
      authMethod: hasAnthropicAuth ? 'opencode_oauth' : 'none',
      provider: 'opencode',
      raw: out.replace(/\x1b\[[0-9;]*m/g, '').trim(), // strip ANSI codes
    });
  } catch (err) {
    const output = (err.stdout || err.stderr || err.message || '').replace(/\x1b\[[0-9;]*m/g, '');
    res.json({ loggedIn: false, provider: 'opencode', raw: output.substring(0, 200) });
  }
});

app.post('/api/auth/login', (req, res) => {
  // Spawn opencode auth login and capture the URL
  const proc = spawn(OPENCODE_BIN, [...OPENCODE_WIN_PREFIX, 'auth', 'login'], {
    stdio: ['pipe', 'pipe', 'pipe'],
    env: { ...process.env, BROWSER: 'echo' }, // prevent auto-opening browser
  });

  let output = '';
  let urlSent = false;

  const sendUrl = (data) => {
    output += data.toString();
    // Look for any OAuth/auth URL
    const match = output.match(/(https:\/\/[^\s]+authorize[^\s]*)/);
    if (match && !urlSent) {
      urlSent = true;
      res.json({ ok: true, url: match[1] });
      proc.on('exit', (code) => {
        broadcast('agent_log', {
          message: code === 0 ? 'OpenCode authenticated successfully!' : 'Auth flow ended (code: ' + code + ')',
          level: code === 0 ? 'success' : 'warning',
          timestamp: new Date().toISOString(),
        });
      });
    }
  };

  proc.stdout.on('data', sendUrl);
  proc.stderr.on('data', sendUrl);

  // Also handle interactive prompts - pipe newline to accept defaults
  setTimeout(() => {
    try { proc.stdin.write('\n'); } catch {}
  }, 2000);

  // Timeout - if no URL found in 15s, return error
  setTimeout(() => {
    if (!urlSent) {
      proc.kill();
      res.status(500).json({ error: 'Timed out waiting for auth URL', output: output.substring(0, 500) });
    }
  }, 15000);
});

app.post('/api/auth/logout', (req, res) => {
  try {
    execSync('opencode auth logout 2>&1', { timeout: 10000, encoding: 'utf-8' });
    broadcast('agent_log', {
      message: 'OpenCode logged out.',
      level: 'info',
      timestamp: new Date().toISOString(),
    });
    res.json({ ok: true });
  } catch (err) {
    const output = err.stdout || err.stderr || err.message || '';
    res.status(500).json({ error: 'Logout failed: ' + output.substring(0, 200) });
  }
});

// ── Health ──────────────────────────────────────────────────────────
app.get('/api/health', async (req, res) => {
  let botHealthy = false;
  try {
    await goAxios.get('/health', { timeout: 3000 });
    botHealthy = true;
  } catch {}
  const account = getActiveAccount();
  const sandboxStates = getSandboxes().map(sandbox => ({
    sandboxId: sandbox.id,
    port: isActiveSandbox(sandbox.id) ? Number(TRADING_BOT_PORT) : orchestrator.getSandboxRuntime(sandbox.id)?.port || null,
    goReady: isActiveSandbox(sandbox.id) ? goReady : (orchestrator.getSandboxRuntime(sandbox.id)?.goReady || false),
    goPid: isActiveSandbox(sandbox.id) ? (goProc?.pid || null) : (orchestrator.getSandboxRuntime(sandbox.id)?.goProc?.pid || null),
    state: isActiveSandbox(sandbox.id) ? harness.state.toJSON() : (orchestrator.getSandboxRuntime(sandbox.id)?.harness.state.toJSON() || null),
  }));
  res.json({
    agent: 'healthy',
    trading_bot: botHealthy ? 'healthy' : 'unavailable',
    trading_bot_managed: goProc !== null,
    activeAccount: account ? { name: account.name, paper: account.paper } : null,
    uptime: process.uptime(),
    state: harness.state.toJSON(),
    sandboxes: sandboxStates,
    scheduler: scheduler.getStatus(),
  });
});

app.post('/api/scheduler/trigger', async (req, res) => {
  const { job, date } = req.body || {};
  if (!job) return res.status(400).json({ error: 'job is required (daily_briefing or weekly_screeners)' });
  const result = await scheduler.triggerJob(job, date || null);
  if (result?.error) return res.status(400).json(result);
  res.json(result);
});

// Serve static files (after API routes)
app.use(express.static(path.join(__dirname, 'public')));

// SPA fallback - serve index.html for non-API routes
app.use((req, res, next) => {
  if (!req.path.startsWith('/api/') && req.method === 'GET') {
    res.sendFile(path.join(__dirname, 'public', 'index.html'));
  } else {
    next();
  }
});

// ── Start Server ───────────────────────────────────────────────────

for (const sandbox of getSandboxes()) {
  if (!isActiveSandbox(sandbox.id)) {
    const runtime = orchestrator.ensureRuntime(sandbox.id);
    bindOperationalHooks(runtime.harness);
  }
}

// Start Go backend with active account
const activeAccount = getActiveAccount();
if (activeAccount) {
  await startGoBackend(activeAccount);
} else {
  console.log('  No active account configured — Go backend not started');
}

await scheduler.start();
scheduler.runStartupChecks().catch(() => {});

// Graceful shutdown
process.on('SIGTERM', async () => {
  console.log('\n  Shutting down...');
  scheduler.stop();
  await harness.stop();
  await orchestrator.shutdown();
  await stopGoBackend();
  process.exit(0);
});
process.on('SIGINT', async () => {
  console.log('\n  Shutting down...');
  scheduler.stop();
  await harness.stop();
  await orchestrator.shutdown();
  await stopGoBackend();
  process.exit(0);
});

app.listen(PORT, '0.0.0.0', () => {
  console.log(`\n  Prophet Agent Dashboard: http://localhost:${PORT}`);
  console.log(`  Network:                http://0.0.0.0:${PORT}`);
  console.log(`  Trading Bot Backend:    ${TRADING_BOT_URL}`);
  console.log(`  Active Account:         ${activeAccount?.name || 'none'}\n`);
});
