// Prophet Agent Harness - Autonomous trading agent with phased heartbeat
// Uses claude CLI subprocess for auth (OAuth/API key handled by CLI)
import { spawn, execSync } from 'child_process';
import { EventEmitter } from 'events';
import fs from 'fs/promises';
import path from 'path';
import { renderToolMenu } from './tool-catalog.js';
import { DEFAULT_AGENT_MODEL, DEFAULT_MAX_TOOL_ROUNDS, BEAT_TIMEOUT_MS, SIGKILL_GRACE_MS, BEAT_BACKOFF } from './defaults.js';

// Default max tool rounds; overridden by permissions config at runtime

// ── Phase Configuration ────────────────────────────────────────────
export const PHASE_DEFAULTS = {
  pre_market:   { seconds: 900,  label: 'Pre-Market',    range: [240, 570]  },
  market_open:  { seconds: 120,  label: 'Market Open',   range: [570, 630]  },
  midday:       { seconds: 600,  label: 'Midday',        range: [630, 900]  },
  market_close: { seconds: 120,  label: 'Market Close',  range: [900, 960]  },
  after_hours:  { seconds: 1800, label: 'After Hours',   range: [960, 1200] },
  closed:       { seconds: 3600, label: 'Markets Closed', range: null },
};

export function getCurrentPhase() {
  const now = new Date();
  const et = now.toLocaleTimeString('en-US', { timeZone: 'America/New_York', hour: '2-digit', minute: '2-digit', hour12: false });
  const [h, m] = et.split(':').map(Number);
  const mins = h * 60 + m;
  const weekday = now.getDay() >= 1 && now.getDay() <= 5;
  if (!weekday) return 'closed';
  for (const [phase, cfg] of Object.entries(PHASE_DEFAULTS)) {
    if (cfg.range && mins >= cfg.range[0] && mins < cfg.range[1]) return phase;
  }
  return 'closed';
}

