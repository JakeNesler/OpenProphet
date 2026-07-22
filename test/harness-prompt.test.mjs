import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildSystemPrompt } from '../agent/harness.js';

test('default system prompt carries the mandate, decision loop, risk discipline, and learning loop', async () => {
  const p = await buildSystemPrompt({ name: 'Prophet' }, {});
  assert.ok(p.includes('You are Prophet'), 'names the agent');
  assert.ok(/Preserve capital/.test(p), 'states capital-preservation mandate');
  assert.ok(p.includes('## Your Heartbeat Loop'), 'has the ordered decision loop');
  assert.ok(p.includes('Risk Discipline'), 'has hard risk discipline');
  assert.ok(p.includes('GUARDRAILS'), 'points at the per-beat guardrails');
  assert.ok(p.includes('find_similar_setups') && p.includes('store_trade_setup'), 'wires recall + store');
  assert.ok(p.includes('place_buy_order'), 'includes the generated tool catalog');
});

test('custom template overrides the identity but keeps the operating instructions', async () => {
  const p = await buildSystemPrompt({ systemPromptTemplate: 'custom', customSystemPrompt: 'I am a custom bot.' }, {});
  assert.ok(p.startsWith('I am a custom bot.'), 'uses the custom identity verbatim');
  assert.ok(p.includes('## Your Heartbeat Loop'), 'still appends the system instructions');
});
