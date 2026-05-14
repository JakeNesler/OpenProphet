#!/usr/bin/env python3
"""
Fetch a one-shot fundamental snapshot for a single ticker.

Emits a single JSON object on stdout with these top-level keys:
  ticker, quote, profile, ratios_ttm, key_metrics_ttm,
  income_annual, income_quarterly, balance_sheet_annual, cash_flow_annual,
  price_target_consensus, analyst_estimates_annual, recent_news

The skill ingests this JSON instead of running 5+ web searches.

Usage:
  python fetch_stock_snapshot.py --ticker SE
  python fetch_stock_snapshot.py --ticker AAPL --annual-years 5 --quarters 4

Exit codes:
  0 success (JSON on stdout)
  1 fatal (missing key, all calls failed, etc.) — stderr explains
"""

import argparse
import json
import sys
from pathlib import Path

SCRIPTS_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(SCRIPTS_DIR))

from fmp_client import FMPClient  # noqa: E402


def fetch_snapshot(
    ticker: str, annual_years: int = 5, quarters: int = 4, news_limit: int = 15
) -> dict:
    client = FMPClient()
    t = ticker.upper()

    snapshot: dict = {"ticker": t}

    snapshot["quote"] = client.get_quote(t)
    snapshot["profile"] = client.get_profile(t)
    snapshot["ratios_ttm"] = client.get_ratios_ttm(t)
    snapshot["key_metrics_ttm"] = client.get_key_metrics_ttm(t)

    snapshot["income_annual"] = client.get_income_statement(t, "annual", annual_years)
    snapshot["income_quarterly"] = client.get_income_statement(t, "quarter", quarters)
    snapshot["balance_sheet_annual"] = client.get_balance_sheet(t, "annual", annual_years)
    snapshot["cash_flow_annual"] = client.get_cash_flow(t, "annual", annual_years)

    snapshot["price_target_consensus"] = client.get_price_target_consensus(t)
    snapshot["analyst_estimates_annual"] = client.get_analyst_estimates(t, "annual", 4)
    snapshot["recent_news"] = client.get_stock_news(t, news_limit)

    snapshot["_api_stats"] = client.get_api_stats()
    return snapshot


def main() -> int:
    parser = argparse.ArgumentParser(description="Fetch FMP fundamental snapshot for a ticker.")
    parser.add_argument("--ticker", required=True, help="Stock ticker (e.g. SE, AAPL)")
    parser.add_argument("--annual-years", type=int, default=5, help="Years of annual statements")
    parser.add_argument("--quarters", type=int, default=4, help="Quarters of quarterly statements")
    parser.add_argument("--news-limit", type=int, default=15, help="Recent news items")
    args = parser.parse_args()

    try:
        snapshot = fetch_snapshot(args.ticker, args.annual_years, args.quarters, args.news_limit)
    except ValueError as e:
        print(f"ERROR: {e}", file=sys.stderr)
        return 1

    if snapshot.get("quote") is None and snapshot.get("profile") is None:
        print(
            f"ERROR: no FMP data returned for ticker '{args.ticker}'. Wrong symbol or rate-limited.",
            file=sys.stderr,
        )
        return 1

    json.dump(snapshot, sys.stdout, indent=2, default=str)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
