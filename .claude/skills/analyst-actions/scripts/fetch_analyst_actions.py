#!/usr/bin/env python3
"""
Fetch recent analyst rating changes and price-target updates for Prophet's
catalyst universe and emit a ranked JSON summary for the daily brief.

Pipeline:
  1. Build universe (static curated + FMP top-volume top-up)
  2. For each ticker, pull grades-historical + price-target-news from FMP
  3. Filter to last N hours (default 24)
  4. Score each event (firm tier x action magnitude); rank desc
  5. Cap output at top N events (default 15)

Output: JSON list of events on stdout. Errors logged to stderr.

Soft-fail: missing FMP_API_KEY exits 0 with an empty list so the daily-brief
pipeline keeps running. Per-ticker FMP failures are swallowed; partial output
is still returned.
"""

import argparse
import json
import os
import sys
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Optional

SCRIPTS_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(SCRIPTS_DIR))

from fmp_client import FMPClient  # noqa: E402
from universe_builder import DEFAULT_STATIC_PATH, build_universe  # noqa: E402


# Firm tiers — tier-1 banks move mega caps more than boutique shops.
# Keys are lowercase substrings; first match wins.
TIER_1 = {
    "goldman", "morgan stanley", "jpmorgan", "j.p. morgan", "jp morgan",
    "bank of america", "bofa", "wells fargo", "citi", "citigroup",
}
TIER_2 = {
    "barclays", "deutsche", "ubs", "jefferies", "wedbush", "bernstein",
    "evercore", "rbc", "piper sandler", "raymond james", "oppenheimer",
    "stifel", "td cowen", "guggenheim", "mizuho",
}


def _firm_tier(firm: str) -> int:
    """Return 1, 2, or 3 (other) for a firm name."""
    if not firm:
        return 3
    lower = firm.lower()
    for needle in TIER_1:
        if needle in lower:
            return 1
    for needle in TIER_2:
        if needle in lower:
            return 2
    return 3


def _parse_dt(raw: Optional[str]) -> Optional[datetime]:
    """Parse FMP-style dates: 'YYYY-MM-DD' or 'YYYY-MM-DD HH:MM:SS'."""
    if not raw or not isinstance(raw, str):
        return None
    for fmt in ("%Y-%m-%d %H:%M:%S", "%Y-%m-%dT%H:%M:%S.%fZ", "%Y-%m-%dT%H:%M:%SZ", "%Y-%m-%d"):
        try:
            dt = datetime.strptime(raw, fmt)
            return dt.replace(tzinfo=timezone.utc)
        except ValueError:
            continue
    return None


def _safe_float(val) -> Optional[float]:
    try:
        if val is None or val == "":
            return None
        return float(val)
    except (TypeError, ValueError):
        return None


def _classify_grade_action(prev: str, new: str) -> str:
    """Heuristic: classify rating change as upgrade/downgrade/reiterate/initiate."""
    pos = {"buy", "strong buy", "outperform", "overweight", "accumulate"}
    neu = {"hold", "neutral", "market perform", "equal-weight", "equalweight", "equal weight"}
    neg = {"sell", "strong sell", "underperform", "underweight", "reduce"}

    def rank(grade: str) -> int:
        g = (grade or "").strip().lower()
        if g in pos:
            return 2
        if g in neu:
            return 1
        if g in neg:
            return 0
        return -1

    pr, nr = rank(prev), rank(new)
    if pr == -1:
        return "initiated"
    if pr == nr:
        return "reiterated"
    return "upgrade" if nr > pr else "downgrade"


def normalize_grade_event(row: dict, ticker: str) -> Optional[dict]:
    dt = _parse_dt(row.get("date") or row.get("publishedDate"))
    if dt is None:
        return None
    firm = row.get("gradingCompany") or row.get("analystCompany") or ""
    prev = row.get("previousGrade") or ""
    new = row.get("newGrade") or row.get("grade") or ""
    return {
        "ticker": ticker,
        "type": "rating_change",
        "firm": firm,
        "action": _classify_grade_action(prev, new),
        "from": prev or None,
        "to": new or None,
        "date": dt.isoformat(),
        "_dt": dt,
        "_tier": _firm_tier(firm),
    }


