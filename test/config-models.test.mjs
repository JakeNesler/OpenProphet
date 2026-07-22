import { test } from 'node:test';
import assert from 'node:assert/strict';
import { getAvailableModels } from '../agent/config-store.js';

test('getAvailableModels returns a non-empty catalog of provider/model ids', () => {
  const models = getAvailableModels();
  assert.ok(Array.isArray(models) && models.length > 0, 'non-empty');
  assert.ok(models.every(m => typeof m.id === 'string' && m.id.includes('/')), 'all ids are provider/model');
});

test('getAvailableModels caches within its TTL (no re-query on the second call)', () => {
  const a = getAvailableModels();
  const b = getAvailableModels();
  assert.equal(a, b, 'second call within TTL returns the cached list');
});

test('getAvailableModels({force}) re-resolves and still returns a valid catalog', () => {
  const models = getAvailableModels({ force: true });
  assert.ok(Array.isArray(models) && models.length > 0);
});
