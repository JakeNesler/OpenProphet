import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import vm from 'node:vm';

const html = await fs.readFile(new URL('../agent/public/index.html', import.meta.url), 'utf8');
const editorSource = html.slice(html.indexOf('// ── Permissions Config'), html.indexOf('// ── Slack Plugin Config'));
const refreshStart = html.includes('let _sandboxConfigRequestId')
  ? html.indexOf('let _sandboxConfigRequestId')
  : html.indexOf('async function refreshSelectedSandboxConfig()');
const refreshSource = html.slice(refreshStart, html.indexOf('// ── Terminal logging'));

function setup() {
  const elements = new Map();
  for (const match of html.matchAll(/<input\b[^>]*\bid="(perm-[^"]+)"[^>]*>/g)) {
    const tag = match[0];
    elements.set(match[1], {
      id: match[1], type: tag.match(/type="([^"]+)"/)[1], value: '', checked: false,
      valid: true, checkValidity() { return this.valid; },
    });
  }
  elements.set('permissions-save-status', { textContent: '' });
  const defaults = {
    allowLiveTrading: false, allowStocks: true, allowOptions: false,
    allow0DTE: false, requireConfirmation: true, maxPositionPct: 5,
    maxDeployedPct: 15, maxDailyLoss: 1, maxOpenPositions: 3,
    maxOrderValue: 2500, maxToolRoundsPerBeat: 20, blockedTools: ['cancel_order'],
  };
  const stored = { a: { ...defaults }, b: { ...defaults, maxPositionPct: 20 } };
  const writes = [], errors = [], timers = new Map();
  let selected = 'a', timerId = 0;
  const response = data => ({ ok: true, json: async () => structuredClone(data) });
  const context = vm.createContext({
    document: { getElementById: id => elements.get(id) },
    config: { sandboxes: { a: { permissions: { ...stored.a } }, b: { permissions: { ...stored.b } } } },
    sandboxScoped: { permissions: { ...stored.a } },
    getEffectiveSandboxId: () => selected,
    setTimeout(fn) { const id = ++timerId; timers.set(id, fn); return id; },
    clearTimeout(id) { timers.delete(id); },
    renderHeartbeat() {}, renderSlack() {}, renderPromptPreview() {}, updateState() {},
    log(message) { errors.push(message); }, showToast(message) { errors.push(message); },
    async fetch(url, options = {}) {
      if (options.method === 'PUT') {
        const { sandboxId, ...patch } = JSON.parse(options.body);
        writes.push({ sandboxId, ...patch });
        Object.assign(stored[sandboxId], patch);
        return response({ ok: true });
      }
      const id = url.match(/sandboxes\/([^/]+)\/dashboard/)[1];
      return response({ permissions: stored[id] });
    },
  });
  vm.runInContext(refreshSource + '\n' + editorSource, context);
  context.renderPermissions();
  return {
    context, elements, stored, writes, errors, response,
    select(id) { selected = id; context.sandboxScoped = { permissions: { ...stored[id] } }; context.renderPermissions(); },
    edit(id, value) {
      const input = elements.get(id);
      if (input.type === 'checkbox') input.checked = value;
      else input.value = String(value);
      context.savePermissions(input);
    },
    async flush() {
      const callbacks = [...timers.values()]; timers.clear();
      await Promise.all(callbacks.map(fn => fn()));
    },
  };
}

test('a background refresh cannot replace 100% with the stored 5% before save', async () => {
  const ui = setup();
  ui.edit('perm-maxpos', 100);
  ui.context.renderPermissions();
  await ui.flush();
  assert.equal(ui.stored.a.maxPositionPct, 100);
  assert.equal(Number(ui.elements.get('perm-maxpos').value), 100);
  assert.deepEqual(ui.writes, [{ sandboxId: 'a', maxPositionPct: 100 }]);
});

test('switching accounts during the debounce saves only the original account', async () => {
  const ui = setup();
  ui.edit('perm-maxpos', 100);
  ui.select('b');
  await ui.flush();
  assert.equal(ui.stored.a.maxPositionPct, 100);
  assert.equal(ui.stored.b.maxPositionPct, 20);
  assert.equal(Number(ui.elements.get('perm-maxpos').value), 20);
});

