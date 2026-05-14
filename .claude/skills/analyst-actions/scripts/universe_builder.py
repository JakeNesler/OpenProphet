#!/usr/bin/env python3
"""
Universe builder: static curated floor + FMP top-volume dynamic top-up.

The static list is the stable core of liquid optionable names Prophet plausibly
trades. The dynamic top-up surfaces names entering the high-volume liquid set
that day, filtered by price (>$20) and market cap (>$5B) so penny pumps and
illiquid names don't pollute the universe.

Soft-fail: if FMP is unavailable, returns the static list alone.
"""

import argparse
import json
import sys
from pathlib import Path
from typing import Optional


DEFAULT_STATIC_PATH = (
    Path(__file__).resolve().parent.parent / "universe.txt"
)


def load_static_universe(path: Path) -> list[str]:
    """Read the curated universe file. Strips comments and blank lines."""
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
    """Query FMP screener for liquid actively-traded names, sorted by volume."""
    rows = client.screen_liquid_universe(
        market_cap_min=market_cap_min,
        price_min=price_min,
        volume_min=volume_min,
        limit=top_up_count * 3,  # over-pull, then sort + cap below
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
    """Merge static + dynamic universes, preserving order and dedup'ing.

    Static names come first to preserve continuity in the brief; dynamic names
    fill remaining slots without displacing the floor.
    """
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
    parser = argparse.ArgumentParser(description="Build Prophet catalyst universe.")
    parser.add_argument(
        "--static-path",
        type=Path,
        default=DEFAULT_STATIC_PATH,
        help=f"Path to curated universe file (default: {DEFAULT_STATIC_PATH})",
    )
    parser.add_argument(
        "--top-up",
        type=int,
        default=15,
        help="Number of dynamic top-up names to add (0 to disable; default 15)",
    )
    parser.add_argument(
        "--no-fmp",
        action="store_true",
        help="Skip the FMP top-up call entirely; return static list only",
    )
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
