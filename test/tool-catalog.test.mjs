import { test } from 'node:test';
import assert from 'node:assert/strict';
import { renderToolMenu, TOOL_CATALOG } from '../agent/tool-catalog.js';

test('tool catalog renders every category exactly once', () => {
  const menu = renderToolMenu();
  assert.equal(menu.split('\n').length, Object.keys(TOOL_CATALOG).length, 'one line per category');
  for (const cat of Object.keys(TOOL_CATALOG)) {
    assert.ok(menu.includes(`**${cat}**:`), `menu missing category ${cat}`);
  }
});

test('tool catalog exposes the core order/recall tools', () => {
  assert.ok(TOOL_CATALOG['Trading'].includes('place_buy_order'));
  assert.ok(TOOL_CATALOG['Options'].includes('place_options_order'));
  const menu = renderToolMenu();
  assert.ok(menu.includes('find_similar_setups'));
  assert.ok(menu.includes('store_trade_setup'));
});
