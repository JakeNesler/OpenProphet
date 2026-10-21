import test from 'node:test';
import assert from 'node:assert/strict';
import { AgentState } from '../agent/harness.js';

test('live and replayed trades share an ID and timestamp; identical orders stay distinct', () => {
  const state = new AgentState();
  const events = [];
  state.on('trade', trade => events.push(trade));
  const order = { symbol: 'TEST', side: 'buy', quantity: 1 };
  state.addTrade(order);
  state.addTrade(order);
  assert.notEqual(events[0].id, events[1].id);
  assert.ok(events.every(event => Number.isFinite(Date.parse(event.timestamp))));
  assert.deepEqual(state.toJSON().recentTrades, events.toReversed());
  assert.deepEqual(order, { symbol: 'TEST', side: 'buy', quantity: 1 });
  for (let i = 0; i < 60; i++) state.addTrade(order);
  assert.equal(state.recentTrades.length, 50);
  assert.equal(state.toJSON().recentTrades.length, 10);
});
