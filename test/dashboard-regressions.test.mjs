import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

// Runs the dashboard's real inline <script> against a minimal fake DOM so the
// regressions below exercise the shipped code paths, not copies of them.
const html = readFileSync(new URL('../agent/public/index.html', import.meta.url), 'utf8');
const script = html.match(/<script>([\s\S]*?)<\/script>/)[1];

const escapeHtml = s => String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');

class FakeElement {
  constructor(id = '') {
    this.id = id;
    this.style = {};
    this.dataset = {};
    this.children = [];
    this.value = '';
    this.disabled = false;
    this.scrollTop = 0;
    this._html = '';
    const classes = new Set();
    this.classList = {
      add: (...c) => c.forEach(x => classes.add(x)),
      remove: (...c) => c.forEach(x => classes.delete(x)),
      toggle: (c, on = !classes.has(c)) => { on ? classes.add(c) : classes.delete(c); return on; },
      contains: c => classes.has(c),
    };
  }
  set innerHTML(v) { this._html = String(v); this.children = []; }
  get innerHTML() { return this._html; }
  set textContent(v) { this._text = String(v); this._html = escapeHtml(v); this.children = []; }
  get textContent() { return this._text ?? ''; }
  appendChild(c) { this.children.push(c); return c; }
  insertBefore(c) { this.children.unshift(c); return c; }
  removeChild(c) { this.children = this.children.filter(x => x !== c); return c; }
  get firstChild() { return this.children[0] || null; }
  get lastChild() { return this.children.at(-1) || null; }
  focus() { this.ownerDocument.activeElement = this; }
  setSelectionRange(start, end) { this.selectionStart = start; this.selectionEnd = end; }
  querySelector() { return null; }
  querySelectorAll() { return []; }
  insertAdjacentHTML() {}
  addEventListener() {}
  setAttribute() {}
  remove() {}
}

function loadDashboard() {
  const els = new Map();
  const document = {
    activeElement: null,
    hidden: false,
    getElementById(id) {
      if (!els.has(id)) { const el = new FakeElement(id); el.ownerDocument = document; els.set(id, el); }
      return els.get(id);
    },
    createElement() { const el = new FakeElement(); el.ownerDocument = document; return el; },
    querySelector() { return null; },
    querySelectorAll() { return []; },
    addEventListener() {},
  };
  document.documentElement = document.createElement('html');

  const sources = [];
  class EventSource {
    constructor(url) { this.url = url; this.listeners = {}; sources.push(this); }
    addEventListener(type, fn) { this.listeners[type] = fn; }
    emit(type, data) { this.listeners[type]({ data: JSON.stringify(data) }); }
    close() {}
  }

  const calls = { toasts: [], tabs: [], fetches: [], alerts: [] };
  const routes = [];
  const ctx = {
    document,
    location: { search: '' },
    localStorage: { removeItem() {}, getItem() { return null; }, setItem() {} },
    EventSource,
    URLSearchParams,
    console,
    setInterval: () => 0,
    clearInterval() {},
    setTimeout: () => 0,
    clearTimeout() {},
    confirm: () => true,
    alert: msg => calls.alerts.push(msg),
    // Unrouted requests never settle, keeping the init-time polling inert.
    fetch: (url, opts) => {
      calls.fetches.push({ url, opts });
      const route = routes.find(r => r.match(url, opts));
      return route ? route.respond(url, opts) : new Promise(() => {});
    },
  };
  ctx.window = ctx;
  vm.createContext(ctx);
  vm.runInContext(script, ctx);

  ctx.showToast = (message, type) => calls.toasts.push({ message, type });
  ctx.switchTab = id => calls.tabs.push(id);
  const run = code => vm.runInContext(code, ctx);
  const route = (match, respond) => routes.push({ match, respond });
  return { ctx, document, sources, calls, run, route };
}

const jsonResponse = (body, status = 200) => Promise.resolve({ ok: status < 400, status, json: async () => body });

// ── Start button ────────────────────────────

test('Start with no account guides to Accounts and does not lock the button', async () => {
  const { ctx, document, calls, run } = loadDashboard();
  run(`config = { accounts: [], agents: [], strategies: [], models: [], sandboxes: {} }`);
  await ctx.startAgent();
  const btn = document.getElementById('btn-start');
  assert.equal(btn.textContent, 'Start');
  assert.equal(btn.disabled, false);
  assert.deepEqual(calls.tabs, ['accounts']);
  assert.match(calls.toasts[0].message, /Accounts/);
  assert.ok(!calls.fetches.some(f => /\/start$/.test(f.url)), 'no start request without a sandbox');
});

test('Start network failure is surfaced and the button resets', async () => {
  const { ctx, document, calls, run, route } = loadDashboard();
  run(`config = { accounts: [{ id: 'a' }], agents: [], strategies: [], models: [], sandboxes: { sbx_a: { id: 'sbx_a' } }, activeSandboxId: 'sbx_a' }`);
  route(url => url.endsWith('/start'), () => Promise.reject(new Error('network down')));
  await ctx.startAgent();
  const btn = document.getElementById('btn-start');
  assert.equal(btn.textContent, 'Start');
  assert.equal(btn.disabled, false);
  assert.ok(calls.toasts.some(t => t.type === 'error' && /network down/.test(t.message)));
});

