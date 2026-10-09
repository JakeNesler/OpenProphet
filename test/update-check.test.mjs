import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  checkForUpdates, securityNotices, formatUpdateNotices, isUpdateCheckDisabled, ADVISORIES_URL,
} from '../agent/update-check.js';

const SHA = 'a'.repeat(40);
const fakeExec = async (_cmd, args) => ({ stdout: args.includes('--abbrev-ref') ? 'main\n' : `${SHA}\n` });
const fakeFetch = (routes) => async (url) => {
  for (const [needle, body] of Object.entries(routes)) {
    if (url.includes(needle)) return { ok: true, status: 200, json: async () => body };
  }
  return { ok: false, status: 404, json: async () => ({}) };
};

test('reports behind-by and advisories from the GitHub compare + advisories endpoints', async () => {
  const fetchImpl = fakeFetch({
    '/compare/': { status: 'ahead', ahead_by: 3, commits: [{ sha: 'b'.repeat(40) }] },
    '/security-advisories': [{ ghsa_id: 'GHSA-x', severity: 'high', summary: 'Token leak', html_url: 'https://x', published_at: '2026-10-01T00:00:00Z' }],
  });
  const r = await checkForUpdates({ env: {}, exec: fakeExec, fetchImpl });
  assert.equal(r.enabled, true);
  assert.equal(r.local.sha, SHA);
  assert.equal(r.behindBy, 3);
  assert.equal(r.status, 'behind');
  assert.equal(r.advisories.length, 1);
  assert.equal(r.advisories[0].severity, 'high');
  const lines = formatUpdateNotices(r);
  assert.match(lines[0], /3 commit\(s\) behind/);
  assert.match(lines.join('\n'), /\[HIGH\] Token leak/);
});

test('current checkout, no advisories', async () => {
  const fetchImpl = fakeFetch({ '/compare/': { status: 'identical', ahead_by: 0, commits: [] }, '/security-advisories': [] });
  const r = await checkForUpdates({ env: {}, exec: fakeExec, fetchImpl });
  assert.equal(r.status, 'current');
  assert.deepEqual(r.errors, []);
  assert.match(formatUpdateNotices(r).join('\n'), /Up to date/);
});

test('unknown commit upstream (fork) degrades to status=unknown but still lists advisories', async () => {
  const fetchImpl = fakeFetch({ '/security-advisories': [] });
  const r = await checkForUpdates({ env: {}, exec: fakeExec, fetchImpl });
  assert.equal(r.status, 'unknown');
  assert.equal(r.errors.length, 1);
  assert.match(formatUpdateNotices(r)[0], /Could not determine/);
});

test('never throws when git and the network are unavailable', async () => {
  const r = await checkForUpdates({
    env: {},
    exec: async () => { throw new Error('git: not found'); },
    fetchImpl: async () => { throw new Error('ENOTFOUND api.github.com'); },
  });
  assert.equal(r.local, null);
  assert.equal(r.status, 'unknown');
  assert.equal(r.errors.length, 1);
});

test('OPENPROPHET_UPDATE_CHECK=0 disables the check without touching git or the network', async () => {
  assert.equal(isUpdateCheckDisabled({ OPENPROPHET_UPDATE_CHECK: '0' }), true);
  assert.equal(isUpdateCheckDisabled({ OPENPROPHET_UPDATE_CHECK: 'false' }), true);
  assert.equal(isUpdateCheckDisabled({}), false);
  let touched = false;
  const r = await checkForUpdates({
    env: { OPENPROPHET_UPDATE_CHECK: '0' },
    exec: async () => { touched = true; return { stdout: '' }; },
    fetchImpl: async () => { touched = true; return { ok: true, json: async () => ({}) }; },
  });
  assert.equal(r.enabled, false);
  assert.equal(touched, false);
});

test('OPENPROPHET_COMMIT replaces git metadata (container builds)', async () => {
  const fetchImpl = fakeFetch({ '/compare/': { status: 'identical', ahead_by: 0, commits: [] }, '/security-advisories': [] });
  const r = await checkForUpdates({ env: { OPENPROPHET_COMMIT: 'c'.repeat(40) }, exec: async () => { throw new Error('no git'); }, fetchImpl });
  assert.equal(r.local.sha, 'c'.repeat(40));
  assert.equal(r.status, 'current');
});

test('securityNotices flags an open non-loopback dashboard, live accounts, and loose .env perms', () => {
  const open = securityNotices({ host: '0.0.0.0', port: 3737, authToken: '', accounts: [{ id: 'a', name: 'Real', paper: false }], envFileMode: 0o644 });
  const codes = open.map(n => n.code);
  assert.deepEqual(codes, ['dashboard-open', 'live-account', 'env-perms', 'advisories']);
  assert.match(open[0].message, /AGENT_AUTH_TOKEN/);
  assert.match(open[3].message, new RegExp(ADVISORIES_URL));

  // token set + loopback + paper + 0600 → only the standing advisories reminder
  const tight = securityNotices({ host: '127.0.0.1', port: 3737, authToken: 'secret', accounts: [{ id: 'p', paper: true }], envFileMode: 0o600 });
  assert.deepEqual(tight.map(n => n.code), ['advisories']);

  // loopback without a token is fine (local-only), missing .env is fine
  const local = securityNotices({ host: '127.0.0.1', port: 3737, authToken: '', accounts: [], envFileMode: null });
  assert.deepEqual(local.map(n => n.code), ['advisories']);
});
