# Changelog

## Unreleased

### Fixed
- **Appliance launcher: Docker access is checked before downloading.** `openprophet install`
  / `update` only ran `docker compose version` (which never touches the daemon), so a user
  outside the `docker` group downloaded the whole appliance archive and then failed with an
  opaque "failed to load appliance image". The launcher now runs `docker info` first and, on
  `permission denied … docker.sock`, prints the `usermod -aG docker` fix instead.
- **0DTE gate was timezone-broken.** The expiry date was parsed as UTC midnight, so on any
  host west of UTC (every US operator) a same-day expiry passed `allow0DTE=false` and the
  next day's expiry was blocked instead. Expiry is now a local calendar date; tests cover
  New York, Los Angeles, UTC, and Tokyo.
- **`maxOrderValue` could be bypassed.** Options were valued at premium × contracts (100×
  too low) and market orders — which carry no price — were valued at $0 and waved through.
  Options now use the 100-share multiplier, the MCP server attaches a live quote to market
  orders, and an order that still cannot be valued fails closed with an actionable message.
  Closing a managed position (an exit) is exempt.
- **Dashboard crash on "Login" without the OpenCode CLI.** `/api/auth/login` had no `error`
  handler on the child process, so a missing `opencode` binary took the whole dashboard down.
  It now returns a 500 with the reason, and no longer waits 15s when the CLI exits early.
- `agent-config.json` (every account's broker keys) is now written atomically
  (write-then-rename, mode 0600) so a crash mid-save can't truncate it.
- Chat-history account/session ids from URL params are validated as single path segments
  (no `../`).
- Dashboard and Go API bearer-token checks are constant-time.
- The SSE stream no longer sends `Access-Control-Allow-Origin: *`.
- Node→dashboard loopback URLs use `127.0.0.1` consistently (no `localhost`/IPv6 ambiguity).
- `npm install` with npm ≥ 12 skipped `better-sqlite3`'s native build (install scripts are
  blocked by default), leaving a broken install. `package.json` now approves the required
  install scripts via `allowScripts`.

### Added
- **Startup security & update reminders.** On boot (and daily) the dashboard prints a
  security checklist (open LAN dashboard without `AGENT_AUTH_TOKEN`, live accounts,
  world-readable `.env`), checks whether the checkout is behind upstream `main`, lists
  published security advisories, and shows a banner in the dashboard. `GET /api/update-status`
  exposes the same data. Disable with `OPENPROPHET_UPDATE_CHECK=0`.
- `npm run update` — pull `main`, `npm ci`, rebuild the Go backend.
- `AGENT_HOST` to choose the dashboard bind address (default unchanged: `0.0.0.0`).
- CI (`.github/workflows/ci.yml`): gofmt, `go vet`, `go test -race`, staticcheck,
  govulncheck, Node 22/24 test matrix, `npm audit`, Docker image build, actionlint.
- Auto-rebase workflow (`.github/workflows/auto-rebase.yml`): after every push to `main`
  (and daily), every non-main branch that is behind `main` is rebased and force-pushed with
  lease; conflicts are reported, Dependabot branches are left alone; forks sync `main` from
  upstream first.
- Dependabot now also tracks GitHub Actions, and groups minor/patch bumps per ecosystem.
- `SECURITY.md` with a private reporting channel and an operator checklist.
- Dockerfile accepts `OPENPROPHET_COMMIT` so containers can report their version.

## v2.0.5

- Requests the paid appliance archive for the launcher's native amd64 or arm64 host.
- Rejects unsupported hosts and architecture-mismatched manifests before downloading,
  loading an image, or persisting entitlement state.

## v2.0.4

- Delivers the paid appliance through a short-lived, entitlement-gated archive URL instead of
  exposing the private GHCR package.
- Verifies the archive checksum and exact image tag before loading it locally, and disables
  registry pulls when starting or updating the appliance.
- Keeps entitlement keys and signed download URLs out of command output and persisted manifests.

## v2.0.3

- Adds eight built-in agent personas with eight paired strategy templates.
- Keeps built-in persona and strategy text execution-mode neutral: the catalog defines research,
  entry, exit, sizing, and risk rules without recommending an account or execution mode.
- Backfills a missing built-in strategy pairing during upgrades while preserving explicit strategy
  choices, custom agents, custom fields, and existing order.
- Restores the canonical signed appliance launcher and installer sources required by the release
  workflow.

## v2.0.2

A security, reliability, and agentic-capability overhaul of the autonomous trading harness.
(Released as v2.0.2 — v2.0.0/v2.0.1 were consumed by earlier appliance-packaging tags.)
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
