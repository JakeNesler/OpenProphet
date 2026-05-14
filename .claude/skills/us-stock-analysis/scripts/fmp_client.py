#!/usr/bin/env python3
"""
FMP API client for us-stock-analysis skill.

Mirrors the pattern used by catalyst-news / analyst-actions / market-top-detector
so the skill stays self-contained (no shared library). Exposes the endpoints
this skill actually consumes: quote, profile, ratios/key metrics TTM, full
statements, historical prices, analyst targets, and stock news.

Soft-fail philosophy: missing FMP_API_KEY raises ValueError at construction.
Callers that want graceful WebSearch fallback should catch and continue.
"""

import os
import sys
import time
from typing import Optional

try:
    import requests
except ImportError:
    print("ERROR: requests library not found. Install with: pip install requests", file=sys.stderr)
    sys.exit(1)


class FMPClient:
    RATE_LIMIT_DELAY = 0.3
    MAX_RETRIES = 1

    STABLE_BASE = "https://financialmodelingprep.com/stable"
    V3_BASE = "https://financialmodelingprep.com/api/v3"

    def __init__(self, api_key: Optional[str] = None):
        self.api_key = api_key or os.getenv("FMP_API_KEY")
        if not self.api_key:
            raise ValueError(
                "FMP API key required. Set FMP_API_KEY environment variable "
                "or pass api_key parameter."
            )
        self.session = requests.Session()
        self.session.headers.update({"apikey": self.api_key})
        self.last_call_time = 0.0
        self.rate_limit_reached = False
        self.api_calls_made = 0

    def _get(self, url: str, params: Optional[dict] = None, quiet: bool = False, retry: int = 0):
        if self.rate_limit_reached:
            return None
        if params is None:
            params = {}

        elapsed = time.time() - self.last_call_time
        if elapsed < self.RATE_LIMIT_DELAY:
            time.sleep(self.RATE_LIMIT_DELAY - elapsed)

        try:
            resp = self.session.get(url, params=params, timeout=30)
            self.last_call_time = time.time()
            self.api_calls_made += 1

            if resp.status_code == 200:
                return resp.json()
            if resp.status_code == 429:
                if retry < self.MAX_RETRIES:
                    print("WARNING: FMP rate limit hit. Waiting 60s...", file=sys.stderr)
                    time.sleep(60)
                    return self._get(url, params, quiet=quiet, retry=retry + 1)
                self.rate_limit_reached = True
                return None
            if not quiet:
                print(
                    f"ERROR: FMP request failed: {resp.status_code} - {resp.text[:200]}",
                    file=sys.stderr,
                )
            return None
        except requests.exceptions.RequestException as e:
            print(f"ERROR: FMP request exception: {e}", file=sys.stderr)
            return None

    def _try_stable_then_v3(self, stable_path: str, v3_path: str, params: dict):
        data = self._get(f"{self.STABLE_BASE}/{stable_path}", params, quiet=True)
        if data is not None:
            return data
        return self._get(f"{self.V3_BASE}/{v3_path}", params)

    # ----- quote / profile -------------------------------------------------

    def get_quote(self, ticker: str) -> Optional[dict]:
        """Real-time quote: price, change, day range, 52w range, volume, market cap, P/E."""
        data = self._try_stable_then_v3(
            f"quote", f"quote/{ticker}", {"symbol": ticker}
        )
        if isinstance(data, list) and data:
            return data[0]
        if isinstance(data, dict):
            return data
        return None

    def get_profile(self, ticker: str) -> Optional[dict]:
        """Company profile: description, sector, industry, CEO, employees, website."""
        data = self._try_stable_then_v3(
            "profile", f"profile/{ticker}", {"symbol": ticker}
        )
        if isinstance(data, list) and data:
            return data[0]
        return None

    # ----- TTM ratios / key metrics ---------------------------------------

    def get_ratios_ttm(self, ticker: str) -> Optional[dict]:
        """TTM ratios: PE, PB, PS, current ratio, ROE, ROA, debt/equity, margins."""
        data = self._try_stable_then_v3(
            "ratios-ttm", f"ratios-ttm/{ticker}", {"symbol": ticker}
        )
        if isinstance(data, list) and data:
            return data[0]
        if isinstance(data, dict):
            return data
        return None

    def get_key_metrics_ttm(self, ticker: str) -> Optional[dict]:
        """TTM key metrics: EV/EBITDA, EV/Sales, FCF yield, ROIC, etc."""
        data = self._try_stable_then_v3(
            "key-metrics-ttm", f"key-metrics-ttm/{ticker}", {"symbol": ticker}
        )
        if isinstance(data, list) and data:
            return data[0]
        if isinstance(data, dict):
            return data
        return None

    # ----- financial statements -------------------------------------------

    def get_income_statement(self, ticker: str, period: str = "annual", limit: int = 5):
        """Income statement. period: 'annual' or 'quarter'."""
        params = {"symbol": ticker, "period": period, "limit": limit}
        return self._try_stable_then_v3(
            "income-statement", f"income-statement/{ticker}", params
        )

    def get_balance_sheet(self, ticker: str, period: str = "annual", limit: int = 5):
        params = {"symbol": ticker, "period": period, "limit": limit}
        return self._try_stable_then_v3(
            "balance-sheet-statement", f"balance-sheet-statement/{ticker}", params
        )

    def get_cash_flow(self, ticker: str, period: str = "annual", limit: int = 5):
        params = {"symbol": ticker, "period": period, "limit": limit}
        return self._try_stable_then_v3(
            "cash-flow-statement", f"cash-flow-statement/{ticker}", params
        )

    # ----- historical prices ---------------------------------------------

    def get_historical_prices(self, ticker: str, days: int = 300) -> Optional[list[dict]]:
        """Daily OHLCV for the last N calendar days. Returns list ordered newest-first."""
        from datetime import date, timedelta

        end = date.today()
        start = end - timedelta(days=days)
        params = {
            "symbol": ticker,
            "from": start.isoformat(),
            "to": end.isoformat(),
        }
        data = self._try_stable_then_v3(
            "historical-price-eod/full",
            f"historical-price-full/{ticker}",
            params,
        )
        if isinstance(data, dict) and "historical" in data:
            return data["historical"]
        if isinstance(data, list):
            return data
        return None

    # ----- analyst data ---------------------------------------------------

    def get_price_target_consensus(self, ticker: str) -> Optional[dict]:
        """Consensus PT: targetHigh, targetLow, targetConsensus, targetMedian."""
        data = self._try_stable_then_v3(
            "price-target-consensus",
            f"price-target-consensus",
            {"symbol": ticker},
        )
        if isinstance(data, list) and data:
            return data[0]
        if isinstance(data, dict):
            return data
        return None

    def get_analyst_estimates(self, ticker: str, period: str = "annual", limit: int = 4):
        """Forward EPS / revenue estimates."""
        params = {"symbol": ticker, "period": period, "limit": limit}
        return self._try_stable_then_v3(
            "analyst-estimates", f"analyst-estimates/{ticker}", params
        )

    def get_stock_news(self, ticker: str, limit: int = 20) -> Optional[list[dict]]:
        """Ticker-filtered news."""
        params = {"symbols": ticker, "limit": limit}
        data = self._try_stable_then_v3("news/stock", "stock_news", params)
        if isinstance(data, list):
            return data
        return None

    def get_api_stats(self) -> dict:
        return {
            "api_calls_made": self.api_calls_made,
            "rate_limit_reached": self.rate_limit_reached,
        }
