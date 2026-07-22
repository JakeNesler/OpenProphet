import { EventEmitter } from 'events';
import http from 'http';
import path from 'path';
import fs from 'fs/promises';
import { fileURLToPath } from 'url';
import { spawn, execSync } from 'child_process';
import axios from 'axios';

import { AgentHarness } from './harness.js';
import { alpacaTradingUrl, portForAgent } from './defaults.js';
import {
  getSandbox,
  getSandboxes,
  getAccountById,
  getAgentById,
  getResolvedAgentForSandbox,
  getStrategyById,
  getHeartbeatForSandboxPhase,
  getPermissionsForSandbox,
} from './config-store.js';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const PROJECT_ROOT = path.join(__dirname, '..');
const HARNESS_EVENTS = [
  'status', 'agent_log', 'agent_text', 'beat_start', 'beat_end',
  'tool_call', 'tool_result', 'heartbeat_change', 'schedule', 'trade',
];

function portOffsetForSandbox(sandboxId) {
  let hash = 0;
  for (const char of String(sandboxId || 'default')) {
    hash = (hash * 31 + char.charCodeAt(0)) % 1000;
  }
  return hash;
}

export class AgentOrchestrator extends EventEmitter {
  constructor(options = {}) {
    super();
    this.projectRoot = options.projectRoot || PROJECT_ROOT;
    this.agentUrl = options.agentUrl || process.env.AGENT_URL || 'http://localhost:3737';
    this.tradingBotBasePort = Number(options.tradingBotBasePort || process.env.TRADING_BOT_PORT || 4534);
    this.chatStore = options.chatStore || null;
    this.runtimes = new Map();
    this._binaryReady = false;
  }

  getSandboxPort(sandboxId) {
    // Deterministic per-sandbox port via the shared, tested allocation policy.
    return portForAgent(sandboxId, this.tradingBotBasePort);
  }

  getSandboxDbPath(sandboxId) {
    const sandbox = getSandbox(sandboxId);
    const accountId = sandbox?.accountId || sandboxId;
    return path.join(this.projectRoot, 'data', 'sandboxes', accountId, 'prophet_trader.db');
  }

  getSandboxRuntime(sandboxId) {
    return this.runtimes.get(sandboxId) || null;
  }

  listRuntimes() {
    return Array.from(this.runtimes.values()).map(runtime => ({
      sandboxId: runtime.sandboxId,
      port: runtime.port,
      goReady: runtime.goReady,
      goPid: runtime.goProc?.pid || null,
      state: runtime.harness.state.toJSON(),
    }));
  }

  ensureRuntime(sandboxId) {
    let runtime = this.runtimes.get(sandboxId);
    if (runtime) return runtime;

    const sandbox = getSandbox(sandboxId);
    if (!sandbox) throw new Error(`Sandbox not found: ${sandboxId}`);

    const port = this.getSandboxPort(sandboxId);
    const tradingBotUrl = `http://127.0.0.1:${port}`;
    const goHttpAgent = new http.Agent({ keepAlive: true, maxSockets: 10, keepAliveMsecs: 30000 });
    const tradingBotToken = process.env.TRADING_BOT_TOKEN || '';
    const goAxios = axios.create({
      baseURL: tradingBotUrl,
      httpAgent: goHttpAgent,
      timeout: 5000,
      headers: tradingBotToken ? { Authorization: `Bearer ${tradingBotToken}` } : {},
    });

    const harness = new AgentHarness({
      sandboxId,
      accountId: sandbox.accountId,
      getSandbox,
      getAccount: getAccountById,
      getAgent: getAgentById,
      getResolvedAgent: getResolvedAgentForSandbox,
      getStrategyById,
      getHeartbeatForPhase: getHeartbeatForSandboxPhase,
      getPermissions: getPermissionsForSandbox,
      chatStore: this.chatStore,
      opencodeEnv: {
        TRADING_BOT_URL: tradingBotUrl,
        TRADING_BOT_TOKEN: tradingBotToken,
        SERVER_HOST: '127.0.0.1',
        AGENT_URL: this.agentUrl,
        OPENPROPHET_SANDBOX_ID: sandboxId,
        OPENPROPHET_ACCOUNT_ID: sandbox.accountId,
        DATABASE_PATH: this.getSandboxDbPath(sandboxId),
      },
    });

    runtime = {
      sandboxId,
      sandbox,
      port,
      tradingBotUrl,
      goAxios,
      goReady: false,
      goProc: null,
      harness,
    };

    for (const event of HARNESS_EVENTS) {
      harness.state.on(event, data => {
        this.emit(event, { sandboxId, ...data });
      });
    }

    this.runtimes.set(sandboxId, runtime);
    return runtime;
  }

  async ensureAllRuntimes() {
    for (const sandbox of getSandboxes()) {
      this.ensureRuntime(sandbox.id);
    }
  }

  async _ensureBinary(force = false) {
    if (!force && this._binaryReady) return;
    const binaryPath = path.join(this.projectRoot, 'prophet_bot');
    let exists = true;
    try { await fs.access(binaryPath); } catch { exists = false; }
    if (force || !exists) {
      // `go build -o` refuses to overwrite a file it doesn't recognize as its own output
      // (a stale/wrong-arch binary), so remove it first when rebuilding.
      if (force && exists) { try { await fs.rm(binaryPath, { force: true }); } catch { /* fall through */ } }
      execSync('go build -o prophet_bot ./cmd/bot', {
        cwd: this.projectRoot,
        timeout: 120000,
        stdio: 'pipe',
      });
    }
    this._binaryReady = true;
  }

