#!/usr/bin/env python3
"""
Fetch ticker-filtered catalyst news (M&A + earnings whispers) for Prophet's
liquid optionable universe and emit up to 3 ranked items for the daily brief.

Scope is deliberately narrow: this skill targets the news classes that
MarketWatch's general feed (already consumed elsewhere in the brief) tends
to miss for specific tickers — M&A activity and pre-earnings guidance moves.
General sentiment / sector pieces are filtered out.

Pipeline:
  1. Build universe (shared with analyst-actions)
  2. Pull last-N-hour ticker news from FMP /stable/news/stock
  3. Keyword-classify items into M&A | earnings | neither
  4. Dedup by (ticker, event_type) — keep newest per bucket
  5. Rank by (recency, keyword strength); cap output at top 3

Soft-fail: missing FMP_API_KEY -> empty list, exit 0.
"""

import argparse
import json
import os
import re
import sys
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Optional

SCRIPTS_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(SCRIPTS_DIR))

from fmp_client import FMPClient, _load_dotenv_from_ancestors  # noqa: E402
from universe_builder import DEFAULT_STATIC_PATH, build_universe  # noqa: E402


# Word-boundary matching to avoid e.g. "deal" matching "dealing".
# Each tuple is (compiled regex, weight). Higher weight = stronger signal.
MA_PATTERNS = [
    (re.compile(r"\bto acquire\b", re.IGNORECASE), 3.0),
    (re.compile(r"\bagrees to (?:buy|acquire|merge)\b", re.IGNORECASE), 3.0),
    (re.compile(r"\b(?:acquir(?:es|ed|ing)|acquisition)\b", re.IGNORECASE), 2.5),
    (re.compile(r"\b(?:merger|merging|merges)\b", re.IGNORECASE), 2.5),
    (re.compile(r"\b(?:buyout|takeover|take[- ]private)\b", re.IGNORECASE), 2.5),
    (re.compile(r"\b(?:bidding war|hostile bid|tender offer)\b", re.IGNORECASE), 2.5),
    (re.compile(r"\bdivest(?:s|ed|ing|iture)\b", re.IGNORECASE), 1.5),
]

EARNINGS_WHISPER_PATTERNS = [
    (re.compile(r"\bpreannounc(?:e|es|ed|ing)\b", re.IGNORECASE), 3.0),
    (re.compile(r"\bprofit warning\b", re.IGNORECASE), 3.0),
    (re.compile(r"\b(?:raises|lifts|hikes|boosts) (?:full[- ]year |fy |q[1-4] )?(?:guidance|forecast|outlook|estimate)\b", re.IGNORECASE), 2.5),
    (re.compile(r"\b(?:cuts|lowers|slashes|trims) (?:full[- ]year |fy |q[1-4] )?(?:guidance|forecast|outlook|estimate)\b", re.IGNORECASE), 2.5),
    (re.compile(r"\b(?:warns|warned) (?:on|about|of)\b", re.IGNORECASE), 2.0),
    (re.compile(r"\b(?:beats|tops|crushes) (?:estimates|expectations|consensus|street)\b", re.IGNORECASE), 1.5),
    (re.compile(r"\b(?:misses|trails) (?:estimates|expectations|consensus|street)\b", re.IGNORECASE), 1.5),
]


def _parse_dt(raw: Optional[str]) -> Optional[datetime]:
    if not raw or not isinstance(raw, str):
        return None
    for fmt in (
        "%Y-%m-%d %H:%M:%S",
        "%Y-%m-%dT%H:%M:%S.%fZ",
        "%Y-%m-%dT%H:%M:%SZ",
        "%Y-%m-%d",
    ):
        try:
            return datetime.strptime(raw, fmt).replace(tzinfo=timezone.utc)
        except ValueError:
            continue
    return None


def _classify(text: str) -> tuple[Optional[str], float]:
    """Return (event_type, weight) where event_type in {ma, earnings, None}.

    First matching pattern wins; M&A takes precedence over earnings to avoid
    a deal-related "raises guidance" being mis-bucketed.
    """
    if not text:
        return None, 0.0
    for pattern, weight in MA_PATTERNS:
        if pattern.search(text):
            return "ma", weight
    for pattern, weight in EARNINGS_WHISPER_PATTERNS:
        if pattern.search(text):
            return "earnings", weight
    return None, 0.0