test('Terminal shows Accounts guidance only while no account exists', () => {
  const { ctx, document, run } = loadDashboard();
  run(`config = { accounts: [], agents: [], strategies: [], models: [], sandboxes: {} }`);
  ctx.renderSandboxTabs();
  assert.equal(document.getElementById('terminal-empty').style.display, '');
  run(`config = { accounts: [{ id: 'a', name: 'Paper' }], agents: [], strategies: [], models: [], sandboxes: { sbx_a: {} }, activeSandboxId: 'sbx_a' }`);
  ctx.renderSandboxTabs();
  assert.equal(document.getElementById('terminal-empty').style.display, 'none');
  assert.match(html, /id="terminal-empty"[\s\S]*?onclick="switchTab\('accounts'\)"/);
});

// ── Trade feed ──────────────────────────────

function tradeDashboard() {
  const dash = loadDashboard();
  dash.run(`config = { accounts: [{ id: 'a' }], agents: [], strategies: [], models: [], sandboxes: { sbx_a: { id: 'sbx_a' } }, activeSandboxId: 'sbx_a' }`);
  const feed = () => {
    const out = dash.document.getElementById('trades-feed').innerHTML;
    return [...out.matchAll(/<span class="trade-symbol">([^<]*)<\/span>/g)].map(m => m[1]);
  };
  const state = recentTrades => ({ sandboxId: 'sbx_a', running: true, stats: {}, recentTrades });
  return { ...dash, feed, state };
}

const trade = (id, symbol, at, extra = {}) => ({
  id, type: 'order', tool: 'place_buy_order', symbol, side: 'buy', quantity: 1,
  timestamp: new Date(Date.UTC(2026, 9, 20, 14, 30, at)).toISOString(), ...extra,
});

test('SSE reconnect replays of the state snapshot do not duplicate trades', () => {
  const { sources, feed, state, ctx } = tradeDashboard();
  const snapshot = [trade('t2', 'MSFT', 2), trade('t1', 'AAPL', 1)]; // server order: newest first
  sources.at(-1).emit('state', state(snapshot));
  ctx.connectSSE(); // reconnect: the server re-sends the same snapshot
  sources.at(-1).emit('state', state(snapshot));
  assert.deepEqual(feed(), ['MSFT', 'AAPL'], 'newest first, each trade once');
});

test('live trades merge with later snapshots without duplicates and missed trades fill in order', () => {
  const { sources, feed, state, ctx } = tradeDashboard();
  sources.at(-1).emit('trade', { ...trade('t1', 'AAPL', 1), sandboxId: 'sbx_a' });
  ctx.connectSSE(); // t2 and t3 happened while disconnected
  sources.at(-1).emit('state', state([trade('t3', 'NVDA', 3), trade('t2', 'MSFT', 2), trade('t1', 'AAPL', 1)]));
  assert.deepEqual(feed(), ['NVDA', 'MSFT', 'AAPL']);
  sources.at(-1).emit('trade', { ...trade('t4', 'TSLA', 4), sandboxId: 'sbx_a' });
  assert.deepEqual(feed(), ['TSLA', 'NVDA', 'MSFT', 'AAPL']);
});

test('distinct trades with identical order fields and time are all retained', () => {
  const { sources, feed, state } = tradeDashboard();
  const a = trade('t1', 'AAPL', 1);
  const b = trade('t2', 'AAPL', 1);
  sources.at(-1).emit('trade', { ...a, sandboxId: 'sbx_a' });
  sources.at(-1).emit('trade', { ...b, sandboxId: 'sbx_a' });
  sources.at(-1).emit('state', state([b, a]));
  assert.deepEqual(feed(), ['AAPL', 'AAPL']);
});

test('a snapshot that lands before its live event does not double the card', () => {
  const { sources, feed, state } = tradeDashboard();
  const t1 = trade('t1', 'AAPL', 1);
  sources.at(-1).emit('state', state([t1]));
  sources.at(-1).emit('trade', { ...t1, sandboxId: 'sbx_a' });
  assert.deepEqual(feed(), ['AAPL']);
});

test('legacy trades without ids dedupe only on exact fields and timestamp', () => {
  const { sources, feed, state, ctx } = tradeDashboard();
  const older = trade(undefined, 'AAPL', 1);
  const same = trade(undefined, 'AAPL', 1);
  const newer = trade(undefined, 'MSFT', 2);
  sources.at(-1).emit('state', state([newer, same, older]));
  ctx.connectSSE();
  sources.at(-1).emit('state', state([newer, same, older]));
  assert.deepEqual(feed(), ['MSFT', 'AAPL', 'AAPL']);
});