// ── System Prompt Builder ──────────────────────────────────────────
export async function buildSystemPrompt(agentConfig, options = {}) {
  const { getStrategyById = () => null } = options;
  let strategyRules = '';
  if (agentConfig.customStrategyRules) {
    strategyRules = agentConfig.customStrategyRules;
  } else if (agentConfig.strategyId) {
    const strategy = getStrategyById(agentConfig.strategyId);
    if (strategy) {
      if (strategy.rulesFile) {
        try { strategyRules = await fs.readFile(path.join(process.cwd(), strategy.rulesFile), 'utf-8'); } catch (err) { console.error(`Warning: Failed to load strategy rules file "${strategy.rulesFile}":`, err.message); }
      } else if (strategy.customRules) {
        strategyRules = strategy.customRules;
      }
    }
  }

  // Build trading rules from strategy
  let tradingRules = strategyRules;
  if (!tradingRules) {
    try { tradingRules = await fs.readFile(path.join(process.cwd(), 'TRADING_RULES.md'), 'utf-8'); } catch (err) { tradingRules = ''; }
  }

  // Layer 1: Agent Identity (custom or default)
  const identity = (agentConfig.systemPromptTemplate === 'custom' && agentConfig.customSystemPrompt)
    ? agentConfig.customSystemPrompt
    : `You are ${agentConfig.name || 'Prophet'}, an autonomous AI trading agent operating a real brokerage account with no human approving your actions in real time. You wake on a heartbeat, read the market, manage your open positions, and place trades yourself.

${agentConfig.description || 'You are a disciplined trading agent.'}

Your mandate, in strict priority order:
1. Preserve capital. A trade you don't take can't lose money — avoiding ruin comes before any gain.
2. Trade only with an edge: a specific, falsifiable thesis backed by current data and how your own past setups actually resolved.
3. Compound consistently. Many small, disciplined wins beat rare large gambles.

You are evidence-based, risk-first, and decisive. You do not trade out of boredom, revenge, or FOMO, and you never invent facts you haven't verified with a tool.`;

  // Layer 2: Strategy Rules
  const rulesBlock = tradingRules
    ? `## Strategy Rules\nThese are the hard rules you MUST follow. They define what you can trade, position sizes, risk limits, and exit criteria.\n\n${tradingRules}`
    : '## No Strategy Rules Assigned\nNo trading rules have been configured. Use conservative defaults: max 10% per position, always use limit orders, maintain 50%+ cash.';

  // Layer 3: System Instructions (tools, heartbeat, operational)
  const systemInstructions = `## Available Tools

${renderToolMenu()}

Call a tool for every fact. Never assume your balance, buying power, positions, prices, or the news from memory — if you haven't checked it this heartbeat, you don't know it.

## Your Heartbeat Loop
Each time you wake, work this loop in order and stop once you've acted or confirmed there's nothing to do:
1. ORIENT — get_datetime; note the market phase and your current heartbeat interval.
2. ASSESS — get_account and get_positions. Know your cash, buying power, open risk, and P&L before deciding anything.
3. MANAGE FIRST — tend open positions before hunting new ones: check stops and targets, exit any thesis that has broken, take profits per your rules.
4. GATHER — only if capital is free to deploy, pull the specific intelligence your decision needs (news, quotes, technicals). Don't over-research.
5. RECALL — before opening any NEW position, call find_similar_setups with your thesis and weigh how similar past setups actually resolved.
6. DECIDE & ACT — place an order only if you have a stated edge AND the guardrails allow it. Use a limit price and always pass a \`thesis\` argument. Otherwise do nothing and say so.
7. RECORD — log_decision with the reasoning behind every trade; when a position closes, call store_trade_setup with the realized result so your memory compounds.

## Phase Playbook (ET)
- Pre-market (4–9:30): gather intelligence, build a watchlist and theses. Don't chase thin pre-market prints.
- Market open (9:30–10:30): execute planned entries into real liquidity; monitor closely.
- Midday (10:30–3): manage positions, tighten stops, avoid low-conviction churn.
- Market close (3–4): decide what to hold overnight vs. flatten, and act before the bell.
- After hours (4–8) / Closed: review, log, and plan. No impulsive after-hours trades.
Tune cadence with apply_heartbeat_profile ("active" | "passive" | "long_horizon" | "earnings_season" | "overnight" | "scalp") or set_heartbeat (seconds) — speed up when volatile, slow down when quiet.

## Risk Discipline (non-negotiable)
- Your Strategy Rules above and the per-heartbeat GUARDRAILS are HARD limits. Never work around them.
- Always use limit orders; never market into a position you can't price.
- Size for survival: respect max-position and max-deployed limits and keep dry powder.
- Every open position needs a pre-defined exit (stop and target). Cut losers at your stop without negotiation.
- One clear thesis per trade. If you can't state it in a sentence, don't take it.

## Operating Rules
- You are autonomous: NEVER ask the user a question or wait for approval — use your tools and act.
- Be decisive and brief. Don't burn heartbeats on analysis paralysis; if nothing meets your criteria, say "no action" and why in a line or two.
- Each heartbeat is independent — re-establish state from tools; don't rely on stale assumptions.
- Report what you did (or deliberately didn't) and why, concisely.`;

  return `${identity}\n\n${rulesBlock}\n\n${systemInstructions}`;
}

// ── Check CLI auth ─────────────────────────────────────────────────
export function checkCliAuth() {
  // API key in env takes precedence — OpenCode picks it up automatically
  if (process.env.ANTHROPIC_API_KEY) return true;
  try {
    const out = execSync('opencode auth list 2>&1', { timeout: 5000, encoding: 'utf-8' });
    // Look for Anthropic credential (oauth or env) in the output
    return out.includes('Anthropic');
  } catch {
    return false;
  }
}

// ── Agent State ────────────────────────────────────────────────────
export class AgentState extends EventEmitter {
  constructor() {
    super();
    this.running = false;
    this.paused = false;
    this.phase = 'closed';
    this.heartbeatSeconds = 120;
    this.heartbeatOverride = null;
    this.beatCount = 0;
    this.lastBeatTime = null;
    this.nextBeatTime = null;
    this.activeAgentId = null;
    this.activeAccountId = null;
    this.activeModel = null;
    this.recentTrades = [];
    this.stats = {
      totalBeats: 0,
      toolCalls: 0,
      trades: 0,
      errors: 0,
      startedAt: null,
    };
  }

  addTrade(trade) {
    this.recentTrades.unshift({ ...trade, timestamp: new Date().toISOString() });
    if (this.recentTrades.length > 50) this.recentTrades.pop();
    this.emit('trade', trade);
  }

  toJSON() {
    return {
      running: this.running,
      paused: this.paused,
      phase: this.phase,
      phaseLabel: PHASE_DEFAULTS[this.phase]?.label || 'Unknown',
      heartbeatSeconds: this.heartbeatSeconds,
      heartbeatOverride: this.heartbeatOverride,
      beatCount: this.beatCount,
      lastBeatTime: this.lastBeatTime,
      nextBeatTime: this.nextBeatTime,
      activeAgentId: this.activeAgentId,
      activeAccountId: this.activeAccountId,
      activeModel: this.activeModel,
      recentTrades: this.recentTrades.slice(0, 10),
      stats: this.stats,
    };
  }
}