def normalize_pt_event(row: dict, ticker: str) -> Optional[dict]:
    dt = _parse_dt(row.get("publishedDate") or row.get("date"))
    if dt is None:
        return None
    firm = row.get("analystCompany") or row.get("newsPublisher") or row.get("publisher") or ""
    pt_new = _safe_float(row.get("priceTarget") or row.get("adjPriceTarget"))
    pt_prev = _safe_float(row.get("priceWhenPosted") or row.get("previousPriceTarget"))
    if pt_new is None:
        return None
    direction: str
    if pt_prev is None:
        direction = "set"
    elif pt_new > pt_prev:
        direction = "raised"
    elif pt_new < pt_prev:
        direction = "lowered"
    else:
        direction = "reiterated"
    return {
        "ticker": ticker,
        "type": "pt_change",
        "firm": firm,
        "action": direction,
        "from": pt_prev,
        "to": pt_new,
        "date": dt.isoformat(),
        "_dt": dt,
        "_tier": _firm_tier(firm),
    }


def _score_event(event: dict) -> float:
    """Higher = more important. Tier-1 firms and bigger PT moves rank higher."""
    tier_w = {1: 3.0, 2: 1.5, 3: 1.0}[event["_tier"]]
    action_w = 1.0
    if event["type"] == "pt_change":
        from_v = _safe_float(event.get("from"))
        to_v = _safe_float(event.get("to"))
        if from_v and to_v and from_v > 0:
            pct = abs(to_v - from_v) / from_v
            action_w = min(3.0, 1.0 + 5.0 * pct)  # 20% PT move -> weight 2.0
        elif event["action"] == "set":
            action_w = 0.8
    else:  # rating_change
        action_w = {"upgrade": 2.0, "downgrade": 2.0, "initiated": 1.2, "reiterated": 0.6}.get(
            event["action"], 1.0
        )
    return tier_w * action_w


def fetch_analyst_actions(
    client: FMPClient,
    tickers: list[str],
    lookback_hours: int = 24,
    limit: int = 15,
) -> list[dict]:
    """Pull grades + PT news for each ticker, filter, rank, cap."""
    cutoff = datetime.now(timezone.utc) - timedelta(hours=lookback_hours)
    events: list[dict] = []
    for ticker in tickers:
        try:
            grades = client.get_grades_historical(ticker, limit=20) or []
            for row in grades:
                norm = normalize_grade_event(row, ticker)
                if norm and norm["_dt"] >= cutoff:
                    events.append(norm)
        except Exception as e:
            print(f"WARNING: grades fetch failed for {ticker}: {e}", file=sys.stderr)

        try:
            pts = client.get_price_target_news(ticker, limit=20) or []
            for row in pts:
                norm = normalize_pt_event(row, ticker)
                if norm and norm["_dt"] >= cutoff:
                    events.append(norm)
        except Exception as e:
            print(f"WARNING: price-target fetch failed for {ticker}: {e}", file=sys.stderr)

    events.sort(key=_score_event, reverse=True)
    out = events[:limit]
    # Strip internal scoring fields before emitting.
    for ev in out:
        ev.pop("_dt", None)
        ev.pop("_tier", None)
    return out


def main() -> int:
    parser = argparse.ArgumentParser(description="Fetch ranked analyst actions for Prophet universe.")
    parser.add_argument("--static-path", type=Path, default=DEFAULT_STATIC_PATH)
    parser.add_argument("--top-up", type=int, default=15)
    parser.add_argument("--lookback-hours", type=int, default=24)
    parser.add_argument("--limit", type=int, default=15)
    args = parser.parse_args()

    if not os.getenv("FMP_API_KEY"):
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

    events = fetch_analyst_actions(
        client,
        tickers,
        lookback_hours=args.lookback_hours,
        limit=args.limit,
    )
    json.dump(events, sys.stdout, indent=2)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
