// Prophet Agent Harness - Autonomous trading agent with phased heartbeat
// Uses claude CLI subprocess for auth (OAuth/API key handled by CLI)
import { spawn, execSync } from 'child_process';

const OPENCODE_BIN = process.platform === 'win32' ? 'cmd.exe' : 'opencode';
const OPENCODE_WIN_PREFIX = process.platform === 'win32' ? ['/c', 'opencode.cmd'] : [];
import { EventEmitter } from 'events';
import fs from 'fs/promises';
import path from 'path';
import { resolvePreflight } from './preflight.js';

// Default max tool rounds; overridden by permissions config at runtime

// ── Phase Configuration ────────────────────────────────────────────
export const PHASE_DEFAULTS = {
  pre_market:   { seconds: 900,  label: 'Pre-Market',    range: [240, 570]  },
  market_open:  { seconds: 120,  label: 'Market Open',   range: [570, 630]  },
  midday:       { seconds: 600,  label: 'Midday',        range: [630, 900]  },
  market_close: { seconds: 120,  label: 'Market Close',  range: [900, 960]  },
  after_hours:  { seconds: 1800, label: 'After Hours',   range: [960, 1200] },
  closed:       { seconds: 14400, label: 'Markets Closed', range: null },
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

// buildGuardrailBlock formats permission/limit lines for inclusion in the
// system prompt. Returns an empty string when there are no guardrails to
// surface. Pulled out of _beat() so the block can live in the system prompt
// (sent once per session) instead of being prepended every heartbeat.
//
// When permissions change, server.js calls reloadConfig({resetSession:true}),
// which rebuilds the system prompt — so guardrail edits still take effect on
// the next beat without per-beat re-injection.
export function buildGuardrailBlock(perms = {}) {
  const lines = [];
  if (!perms.allowLiveTrading) lines.push('READ-ONLY MODE: Do NOT place any orders. Analysis and monitoring only.');
  if (!perms.allowOptions) lines.push('Options trading is DISABLED.');
  if (!perms.allowStocks) lines.push('Stock trading is DISABLED.');
  if (!perms.allow0DTE) lines.push('0DTE options are NOT allowed.');
  if (perms.maxPositionPct) lines.push(`Max position size: ${perms.maxPositionPct}% of portfolio.`);
  if (perms.maxDeployedPct) lines.push(`Max deployed capital: ${perms.maxDeployedPct}%.`);
  if (perms.maxDailyLoss) lines.push(`Max daily loss: ${perms.maxDailyLoss}% — auto-pause if exceeded.`);
  if (perms.maxOpenPositions) lines.push(`Max open positions: ${perms.maxOpenPositions}.`);
  if (perms.maxOrderValue > 0) lines.push(`Max single order value: $${perms.maxOrderValue}.`);
  if (perms.blockedTools?.length) lines.push(`Blocked tools (do NOT call): ${perms.blockedTools.join(', ')}.`);
  return lines.length ? `## Guardrails\n${lines.join('\n')}` : '';
}

// ── System Prompt Builder ──────────────────────────────────────────
export async function buildSystemPrompt(agentConfig, options = {}) {
  const { getStrategyById = () => null, permissions = null } = options;
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
    : `You are ${agentConfig.name || 'Prophet'}, an autonomous AI trading agent. You run on a heartbeat loop — each time you wake up, you assess the market, manage positions, and decide what to do.\n\n${agentConfig.description || 'You are a disciplined trading agent'}\n\nYou are running autonomously — no human is approving your actions in real-time.`;

  // Layer 2: Strategy Rules
  const rulesBlock = tradingRules
    ? `## Strategy Rules\nThese are the hard rules you MUST follow. They define what you can trade, position sizes, risk limits, and exit criteria.\n\n${tradingRules}`
    : '## No Strategy Rules Assigned\nNo trading rules have been configured. Use conservative defaults: max 10% per position, always use limit orders, maintain 50%+ cash.';

  // Layer 3: System Instructions (tools, heartbeat, operational)
  const systemInstructions = `## Available Tools

Use your registered MCP tools — your tool list is already loaded. Do NOT call get_agent_config to discover them. \`read_latest_report\` accepts type = "daily_brief" | "weekly_regime" | "vcp" | "pead" | "scenario" | "review" | "market_alert".

## Heartbeat Behavior

Each heartbeat you should:
1. Check time and market status
2. Review account and positions
3. Decide and act based on phase:
   - Pre-market: ALWAYS (a) read_latest_report("daily_brief") for macro context and market posture, then (b) call get_marketwatch_bulletins AND get_quick_market_intelligence to scan for breaking news — sector contagion, earnings surprises, executive commentary, macro shocks, or any news about companies you hold or plan to trade. Only proceed to trade planning after news is reviewed.
   - Market open: Execute, monitor
   - Midday: Monitor positions, check stops; if any major news broke since open, reassess immediately
   - Market close: Review, decide holds
   - After hours: Call get_marketwatch_bulletins for any after-hours earnings or macro releases; review, log activity
4. Follow your strategy rules
5. Summarize what you did

Heartbeat control:
- apply_heartbeat_profile: "active" | "passive" | "long_horizon" | "earnings_season" | "overnight" | "scalp"
- set_heartbeat: override interval in seconds
- Phases (ET): pre_market 4-9:30am, market_open 9:30-10:30am, midday 10:30am-3pm, market_close 3-4pm, after_hours 4-8pm

## Operational Rules
- Be decisive. Analyze and act.
- Don't waste heartbeats on excessive analysis.
- If nothing to do, say so briefly.
- Always log trade reasoning with log_decision.
- NEVER ask the user questions. You are autonomous.
- If you need information, use your tools.
- Each heartbeat is independent - gather data, decide, act, summarize.`;

  const guardrailBlock = buildGuardrailBlock(permissions || {});
  const sections = [identity, rulesBlock, systemInstructions];
  if (guardrailBlock) sections.push(guardrailBlock);
  return sections.join('\n\n');
}

// ── Check CLI auth ─────────────────────────────────────────────────
export function checkCliAuth() {
  // API key in env takes precedence — OpenCode picks it up automatically
  if (process.env.CLAUDE_API_KEY) return true;
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
      skippedBeats: 0,
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
      getRuntime = null,
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
    this.getRuntime = getRuntime;
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
    this._emergencyQueued = null;
    this._emergencyReason = null;
    this._lastBeatPhase = null; // tracked across beats for sessionMode='daily' boundary detection
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
      throw new Error('OpenCode not authenticated. Run "opencode auth login" or set CLAUDE_API_KEY in .env');
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

    // Hybrid startup for scheduledBeats.exclusive agents: only fire the immediate
    // beat if we're inside the agent's accepted window (windowMinutes around any
    // scheduled time). Outside the window, the agent's own rules would reject the
    // beat anyway — skip it to avoid burning an LLM invocation on a noop.
    const sb = this._agentConfig?.scheduledBeats;
    if (sb?.exclusive && sb.times?.length && !this._isWithinScheduledWindow()) {
      const nextSecs = this._getSecondsToNextScheduledBeat();
      const nextBeat = nextSecs !== null ? new Date(Date.now() + nextSecs * 1000).toISOString() : 'unknown';
      this.state.emit('agent_log', {
        message: `Startup outside scheduled window — skipping immediate beat. Next beat: ${nextBeat} (in ${nextSecs !== null ? Math.round(nextSecs / 60) : '?'} min).`,
        level: 'info',
      });
    } else {
      await this._beat();
    }
    this._scheduleNext();
  }

  async reloadConfig(options = {}) {
    const { resetSession = true, silent = false } = options;

    this._sandboxConfig = this._resolveSandbox();
    if (!this._sandboxConfig) throw new Error(`Sandbox not found: ${this.sandboxId || 'unknown'}`);

    this._agentConfig = this._resolveAgent();
    if (!this._agentConfig) throw new Error(`Agent not found for sandbox ${this._sandboxConfig.id}`);

    const account = this._resolveAccount();
    const model = this._agentConfig.model || this._sandboxConfig.agent?.model || 'anthropic/claude-sonnet-4-6';

    this.state.activeAgentId = this._agentConfig.id;
    this.state.activeAccountId = account?.id || this._sandboxConfig.accountId || null;
    this.state.activeModel = model;
    this.systemPrompt = await buildSystemPrompt(this._agentConfig, {
      getStrategyById: this.getStrategyById,
      permissions: this._resolvePermissions(),
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

  emergencyWake(reason) {
    if (!this.state.running || this.state.paused) return;
    this._emergencyQueued = reason;
    if (this._beating) return;
    if (this._timer) { clearTimeout(this._timer); this._timer = null; }
    setImmediate(() => this._runBeatCycle());
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

## Available Tools

Use your registered MCP tools — your tool list is already loaded. Do NOT call get_agent_config to discover them.

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

  _getSecondsToNextPhaseBoundary() {
    const now = new Date();
    const weekday = now.getDay() >= 1 && now.getDay() <= 5;
    if (!weekday) return null;
    const etStr = now.toLocaleTimeString('en-US', {
      timeZone: 'America/New_York',
      hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
    });
    const [h, m, s] = etStr.split(':').map(Number);
    const nowSecs = (h * 60 + m) * 60 + s;
    const boundaries = Object.values(PHASE_DEFAULTS)
      .filter(cfg => cfg.range)
      .map(cfg => cfg.range[0] * 60)
      .sort((a, b) => a - b);
    for (const bSecs of boundaries) {
      if (bSecs > nowSecs) return bSecs - nowSecs;
    }
    return null;
  }

  // Returns ET wall-clock weekday (1=Mon..7=Sun) and seconds-since-midnight ET.
  // Used by scheduledBeats helpers; differs from _getSecondsToNextPhaseBoundary
  // which uses local-timezone weekday (kept as-is to avoid changing existing behavior).
  _getETNow() {
    const now = new Date();
    const dayName = now.toLocaleDateString('en-US', { timeZone: 'America/New_York', weekday: 'long' });
    const dayMap = { Monday: 1, Tuesday: 2, Wednesday: 3, Thursday: 4, Friday: 5, Saturday: 6, Sunday: 7 };
    const weekday = dayMap[dayName] || 1;
    const etStr = now.toLocaleTimeString('en-US', {
      timeZone: 'America/New_York',
      hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
    });
    const [h, m, s] = etStr.split(':').map(Number);
    return { weekday, secsOfDay: h * 3600 + m * 60 + s };
  }

  // Returns seconds until the next scheduledBeats.times occurrence in ET, skipping
  // weekends if weekdaysOnly is set. Returns null when scheduledBeats isn't configured
  // or has no times. setTimeout's max delay is ~24.8 days; we look only 8 days ahead
  // (worst case = Friday post-window → Monday's window) so we're well within range.
  // DST drift is acceptable: _scheduleNext re-runs after each beat and self-corrects.
  _getSecondsToNextScheduledBeat() {
    const sb = this._agentConfig?.scheduledBeats;
    if (!sb?.times?.length) return null;
    const weekdaysOnly = sb.weekdaysOnly !== false;
    const targets = sb.times.map(t => {
      const [h, m] = String(t).split(':').map(Number);
      return h * 3600 + m * 60;
    }).sort((a, b) => a - b);
    const { weekday: nowDow, secsOfDay: nowSecs } = this._getETNow();
    for (let dayOffset = 0; dayOffset < 8; dayOffset++) {
      const dow = ((nowDow - 1 + dayOffset) % 7) + 1; // 1=Mon..7=Sun
      if (weekdaysOnly && (dow === 6 || dow === 7)) continue;
      for (const t of targets) {
        const offset = dayOffset * 86400 + t - nowSecs;
        if (offset > 0) return offset;
      }
    }
    return null;
  }

  // True when the current ET wall-clock time is within scheduledBeats.windowMinutes
  // of any scheduled time today. Used at startup to decide whether to fire an
  // immediate beat (hybrid behavior: only beat on start if we're inside the window
  // the agent's own rules would accept).
  _isWithinScheduledWindow() {
    const sb = this._agentConfig?.scheduledBeats;
    if (!sb?.times?.length) return false;
    const windowSecs = (sb.windowMinutes ?? 0) * 60;
    if (windowSecs <= 0) return false;
    const weekdaysOnly = sb.weekdaysOnly !== false;
    const { weekday: nowDow, secsOfDay: nowSecs } = this._getETNow();
    if (weekdaysOnly && (nowDow === 6 || nowDow === 7)) return false;
    for (const t of sb.times) {
      const [h, m] = String(t).split(':').map(Number);
      const tSecs = h * 3600 + m * 60;
      if (Math.abs(nowSecs - tSecs) <= windowSecs) return true;
    }
    return false;
  }

  _scheduleNext() {
    if (!this.state.running) return;
    // Always clear any existing timer first to prevent dual timers
    if (this._timer) { clearTimeout(this._timer); this._timer = null; }

    // scheduledBeats.exclusive: agent fires only at the configured ET wall-clock
    // times, ignoring phase intervals and the phase-boundary snap entirely. Used
    // for daily-cadence agents like TrendProphet that must hit a specific window.
    const sb = this._agentConfig?.scheduledBeats;
    if (sb?.exclusive && sb.times?.length) {
      const scheduledSecs = this._getSecondsToNextScheduledBeat();
      if (scheduledSecs !== null) {
        this.state.heartbeatSeconds = scheduledSecs;
        this.state.nextBeatTime = new Date(Date.now() + scheduledSecs * 1000).toISOString();
        this.state.emit('schedule', { seconds: scheduledSecs, nextBeat: this.state.nextBeatTime, phase: this.state.phase });
        this._timer = setTimeout(() => this._runBeatCycle(), scheduledSecs * 1000);
        return;
      }
      // Defensive: scheduledSecs should never be null when times.length > 0; fall
      // through to phase-based scheduling rather than getting stuck.
    }

    let seconds = this._getHeartbeatSeconds();
    // Fire at phase boundaries so agents always wake at market open, market close, etc.
    const secsToBoundary = this._getSecondsToNextPhaseBoundary();
    if (secsToBoundary !== null && secsToBoundary > 10 && secsToBoundary < seconds) {
      seconds = secsToBoundary;
      this.state.emit('agent_log', {
        message: `Phase transition in ${Math.round(seconds)}s — scheduling early heartbeat.`,
        level: 'info',
      });
    }
    this.state.heartbeatSeconds = seconds;
    this.state.nextBeatTime = new Date(Date.now() + seconds * 1000).toISOString();
    this.state.emit('schedule', { seconds, nextBeat: this.state.nextBeatTime, phase: this.state.phase });
    this._timer = setTimeout(() => this._runBeatCycle(), seconds * 1000);
  }

  async _runBeatCycle() {
    if (!this.state.running) return;
    if (this.state.paused) {
      this.state.emit('agent_log', { message: 'Beat skipped (paused)', level: 'info' });
      this._scheduleNext();
      return;
    }
    const reason = this._emergencyQueued;
    this._emergencyQueued = null;
    this._emergencyReason = reason;
    try {
      await this._beat();
    } finally {
      this._emergencyReason = null;
    }
    if (this._emergencyQueued) {
      if (this._timer) { clearTimeout(this._timer); this._timer = null; }
      setImmediate(() => this._runBeatCycle());
    } else {
      this._scheduleNext();
    }
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

    // sessionMode='daily': reset session on the first beat of each new day
    // (transition into pre_market). Bounds continuous-session context growth
    // for high-frequency agents — each day starts with just system prompt +
    // first heartbeat instead of N days of replay. Activity logs / decisions
    // remain queryable via tools, so cross-day memory isn't truly lost.
    if (this._agentConfig?.sessionMode === 'daily'
        && phase === 'pre_market'
        && this._lastBeatPhase != null
        && this._lastBeatPhase !== 'pre_market'
        && this._sessionId) {
      this.state.emit('agent_log', {
        message: `[session] Daily reset — clearing session ${this._sessionId} on ${this._lastBeatPhase}→pre_market transition.`,
        level: 'info',
      });
      this._sessionId = null;
      this._sessionEpoch += 1;
    }
    this._lastBeatPhase = phase;

    this.state.emit('beat_start', { beat: beatNum, phase, time: this.state.lastBeatTime });
    this.state.emit('agent_log', {
      message: `--- Heartbeat #${beatNum} | ${PHASE_DEFAULTS[phase].label} ---`,
      level: 'info',
    });

    // Pre-flight skip check. Emergency wakes always run the LLM (the whole
    // point of the wake is for the agent to react). Message beats are
    // handled in _adHocBeat and never reach this code path.
    //
    // First-beat-of-session is NOT exempted: predicates that check actual
    // world state (positions, signals, time window) already know whether
    // there is anything to reconcile, so there is no benefit in spending
    // LLM tokens on a beat the predicate can clearly skip. If you want a
    // particular agent to always run on its first beat, the predicate
    // should encode that — not the harness.
    const isEmergency = !!this._emergencyReason;
    if (!isEmergency) {
      const strategyId = this._agentConfig?.strategyId;
      const runtime = this.getRuntime ? this.getRuntime(this.sandboxId) : null;
      const preflight = await resolvePreflight(strategyId, runtime, this._agentConfig);
      if (preflight.skip) {
        this.state.emit('agent_log', {
          message: `Beat #${beatNum} skipped (preflight): ${preflight.reason}`,
          level: 'info',
        });
        this.state.emit('beat_skip', { beat: beatNum, phase, reason: preflight.reason });
        this.state.stats.skippedBeats = (this.state.stats.skippedBeats || 0) + 1;
        this.state.emit('beat_end', { beat: beatNum, phase, skipped: true });
        this._beating = false;
        return;
      } else if (preflight.reason) {
        this.state.emit('agent_log', {
          message: `Beat #${beatNum} preflight: did not skip — ${preflight.reason}`,
          level: 'info',
        });
      }
    }

    // Guardrails are baked into the system prompt by reloadConfig() — sent
    // once per session instead of every beat. When permissions change,
    // server.js calls reloadConfig({resetSession:true}), which rebuilds the
    // system prompt and resets the session so updates take effect on the
    // next beat. No per-beat injection needed here.

    const emergencyPrefix = this._emergencyReason
      ? `\n\n[EMERGENCY ALERT] The mid-session market scanner detected a breaking development that requires your immediate attention:\n${this._emergencyReason}\n\nReview this alert and assess whether it requires immediate position action before your routine duties.`
      : '';
    const prompt = `[HEARTBEAT #${beatNum}] Phase: ${PHASE_DEFAULTS[phase].label}. Time: ${new Date().toLocaleString('en-US', { timeZone: 'America/New_York' })} ET. Current heartbeat interval: ${this.state.heartbeatSeconds}s.${emergencyPrefix}\n\nPerform your duties for this phase.`;

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

    } catch (err) {
      this.state.stats.errors++;
      this.state.emit('agent_log', { message: `Beat #${beatNum} error: ${err.message}`, level: 'error' });
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
    return new Promise(async (resolve, reject) => {
      const sessionEpoch = this._sessionEpoch;
      // OpenCode model format: anthropic/claude-sonnet-4-6
      const ocModel = model?.includes('/') ? model : `anthropic/${model || 'claude-sonnet-4-6'}`;

      // Check max tool rounds from permissions
      const perms = this._resolvePermissions();
      const maxToolRounds = perms.maxToolRoundsPerBeat || 25;

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

      // On Windows, cmd.exe cannot handle multi-line strings or long prompts as
      // command-line args (EINVAL). Always write to a temp file and attach via
      // --file. The message must come before --file so yargs doesn't consume it
      // as a second file path (--file is an array type).
      let tempPromptFile = null;
      if (process.platform === 'win32') {
        const os = await import('os');
        tempPromptFile = path.join(os.tmpdir(), `prophet_prompt_${Date.now()}.txt`);
        await fs.writeFile(tempPromptFile, fullPrompt, 'utf-8');
        args.push('Process the full prompt from the attached file.', '--file', tempPromptFile);
      } else {
        args.push(fullPrompt);
      }

      if (!this._isMessageBeat) {
        this.state.emit('agent_log', {
          message: `Spawning opencode (${ocModel})...`,
          level: 'info',
        });
      }

      // Per-agent MCP tool allowlist. Empty array = no filtering (backwards compatible).
      const allowedTools = Array.isArray(perms.allowedTools) ? perms.allowedTools.filter(Boolean) : [];

      const proc = spawn(OPENCODE_BIN, [...OPENCODE_WIN_PREFIX, ...args], {
        cwd: process.cwd(),
        env: {
          ...process.env,
          // Map CLAUDE_API_KEY → ANTHROPIC_API_KEY so opencode uses the correct key
          // rather than whatever may be stored in its own auth file.
          ANTHROPIC_API_KEY: process.env.CLAUDE_API_KEY || '',
          ...this.opencodeEnv,
          OPENPROPHET_SANDBOX_ID: this.sandboxId || '',
          OPENPROPHET_ACCOUNT_ID: this.state.activeAccountId || this.accountId || '',
          // OPENPROPHET_STRATEGY lets the MCP server tag outgoing orders by
          // strategy without each agent having to remember to pass its own
          // ID on every place_*_order call. Empty string for agents whose
          // strategyId is unset (legacy/default behavior unchanged).
          OPENPROPHET_STRATEGY: this._agentConfig?.strategyId || '',
          ...(allowedTools.length > 0 ? { OPENPROPHET_TOOL_ALLOWLIST: allowedTools.join(',') } : {}),
        },
        stdio: ['pipe', 'pipe', 'pipe'],
      });

      // Store reference so sendMessage() can kill it for interrupts
      this._proc = proc;
      this._interrupted = false;

      proc.stdin.on('error', () => {});
      proc.stdin.end();

      let fullText = '';
      let toolCalls = 0;
      let sessionId = null;
      let buffer = '';
      let totalCost = 0;
      let totalTokens = 0;
      let ocError = null;

      const makeCtx = () => ({
        addToolCall: () => toolCalls++,
        addText: (t) => { fullText += t; },
        setSession: (id) => { sessionId = id; },
        addCost: (c) => { totalCost += c; },
        addTokens: (t) => { totalTokens += t; },
        setError: (e) => { ocError = e; },
      });

      proc.stdout.on('data', (chunk) => {
        buffer += chunk.toString();
        const lines = buffer.split('\n');
        buffer = lines.pop(); // keep incomplete line in buffer

        for (const line of lines) {
          if (!line.trim()) continue;
          try {
            const event = JSON.parse(line);
            this._handleOpenCodeEvent(event, makeCtx());
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
        if (tempPromptFile) fs.unlink(tempPromptFile).catch(() => {});

        // Process remaining buffer
        if (buffer.trim()) {
          try {
            const event = JSON.parse(buffer);
            this._handleOpenCodeEvent(event, makeCtx());
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

        if (ocError) {
          resolve({ error: ocError, text: fullText, toolCalls, sessionId, sessionEpoch });
        } else if (code !== 0 && code !== null && !fullText) {
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
        }
      }, 300000);
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

    // Always update session ID — captures new sessions and handles the case
    // where opencode created a fresh session because the old one was invalid.
    if (event.sessionID) {
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

        // Track trades — only actual order placement/closure tools, not read queries
        const isNewOrder = fullToolName.includes('place_buy_order') ||
                           fullToolName.includes('place_sell_order') ||
                           fullToolName.includes('place_options_order') ||
                           fullToolName.includes('place_managed');
        const isClose    = fullToolName.includes('close_managed');
        if (isNewOrder || isClose) {
          this.state.stats.trades++;
          if (isClose) {
            const posId = toolInput.position_id || toolInput.id || '';
            this.state.addTrade({
              type: 'close',
              tool: toolName,
              symbol: posId ? posId.substring(0, 12) : '??',
              side: 'close',
              quantity: null,
              price: null,
            });
          } else {
            const qty = toolInput.quantity || toolInput.qty;
            const dollars = toolInput.allocation_dollars;
            this.state.addTrade({
              type: 'order',
              tool: toolName,
              symbol: toolInput.symbol || toolInput.underlying || '??',
              side: toolInput.side || (fullToolName.includes('buy') ? 'buy' : 'sell'),
              quantity: qty || (dollars ? `$${dollars}` : null),
              price: toolInput.limit_price,
            });
          }
        }
        break;
      }

      case 'step_finish': {
        const part = event.part || {};
        if (part.cost) ctx.addCost(part.cost);
        if (part.tokens?.total) ctx.addTokens(part.tokens.total);
        break;
      }

      case 'error': {
        const errData = event.error || {};
        const msg = errData?.data?.message || errData?.message || JSON.stringify(errData);
        this.state.emit('agent_log', { message: `[opencode error] ${msg}`, level: 'error' });
        ctx.setError(msg);
        break;
      }

      case 'step_start':
        break;
    }
  }
}