test('trade feed is capped at 50 newest trades', () => {
  const { run, feed } = tradeDashboard();
  for (let i = 0; i < 60; i++) run(`addTradeCard(${JSON.stringify({ ...trade('t' + i, 'S' + i, i), sandboxId: 'sbx_a' })})`);
  const shown = feed();
  assert.equal(shown.length, 50);
  assert.equal(shown[0], 'S59');
  assert.equal(shown.at(-1), 'S10');
});

// ── Agent identity drafts ───────────────────

function agentsDashboard(prompt = 'saved prompt') {
  const dash = loadDashboard();
  dash.run(`config = { accounts: [], agents: [{ id: 'a1', name: 'Alpha', customSystemPrompt: ${JSON.stringify(prompt)} }], strategies: [], models: [], sandboxes: {} }`);
  const list = () => dash.document.getElementById('agents-list').innerHTML;
  const openIdentity = () => {
    // Mirror the rendered (closed) panel, then click "Edit Identity".
    dash.document.getElementById('agent-prompt-a1').style.display = 'none';
    dash.document.getElementById('agent-prompt-text-a1').value = 'saved prompt';
    dash.ctx.toggleAgentPrompt('a1');
  };
  return { ...dash, list, openIdentity };
}

test('health-poll re-render keeps unsaved identity edits and open panels without focus', async () => {
  const { ctx, run, route, list, openIdentity } = agentsDashboard();
  ctx.renderAgents();
  assert.match(list(), />saved prompt<\/textarea>/);

  openIdentity();
  ctx.trackAgentMore('a1', true);
  ctx.trackAgentPromptDraft('a1', 'my <unsaved> edit');

  // The 15s health poll and an SSE config event both rebuild the Agents list.
  route(url => url === '/api/health', () => jsonResponse({ sandboxes: [], trading_bot: 'healthy' }));
  ctx.renderAccounts = () => {};
  await ctx.checkHealth();
  run(`config = { ...config, agents: [{ id: 'a1', name: 'Alpha', customSystemPrompt: 'changed elsewhere' }] }`);
  ctx.renderAgents();

  const out = list();
  assert.match(out, />my &lt;unsaved&gt; edit<\/textarea>/);
  assert.match(out, /id="agent-prompt-a1" style="display:block/);
  assert.match(out, /<details class="agent-more" open/);
});

test('re-render restores focus and caret in the identity editor', () => {
  const { ctx, document } = agentsDashboard();
  document.activeElement = { id: 'agent-prompt-text-a1', selectionStart: 3, selectionEnd: 7, scrollTop: 40 };
  ctx.renderAgents();
  const ta = document.getElementById('agent-prompt-text-a1');
  assert.equal(document.activeElement, ta);
  assert.equal(ta.selectionStart, 3);
  assert.equal(ta.selectionEnd, 7);
  assert.equal(ta.scrollTop, 40);
});

test('saving clears the draft only on success; a failed save keeps it', async () => {
  const { ctx, document, run, route, calls } = agentsDashboard();
  ctx.renderAll = () => {};
  ctx.logToSandbox = () => {};
  let status = 400;
  route(url => url === '/api/agents/a1', () => jsonResponse(status < 400 ? { ok: true } : { error: 'disk full' }, status));
  route(url => url === '/api/config', () => jsonResponse(run('config')));

  document.getElementById('agent-prompt-text-a1').value = 'new identity';
  ctx.trackAgentPromptDraft('a1', 'new identity');
  await ctx.saveAgentPrompt('a1');
  assert.match(calls.alerts[0], /disk full/);
  assert.equal(run(`agentPromptDrafts.get('a1')`), 'new identity');

  status = 200;
  await ctx.saveAgentPrompt('a1');
  assert.equal(run(`agentPromptDrafts.has('a1')`), false);
  const put = calls.fetches.filter(f => f.url === '/api/agents/a1').at(-1);
  assert.equal(JSON.parse(put.opts.body).customSystemPrompt, 'new identity');
});

test('reset to default discards the draft', async () => {
  const { ctx, run, route } = agentsDashboard();
  ctx.renderAll = () => {};
  route(url => url === '/api/agents/a1', () => jsonResponse({ ok: true }));
  route(url => url === '/api/config', () => jsonResponse(run('config')));
  ctx.trackAgentPromptDraft('a1', 'scratch');
  await ctx.clearAgentPrompt('a1');
  assert.equal(run(`agentPromptDrafts.has('a1')`), false);
});

// ── About images ────────────────────────────

test('About images load lazily with reserved dimensions', () => {
  for (const [src, size] of [['img/jake.jpg', 56], ['img/anyrentcloud.png', 48]]) {
    const tag = html.match(new RegExp(`<img src="${src}"[^>]*>`))?.[0];
    assert.ok(tag, `${src} present`);
    assert.match(tag, /loading="lazy"/);
    assert.match(tag, /decoding="async"/);
    assert.match(tag, new RegExp(`width="${size}" height="${size}"`));
    assert.match(tag, new RegExp(`width:${size}px;height:${size}px`), 'attributes match the rendered size');
  }
});
