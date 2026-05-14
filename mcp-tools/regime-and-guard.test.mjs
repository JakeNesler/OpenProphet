// Tests for the regime gate + trade guard MCP tools. Run via: npm test
//
// The tools are thin wrappers over the Go bot's HTTP endpoints, so the test
// surface is small: schema sanity (no required args for either tool), correct
// endpoint hit, response shape. Network is faked through the injected
// callTradingBot.

import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  regimeAndGuardTools,
  handleRegimeAndGuardTool,
} from './regime-and-guard.mjs';

// ── helpers ────────────────────────────────────────────────────────

function makeRecorder(responseFor) {
  const calls = [];
  return {
    calls,
    async callTradingBot(endpoint, method = 'GET', data = null) {
      calls.push({ endpoint, method, data });
      return responseFor[endpoint] ?? {};
    },
  };
}

function parsedTextFromContent(result) {
  // MCP tool results wrap the payload as { content: [{ type: 'text', text }] }.
  // The text is JSON.stringified; parse it back so tests assert against the
  // structured payload, not formatting.
  assert.equal(result.content.length, 1);
  assert.equal(result.content[0].type, 'text');
  return JSON.parse(result.content[0].text);
}

// ── tool registration ──────────────────────────────────────────────

test('regimeAndGuardTools: exports both tools with no-arg schemas', () => {
  const names = regimeAndGuardTools.map((t) => t.name).sort();
  assert.deepEqual(names, ['get_guard_status', 'get_regime_gate_status']);

  for (const tool of regimeAndGuardTools) {
    assert.ok(tool.description?.length > 0, `${tool.name}: missing description`);
    // Both are no-arg tools. Schema must be a valid no-required object so the
    // MCP client doesn't ask the model for arguments it can't supply.
    assert.equal(tool.inputSchema.type, 'object');
    assert.deepEqual(tool.inputSchema.required ?? [], []);
  }
});

// ── get_regime_gate_status ─────────────────────────────────────────

test('handle get_regime_gate_status: hits /regime-gate/status and returns body', async () => {
  const rec = makeRecorder({
    '/regime-gate/status': {
      score: 62,
      tier: 'NORMAL',
      sizing_multiplier: 0.8,
      block_new_entries: false,
    },
  });
  const result = await handleRegimeAndGuardTool(
    'get_regime_gate_status',
    {},
    rec.callTradingBot,
  );
  assert.equal(rec.calls.length, 1);
  assert.equal(rec.calls[0].endpoint, '/regime-gate/status');
  assert.equal(rec.calls[0].method, 'GET');

  const body = parsedTextFromContent(result);
  assert.equal(body.score, 62);
  assert.equal(body.tier, 'NORMAL');
  assert.equal(body.sizing_multiplier, 0.8);
});

// ── get_guard_status ──────────────────────────────────────────────

test('handle get_guard_status: hits /guard/status and returns body', async () => {
  const rec = makeRecorder({
    '/guard/status': {
      penny_max_capital_pct: 0.2,
      sector_exposure_dollars: { TECH: 12500, INDEX_BETA: 8000 },
      sector_max_by_bucket_dollars: { TECH: 20000, INDEX_BETA: 25000 },
    },
  });
  const result = await handleRegimeAndGuardTool(
    'get_guard_status',
    {},
    rec.callTradingBot,
  );
  assert.equal(rec.calls.length, 1);
  assert.equal(rec.calls[0].endpoint, '/guard/status');

  const body = parsedTextFromContent(result);
  assert.equal(body.sector_exposure_dollars.TECH, 12500);
  assert.equal(body.sector_max_by_bucket_dollars.INDEX_BETA, 25000);
});

// ── unknown tool fall-through ─────────────────────────────────────

test('handle returns null for tool names this module does not own', async () => {
  // mcp-server.js's switch falls through to its own cases; returning null here
  // means "not my problem, keep dispatching." Returning anything truthy would
  // shadow the host's existing tools.
  const rec = makeRecorder({});
  const result = await handleRegimeAndGuardTool(
    'analyze_stocks',
    { symbols: ['AAPL'] },
    rec.callTradingBot,
  );
  assert.equal(result, null);
  assert.equal(rec.calls.length, 0);
});

// ── error propagation ─────────────────────────────────────────────

test('handle surfaces callTradingBot errors as the MCP content text', async () => {
  // If the bot is down or the endpoint 5xx's, the MCP client should see the
  // error inside the content payload — not a raw thrown exception that would
  // poison the agent's tool-use loop. Match the existing pattern from other
  // tools in mcp-server.js (errors come back as JSON-stringified text).
  const failing = {
    async callTradingBot() {
      throw new Error('Trading bot error: ECONNREFUSED');
    },
  };
  const result = await handleRegimeAndGuardTool(
    'get_regime_gate_status',
    {},
    failing.callTradingBot,
  );
  const body = parsedTextFromContent(result);
  assert.ok(body.error, 'expected error field in response');
  assert.match(body.error, /ECONNREFUSED/);
});