// ── Agent Harness (CLI subprocess) ─────────────────────────────────
export class AgentHarness {
  constructor(options = {}) {
    const {
      sandboxId = null,
      accountId = null,
      getSandbox = () => null,
      getAccount = () => null,
      getAgent = () => null,
      getResolvedAgent = null,
      getStrategyById = () => null,
      getHeartbeatForPhase = () => null,
      getPermissions = () => ({}),
      chatStore = null,
      opencodeEnv = {},
      checkCliAuthFn = checkCliAuth,
      getCurrentPhaseFn = getCurrentPhase,
    } = options;

    this.state = new AgentState();
    this.sandboxId = sandboxId;
    this.accountId = accountId;
    this.getSandbox = getSandbox;
    this.getAccount = getAccount;
    this.getAgent = getAgent;
    this.getResolvedAgent = getResolvedAgent;
    this.getStrategyById = getStrategyById;
    this.getHeartbeatForPhase = getHeartbeatForPhase;
    this.getPermissions = getPermissions;
    this.chatStore = chatStore;
    this.opencodeEnv = opencodeEnv;
    this.checkCliAuthFn = checkCliAuthFn;
    this.getCurrentPhaseFn = getCurrentPhaseFn;
    this.systemPrompt = '';
    this._timer = null;
    this._beating = false;
    this._agentConfig = null;
    this._sandboxConfig = null;
    this._sessionId = null; // persist session across beats for context
    this._proc = null;       // current opencode subprocess
    this._pendingMessages = [];
    this._interrupted = false;
    this._beatTimeout = null;
    this._sessionEpoch = 0;
    this._consecutiveErrors = 0;
  }

  _resolveSandbox() {
    return this.getSandbox(this.sandboxId);
  }

  _resolveAccount() {
    const sandbox = this._resolveSandbox();
    return this.getAccount(this.accountId || sandbox?.accountId || null);
  }

  _resolveAgent() {
    if (this.getResolvedAgent) {
      return this.getResolvedAgent(this.sandboxId);
    }
    const sandbox = this._resolveSandbox();
    const agentId = sandbox?.agent?.activeAgentId || null;
    return agentId ? this.getAgent(agentId) : null;
  }

  _resolvePermissions() {
    return this.getPermissions(this.sandboxId) || {};
  }

  async _persistSession(sessionId, metadata = {}) {
    if (!this.chatStore || !sessionId || !this.state.activeAccountId) return;
    await this.chatStore.startSession(this.state.activeAccountId, sessionId, {
      sandboxId: this.sandboxId,
      accountId: this.state.activeAccountId,
      agentId: this.state.activeAgentId,
      agentName: this._agentConfig?.name,
      model: this.state.activeModel,
      ...metadata,
    });
  }

  async _persistMessages(sessionId, messages = []) {
    if (!this.chatStore || !sessionId || !this.state.activeAccountId) return;
    for (const message of messages) {
      if (!message?.content?.trim()) continue;
      await this.chatStore.addMessage(this.state.activeAccountId, sessionId, message);
    }
  }

  async start() {
    if (this.state.running) return;

    // Check CLI auth
    if (!this.checkCliAuthFn()) {
      throw new Error('OpenCode not authenticated. Run "opencode auth login" or set ANTHROPIC_API_KEY in .env');
    }

    await this.reloadConfig({ resetSession: true, silent: true });

    this.state.running = true;
    this.state.paused = false;
    this.state.stats = { totalBeats: 0, toolCalls: 0, trades: 0, errors: 0, startedAt: new Date().toISOString() };
    this.state.beatCount = 0;
    this.state.recentTrades = [];
    this._sessionId = null;

    const account = this._resolveAccount();
    const model = this.state.activeModel;
    this.state.emit('status', { status: 'started', sandboxId: this.sandboxId, agent: this._agentConfig.name, model, account: account?.name });
    this.state.emit('agent_log', {
      message: `Agent "${this._agentConfig.name}" started on ${model}${account ? ` | Account: ${account.name}` : ''}${this.sandboxId ? ` | Sandbox: ${this.sandboxId}` : ''}`,
      level: 'success',
    });

    await this._beat();
    this._scheduleNext();
  }

