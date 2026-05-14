"""Tests for fetch_analyst_actions.

Covers normalization of FMP grade + PT-news payloads, the 24-hour lookback
filter, tier-aware ranking, soft-fail on per-ticker errors, and the cap.
"""

import sys
from datetime import datetime, timedelta, timezone
from pathlib import Path

import pytest

SCRIPTS_DIR = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(SCRIPTS_DIR))

from fetch_analyst_actions import (  # noqa: E402
    _classify_grade_action,
    _firm_tier,
    _score_event,
    fetch_analyst_actions,
    normalize_grade_event,
    normalize_pt_event,
)


# ── helpers ────────────────────────────────────────────────────────────────


def _iso(dt: datetime) -> str:
    return dt.strftime("%Y-%m-%d %H:%M:%S")


class FakeFMP:
    """Mock client returning canned grades + PT news per ticker."""

    def __init__(self, grades_by_ticker=None, pt_by_ticker=None, raise_on=None):
        self.grades_by_ticker = grades_by_ticker or {}
        self.pt_by_ticker = pt_by_ticker or {}
        self.raise_on = raise_on or set()

    def get_grades_historical(self, symbol: str, limit: int = 20):
        if ("grades", symbol) in self.raise_on:
            raise RuntimeError("simulated grades failure")
        return self.grades_by_ticker.get(symbol, [])

    def get_price_target_news(self, symbol: str, limit: int = 20):
        if ("pt", symbol) in self.raise_on:
            raise RuntimeError("simulated PT failure")
        return self.pt_by_ticker.get(symbol, [])


# ── firm tier ──────────────────────────────────────────────────────────────


def test_firm_tier_tier1_recognized():
    assert _firm_tier("Goldman Sachs") == 1
    assert _firm_tier("J.P. Morgan") == 1
    assert _firm_tier("Bank of America") == 1


def test_firm_tier_tier2_recognized():
    assert _firm_tier("Barclays Capital") == 2
    assert _firm_tier("Wedbush Securities") == 2


def test_firm_tier_unknown_falls_to_three():
    assert _firm_tier("Some Tiny Boutique") == 3
    assert _firm_tier("") == 3
    assert _firm_tier(None) == 3


# ── grade classification ──────────────────────────────────────────────────


def test_classify_grade_action_upgrade():
    assert _classify_grade_action("Hold", "Buy") == "upgrade"


def test_classify_grade_action_downgrade():
    assert _classify_grade_action("Buy", "Hold") == "downgrade"


def test_classify_grade_action_reiterate():
    assert _classify_grade_action("Buy", "Buy") == "reiterated"
    assert _classify_grade_action("Hold", "Neutral") == "reiterated"


def test_classify_grade_action_initiated_when_no_prev():
    assert _classify_grade_action("", "Buy") == "initiated"


# ── PT normalization ──────────────────────────────────────────────────────


def test_normalize_pt_raised():
    row = {
        "publishedDate": "2026-05-14 12:30:00",
        "analystCompany": "Wells Fargo",
        "priceTarget": 210,
        "priceWhenPosted": 175,
    }
    ev = normalize_pt_event(row, "NVDA")
    assert ev["action"] == "raised"
    assert ev["from"] == 175.0
    assert ev["to"] == 210.0
    assert ev["_tier"] == 1


def test_normalize_pt_lowered():
    row = {
        "publishedDate": "2026-05-14 12:30:00",
        "analystCompany": "Some Boutique",
        "priceTarget": 80,
        "priceWhenPosted": 100,
    }
    ev = normalize_pt_event(row, "AMD")
    assert ev["action"] == "lowered"
    assert ev["_tier"] == 3


def test_normalize_pt_missing_target_skipped():
    row = {"publishedDate": "2026-05-14 12:30:00", "analystCompany": "X"}
    assert normalize_pt_event(row, "NVDA") is None


def test_normalize_pt_missing_previous_marked_set():
    row = {"publishedDate": "2026-05-14", "analystCompany": "Citi", "priceTarget": 150}
    ev = normalize_pt_event(row, "NVDA")
    assert ev["action"] == "set"
    assert ev["from"] is None
    assert ev["to"] == 150.0


# ── grade normalization ───────────────────────────────────────────────────


def test_normalize_grade_event():
    row = {
        "date": "2026-05-14",
        "gradingCompany": "Morgan Stanley",
        "previousGrade": "Hold",
        "newGrade": "Buy",
    }
    ev = normalize_grade_event(row, "TSLA")
    assert ev["action"] == "upgrade"
    assert ev["from"] == "Hold"
    assert ev["to"] == "Buy"
    assert ev["_tier"] == 1


