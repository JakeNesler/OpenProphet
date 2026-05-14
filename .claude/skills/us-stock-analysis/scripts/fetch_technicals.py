#!/usr/bin/env python3
"""
Fetch historical OHLCV and compute technicals locally from real candles.

Returns JSON with:
  ticker, as_of_date, current_price, prev_close, day_change_pct,
  ma20, ma50, ma200, distance_to_ma50_pct, distance_to_ma200_pct,
  rsi14, macd, macd_signal, macd_hist,
  high_52w, low_52w, pct_off_52w_high, pct_off_52w_low,
  avg_volume_20d, last_volume, volume_ratio,
  atr14, atr_pct

This replaces the unreliable "technical analysis page" web search, which often
serves stale or wrong MAs.

Usage:
  python fetch_technicals.py --ticker SE
  python fetch_technicals.py --ticker AAPL --lookback 400
"""

import argparse
import json
import statistics
import sys
from pathlib import Path
from typing import Optional

SCRIPTS_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(SCRIPTS_DIR))

from fmp_client import FMPClient  # noqa: E402


def _ema(values: list[float], period: int) -> list[float]:
    if not values:
        return []
    k = 2.0 / (period + 1)
    out = [values[0]]
    for v in values[1:]:
        out.append(v * k + out[-1] * (1 - k))
    return out


def _rsi(closes: list[float], period: int = 14) -> Optional[float]:
    if len(closes) <= period:
        return None
    gains, losses = [], []
    for i in range(1, len(closes)):
        diff = closes[i] - closes[i - 1]
        gains.append(max(diff, 0.0))
        losses.append(max(-diff, 0.0))
    avg_gain = sum(gains[:period]) / period
    avg_loss = sum(losses[:period]) / period
    for i in range(period, len(gains)):
        avg_gain = (avg_gain * (period - 1) + gains[i]) / period
        avg_loss = (avg_loss * (period - 1) + losses[i]) / period
    if avg_loss == 0:
        return 100.0
    rs = avg_gain / avg_loss
    return 100.0 - (100.0 / (1.0 + rs))


def _macd(closes: list[float]) -> tuple[Optional[float], Optional[float], Optional[float]]:
    if len(closes) < 35:
        return None, None, None
    ema12 = _ema(closes, 12)
    ema26 = _ema(closes, 26)
    macd_line = [a - b for a, b in zip(ema12[-len(ema26):], ema26)]
    signal = _ema(macd_line, 9)
    if not macd_line or not signal:
        return None, None, None
    return macd_line[-1], signal[-1], macd_line[-1] - signal[-1]


def _atr(highs: list[float], lows: list[float], closes: list[float], period: int = 14) -> Optional[float]:
    if len(closes) <= period:
        return None
    trs = []
    for i in range(1, len(closes)):
        tr = max(
            highs[i] - lows[i],
            abs(highs[i] - closes[i - 1]),
            abs(lows[i] - closes[i - 1]),
        )
        trs.append(tr)
    if len(trs) < period:
        return None
    atr = sum(trs[:period]) / period
    for tr in trs[period:]:
        atr = (atr * (period - 1) + tr) / period
    return atr


def _pct(a: float, b: float) -> Optional[float]:
    if b == 0 or b is None:
        return None
    return round((a - b) / b * 100.0, 2)


def compute_technicals(ticker: str, lookback_days: int = 400) -> dict:
    client = FMPClient()
    bars = client.get_historical_prices(ticker.upper(), days=lookback_days)
    if not bars:
        return {"ticker": ticker.upper(), "error": "no historical price data"}

    # FMP returns newest-first; flip to oldest-first for indicator math.
    bars = list(reversed(bars))
    closes = [float(b["close"]) for b in bars if b.get("close") is not None]
    highs = [float(b["high"]) for b in bars if b.get("high") is not None]
    lows = [float(b["low"]) for b in bars if b.get("low") is not None]
    volumes = [float(b.get("volume") or 0) for b in bars]
    last_bar = bars[-1]

    current = closes[-1]
    prev_close = closes[-2] if len(closes) > 1 else None

    ma20 = sum(closes[-20:]) / 20 if len(closes) >= 20 else None
    ma50 = sum(closes[-50:]) / 50 if len(closes) >= 50 else None
    ma200 = sum(closes[-200:]) / 200 if len(closes) >= 200 else None

    window_252 = closes[-252:] if len(closes) >= 252 else closes
    high_52w = max(window_252)
    low_52w = min(window_252)

    rsi14 = _rsi(closes, 14)
    macd_line, macd_sig, macd_hist = _macd(closes)
    atr14 = _atr(highs, lows, closes, 14)
    avg_vol_20 = statistics.mean(volumes[-20:]) if len(volumes) >= 20 else None
    last_vol = volumes[-1] if volumes else None

    return {
        "ticker": ticker.upper(),
        "as_of_date": last_bar.get("date"),
        "current_price": round(current, 2),
        "prev_close": round(prev_close, 2) if prev_close else None,
        "day_change_pct": _pct(current, prev_close) if prev_close else None,
        "ma20": round(ma20, 2) if ma20 else None,
        "ma50": round(ma50, 2) if ma50 else None,
        "ma200": round(ma200, 2) if ma200 else None,
        "distance_to_ma50_pct": _pct(current, ma50) if ma50 else None,
        "distance_to_ma200_pct": _pct(current, ma200) if ma200 else None,
        "rsi14": round(rsi14, 2) if rsi14 is not None else None,
        "macd": round(macd_line, 4) if macd_line is not None else None,
        "macd_signal": round(macd_sig, 4) if macd_sig is not None else None,
        "macd_hist": round(macd_hist, 4) if macd_hist is not None else None,
        "high_52w": round(high_52w, 2),
        "low_52w": round(low_52w, 2),
        "pct_off_52w_high": _pct(current, high_52w),
        "pct_off_52w_low": _pct(current, low_52w),
        "avg_volume_20d": int(avg_vol_20) if avg_vol_20 else None,
        "last_volume": int(last_vol) if last_vol else None,
        "volume_ratio": round(last_vol / avg_vol_20, 2) if (avg_vol_20 and last_vol) else None,
        "atr14": round(atr14, 2) if atr14 is not None else None,
        "atr_pct": round(atr14 / current * 100.0, 2) if (atr14 is not None and current) else None,
        "_api_stats": client.get_api_stats(),
    }


def main() -> int:
    parser = argparse.ArgumentParser(description="Compute technicals from FMP historical prices.")
    parser.add_argument("--ticker", required=True)
    parser.add_argument("--lookback", type=int, default=400, help="Calendar days to pull")
    args = parser.parse_args()

    try:
        result = compute_technicals(args.ticker, args.lookback)
    except ValueError as e:
        print(f"ERROR: {e}", file=sys.stderr)
        return 1

    json.dump(result, sys.stdout, indent=2, default=str)
    sys.stdout.write("\n")
    return 0 if "error" not in result else 1


if __name__ == "__main__":
    sys.exit(main())
