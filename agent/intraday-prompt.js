// Intraday-context prompt-injection helpers used by harness.js to prepend
// a compact per-symbol blob to Prophet's market-hours heartbeats.
//
// Two surfaces:
//   - shouldInjectIntraday(strategyId, phase): gate; only true for Prophet
//     during market_open / midday / market_close.
//   - renderIntradayBlock(set): turns IntradaySignalSet (from /api/v1/intraday/
//     signals) into a compact aligned-columns markdown block. Returns empty
//     string on null/empty input so the caller can append unconditionally.

const PROPHET_STRATEGY = 'v2-options';
const MARKET_HOURS_PHASES = new Set(['market_open', 'midday', 'market_close']);

export function shouldInjectIntraday(strategyId, phase) {
  if (!strategyId || !phase) return false;
  if (strategyId !== PROPHET_STRATEGY) return false;
  return MARKET_HOURS_PHASES.has(phase);
}

// fmtNum produces a fixed-width string for an aligned column. Handles undefined
// / null / NaN by emitting "--" so the column still aligns.
function fmtNum(n, digits = 2, width = 7) {
  if (n === undefined || n === null || Number.isNaN(n)) return '--'.padEnd(width);
  const sign = n > 0 ? '+' : '';
  return (sign + n.toFixed(digits)).padEnd(width);
}

function fmtPrice(p, width = 8) {
  if (p === undefined || p === null || Number.isNaN(p)) return '--'.padEnd(width);
  return p.toFixed(2).padEnd(width);
}

function fmtSectorPct(s, width = 7) {
  if (s === undefined || s === null) return '--'.padEnd(width);
  if (Number.isNaN(s) || s === 0) return '--'.padEnd(width);
  const sign = s > 0 ? '+' : '';
  return (sign + s.toFixed(2)).padEnd(width);
}

export function renderIntradayBlock(set) {
  if (!set || !Array.isArray(set.signals) || set.signals.length === 0) {
    return '';
  }
  const sigs = set.signals;

  // Build aligned columns. Symbol column width = max(symbol length + 1, 7).
  const symWidth = Math.max(...sigs.map(s => (s.symbol || '').length), 6) + 1;
  const pad = ' '.repeat(symWidth);

  const header = pad + sigs.map(s => (s.symbol || '?').padEnd(8)).join('');
  const priceRow = 'price'.padEnd(symWidth) + sigs.map(s => fmtPrice(s.price)).join('');
  const dayRow   = 'day%'.padEnd(symWidth)  + sigs.map(s => fmtNum(s.day_change_pct)).join(' ');
  const vwapRow  = 'vwap%'.padEnd(symWidth) + sigs.map(s => fmtNum(s.dist_from_vwap_pct)).join(' ');
  const rvolRow  = 'rvol'.padEnd(symWidth)  + sigs.map(s => fmtNum(s.rvol, 2, 8)).join('');
  const rngRow   = 'rng/A'.padEnd(symWidth) + sigs.map(s => fmtNum(s.range_over_atr, 2, 8)).join('');
  const secRow   = 'sec%'.padEnd(symWidth)  + sigs.map(s => fmtSectorPct(s.sector_change_pct)).join(' ');

  // Sector ETF legend — only emit if at least one symbol has one mapped.
  const sectorEntries = sigs.filter(s => s.sector_etf).map(s => `${s.symbol}=${s.sector_etf}`);
  const legend = sectorEntries.length
    ? `(sec ETFs: ${sectorEntries.join(', ')})`
    : '';

  const failedNotes = sigs.filter(s => s.note).map(s => `${s.symbol}: ${s.note}`);
  const notesLine = failedNotes.length ? `notes: ${failedNotes.join('; ')}` : '';

  const lines = [
    '## Intraday Context (snapshot, <60s cached)',
    '',
    header,
    priceRow,
    dayRow,
    vwapRow,
    rvolRow,
    rngRow,
    secRow,
  ];
  if (legend) lines.push(legend);
  if (notesLine) lines.push(notesLine);

  return lines.join('\n');
}
