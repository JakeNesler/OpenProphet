// update-check.js — startup reminders for self-hosted operators: is this checkout behind
// upstream `main`, and are there published security advisories? Everything here is
// best-effort and bounded by a timeout; it never blocks boot and never throws.
// Disable on air-gapped hosts with OPENPROPHET_UPDATE_CHECK=0.
import { execFile } from 'child_process';
import { promisify } from 'util';

const execFileP = promisify(execFile);

export const UPSTREAM_REPO = 'JakeNesler/OpenProphet';
export const REPO_URL = `https://github.com/${UPSTREAM_REPO}`;
export const ADVISORIES_URL = `${REPO_URL}/security/advisories`;
export const UPDATE_COMMAND = 'npm run update';

const LOOPBACK_HOSTS = new Set(['127.0.0.1', 'localhost', '::1']);

export function isUpdateCheckDisabled(env = process.env) {
  const v = String(env.OPENPROPHET_UPDATE_CHECK ?? '').trim().toLowerCase();
  return v === '0' || v === 'false' || v === 'off';
}

// Local commit + branch via git. Docker images ship without .git, so OPENPROPHET_COMMIT
// (set at build time) wins when present. Returns null when neither is available.
export async function localGitState(cwd, { env = process.env, exec = execFileP } = {}) {
  if (env.OPENPROPHET_COMMIT) return { sha: env.OPENPROPHET_COMMIT.trim(), branch: null };
  try {
    const opts = { cwd, timeout: 3000, encoding: 'utf-8' };
    const { stdout: sha } = await exec('git', ['rev-parse', 'HEAD'], opts);
    const { stdout: branch } = await exec('git', ['rev-parse', '--abbrev-ref', 'HEAD'], opts);
    return { sha: sha.trim(), branch: branch.trim() };
  } catch {
    return null;
  }
}

// Compare the local commit against upstream main and list published advisories.
// Result shape is stable so the dashboard can render it:
//   { enabled, checkedAt, local, behindBy, status, latestSha, advisories, errors }
export async function checkForUpdates({
  cwd = process.cwd(),
  repo = UPSTREAM_REPO,
  env = process.env,
  fetchImpl = globalThis.fetch,
  exec,
  timeoutMs = 6000,
} = {}) {
  const result = {
    enabled: !isUpdateCheckDisabled(env),
    checkedAt: new Date().toISOString(),
    local: null,
    behindBy: null,   // commits on upstream main that this checkout lacks
    status: null,     // 'current' | 'behind' | 'diverged' | 'unknown'
    latestSha: null,
    advisories: [],
    errors: [],
  };
  if (!result.enabled) return result;

  result.local = await localGitState(cwd, { env, exec });

  const headers = { Accept: 'application/vnd.github+json', 'User-Agent': 'openprophet-update-check' };
  const getJson = async (url) => {
    const res = await fetchImpl(url, { headers, signal: AbortSignal.timeout(timeoutMs) });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    return res.json();
  };

  if (result.local?.sha) {
    try {
      // base = our commit, head = upstream main → ahead_by is how far behind we are.
      const cmp = await getJson(`https://api.github.com/repos/${repo}/compare/${result.local.sha}...main`);
      result.behindBy = Number(cmp.ahead_by ?? 0);
      result.latestSha = cmp.commits?.length ? cmp.commits[cmp.commits.length - 1].sha : null;
      result.status = cmp.status === 'diverged' ? 'diverged' : (result.behindBy > 0 ? 'behind' : 'current');
    } catch (err) {
      // 404 = our commit is unknown upstream (a fork or local branch); still report advisories.
      result.status = 'unknown';
      result.errors.push(`update check: ${err.message}`);
    }
  } else {
    result.status = 'unknown';
  }

  try {
    const list = await getJson(`https://api.github.com/repos/${repo}/security-advisories?state=published&per_page=10`);
    result.advisories = (Array.isArray(list) ? list : []).map(a => ({
      id: a.ghsa_id,
      severity: a.severity,
      summary: a.summary,
      url: a.html_url,
      publishedAt: a.published_at,
    }));
  } catch (err) {
    result.errors.push(`advisories: ${err.message}`);
  }

  return result;
}

// Security posture notices derived from how this instance is configured. Pure: pass what
// the server already knows; `envFileMode` is the POSIX mode of .env (or null if absent).
export function securityNotices({ host, port, authToken, accounts = [], envFileMode = null } = {}) {
  const notices = [];
  if (!authToken && !LOOPBACK_HOSTS.has(host)) {
    notices.push({
      level: 'warning', code: 'dashboard-open',
      message: `Dashboard API is reachable on ${host}:${port} with NO auth token — anyone on your network can start, stop, and direct trading. Set AGENT_AUTH_TOKEN in .env, or AGENT_HOST=127.0.0.1 to keep it local.`,
    });
  }
  const live = accounts.filter(a => a && a.paper === false);
  if (live.length) {
    notices.push({
      level: 'warning', code: 'live-account',
      message: `${live.length} LIVE brokerage account(s) configured (${live.map(a => a.name || a.id).join(', ')}) — real money. Keep maxOrderValue, maxDailyLoss and requireConfirmation tight.`,
    });
  }
  if (envFileMode != null && (envFileMode & 0o044)) {
    notices.push({
      level: 'warning', code: 'env-perms',
      message: '.env (API keys) is readable by other users on this machine — run: chmod 600 .env',
    });
  }
  notices.push({
    level: 'info', code: 'advisories',
    message: `Review security advisories before each update: ${ADVISORIES_URL}`,
  });
  return notices;
}

// Console lines for the startup banner.
export function formatUpdateNotices(result) {
  const lines = [];
  if (!result?.enabled) {
    lines.push('Update check disabled (OPENPROPHET_UPDATE_CHECK=0).');
    return lines;
  }
  if (result.status === 'behind') {
    lines.push(`UPDATE AVAILABLE: this checkout is ${result.behindBy} commit(s) behind ${UPSTREAM_REPO} main — run \`${UPDATE_COMMAND}\` (then restart).`);
  } else if (result.status === 'diverged') {
    lines.push(`This checkout has diverged from ${UPSTREAM_REPO} main (${result.behindBy} upstream commit(s) not pulled) — rebase or \`${UPDATE_COMMAND}\`.`);
  } else if (result.status === 'current') {
    lines.push('Up to date with upstream main.');
  } else {
    lines.push(`Could not determine whether this checkout is current (${result.errors[0] || 'no git metadata'}) — pull regularly: \`${UPDATE_COMMAND}\`.`);
  }
  if (result.advisories.length) {
    lines.push(`SECURITY ADVISORIES PUBLISHED (${result.advisories.length}):`);
    for (const a of result.advisories.slice(0, 5)) {
      lines.push(`  - [${(a.severity || 'unknown').toUpperCase()}] ${a.summary} — ${a.url}`);
    }
  } else if (!result.errors.some(e => e.startsWith('advisories'))) {
    lines.push('No published security advisories.');
  }
  return lines;
}
