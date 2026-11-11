import { alpacaTradingUrl } from './defaults.js';

export function marketWeekday(now = new Date()) {
  return new Intl.DateTimeFormat('en-US', { timeZone: 'America/New_York', weekday: 'short' }).format(now);
}

// The broker clock includes exchange holidays and early closes.
export async function getMarketSession(account, { now = new Date(), fetchFn = fetch } = {}) {
  if (!account?.publicKey || !account?.secretKey) throw new Error('Broker credentials unavailable');
  const response = await fetchFn(new URL('/v2/clock', alpacaTradingUrl(account.paper, account.baseUrl)), {
    headers: { 'APCA-API-KEY-ID': account.publicKey, 'APCA-API-SECRET-KEY': account.secretKey },
    signal: AbortSignal.timeout(5000),
  });
  if (!response.ok) throw new Error(`Broker clock unavailable (HTTP ${response.status})`);
  const clock = await response.json();
  if (typeof clock.is_open !== 'boolean' || !Number.isFinite(Date.parse(clock.timestamp))) {
    throw new Error('Invalid broker clock response');
  }
  if (Math.abs(now.getTime() - Date.parse(clock.timestamp)) > 120000) throw new Error('Broker clock is stale');
  return {
    isOpen: clock.is_open && !['Sat', 'Sun'].includes(marketWeekday(now)),
    timestamp: clock.timestamp,
    nextOpen: clock.next_open,
    nextClose: clock.next_close,
  };
}