  async reloadConfig(options = {}) {
    const { resetSession = true, silent = false } = options;

    this._sandboxConfig = this._resolveSandbox();
    if (!this._sandboxConfig) throw new Error(`Sandbox not found: ${this.sandboxId || 'unknown'}`);

    this._agentConfig = this._resolveAgent();
    if (!this._agentConfig) throw new Error(`Agent not found for sandbox ${this._sandboxConfig.id}`);

    const account = this._resolveAccount();
    const model = this._agentConfig.model || this._sandboxConfig.agent?.model || DEFAULT_AGENT_MODEL;

    this.state.activeAgentId = this._agentConfig.id;
    this.state.activeAccountId = account?.id || this._sandboxConfig.accountId || null;
    this.state.activeModel = model;
    this.systemPrompt = await buildSystemPrompt(this._agentConfig, {
      getStrategyById: this.getStrategyById,
    });

    if (resetSession) {
      this._sessionId = null;
      this._sessionEpoch += 1;
    }
    if (!silent) {
      this.state.emit('agent_log', {
        message: `Sandbox config reloaded for ${this.sandboxId}. Prompt changes apply on the next beat.`,
        level: 'info',
      });
    }
  }

  async stop() {
    this.state.running = false;
    if (this._timer) { clearTimeout(this._timer); this._timer = null; }
    if (this._beatTimeout) { clearTimeout(this._beatTimeout); this._beatTimeout = null; }
    this._sessionEpoch += 1;
    this._sessionId = null;

    const procToKill = this._proc;
    if (procToKill && !procToKill.killed) {
      procToKill.kill('SIGTERM');
      setTimeout(() => {
        if (!procToKill.killed) procToKill.kill('SIGKILL');
      }, 2000);
    }

    await new Promise((resolve) => {
      const started = Date.now();
      const check = () => {
        if (!this._beating && (!procToKill || procToKill.killed || this._proc !== procToKill)) return resolve();
        if (Date.now() - started > 5000) return resolve();
        setTimeout(check, 100);
      };
      check();
    });

    this.state.emit('status', { status: 'stopped' });
    this.state.emit('agent_log', { message: 'Agent stopped.', level: 'warning' });
  }

  pause() {
    this.state.paused = true;
    this.state.emit('status', { status: 'paused' });
    this.state.emit('agent_log', { message: 'Agent paused.', level: 'warning' });
  }

  resume() {
    this.state.paused = false;
    this.state.emit('status', { status: 'resumed' });
    this.state.emit('agent_log', { message: 'Agent resumed.', level: 'success' });
  }

  /**
   * Send a user message to the agent.
   * - If idle: triggers an immediate ad-hoc beat with the message.
   * - If busy: interrupts the current beat (kills the opencode process),
   *   then resumes the SAME session with the user's message so context is preserved.
   */
  async sendMessage(message) {
    if (!this.state.running) {
      throw new Error('Agent is not running. Start the agent first.');
    }

    const trimmed = message.trim();
    
    // Handle slash commands locally
    if (trimmed.startsWith('/')) {
      const cmd = trimmed.split(' ')[0].toLowerCase();
      const parts = trimmed.split(' ');
      
      if (cmd === '/help' || cmd === '/?') {
        this.state.emit('agent_log', { message: 'Available commands:\n/newagent - Create new agent\n/editagent <id> - Edit agent\n/agents - List agents\n/sandboxes - List portfolios\n/status - Portfolio status\n/portfolios - Portfolio status', level: 'info' });
        return { sent: true };
      }
      
      if (cmd === '/agents') {
        const agents = this._agentConfig ? [this._agentConfig] : [];
        this.state.emit('agent_log', { message: 'Agents: ' + agents.map(a => a.name || a.id).join(', ') + '\nUse /editagent <id> to edit', level: 'info' });
        return { sent: true };
      }
      
      if (cmd === '/newagent') {
        this.state.emit('agent_log', { message: 'Opening agent builder... Type /help for other commands.', level: 'info' });
        return { sent: true };
      }
    }

    this.state.emit('agent_log', {
      message: `User: ${message}`,
      level: 'info',
    });

    if (this._beating) {
      // Interrupt the current beat
      this.state.emit('agent_log', {
        message: 'Interrupting current beat to handle user message...',
        level: 'warning',
      });

      this._interrupted = true;

      // Kill the running opencode subprocess — capture ref so the
      // SIGKILL fallback targets THIS process, not a future one.
      const procToKill = this._proc;
      if (procToKill && !procToKill.killed) {
        procToKill.kill('SIGTERM');
        setTimeout(() => {
          if (!procToKill.killed) {
            procToKill.kill('SIGKILL');
          }
        }, 2000);
      }

      // Fire the follow-up beat asynchronously (don't await — return immediately)
      this._interruptAndResume(message);
      return { interrupted: true, sent: true };
    }

    // Not busy — cancel the scheduled next beat and run immediately
    if (this._timer) { clearTimeout(this._timer); this._timer = null; }

    // Also fire async so the HTTP response returns fast
    this._fireAdHocBeat(message);
    return { sent: true };
  }

