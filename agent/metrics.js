import { alpacaTradingUrl } from './defaults.js';
import { getMarketSession } from './market-session.js';
import { timingSafeEqual } from 'node:crypto';

export function metricsTokenMatches(presented, expected) {
  if (typeof presented !== 'string' || !expected) return false;
  const a = Buffer.from(presented);
  const b = Buffer.from(expected);
  return a.length === b.length && timingSafeEqual(a, b);
}

const DEFINITIONS = {
  broker_up: ['gauge', 'Broker account, position, order, and clock reads succeeded.'],
  snapshot_timestamp_seconds: ['gauge', 'Unix time of the last successful broker snapshot.'],
  agent_running: ['gauge', 'Agent heartbeat loop is running.'],
  agent_paused: ['gauge', 'Agent heartbeat loop is paused.'],
  agent_heartbeats_total: ['counter', 'Completed or attempted model heartbeats since agent start.'],
  agent_errors_total: ['counter', 'Agent errors since agent start.'],
  last_heartbeat_timestamp_seconds: ['gauge', 'Unix time of the last model heartbeat.'],
  market_open: ['gauge', 'Broker reports an open weekday exchange session.'],
  equity_usd: ['gauge', 'Current broker account equity in USD.'],
  cash_usd: ['gauge', 'Current broker account cash in USD.'],
  buying_power_usd: ['gauge', 'Current broker buying power in USD.'],
  daily_equity_change_usd: ['gauge', 'Equity minus previous close equity; includes deposits and withdrawals.'],
  daily_equity_change_percent: ['gauge', 'Equity change as percent of previous close equity.'],
  unrealized_pl_usd: ['gauge', 'Sum of unrealized position profit and loss in USD.'],
  gross_exposure_usd: ['gauge', 'Sum of absolute position market values in USD.'],
  open_positions: ['gauge', 'Current number of broker positions.'],
  open_orders: ['gauge', 'Current number of open broker orders, capped at 500.'],
  open_orders_truncated: ['gauge', 'Open order count may be truncated at the broker page limit.'],
};

function escaped(value) {
  return String(value).replaceAll('\\', '\\\\').replaceAll('\n', '\\n').replaceAll('"', '\\"');
}

function numeric(value) {
  return value === null || value === undefined || value === '' ? NaN : Number(value);
}

export function renderTradingMetrics(snapshots) {
  const lines = [];
  for (const [name, [type, help]] of Object.entries(DEFINITIONS)) {
    lines.push(`# HELP openprophet_${name} ${help}`, `# TYPE openprophet_${name} ${type}`);
    for (const snapshot of snapshots) {
      const value = snapshot.values[name];
      if (!Number.isFinite(value)) continue;
      const labels = Object.entries(snapshot.labels).map(([k, v]) => `${k}="${escaped(v)}"`).join(',');
      lines.push(`openprophet_${name}{${labels}} ${value}`);
    }
  }
  return lines.join('\n') + '\n';
}

export function createTradingMetrics({ getSandboxes, getAccount, getState, fetchFn = fetch, now = () => new Date(), cacheMs = 30000 }) {
  let expires = 0;
  let cached = '';
  let pending;

  async function collect(sandbox) {
    const account = getAccount(sandbox.accountId);
    const state = getState(sandbox.id) || {};
    const snapshot = {
      labels: { sandbox: sandbox.id, mode: account?.paper ? 'paper' : 'live', model: state.activeModel || sandbox.agent?.model || 'unknown' },
      values: {
        broker_up: 0,
        agent_running: Number(Boolean(state.running)),
        agent_paused: Number(Boolean(state.paused)),
        agent_heartbeats_total: numeric(state.stats?.totalBeats || 0),
        agent_errors_total: numeric(state.stats?.errors || 0),
        last_heartbeat_timestamp_seconds: Date.parse(state.lastBeatTime) / 1000,
      },
    };
    if (!account) return snapshot;
    const timestamp = now();
    const read = async endpoint => {
      const response = await fetchFn(new URL(endpoint, alpacaTradingUrl(account.paper, account.baseUrl)), {
        headers: { 'APCA-API-KEY-ID': account.publicKey, 'APCA-API-SECRET-KEY': account.secretKey },
        signal: AbortSignal.timeout(5000),
      });
      if (!response.ok) throw new Error('Broker read failed');
      return response.json();
    };
    try {
      const [balance, positions, orders, clock] = await Promise.all([
        read('/v2/account'), read('/v2/positions'), read('/v2/orders?status=open&limit=500'),
        getMarketSession(account, { now: timestamp, fetchFn }),
      ]);
      if (!Array.isArray(positions) || !Array.isArray(orders) || !Number.isFinite(numeric(balance.equity))) throw new Error('Invalid broker data');
      const equity = numeric(balance.equity);
      const previous = numeric(balance.last_equity);
      Object.assign(snapshot.values, {
        broker_up: 1,
        snapshot_timestamp_seconds: timestamp.getTime() / 1000,
        market_open: Number(clock.isOpen),
        equity_usd: equity,
        cash_usd: numeric(balance.cash),
        buying_power_usd: numeric(balance.buying_power),
        daily_equity_change_usd: equity - previous,
        daily_equity_change_percent: previous > 0 ? (equity - previous) / previous * 100 : NaN,
        unrealized_pl_usd: positions.reduce((sum, p) => sum + numeric(p.unrealized_pl), 0),
        gross_exposure_usd: positions.reduce((sum, p) => sum + Math.abs(numeric(p.market_value)), 0),
        open_positions: positions.length,
        open_orders: orders.length,
        open_orders_truncated: Number(orders.length === 500),
      });
    } catch {
      // Omit financial values on failure so Grafana cannot mistake stale data for fresh data.
    }
    return snapshot;
  }

  return {
    async render() {
      if (now().getTime() < expires) return cached;
      if (!pending) {
        pending = Promise.all(getSandboxes().map(collect)).then(snapshots => {
          cached = renderTradingMetrics(snapshots);
          expires = now().getTime() + cacheMs;
          return cached;
        }).finally(() => { pending = null; });
      }
      return pending;
    },
  };
}
