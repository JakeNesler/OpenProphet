# Changelog

## v2.0.0

A security, reliability, and agentic-capability overhaul of the autonomous trading harness.
Paper trading only — options trading carries significant risk of loss.

### Security
- **Authenticated trading backend.** The Go API now binds `127.0.0.1` by default (was all
  interfaces) and enforces a bearer token on `/api/v1` (`/health` stays open). CORS reflects
  only localhost/allowlisted origins.
- **Secure by default.** If `TRADING_BOT_TOKEN` is unset, the dashboard mints an ephemeral
  token at startup and injects it into the Go backend, its own client, and the MCP subprocess,
  so the loopback API is never left unauthenticated.
- **Config secret masking** is now an allowlist-style recursive redactor — any secret-named
  field (tokens, keys, webhooks, `publicKey`) is masked in the dashboard/SSE, not just one field.

### Reliability (order path)
- **Order idempotency.** Every broker submit — stock, options, and all managed-position legs
  (entry / stop / take-profit / partial / close) — now carries a `ClientOrderID`, is persisted
  as intent **before** submission, and is marked `submit_failed` on error. Protective/exit legs
  fail *open* (a DB/id hiccup never withholds a stop or close); entries fail *closed*.
- **Startup reconciliation.** On boot the backend looks up `pending`/`submit_failed` orders by
  client id and repairs local state to broker truth — only ever updating orders it can confirm,
  closing the ambiguous-timeout duplicate window.
- **Self-healing bot binary.** The Go binary auto-builds when missing and rebuilds for the
  current platform if a stale/wrong-arch binary crashes on start (both the main and per-sandbox
  lifecycles).
- **Heartbeat robustness.** The per-beat timeout now escalates SIGTERM → SIGKILL, and repeated
  beat failures trigger an exponential heartbeat backoff (cleared by any clean beat).

### Agent capability
- **Rewritten system prompt** — a priority-ordered mandate (preserve capital → trade only with
  an edge → compound), an explicit per-heartbeat decision loop (orient → assess → manage-first
  → gather → recall → decide → record), a per-phase playbook, and hard risk discipline.
- **Closed the learning loop.** Order tools auto-capture the trade thesis into the vector
  memory on success (best-effort, non-blocking); the prompt mandates recalling similar setups
  before new positions and storing outcomes after close. Win-rate is computed over *resolved*
  trades. Only opening trades seed the recall corpus.
- **Auto-updating model catalog.** The model list is now a live, TTL-cached registry refreshed
  from the provider instead of a frozen, hand-maintained snapshot.

### Configuration
- Centralized previously-scattered defaults into `agent/defaults.js` (default model, Alpaca
  endpoints, sandbox-port allocation, harness operational constants) with a single source of
  truth and override precedence.

### Dashboard
- **Redesigned agent create + control.** A guided 5-step agent builder (Identity → Model →
  Risk → Prompt → Review) with a searchable model picker over the live catalog; per-agent
  state pills and controls; clearer account/agent control scoping. Accessibility + responsive.
- **Dark-only visual system.** The dashboard commits to a single dark theme — a deep ink-black
  "trading terminal" palette with vivid long/short P&L semantics, a living glow on market-open
  and running-agent states, and tactile primary controls. The light theme and its toggle were
  removed (dark is forced; any stale preference is cleared on load).

### Testing & tooling
- First automated test suites: JS (`node --test` — permission gate, model registry, prompt,
  tool catalog, trade stats) and Go (order persist-before-submit, upsert, reconciliation).
- Added a container `Dockerfile` for self-hosting.