def normalize_news_item(row: dict) -> Optional[dict]:
    title = row.get("title") or row.get("text") or ""
    snippet = row.get("text") or row.get("snippet") or ""
    haystack = f"{title} {snippet}"
    event_type, weight = _classify(haystack)
    if event_type is None:
        return None

    dt = _parse_dt(row.get("publishedDate") or row.get("date"))
    if dt is None:
        return None

    ticker = row.get("symbol") or row.get("ticker") or ""
    if not ticker:
        return None

    return {
        "ticker": ticker.upper(),
        "event_type": event_type,
        "headline": title.strip(),
        "source": row.get("site") or row.get("publisher") or "",
        "url": row.get("url") or "",
        "published": dt.isoformat(),
        "_dt": dt,
        "_weight": weight,
    }


def fetch_catalyst_news(
    client: FMPClient,
    tickers: list[str],
    lookback_hours: int = 24,
    limit: int = 3,
    raw_news_limit: int = 100,
) -> list[dict]:
    """Pull ticker news, classify, dedup by (ticker, event_type), rank, cap.

    FMP's news/stock endpoint accepts comma-separated symbols, so a single call
    covers the full universe. We over-pull (default 100) to be sure the M&A /
    earnings hits aren't truncated under the page limit.
    """
    cutoff = datetime.now(timezone.utc) - timedelta(hours=lookback_hours)

    try:
        rows = client.get_stock_news(tickers, limit=raw_news_limit) or []
    except Exception as e:
        print(f"WARNING: ticker news fetch failed: {e}", file=sys.stderr)
        return []

    candidates: list[dict] = []
    for row in rows:
        norm = normalize_news_item(row)
        if norm is None or norm["_dt"] < cutoff:
            continue
        candidates.append(norm)

    # Dedup by (ticker, event_type) — keep highest (weight, recency) per bucket.
    buckets: dict[tuple[str, str], dict] = {}
    for item in candidates:
        key = (item["ticker"], item["event_type"])
        cur = buckets.get(key)
        if cur is None:
            buckets[key] = item
            continue
        # Prefer higher weight; tie-break on newest.
        if (item["_weight"], item["_dt"]) > (cur["_weight"], cur["_dt"]):
            buckets[key] = item

    ranked = sorted(
        buckets.values(),
        key=lambda x: (x["_weight"], x["_dt"]),
        reverse=True,
    )
    out = ranked[:limit]
    for ev in out:
        ev.pop("_dt", None)
        ev.pop("_weight", None)
    return out


def main() -> int:
    parser = argparse.ArgumentParser(description="Fetch ticker catalyst news for Prophet universe.")
    parser.add_argument("--static-path", type=Path, default=DEFAULT_STATIC_PATH)
    parser.add_argument("--top-up", type=int, default=15)
    parser.add_argument("--lookback-hours", type=int, default=24)
    parser.add_argument("--limit", type=int, default=3)
    parser.add_argument("--raw-news-limit", type=int, default=100)
    args = parser.parse_args()

    if not (os.getenv("FMP_API_KEY") or _load_dotenv_from_ancestors("FMP_API_KEY")):
        print("WARNING: FMP_API_KEY not set; emitting empty list", file=sys.stderr)
        json.dump([], sys.stdout)
        sys.stdout.write("\n")
        return 0

    try:
        client = FMPClient()
    except Exception as e:
        print(f"ERROR: FMP client init failed: {e}", file=sys.stderr)
        json.dump([], sys.stdout)
        sys.stdout.write("\n")
        return 0

    universe = build_universe(
        static_path=args.static_path,
        top_up_count=args.top_up,
        client=client,
    )
    tickers = universe["tickers"]
    print(
        f"INFO: universe size={len(tickers)} (static={universe['static_count']}, "
        f"dynamic={universe['dynamic_count']})",
        file=sys.stderr,
    )

    events = fetch_catalyst_news(
        client,
        tickers,
        lookback_hours=args.lookback_hours,
        limit=args.limit,
        raw_news_limit=args.raw_news_limit,
    )
    json.dump(events, sys.stdout, indent=2)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
