import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  portForAgent, alpacaTradingUrl, resolveAgentModel,
  DEFAULT_AGENT_MODEL, ALPACA_PAPER_TRADING_URL, ALPACA_LIVE_TRADING_URL,
  BEAT_TIMEOUT_MS, SIGKILL_GRACE_MS, DEFAULT_MAX_TOOL_ROUNDS, BEAT_BACKOFF,
} from '../agent/defaults.js';

// The ORIGINAL orchestrator.getSandboxPort algorithm, kept here as the oracle so any drift in
// the extracted portForAgent (which would move a running sandbox to a different port) fails loudly.
function originalSandboxPort(sandboxId, base) {
  let hash = 0;
  for (const char of String(sandboxId || 'default')) {
    hash = (hash * 31 + char.charCodeAt(0)) % 1000;
  }
  return base + (hash % 10) + 1;
}

test('portForAgent is byte-for-byte identical to the original port algorithm', () => {
  for (const id of ['default', '', 'sbx_abc', '92a61c6a', 'account-1', 'Prophet', 'x'.repeat(64), 'ünïcode']) {
    assert.equal(portForAgent(id, 4534), originalSandboxPort(id, 4534), `port mismatch for id "${id}"`);
  }
  // ports stay within the documented 4535–4544 window
  for (const id of ['a', 'b', 'c', 'd', 'e']) {
    const p = portForAgent(id, 4534);
    assert.ok(p >= 4535 && p <= 4544, `${id} -> ${p} out of range`);
  }
});

test('alpacaTradingUrl never infers live and honors an explicit override', () => {
  assert.equal(alpacaTradingUrl(true), ALPACA_PAPER_TRADING_URL);
  assert.equal(alpacaTradingUrl(false), ALPACA_LIVE_TRADING_URL);
  assert.equal(alpacaTradingUrl(undefined), ALPACA_LIVE_TRADING_URL); // matches prior ternary (falsy paper -> live)
  assert.equal(alpacaTradingUrl(true, 'https://custom'), 'https://custom');
});

test('resolveAgentModel precedence: explicit > persisted > env > default', () => {
  assert.equal(resolveAgentModel({ explicit: 'X', persisted: 'Y', env: 'Z' }), 'X');
  assert.equal(resolveAgentModel({ persisted: 'Y', env: 'Z' }), 'Y');
  assert.equal(resolveAgentModel({ env: 'Z' }), 'Z');
  assert.equal(resolveAgentModel({}), DEFAULT_AGENT_MODEL);
});

test('operational constants keep their extracted values (regression lock)', () => {
  assert.equal(BEAT_TIMEOUT_MS, 300000);
  assert.equal(SIGKILL_GRACE_MS, 5000);
  assert.equal(DEFAULT_MAX_TOOL_ROUNDS, 25);
  assert.deepEqual(BEAT_BACKOFF, { threshold: 3, factor: 16, capSeconds: 3600 });
});