def test_normalize_grade_event_missing_date_returns_none():
    row = {"gradingCompany": "X", "previousGrade": "Hold", "newGrade": "Buy"}
    assert normalize_grade_event(row, "TSLA") is None


# ── scoring ───────────────────────────────────────────────────────────────


def test_score_pt_change_with_large_delta_outranks_small():
    big = {
        "type": "pt_change",
        "_tier": 1,
        "action": "raised",
        "from": 100.0,
        "to": 130.0,  # 30% move
    }
    small = {
        "type": "pt_change",
        "_tier": 1,
        "action": "raised",
        "from": 100.0,
        "to": 102.0,  # 2% move
    }
    assert _score_event(big) > _score_event(small)


def test_score_tier1_outranks_tier3_at_same_action():
    t1 = {"type": "rating_change", "_tier": 1, "action": "upgrade"}
    t3 = {"type": "rating_change", "_tier": 3, "action": "upgrade"}
    assert _score_event(t1) > _score_event(t3)


# ── pipeline ──────────────────────────────────────────────────────────────


def test_fetch_filters_outside_lookback():
    inside = datetime.now(timezone.utc) - timedelta(hours=4)
    outside = datetime.now(timezone.utc) - timedelta(hours=48)
    fmp = FakeFMP(
        pt_by_ticker={
            "NVDA": [
                {
                    "publishedDate": _iso(inside),
                    "analystCompany": "Goldman Sachs",
                    "priceTarget": 200,
                    "priceWhenPosted": 180,
                },
                {
                    "publishedDate": _iso(outside),
                    "analystCompany": "Goldman Sachs",
                    "priceTarget": 999,
                    "priceWhenPosted": 100,
                },
            ]
        }
    )
    events = fetch_analyst_actions(fmp, ["NVDA"], lookback_hours=24)
    assert len(events) == 1
    assert events[0]["to"] == 200.0


def test_fetch_ranks_by_score():
    now = datetime.now(timezone.utc) - timedelta(hours=1)
    fmp = FakeFMP(
        pt_by_ticker={
            "AAA": [
                {
                    "publishedDate": _iso(now),
                    "analystCompany": "Tiny Boutique",
                    "priceTarget": 102,
                    "priceWhenPosted": 100,
                }
            ],
            "BBB": [
                {
                    "publishedDate": _iso(now),
                    "analystCompany": "Goldman Sachs",
                    "priceTarget": 130,
                    "priceWhenPosted": 100,
                }
            ],
        }
    )
    events = fetch_analyst_actions(fmp, ["AAA", "BBB"], lookback_hours=24)
    # Goldman + 30% PT move should outrank tiny boutique + 2% move.
    assert events[0]["ticker"] == "BBB"
    assert events[1]["ticker"] == "AAA"


def test_fetch_caps_at_limit():
    now = datetime.now(timezone.utc) - timedelta(hours=1)
    fmp = FakeFMP(
        pt_by_ticker={
            f"T{i}": [
                {
                    "publishedDate": _iso(now),
                    "analystCompany": "Goldman Sachs",
                    "priceTarget": 100 + i,
                    "priceWhenPosted": 90,
                }
            ]
            for i in range(30)
        }
    )
    events = fetch_analyst_actions(fmp, [f"T{i}" for i in range(30)], lookback_hours=24, limit=5)
    assert len(events) == 5


def test_fetch_soft_fails_on_per_ticker_error():
    now = datetime.now(timezone.utc) - timedelta(hours=1)
    fmp = FakeFMP(
        pt_by_ticker={
            "GOOD": [
                {
                    "publishedDate": _iso(now),
                    "analystCompany": "Goldman Sachs",
                    "priceTarget": 200,
                    "priceWhenPosted": 180,
                }
            ]
        },
        raise_on={("grades", "BAD"), ("pt", "BAD")},
    )
    events = fetch_analyst_actions(fmp, ["GOOD", "BAD"], lookback_hours=24)
    assert len(events) == 1
    assert events[0]["ticker"] == "GOOD"


def test_fetch_strips_internal_fields():
    now = datetime.now(timezone.utc) - timedelta(hours=1)
    fmp = FakeFMP(
        pt_by_ticker={
            "X": [
                {
                    "publishedDate": _iso(now),
                    "analystCompany": "Citi",
                    "priceTarget": 200,
                    "priceWhenPosted": 180,
                }
            ]
        }
    )
    events = fetch_analyst_actions(fmp, ["X"], lookback_hours=24)
    assert events
    assert "_dt" not in events[0]
    assert "_tier" not in events[0]


if __name__ == "__main__":
    sys.exit(pytest.main([__file__, "-v"]))
