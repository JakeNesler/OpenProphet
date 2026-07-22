// defaults.js — single source of built-in defaults for the Node side.
// Values are extracted verbatim from where they were previously hardcoded; centralizing them
// means a change is made in ONE place. Precedence for callers: explicit override → persisted
// agent/strategy config → environment → these built-in defaults. Security/idempotency/permission
// invariants intentionally do NOT live here — they are not operator-tunable.

// ── Models ─────────────────────────────────────────────────────────
// Kept as the current values on purpose; do not change an ID here without verifying the
// provider actually serves it (that needs a runtime capability check).
export const DEFAULT_AGENT_MODEL = 'anthropic/claude-sonnet-4-6';
export const DEFAULT_GEMINI_MODEL = 'gemini-2.0-flash-exp';

// Resolve the model to use: explicit run override → persisted agent choice → env → built-in.
export function resolveAgentModel({ explicit, persisted, env = process.env.OPENPROPHET_MODEL } = {}) {
  return explicit || persisted || env || DEFAULT_AGENT_MODEL;
}

// ── Alpaca endpoints ───────────────────────────────────────────────
export const ALPACA_PAPER_TRADING_URL = 'https://paper-api.alpaca.markets';
export const ALPACA_LIVE_TRADING_URL = 'https://api.alpaca.markets';
export const ALPACA_DATA_URL = 'https://data.alpaca.markets';

// Trading endpoint for an account. Never infers or auto-switches to live — paper is the default
// and live is used only when the account is explicitly non-paper.
export function alpacaTradingUrl(paper, override) {
  return override || (paper ? ALPACA_PAPER_TRADING_URL : ALPACA_LIVE_TRADING_URL);
}

// ── Hosts & ports ──────────────────────────────────────────────────
export const LOOPBACK_HOST = '127.0.0.1';
export const DEFAULT_AGENT_PORT = 3737;
export const DEFAULT_TRADING_BOT_PORT = 4534;

// Per-agent trading-backend port allocation (sandbox mode). The hash is a stable contract —
// do not "improve" the algorithm without a migration, or running agents move ports.
export const AGENT_PORT_ALLOC = { base: 4535, slots: 10, algorithmVersion: 1 };
export function portForAgent(agentId, basePort = DEFAULT_TRADING_BOT_PORT) {
  let hash = 0;
  for (const char of String(agentId || 'default')) {
    hash = (hash * 31 + char.charCodeAt(0)) % 1000;
  }
  const offset = (hash % AGENT_PORT_ALLOC.slots) + 1; // ports base..base+slots-1
  return basePort + offset;
}

// ── Harness operational policy ─────────────────────────────────────
// Extracted verbatim from agent/harness.js. SIGKILL fallback + restart behavior remain
// invariants; only these durations/limits are named here.
export const BEAT_TIMEOUT_MS = 300000;        // hard cap per beat before SIGTERM
export const SIGKILL_GRACE_MS = 5000;         // wait after SIGTERM before SIGKILL
export const DEFAULT_MAX_TOOL_ROUNDS = 25;    // --max-turns default when perms don't set one
export const BEAT_BACKOFF = {
  threshold: 3,     // consecutive failures before backoff engages
  factor: 16,       // max multiplier cap (2^(n-2) capped here)
  capSeconds: 3600, // absolute ceiling on the backed-off interval
};