  /** Wait for current beat to die, then run ad-hoc beat with user message */
  async _interruptAndResume(message) {
    // Wait for the current beat to finish its cleanup
    await new Promise((resolve) => {
      const check = () => {
        if (!this._beating) return resolve();
        setTimeout(check, 100);
      };
      check();
    });

    if (!this.state.running) return;
    if (this._timer) { clearTimeout(this._timer); this._timer = null; }
    await this._adHocBeat(message);
    this._scheduleNext();
  }

  /** Cancel scheduled beat and fire an ad-hoc beat, then reschedule */
  async _fireAdHocBeat(message) {
    if (!this.state.running) return;
    if (this._timer) { clearTimeout(this._timer); this._timer = null; }
    await this._adHocBeat(message);
    this._scheduleNext();
  }

  async _adHocBeat(userMessage) {
    if (!this.state.running) return;
    if (this._beating) return;
    this._beating = true;

    const beatNum = ++this.state.beatCount;
    this.state.stats.totalBeats = beatNum;
    this.state.lastBeatTime = new Date().toISOString();
    const phase = this.getCurrentPhaseFn();
    this.state.phase = phase;
    const model = this.state.activeModel;

    // Drain any queued messages
    const queued = this._pendingMessages || [];
    this._pendingMessages = [];
    const allMessages = [userMessage, ...queued].filter(Boolean);
    const userBlock = allMessages.map(m => `USER MESSAGE: ${m}`).join('\n');

    this._isMessageBeat = true;
    this.state.emit('beat_start', { beat: beatNum, phase, time: this.state.lastBeatTime, isMessage: true });

    const prompt = `[MESSAGE BEAT #${beatNum}] Phase: ${PHASE_DEFAULTS[phase].label}. Time: ${new Date().toLocaleString('en-US', { timeZone: 'America/New_York' })} ET.

The user/operator is sending you a direct message. Read it carefully and respond with a complete answer. You MUST respond with useful output - never just ask questions back without providing value first.

## Your Available Tools (DO NOT call get_agent_config to discover these)

${renderToolMenu()}

## Instructions
- If the user asks to create a new agent: use create_agent to create it, then optionally create_strategy for its rules, then assign_agent_to_sandbox to activate it.
- If the user asks to update the current agent: use update_agent_prompt and update_strategy_rules.
- If the user asks about trading, use your trading/market data tools.
- Always respond with concrete actions and details. Never ask what tools you have - they are listed above.

${userBlock}`;

    try {
      const result = await this._runClaude(prompt, model);
      if (result.error) throw new Error(result.error);
      // Text already streamed via _handleOpenCodeEvent agent_text events
      if (result.toolCalls) this.state.stats.toolCalls += result.toolCalls;
      const effectiveSessionId = result.sessionEpoch === this._sessionEpoch ? result.sessionId : null;
      if (effectiveSessionId) this._sessionId = effectiveSessionId;
      await this._persistSession(effectiveSessionId, { mode: 'message' });
      await this._persistMessages(effectiveSessionId, [
        ...allMessages.map(content => ({ role: 'user', kind: 'message', beat: beatNum, content })),
        { role: 'assistant', kind: 'message', beat: beatNum, toolCalls: result.toolCalls || 0, content: result.text || '' },
      ]);
    } catch (err) {
      this.state.stats.errors++;
      this.state.emit('agent_log', { message: `Message beat error: ${err.message}`, level: 'error' });
    }

    this.state.emit('beat_end', { beat: beatNum, phase, isMessage: true });
    this._isMessageBeat = false;
    this._beating = false;
  }

  _getHeartbeatSeconds() {
    const base = this._baseHeartbeatSeconds();
    // Exponential backoff after repeated beat failures: slow the loop so a broken
    // dependency (auth, broker, opencode) isn't hammered. Caps at 16x / 1 hour.
    if (this._consecutiveErrors >= BEAT_BACKOFF.threshold) {
      const factor = Math.min(2 ** (this._consecutiveErrors - 2), BEAT_BACKOFF.factor);
      return Math.min(base * factor, BEAT_BACKOFF.capSeconds);
    }
    return base;
  }

