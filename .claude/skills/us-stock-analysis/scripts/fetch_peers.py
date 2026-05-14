#!/usr/bin/env python3
"""
Build a side-by-side comparable-multiples table for N tickers.

Emits a JSON list, one row per ticker, with normalized fields:
  ticker, name, sector, industry, market_cap, price,
  pe_ttm, pe_forward, peg, ps, pb, ev_ebitda, ev_sales,
  fcf_yield, roe, roic, gross_margin, operating_margin, net_margin,
  revenue_growth_ttm, debt_to_equity, current_ratio

Usage:
  python fetch_peers.py --tickers SE,MELI,PDD,GRAB
"""

import argparse
import json
import sys
from pathlib import Path
from typing import Optional

SCRIPTS_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(SCRIPTS_DIR))

from fmp_client import FMPClient  # noqa: E402


def _g(d: Optional[dict], *keys):
    """Get first non-None value across alt key names (FMP renames fields between endpoints)."""
    if not d:
        return None
    for k in keys:
        v = d.get(k)
        if v is not None and v != "":
            return v
    return None


def _round(v, digits=2):
    try:
        return round(float(v), digits) if v is not None else None
    except (TypeError, ValueError):
        return None


def build_peer_row(client: FMPClient, ticker: str) -> dict:
    t = ticker.upper()
    quote = client.get_quote(t) or {}
    profile = client.get_profile(t) or {}
    ratios = client.get_ratios_ttm(t) or {}
    metrics = client.get_key_metrics_ttm(t) or {}

    return {
        "ticker": t,
        "name": _g(profile, "companyName", "name"),
        "sector": _g(profile, "sector"),
        "industry": _g(profile, "industry"),
        "price": _round(_g(quote, "price")),
        "market_cap": _g(quote, "marketCap", "mktCap"),
        "pe_ttm": _round(_g(ratios, "priceToEarningsRatioTTM", "priceEarningsRatioTTM")),
        "peg": _round(_g(ratios, "priceToEarningsGrowthRatioTTM", "priceEarningsToGrowthRatioTTM")),
        "ps": _round(_g(ratios, "priceToSalesRatioTTM")),
        "pb": _round(_g(ratios, "priceToBookRatioTTM")),
        "ev_ebitda": _round(_g(metrics, "evToEBITDATTM", "enterpriseValueOverEBITDATTM")),
        "ev_sales": _round(_g(metrics, "evToSalesTTM", "enterpriseValueOverRevenueTTM")),
        "fcf_yield": _round(_g(metrics, "freeCashFlowYieldTTM"), 4),
        "earnings_yield": _round(_g(metrics, "earningsYieldTTM"), 4),
        "roe": _round(_g(metrics, "returnOnEquityTTM") or _g(ratios, "returnOnEquityTTM"), 4),
        "roic": _round(_g(metrics, "returnOnInvestedCapitalTTM", "roicTTM"), 4),
        "gross_margin": _round(_g(ratios, "grossProfitMarginTTM"), 4),
        "operating_margin": _round(_g(ratios, "operatingProfitMarginTTM"), 4),
        "net_margin": _round(_g(ratios, "netProfitMarginTTM"), 4),
        "debt_to_equity": _round(_g(ratios, "debtToEquityRatioTTM", "debtEquityRatioTTM")),
        "current_ratio": _round(_g(ratios, "currentRatioTTM")),
        "dividend_yield": _round(_g(ratios, "dividendYieldTTM"), 4),
    }


def main() -> int:
    parser = argparse.ArgumentParser(description="Peer comparable-multiples table.")
    parser.add_argument(
        "--tickers", required=True, help="Comma-separated tickers, e.g. SE,MELI,PDD,GRAB"
    )
    args = parser.parse_args()

    tickers = [t.strip() for t in args.tickers.split(",") if t.strip()]
    if not tickers:
        print("ERROR: at least one ticker required", file=sys.stderr)
        return 1

    try:
        client = FMPClient()
    except ValueError as e:
        print(f"ERROR: {e}", file=sys.stderr)
        return 1

    rows = []
    for t in tickers:
        try:
            rows.append(build_peer_row(client, t))
        except Exception as e:  # noqa: BLE001
            print(f"WARN: {t} failed: {e}", file=sys.stderr)
            rows.append({"ticker": t.upper(), "error": str(e)})

    output = {"peers": rows, "_api_stats": client.get_api_stats()}
    json.dump(output, sys.stdout, indent=2, default=str)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
