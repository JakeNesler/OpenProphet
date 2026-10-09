// Regression: the 0DTE gate must use LOCAL calendar dates. Under a US timezone the old
// `new Date('YYYY-MM-DD')` (UTC midnight) parse landed on the previous evening, so a
// same-day expiry passed and a next-day expiry was wrongly blocked. Node re-reads TZ on
// assignment, so set it before any Date is constructed in this file.
process.env.TZ = 'America/New_York';

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { checkPermissions } from '../permissions.js';

const PERMS = {
  allowLiveTrading: true, allowStocks: true, allowOptions: true, allow0DTE: false,
  requireConfirmation: false, maxOrderValue: 0, blockedTools: [],
};

for (const tz of ['America/New_York', 'America/Los_Angeles', 'UTC', 'Asia/Tokyo']) {
  test(`allow0DTE=false is timezone-correct in ${tz}`, () => {
    process.env.TZ = tz;
    // 2026-03-20 08:00 local, i.e. a trading-day morning in that zone
    const now = new Date(2026, 2, 20, 8, 0, 0);
    assert.throws(() => checkPermissions('place_options_order', { symbol: 'AAPL260320C00400000', quantity: 1 }, PERMS, now), /0DTE options are NOT allowed/, 'same-day expiry must be blocked');
    assert.doesNotThrow(() => checkPermissions('place_options_order', { symbol: 'AAPL260321C00400000', quantity: 1 }, PERMS, now), 'next-day expiry must be allowed');
    assert.doesNotThrow(() => checkPermissions('place_options_order', { symbol: 'AAPL260319C00400000', quantity: 1 }, PERMS, now), 'yesterday\'s expiry is not 0DTE');
  });
}
