# Security Policy

OpenProphet drives a real brokerage account. Treat it like you would treat your bank login.

## Supported versions

Only the latest commit on `main` receives fixes. There are no long-lived release branches —
if you run OpenProphet, keep it current (`npm run update`). The dashboard checks for new
upstream commits and published advisories at startup and once a day, and shows a banner when
either exists.

## Reporting a vulnerability

Please **do not open a public issue** for security problems.

Use GitHub's private reporting: [Report a vulnerability](https://github.com/JakeNesler/OpenProphet/security/advisories/new).
Include the commit you are running, how to reproduce, and what an attacker could do with it.
You will get an acknowledgement within 72 hours. Fixes ship as a published advisory
(https://github.com/JakeNesler/OpenProphet/security/advisories), which the running dashboard
will surface to operators.

## Operator checklist

- **Never expose the dashboard to the internet.** It is LAN-only by design. Set
  `AGENT_AUTH_TOKEN` whenever `AGENT_HOST` is not `127.0.0.1`, and put it behind a VPN or
  SSH tunnel for remote access.
- **Keep `.env` private** (`chmod 600 .env`). It holds broker and LLM keys. `.env`,
  `data/`, and `agent-config.json` are gitignored — never commit them.
- **Start on a paper account.** The app never switches an account from paper to live by
  itself; the warning at startup tells you if any live account is configured.
- **Keep the risk gate on**: `maxOrderValue`, `maxDailyLoss`, `allow0DTE=false`, and
  `requireConfirmation` until you trust the strategy.
- **Update regularly** — `npm run update` pulls `main`, reinstalls dependencies, and rebuilds
  the Go backend. Review the changelog and advisories before each update.
- Dependabot keeps Node, Go, Docker, and Actions dependencies current; CI runs
  `govulncheck` and `npm audit` on every change.