  _baseHeartbeatSeconds() {
    if (this.state.heartbeatOverride) {
      const override = this.state.heartbeatOverride;
      if (override.oneTime) this.state.heartbeatOverride = null;
      return override.seconds;
    }
    const phase = this.getCurrentPhaseFn();
    this.state.phase = phase;
    // Agent-level overrides take priority, then global config, then hardcoded defaults
    if (this._agentConfig?.heartbeatOverrides?.[phase]) {
      return this._agentConfig.heartbeatOverrides[phase];
    }
    return this.getHeartbeatForPhase(this.sandboxId, phase) || PHASE_DEFAULTS[phase]?.seconds || 600;
  }

  _scheduleNext() {
    if (!this.state.running) return;
    // Always clear any existing timer first to prevent dual timers
    if (this._timer) { clearTimeout(this._timer); this._timer = null; }
    const seconds = this._getHeartbeatSeconds();
    this.state.heartbeatSeconds = seconds;
    this.state.nextBeatTime = new Date(Date.now() + seconds * 1000).toISOString();
    this.state.emit('schedule', { seconds, nextBeat: this.state.nextBeatTime, phase: this.state.phase });

    this._timer = setTimeout(async () => {
      if (!this.state.running) return;
      if (this.state.paused) {
        this.state.emit('agent_log', { message: 'Beat skipped (paused)', level: 'info' });
        this._scheduleNext();
        return;
      }
      await this._beat();
      this._scheduleNext();
    }, seconds * 1000);
  }

  async _beat() {
    if (this._beating) return;
    this._beating = true;

    const beatNum = ++this.state.beatCount;
    this.state.stats.totalBeats = beatNum;
    this.state.lastBeatTime = new Date().toISOString();
    const phase = this.getCurrentPhaseFn();
    this.state.phase = phase;
    const model = this.state.activeModel;

    this.state.emit('beat_start', { beat: beatNum, phase, time: this.state.lastBeatTime });
    this.state.emit('agent_log', {
      message: `--- Heartbeat #${beatNum} | ${PHASE_DEFAULTS[phase].label} ---`,
      level: 'info',
    });

    // Build permissions context for the prompt
    const perms = this._resolvePermissions();
    const permLines = [];
    if (!perms.allowLiveTrading) permLines.push('READ-ONLY MODE: Do NOT place any orders. Analysis and monitoring only.');
    if (!perms.allowOptions) permLines.push('Options trading is DISABLED.');
    if (!perms.allowStocks) permLines.push('Stock trading is DISABLED.');
    if (!perms.allow0DTE) permLines.push('0DTE options are NOT allowed.');
    if (perms.maxPositionPct) permLines.push(`Max position size: ${perms.maxPositionPct}% of portfolio.`);
    if (perms.maxDeployedPct) permLines.push(`Max deployed capital: ${perms.maxDeployedPct}%.`);
    if (perms.maxDailyLoss) permLines.push(`Max daily loss: ${perms.maxDailyLoss}% — auto-pause if exceeded.`);
    if (perms.maxOpenPositions) permLines.push(`Max open positions: ${perms.maxOpenPositions}.`);
    if (perms.maxOrderValue > 0) permLines.push(`Max single order value: $${perms.maxOrderValue}.`);
    if (perms.blockedTools?.length) permLines.push(`Blocked tools (do NOT call): ${perms.blockedTools.join(', ')}.`);
    const permStr = permLines.length ? '\n\nGUARDRAILS:\n' + permLines.join('\n') : '';

    const prompt = `[HEARTBEAT #${beatNum}] Phase: ${PHASE_DEFAULTS[phase].label}. Time: ${new Date().toLocaleString('en-US', { timeZone: 'America/New_York' })} ET. Current heartbeat interval: ${this.state.heartbeatSeconds}s.${permStr}\n\nPerform your duties for this phase.`;

    try {
      const result = await this._runClaude(prompt, model);
      if (result.error) {
        throw new Error(result.error);
      }
      // Text already streamed via _handleOpenCodeEvent agent_text events
      if (result.toolCalls) this.state.stats.toolCalls += result.toolCalls;

      // Save session ID for continuation
      const effectiveSessionId = result.sessionEpoch === this._sessionEpoch ? result.sessionId : null;
      if (effectiveSessionId) this._sessionId = effectiveSessionId;
      await this._persistSession(effectiveSessionId, { mode: 'heartbeat' });
      await this._persistMessages(effectiveSessionId, [
        { role: 'assistant', kind: 'heartbeat', beat: beatNum, phase, toolCalls: result.toolCalls || 0, content: result.text || '' },
      ]);

      this._consecutiveErrors = 0; // clean beat clears any backoff

    } catch (err) {
      this.state.stats.errors++;
      this._consecutiveErrors++;
      this.state.emit('agent_log', { message: `Beat #${beatNum} error: ${err.message}`, level: 'error' });
      if (this._consecutiveErrors >= BEAT_BACKOFF.threshold) {
        this.state.emit('agent_log', { message: `${this._consecutiveErrors} consecutive beat failures — backing off the next heartbeat.`, level: 'warning' });
      }
      console.error(`Beat #${beatNum} error:`, err);
    }

    this.state.emit('beat_end', { beat: beatNum, phase });
    
    // Reset session if sessionMode is 'fresh' - start fresh each beat
    if (this._agentConfig?.sessionMode === 'fresh') {
      this._sessionId = null;
      this.state.emit('agent_log', { message: '[session] Resetting for fresh start next beat', level: 'info' });
    }
    
    this._beating = false;
  }