  async startGoBackend(sandboxId, _isRetry = false) {
    const runtime = this.ensureRuntime(sandboxId);
    const account = getAccountById(runtime.sandbox.accountId);
    if (!account) throw new Error(`Account not found for sandbox ${sandboxId}`);

    await this.stopGoBackend(sandboxId);
    await this._ensureBinary();
    await fs.mkdir(path.dirname(this.getSandboxDbPath(sandboxId)), { recursive: true });

    const env = {
      ...process.env,
      ALPACA_API_KEY: account.publicKey,
      ALPACA_SECRET_KEY: account.secretKey,
      ALPACA_BASE_URL: alpacaTradingUrl(account.paper, account.baseUrl),
      ALPACA_PAPER: account.paper ? 'true' : 'false',
      PORT: String(runtime.port),
      SERVER_HOST: '127.0.0.1',
      TRADING_BOT_TOKEN: process.env.TRADING_BOT_TOKEN || '',
      DATABASE_PATH: this.getSandboxDbPath(sandboxId),
      ACTIVITY_LOG_DIR: path.join(this.projectRoot, 'data', 'sandboxes', account.id, 'activity_logs'),
      OPENPROPHET_SANDBOX_ID: sandboxId,
      OPENPROPHET_ACCOUNT_ID: account.id,
    };

    const binaryPath = path.join(this.projectRoot, 'prophet_bot');
    runtime.goProc = spawn(binaryPath, [], {
      cwd: this.projectRoot,
      env,
      stdio: ['ignore', 'pipe', 'pipe'],
    });

    runtime.goReady = false;

    runtime.goProc.stdout.on('data', chunk => {
      const message = chunk.toString().trim();
      if (message) {
        this.emit('agent_log', {
          sandboxId,
          level: 'info',
          message: `[go:${runtime.port}] ${message}`,
        });
      }
    });

    runtime.goProc.stderr.on('data', chunk => {
      const message = chunk.toString().trim();
      if (message) {
        this.emit('agent_log', {
          sandboxId,
          level: 'warning',
          message: `[go:${runtime.port}] ${message}`,
        });
      }
    });

    runtime.goProc.on('exit', (code, signal) => {
      runtime.goReady = false;
      runtime.goProc = null;
      this.emit('agent_log', {
        sandboxId,
        level: code === 0 || signal === 'SIGTERM' ? 'info' : 'error',
        message: `Trading backend exited (code: ${code}, signal: ${signal})`,
      });
    });

    for (let i = 0; i < 20; i++) {
      await new Promise(resolve => setTimeout(resolve, 500));
      try {
        await runtime.goAxios.get('/health', { timeout: 2000 });
        runtime.goReady = true;
        this.emit('agent_log', {
          sandboxId,
          level: 'success',
          message: `Trading backend ready on port ${runtime.port} for ${account.name}`,
        });
        return runtime;
      } catch {
        // keep waiting
      }
    }

    // Didn't come up — the binary may be stale/wrong-arch for this host. Rebuild once and retry.
    if (!_isRetry) {
      this.emit('agent_log', { sandboxId, level: 'warning', message: `Trading backend did not become ready — rebuilding binary for this platform and retrying...` });
      await this._ensureBinary(true);
      return this.startGoBackend(sandboxId, true);
    }

    throw new Error(`Trading backend failed to start for sandbox ${sandboxId}`);
  }

  async stopGoBackend(sandboxId) {
    const runtime = this.getSandboxRuntime(sandboxId);
    if (!runtime?.goProc) return;

    const pid = runtime.goProc.pid;
    runtime.goProc.kill('SIGTERM');
    await new Promise(resolve => setTimeout(resolve, 1500));
    try {
      process.kill(pid, 0);
      runtime.goProc.kill('SIGKILL');
    } catch {
      // process already gone
    }
    runtime.goProc = null;
    runtime.goReady = false;
  }

  async startSandbox(sandboxId) {
    const runtime = this.ensureRuntime(sandboxId);
    if (!runtime.goReady) {
      await this.startGoBackend(sandboxId);
    }
    await runtime.harness.start();
    return runtime;
  }

  async stopSandbox(sandboxId) {
    const runtime = this.getSandboxRuntime(sandboxId);
    if (!runtime) return;
    await runtime.harness.stop();
    await this.stopGoBackend(sandboxId);
  }

  pauseSandbox(sandboxId) {
    const runtime = this.ensureRuntime(sandboxId);
    runtime.harness.pause();
  }

  resumeSandbox(sandboxId) {
    const runtime = this.ensureRuntime(sandboxId);
    runtime.harness.resume();
  }

  async sendMessage(sandboxId, message) {
    const runtime = this.ensureRuntime(sandboxId);
    return runtime.harness.sendMessage(message);
  }

  getState(sandboxId) {
    const runtime = this.ensureRuntime(sandboxId);
    return runtime.harness.state.toJSON();
  }

  async shutdown() {
    const sandboxIds = Array.from(this.runtimes.keys());
    for (const sandboxId of sandboxIds) {
      await this.stopSandbox(sandboxId);
    }
  }
}

export default AgentOrchestrator;