test('edits on separate accounts keep their own values and timers', async () => {
  const ui = setup();
  ui.edit('perm-maxpos', 100);
  ui.select('b');
  ui.edit('perm-maxpos', 30);
  ui.select('a');
  assert.equal(Number(ui.elements.get('perm-maxpos').value), 100);
  await ui.flush();
  assert.equal(ui.stored.a.maxPositionPct, 100);
  assert.equal(ui.stored.b.maxPositionPct, 30);
});

test('rapid edits are serialized so an older request cannot overwrite the last edit', async () => {
  const ui = setup();
  const original = ui.context.fetch;
  let release;
  const gate = new Promise(resolve => { release = resolve; });
  let putCount = 0;
  ui.context.fetch = async (url, options) => {
    if (options?.method === 'PUT' && ++putCount === 1) await gate;
    return original(url, options);
  };
  ui.edit('perm-maxpos', 10);
  const first = ui.flush();
  ui.edit('perm-maxpos', 100);
  await ui.flush();
  assert.equal(putCount, 1, 'second write waits for the first');
  ui.context.renderPermissions();
  assert.equal(Number(ui.elements.get('perm-maxpos').value), 100);
  release();
  await first;
  await ui.flush();
  assert.deepEqual(ui.writes.map(w => w.maxPositionPct), [10, 100]);
  assert.equal(ui.stored.a.maxPositionPct, 100);
});

test('zero order value and explicit false toggles survive refresh and save', async () => {
  const ui = setup();
  ui.edit('perm-maxorder', 0);
  ui.edit('perm-stocks', false);
  await ui.context.refreshSelectedSandboxConfig();
  await ui.flush();
  assert.equal(ui.stored.a.maxOrderValue, 0);
  assert.equal(ui.stored.a.allowStocks, false);
  assert.equal(ui.stored.a.allowLiveTrading, false, 'unedited controls are unchanged');
});

test('empty or invalid numeric inputs never save fallback defaults', async () => {
  const ui = setup();
  ui.edit('perm-maxpos', '');
  ui.context.renderPermissions();
  assert.equal(ui.elements.get('perm-maxpos').value, '');
  await ui.flush();
  assert.equal(ui.writes.length, 0);
  ui.elements.get('perm-maxpos').valid = false;
  ui.edit('perm-maxpos', 101);
  await ui.flush();
  assert.equal(ui.writes.length, 0);
  assert.equal(ui.stored.a.maxPositionPct, 5);
});

test('a failed save keeps the edit visible, reports the error, and allows retry', async () => {
  const ui = setup();
  const original = ui.context.fetch;
  ui.context.fetch = async (url, options) => options?.method === 'PUT'
    ? { ok: false, status: 503, json: async () => ({ error: 'Try again' }) }
    : original(url, options);
  ui.edit('perm-maxpos', 100);
  await ui.flush();
  await ui.context.refreshSelectedSandboxConfig();
  assert.equal(Number(ui.elements.get('perm-maxpos').value), 100);
  assert.equal(ui.stored.a.maxPositionPct, 5);
  assert.match(ui.errors[0], /Could not save: Try again/);
  ui.context.fetch = original;
  ui.edit('perm-maxpos', 100);
  await ui.flush();
  assert.equal(ui.stored.a.maxPositionPct, 100);
});

test('a late account refresh cannot populate another account with old permissions', async () => {
  const ui = setup();
  const original = ui.context.fetch;
  let release;
  ui.context.fetch = async url => {
    if (url.includes('/a/dashboard')) return new Promise(resolve => { release = () => resolve(ui.response({ permissions: ui.stored.a })); });
    return original(url);
  };
  const oldRefresh = ui.context.refreshSelectedSandboxConfig();
  ui.select('b');
  await ui.context.refreshSelectedSandboxConfig();
  release();
  await oldRefresh;
  assert.equal(Number(ui.elements.get('perm-maxpos').value), 20);
});

test('a refresh started before a save cannot restore a stale value after it', async () => {
  const ui = setup();
  const original = ui.context.fetch;
  let release;
  let getCount = 0;
  ui.context.fetch = async (url, options) => {
    if (!options?.method && ++getCount === 1) {
      const stale = structuredClone(ui.stored.a);
      return new Promise(resolve => { release = () => resolve(ui.response({ permissions: stale })); });
    }
    return original(url, options);
  };
  const oldRefresh = ui.context.refreshSelectedSandboxConfig();
  ui.edit('perm-maxpos', 100);
  await ui.flush();
  release();
  await oldRefresh;
  assert.equal(Number(ui.elements.get('perm-maxpos').value), 100);
  assert.equal(ui.stored.a.maxPositionPct, 100);
});