  /**
   * Run opencode as subprocess with MCP tools and stream JSON events
   */
  _runClaude(prompt, model) {
    return new Promise((resolve, reject) => {
      const sessionEpoch = this._sessionEpoch;
      // OpenCode model format: anthropic/claude-sonnet-4-6
      const ocModel = model?.includes('/') ? model : `anthropic/${model || 'claude-sonnet-4-6'}`;

      // Check max tool rounds from permissions
      const perms = this._resolvePermissions();
      const maxToolRounds = perms.maxToolRoundsPerBeat || DEFAULT_MAX_TOOL_ROUNDS;

      const args = [
        'run',
        '--format', 'json',
        '--model', ocModel,
      ];

      // Continue session if we have one
      if (this._sessionId) {
        args.push('--session', this._sessionId);
      }

      // Only include system prompt on first beat (new session) — subsequent beats
      // on the same session already have it in context, saving ~2000 tokens/beat
      const isNewSession = !this._sessionId;
      const fullPrompt = isNewSession
        ? `[SYSTEM INSTRUCTIONS - Follow these at all times]\n${this.systemPrompt}\n\n[END SYSTEM INSTRUCTIONS]\n\n${prompt}`
        : prompt;

      if (!this._isMessageBeat) {
        this.state.emit('agent_log', {
          message: `Spawning opencode (${ocModel})...`,
          level: 'info',
        });
      }

      const proc = spawn('opencode', args, {
        cwd: process.cwd(),
        env: {
          ...process.env,
          ...this.opencodeEnv,
          OPENPROPHET_SANDBOX_ID: this.sandboxId || '',
          OPENPROPHET_ACCOUNT_ID: this.state.activeAccountId || this.accountId || '',
        },
        stdio: ['pipe', 'pipe', 'pipe'],
      });

      // Store reference so sendMessage() can kill it for interrupts
      this._proc = proc;
      this._interrupted = false;

      // Write prompt via stdin then close it
      proc.stdin.on('error', (err) => {
        this.state.emit('agent_log', { message: `stdin error: ${err.message}`, level: 'error' });
      });
      proc.stdin.write(fullPrompt);
      proc.stdin.end();

      let fullText = '';
      let toolCalls = 0;
      let sessionId = null;
      let buffer = '';
      let totalCost = 0;
      let totalTokens = 0;

      proc.stdout.on('data', (chunk) => {
        buffer += chunk.toString();
        const lines = buffer.split('\n');
        buffer = lines.pop(); // keep incomplete line in buffer

        for (const line of lines) {
          if (!line.trim()) continue;
          try {
            const event = JSON.parse(line);
            this._handleOpenCodeEvent(event, {
              addToolCall: () => toolCalls++,
              addText: (t) => { fullText += t; },
              setSession: (id) => { sessionId = id; },
              addCost: (c) => { totalCost += c; },
              addTokens: (t) => { totalTokens += t; },
            });
          } catch { /* skip unparseable lines */ }
        }
      });

      proc.stderr.on('data', (chunk) => {
        const msg = chunk.toString().trim();
        if (msg) {
          this.state.emit('agent_log', { message: `[opencode] ${msg}`, level: 'info' });
        }
      });

      proc.on('error', (err) => {
        reject(new Error(`Failed to spawn opencode: ${err.message}`));
      });

      proc.on('exit', (code, signal) => {
        // Clear proc reference and cancel safety timeout
        this._proc = null;
        if (this._beatTimeout) { clearTimeout(this._beatTimeout); this._beatTimeout = null; }

        // Process remaining buffer
        if (buffer.trim()) {
          try {
            const event = JSON.parse(buffer);
            this._handleOpenCodeEvent(event, {
              addToolCall: () => toolCalls++,
              addText: (t) => { fullText += t; },
              setSession: (id) => { sessionId = id; },
              addCost: (c) => { totalCost += c; },
              addTokens: (t) => { totalTokens += t; },
            });
          } catch {}
        }

        if (totalCost > 0) {
          this.state.emit('agent_log', {
            message: `Beat cost: $${totalCost.toFixed(4)} | Tokens: ${totalTokens}`,
            level: 'info',
          });
        }

        // If we were interrupted by a user message, resolve gracefully
        if (this._interrupted) {
          this.state.emit('agent_log', {
            message: `Beat interrupted by user (collected ${fullText.length} chars, ${toolCalls} tools before interrupt)`,
            level: 'warning',
          });
          resolve({ text: fullText, toolCalls, sessionId, interrupted: true, sessionEpoch });
          return;
        }

        this.state.emit('agent_log', {
          message: `opencode exited (code: ${code}, signal: ${signal}, text: ${fullText.length} chars, tools: ${toolCalls})`,
          level: code === 0 ? 'info' : 'warning',
        });

        if (code !== 0 && code !== null && !fullText) {
          resolve({ error: `opencode exited with code ${code} signal ${signal}`, text: fullText, toolCalls, sessionId, sessionEpoch });
        } else if (signal && !fullText) {
          resolve({ error: `opencode killed by ${signal}`, text: fullText, toolCalls, sessionId, sessionEpoch });
        } else {
          resolve({ text: fullText, toolCalls, sessionId, sessionEpoch });
        }
      });

      // Safety timeout - 5 minutes max per beat
      this._beatTimeout = setTimeout(() => {
        if (proc && !proc.killed) {
          this.state.emit('agent_log', { message: 'Beat timed out (5 min max), killing process.', level: 'warning' });
          proc.kill('SIGTERM');
          // Escalate to SIGKILL if the process ignores SIGTERM (otherwise the beat hangs forever).
          setTimeout(() => {
            if (proc && !proc.killed) {
              this.state.emit('agent_log', { message: 'Process ignored SIGTERM after timeout — sending SIGKILL.', level: 'error' });
              proc.kill('SIGKILL');
            }
          }, SIGKILL_GRACE_MS);
        }
      }, BEAT_TIMEOUT_MS);
    });
  }

