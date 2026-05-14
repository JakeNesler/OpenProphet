#!/usr/bin/env python3
"""
Universe builder for catalyst-news.

Reads the shared static curated file from analyst-actions (single source of
truth) and adds an FMP top-volume top-up filtered by price >$20 and market
cap >$5B. Mirrors the analyst-actions builder so each skill stays
self-contained per the project pattern; the static file path is the only
cross-skill coupling.
"""

import argparse
import json
import sys
from pathlib import Path
from typing import Optional


# Shared static file lives in analyst-actions (single source of truth).
DEFAULT_STATIC_PATH = (
    Path(__file__).resolve().parent.parent.parent / "analyst-actions" / "universe.txt"
)


def load_static_universe(path: Path) -> list[str]:
    if not path.exists():
        return []
    tickers: list[str] = []
    for raw in path.read_text(encoding="utf-8").splitlines():
        line = raw.split("#", 1)[0].strip()
        if line:
            tickers.append(line.upper())
    return tickers


def fetch_dynamic_topup(
    client,
    top_up_count: int = 15,
    market_cap_min: int = 5_000_000_000,
    price_min: float = 20.0,
    volume_min: int = 5_000_000,
) -> list[str]:
    rows = client.screen_liquid_universe(
        market_cap_min=market_cap_min,
        price_min=price_min,
        volume_min=volume_min,
        limit=top_up_count * 3,
    )
    if not rows:
        return []

    def vol(row: dict) -> float:
        for key in ("volume", "avgVolume", "averageVolume"):
            v = row.get(key)
            if isinstance(v, (int, float)) and v > 0:
                return float(v)
        return 0.0

    sorted_rows = sorted(rows, key=vol, reverse=True)
    out: list[str] = []
    for row in sorted_rows:
        sym = row.get("symbol")
        if isinstance(sym, str) and sym:
            out.append(sym.upper())
        if len(out) >= top_up_count:
            break
    return out


def build_universe(
    static_path: Path = DEFAULT_STATIC_PATH,
    top_up_count: int = 15,
    client: Optional[object] = None,
) -> dict:
    static = load_static_universe(static_path)
    seen = {t for t in static}
    dynamic: list[str] = []

    if top_up_count > 0 and client is not None:
        try:
            for sym in fetch_dynamic_topup(client, top_up_count=top_up_count):
                if sym not in seen:
                    dynamic.append(sym)
                    seen.add(sym)
        except Exception as e:
            print(f"WARNING: dynamic top-up failed: {e}", file=sys.stderr)

    return {
        "static_count": len(static),
        "dynamic_count": len(dynamic),
        "tickers": static + dynamic,
    }


def main() -> int:
    parser = argparse.ArgumentParser(description="Build Prophet catalyst universe (catalyst-news).")
    parser.add_argument("--static-path", type=Path, default=DEFAULT_STATIC_PATH)
    parser.add_argument("--top-up", type=int, default=15)
    parser.add_argument("--no-fmp", action="store_true")
    args = parser.parse_args()

    client = None
    if not args.no_fmp and args.top_up > 0:
        try:
            from fmp_client import FMPClient

            client = FMPClient()
        except Exception as e:
            print(f"WARNING: FMP client unavailable: {e}", file=sys.stderr)

    result = build_universe(
        static_path=args.static_path,
        top_up_count=args.top_up,
        client=client,
    )
    json.dump(result, sys.stdout, indent=2)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
