#!/usr/bin/env python3
"""Consolidate four daily regime-skill outputs into regime_gate.json.

This is the writer side of Item 2's regime gate. Output is consumed by
services/regime_gate_service.go (regimeGateFile struct) and exposed to
agents via GET /api/v1/regime-gate/status.

Formula (per docs/superpowers/plans/2026-05-14-risk-and-diversification-plan.md
plus the post-Item-1 polarity correction):

    score = 0.35*breadth + 0.30*(100-macro) + 0.20*(100-top) + 0.15*(100-bubble)

- breadth is health (high = healthy participation)
- macro, top, bubble are risk (high = bad), inverted in the formula

Fail-soft: any input that is missing, unreadable, or malformed contributes a
neutral 50. The components block records `present: false` for forensics.
Operator gets the loud signal via stderr warning; downstream Go service
keeps trading on the most recent good value.

Usage (scheduler invocation):

    python scripts/compute_daily_regime_score.py \\
        --breadth data/reports/breadth_YYYYMMDD.json \\
        --macro data/reports/macro_regime_YYYY-MM-DD_HHMMSS.json \\
        --top data/reports/market_top_YYYY-MM-DD_HHMMSS.json \\
        --bubble data/reports/bubble_YYYYMMDD.json \\
        --output data/reports/regime_gate.json
"""
from __future__ import annotations

import argparse
import json
import sys
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any

NEUTRAL_VALUE = 50  # used when an input is missing / unparseable
STALE_AFTER_HOURS = 29  # must match services/regime_gate_service_test.go convention

# Per-source extraction paths into the upstream skills' JSON. Each entry is a
# tuple of nested keys; a single-element tuple means a top-level field.
# Paths reflect the skills' current outputs (verified 2026-05-14). If an
# upstream skill changes its schema, update the path here and the
# corresponding fixture in scripts/tests/test_compute_daily_regime_score.py.
EXTRACTION_PATHS = {
    "breadth": ("current_value_percent",),
    "macro": ("composite", "composite_score"),
    "top_risk": ("composite", "composite_score"),
    "bubble": ("percentage",),
}

# Formula weights. Must sum to 1.0.
WEIGHTS = {"breadth": 0.35, "macro": 0.30, "top_risk": 0.20, "bubble": 0.15}

FORMULA_STRING = (
    "0.35*breadth + 0.30*(100-macro) + 0.20*(100-top_risk) + 0.15*(100-bubble)"
)


def _extract(data: dict, path: tuple[str, ...]) -> Any:
    cur: Any = data
    for key in path:
        if not isinstance(cur, dict) or key not in cur:
            raise KeyError(f"missing path {'.'.join(path)}")
        cur = cur[key]
    return cur


def _read_score(component: str, file_path: Path | None) -> tuple[int, str | None, bool]:
    """Return (value, source_basename, present). Falls back to neutral 50 with
    `present=False` on any failure mode."""
    if file_path is None:
        return NEUTRAL_VALUE, None, False
    if not file_path.exists():
        print(
            f"warn: {component} input not found: {file_path} — using neutral {NEUTRAL_VALUE}",
            file=sys.stderr,
        )
        return NEUTRAL_VALUE, file_path.name, False
    try:
        raw = json.loads(file_path.read_text())
    except (json.JSONDecodeError, OSError) as exc:
        print(
            f"warn: {component} input unreadable ({exc}): {file_path} — using neutral {NEUTRAL_VALUE}",
            file=sys.stderr,
        )
        return NEUTRAL_VALUE, file_path.name, False
    try:
        value = _extract(raw, EXTRACTION_PATHS[component])
    except KeyError as exc:
        print(
            f"warn: {component} schema mismatch ({exc}): {file_path} — using neutral {NEUTRAL_VALUE}",
            file=sys.stderr,
        )
        return NEUTRAL_VALUE, file_path.name, False
    # Coerce to int and clamp to 0-100 — upstream may emit floats or out-of-range
    # values; we don't want a single bad reading to wreck the weighted score.
    try:
        coerced = int(round(float(value)))
    except (TypeError, ValueError):
        print(
            f"warn: {component} value not numeric ({value!r}): {file_path} — using neutral {NEUTRAL_VALUE}",
            file=sys.stderr,
        )
        return NEUTRAL_VALUE, file_path.name, False
    clamped = max(0, min(100, coerced))
    return clamped, file_path.name, True


def compute_score(
    breadth: int, macro: int, top_risk: int, bubble: int
) -> int:
    """Apply the weighted formula and clamp to [0, 100]. Pure function for
    easy testing in isolation."""
    raw = (
        WEIGHTS["breadth"] * breadth
        + WEIGHTS["macro"] * (100 - macro)
        + WEIGHTS["top_risk"] * (100 - top_risk)
        + WEIGHTS["bubble"] * (100 - bubble)
    )
    return max(0, min(100, int(round(raw))))


def build_payload(
    *,
    breadth_path: Path | None,
    macro_path: Path | None,
    top_path: Path | None,
    bubble_path: Path | None,
    now: datetime | None = None,
) -> dict:
    """Read inputs, compute score, return the dict that will be written to
    regime_gate.json. Split out from main() so tests can call it directly if
    we ever want to bypass the subprocess hop."""
    if now is None:
        now = datetime.now(timezone.utc)

    components = {}
    for name, path in (
        ("breadth", breadth_path),
        ("macro", macro_path),
        ("top_risk", top_path),
        ("bubble", bubble_path),
    ):
        value, source, present = _read_score(name, path)
        components[name] = {"value": value, "source": source, "present": present}

    score = compute_score(
        components["breadth"]["value"],
        components["macro"]["value"],
        components["top_risk"]["value"],
        components["bubble"]["value"],
    )
    as_of = now.replace(microsecond=0)
    stale_after = as_of + timedelta(hours=STALE_AFTER_HOURS)

    return {
        "score": score,
        "as_of": as_of.isoformat().replace("+00:00", "Z"),
        "stale_after": stale_after.isoformat().replace("+00:00", "Z"),
        "components": components,
        "formula": FORMULA_STRING,
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--breadth", type=Path, default=None,
                        help="path to breadth-chart-analyst JSON output")
    parser.add_argument("--macro", type=Path, default=None,
                        help="path to macro-regime-detector JSON output")
    parser.add_argument("--top", type=Path, default=None,
                        help="path to market-top-detector JSON output")
    parser.add_argument("--bubble", type=Path, default=None,
                        help="path to us-market-bubble-detector JSON output")
    parser.add_argument("--output", type=Path, required=True,
                        help="path to write regime_gate.json")
    args = parser.parse_args()

    payload = build_payload(
        breadth_path=args.breadth,
        macro_path=args.macro,
        top_path=args.top,
        bubble_path=args.bubble,
    )
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(payload, indent=2))
    return 0


if __name__ == "__main__":
    sys.exit(main())