  /**
   * Handle OpenCode JSON stream events:
   *   step_start  - new LLM turn
   *   text        - assistant text output
   *   tool_use    - tool call with input/output already resolved
   *   step_finish - turn complete with cost/token info
   */
  _handleOpenCodeEvent(event, ctx) {
    const beatNum = this.state.beatCount;

    // Capture session ID from any event
    if (event.sessionID && !this._sessionId) {
      ctx.setSession(event.sessionID);
    }

    switch (event.type) {
      case 'text': {
        const text = event.part?.text;
        if (text?.trim()) {
          ctx.addText(text);
          this.state.emit('agent_text', { text, beat: beatNum });
        }
        break;
      }

      case 'tool_use': {
        const part = event.part || {};
        const toolName = (part.tool || '??').replace('prophet_', ''); // strip MCP prefix for display
        const toolInput = part.state?.input || {};
        const toolOutput = part.state?.output || '';
        const fullToolName = part.tool || toolName;

        ctx.addToolCall();
        this.state.stats.toolCalls++;

        // Emit tool call event
        this.state.emit('tool_call', { name: toolName, args: toolInput, beat: beatNum });

        // Emit tool result
        const resultStr = typeof toolOutput === 'string' ? toolOutput : JSON.stringify(toolOutput);
        this.state.emit('tool_result', { name: toolName, result: resultStr.substring(0, 500), beat: beatNum });

        // Track trades
        if (fullToolName.includes('buy') || fullToolName.includes('sell') || fullToolName.includes('order') || fullToolName.includes('managed')) {
          this.state.stats.trades++;
          this.state.addTrade({
            type: 'order',
            tool: toolName,
            symbol: toolInput.symbol || '??',
            side: toolInput.side || (fullToolName.includes('buy') ? 'buy' : 'sell'),
            quantity: toolInput.quantity || toolInput.qty,
            price: toolInput.limit_price,
          });
        }
        break;
      }

      case 'step_finish': {
        const part = event.part || {};
        if (part.cost) ctx.addCost(part.cost);
        if (part.tokens?.total) ctx.addTokens(part.tokens.total);
        break;
      }

      case 'step_start':
        // Just informational, nothing to do
        break;
    }
  }
}
