import { test } from 'node:test';
import assert from 'node:assert/strict';
import { AgentHarness, checkCliAuth, getCurrentPhase } from '../agent/harness.js';
import { getMarketSession } from '../agent/market-session.js';

const account = { paper: true, publicKey: 'test-key', secretKey: 'test-secret', baseUrl: 'https://paper-api.alpaca.markets/v2' };
const now = new Date('2026-11-09T15:00:00Z');
const clock = { is_open: true, timestamp: now.toISOString(), next_open: '2026-11-10T14:30:00Z', next_close: '2026-11-09T21:00:00Z' };

test('phase uses the New York weekday as well as the New York time', () => {
  assert.equal(getCurrentPhase(new Date('2026-11-07T00:30:00Z')), 'after_hours');
  assert.equal(getCurrentPhase(new Date('2026-11-09T00:30:00Z')), 'closed');
  assert.equal(getCurrentPhase(now), 'market_open');
});

test('broker clock respects holiday and early-close responses and normalizes the URL', async () => {
  let requested;
  const fetchFn = async (url, options) => {
    requested = url.toString();
    assert.equal(options.headers['APCA-API-SECRET-KEY'], 'test-secret');
    return Response.json({ ...clock, is_open: false });
  };
  assert.equal((await getMarketSession(account, { now, fetchFn })).isOpen, false);
  assert.equal(requested, 'https://paper-api.alpaca.markets/v2/clock');
  assert.equal((await getMarketSession(account, { now, fetchFn: async () => Response.json(clock) })).isOpen, true);
});

test('closed weekdays, stale responses, invalid clocks, and broker failures fail closed', async () => {
  for (const response of [Response.json({}, { status: 401 }), Response.json({}), Response.json({ ...clock, timestamp: '2026-11-08T15:00:00Z' })]) {
    await assert.rejects(getMarketSession(account, { now, fetchFn: async () => response }));
  }
  const sunday = new Date('2026-11-08T15:00:00Z');
  assert.equal((await getMarketSession(account, { now: sunday, fetchFn: async () => Response.json({ ...clock, timestamp: sunday.toISOString() }) })).isOpen, false);
});

test('OpenAI OAuth satisfies the selected provider without Anthropic credentials', () => {
  assert.equal(checkCliAuth('openai/gpt-6.1-sol', { env: {}, exec: () => 'OpenAI oauth' }), true);
  assert.equal(checkCliAuth('openai/gpt-6.1-sol', { env: { ANTHROPIC_API_KEY: 'unrelated' }, exec: () => 'Anthropic oauth' }), false);
  assert.equal(checkCliAuth('openai/gpt-6.1-sol', { env: { OPENAI_API_KEY: 'test' }, exec: () => { throw new Error('should not run'); } }), true);
});

test('closed market skips the model and records the next opening; clock failure skips too', async () => {
  const harness = new AgentHarness({
    getPermissions: () => ({ regularMarketHoursOnly: true }),
    getAccount: () => account,
    getMarketSessionFn: async () => ({ isOpen: false, nextOpen: clock.next_open }),
  });
  harness._runClaude = () => { throw new Error('Model must not run outside the session'); };
  await harness._beat();
  assert.equal(harness.state.beatCount, 0);
  assert.equal(harness._nextMarketOpen, clock.next_open);
  assert.equal(harness._beating, false);
  harness.getMarketSessionFn = async () => { throw new Error('offline'); };
  await harness._beat();
  assert.equal(harness.state.stats.errors, 1);
  assert.equal(harness.state.beatCount, 0);
  assert.equal(harness._beating, false);
});
