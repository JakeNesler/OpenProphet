#!/usr/bin/env python3
"""
FMP API client for catalyst-news skill.

Mirrors the analyst-actions client so each skill stays self-contained
(matches the existing project pattern). Only the news/stock endpoint is
actively used here; the screener method backs the universe top-up.
"""

import os
import sys
import time
from pathlib import Path
from typing import Optional

try:
    import requests
except ImportError:
    print("ERROR: requests library not found. Install with: pip install requests", file=sys.stderr)
    sys.exit(1)


def _load_dotenv_from_ancestors(key: str) -> Optional[str]:
    """Walk up from this script's directory looking for a .env file and return key's value.

    Lets the skill work when the user has FMP_API_KEY in a project-root .env
    but hasn't exported it to the shell environment.
    """
    for d in (Path(__file__).resolve(), *Path(__file__).resolve().parents):
        env_path = d / ".env" if d.is_dir() else d.parent / ".env"
        if env_path.is_file():
            try:
                for line in env_path.read_text(encoding="utf-8").splitlines():
                    line = line.strip()
                    if not line or line.startswith("#") or "=" not in line:
                        continue
                    k, _, v = line.partition("=")
                    if k.strip() == key:
                        return v.strip().strip('"').strip("'")
            except OSError:
                pass
    return None


class FMPClient:
    RATE_LIMIT_DELAY = 0.3
    MAX_RETRIES = 1

    STABLE_BASE = "https://financialmodelingprep.com/stable"
    V3_BASE = "https://financialmodelingprep.com/api/v3"

    def __init__(self, api_key: Optional[str] = None):
        self.api_key = (
            api_key
            or os.getenv("FMP_API_KEY")
            or _load_dotenv_from_ancestors("FMP_API_KEY")
        )
        if not self.api_key:
            raise ValueError(
                "FMP API key required. Set FMP_API_KEY environment variable, "
                "add it to a project .env file, or pass api_key parameter."
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

    def screen_liquid_universe(
        self,
        market_cap_min: int = 5_000_000_000,
        price_min: float = 20.0,
        volume_min: int = 5_000_000,
        limit: int = 50,
    ) -> Optional[list[dict]]:
        params = {
            "marketCapMoreThan": market_cap_min,
            "priceMoreThan": price_min,
            "volumeMoreThan": volume_min,
            "isActivelyTrading": "true",
            "country": "US",
            "limit": limit,
        }
        data = self._try_stable_then_v3("company-screener", "stock-screener", params)
        if isinstance(data, list):
            return data
        return None

    def get_stock_news(self, symbols: list[str], limit: int = 50) -> Optional[list[dict]]:
        """Ticker-filtered news. FMP accepts comma-separated symbols."""
        params = {"symbols": ",".join(symbols), "limit": limit}
        data = self._try_stable_then_v3("news/stock", "stock_news", params)
        if isinstance(data, list):
            return data
        return None

    def get_api_stats(self) -> dict:
        return {
            "api_calls_made": self.api_calls_made,
            "rate_limit_reached": self.rate_limit_reached,
        }
