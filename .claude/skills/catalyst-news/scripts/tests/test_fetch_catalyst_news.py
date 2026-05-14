"""Tests for fetch_catalyst_news.

Covers keyword classification (M&A vs earnings vs noise), the 24h lookback
filter, dedup by (ticker, event_type), ranking, the cap, and soft-fail.
"""

import sys
from datetime import datetime, timedelta, timezone
from pathlib import Path

import pytest

SCRIPTS_DIR = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(SCRIPTS_DIR))

from fetch_catalyst_news import (  # noqa: E402
    _classify,
    fetch_catalyst_news,
    normalize_news_item,
)


def _iso(dt: datetime) -> str:
    return dt.strftime("%Y-%m-%d %H:%M:%S")


class FakeFMP:
    def __init__(self, news_response=None, raise_on_news=False):
        self._news = news_response
        self._raise = raise_on_news

    def get_stock_news(self, symbols, limit=100):
        if self._raise:
            raise RuntimeError("simulated FMP news failure")
        return self._news


# ── classification ─────────────────────────────────────────────────────────


def test_classify_ma_acquisition():
    et, w = _classify("Acme Inc agrees to acquire Widgets Co for $3B")
    assert et == "ma"
    assert w >= 2.5


def test_classify_ma_takeover():
    et, _ = _classify("Hostile bid emerges for Foo Corp")
    assert et == "ma"


def test_classify_earnings_preannounce():
    et, w = _classify("XYZ preannounces Q3 results above consensus")
    assert et == "earnings"
    assert w >= 2.5


def test_classify_earnings_guidance_cut():
    et, _ = _classify("Company slashes full-year guidance citing weak demand")
    assert et == "earnings"


def test_classify_earnings_beat():
    et, _ = _classify("ABC beats estimates as revenue tops Street consensus")
    assert et == "earnings"


def test_classify_noise_returns_none():
    et, w = _classify("Sector rotation continues as tech leads broader market")
    assert et is None
    assert w == 0.0


def test_classify_dealing_does_not_match_deal():
    # Make sure the word-boundary regex doesn't false-fire on "dealing".
    et, _ = _classify("Trader is dealing with volatile conditions")
    assert et is None


def test_classify_ma_takes_precedence_over_earnings():
    # A deal headline that also mentions guidance should bucket as M&A.
    et, _ = _classify("Acquires rival; raises guidance on combined entity")
    assert et == "ma"


# ── normalization ──────────────────────────────────────────────────────────


def test_normalize_ma_item():
    now = datetime.now(timezone.utc) - timedelta(hours=2)
    row = {
        "symbol": "NVDA",
        "title": "NVIDIA agrees to acquire Arm for $40B",
        "text": "deal details below...",
        "publishedDate": _iso(now),
        "site": "Reuters",
        "url": "https://example.com/1",
    }
    norm = normalize_news_item(row)
    assert norm is not None
    assert norm["ticker"] == "NVDA"
    assert norm["event_type"] == "ma"
    assert norm["source"] == "Reuters"


def test_normalize_skips_non_catalyst():
    row = {
        "symbol": "NVDA",
        "title": "Bank says tech valuations are stretched",
        "publishedDate": "2026-05-14 10:00:00",
    }
    assert normalize_news_item(row) is None


def test_normalize_skips_missing_ticker():
    row = {"title": "Acme acquires Widgets", "publishedDate": "2026-05-14 10:00:00"}
    assert normalize_news_item(row) is None


def test_normalize_skips_unparseable_date():
    row = {"symbol": "X", "title": "Acquires rival", "publishedDate": "not a date"}
    assert normalize_news_item(row) is None


# ── pipeline ───────────────────────────────────────────────────────────────


def test_fetch_filters_outside_lookback():
    inside = datetime.now(timezone.utc) - timedelta(hours=3)
    outside = datetime.now(timezone.utc) - timedelta(hours=48)
    fmp = FakeFMP(
        news_response=[
            {
                "symbol": "NVDA",
                "title": "NVIDIA agrees to acquire Arm",
                "publishedDate": _iso(inside),
            },
            {
                "symbol": "AMD",
                "title": "AMD agrees to acquire Xilinx",
                "publishedDate": _iso(outside),
            },
        ]
    )
    out = fetch_catalyst_news(fmp, ["NVDA", "AMD"], lookback_hours=24, limit=3)
    assert len(out) == 1
    assert out[0]["ticker"] == "NVDA"


def test_fetch_dedups_by_ticker_and_event_type():
    now = datetime.now(timezone.utc) - timedelta(hours=1)
    later = datetime.now(timezone.utc) - timedelta(minutes=10)
    fmp = FakeFMP(
        news_response=[
            {
                "symbol": "NVDA",
                "title": "NVIDIA agrees to acquire Arm",
                "publishedDate": _iso(now),
            },
            {
                "symbol": "NVDA",
                "title": "NVIDIA acquisition deal expanded — to acquire more assets",
                "publishedDate": _iso(later),
            },
        ]
    )
    out = fetch_catalyst_news(fmp, ["NVDA"], lookback_hours=24, limit=3)
    # Same (ticker, event_type) bucket -> only one entry should survive.
    assert len(out) == 1
    # The "to acquire" pattern has the higher weight, so that headline wins.
    assert "to acquire" in out[0]["headline"]


def test_fetch_keeps_distinct_event_types_for_same_ticker():
    now = datetime.now(timezone.utc) - timedelta(hours=1)
    fmp = FakeFMP(
        news_response=[
            {
                "symbol": "NVDA",
                "title": "NVIDIA agrees to acquire Arm",
                "publishedDate": _iso(now),
            },
            {
                "symbol": "NVDA",
                "title": "NVIDIA preannounces Q3 results",
                "publishedDate": _iso(now),
            },
        ]
    )
    out = fetch_catalyst_news(fmp, ["NVDA"], lookback_hours=24, limit=5)
    types = {ev["event_type"] for ev in out}
    assert types == {"ma", "earnings"}


def test_fetch_caps_at_limit():
    now = datetime.now(timezone.utc) - timedelta(hours=1)
    rows = []
    for i in range(10):
        rows.append(
            {
                "symbol": f"T{i}",
                "title": f"T{i} agrees to acquire something",
                "publishedDate": _iso(now),
            }
        )
    fmp = FakeFMP(news_response=rows)
    out = fetch_catalyst_news(fmp, [f"T{i}" for i in range(10)], limit=3)
    assert len(out) == 3


def test_fetch_soft_fails_on_news_error():
    fmp = FakeFMP(raise_on_news=True)
    out = fetch_catalyst_news(fmp, ["NVDA"], lookback_hours=24, limit=3)
    assert out == []


def test_fetch_soft_fails_on_empty_response():
    assert fetch_catalyst_news(FakeFMP(news_response=None), ["NVDA"]) == []
    assert fetch_catalyst_news(FakeFMP(news_response=[]), ["NVDA"]) == []


def test_fetch_strips_internal_fields():
    now = datetime.now(timezone.utc) - timedelta(hours=1)
    fmp = FakeFMP(
        news_response=[
            {
                "symbol": "NVDA",
                "title": "NVIDIA agrees to acquire Arm",
                "publishedDate": _iso(now),
            }
        ]
    )
    out = fetch_catalyst_news(fmp, ["NVDA"], limit=3)
    assert out
    assert "_dt" not in out[0]
    assert "_weight" not in out[0]


if __name__ == "__main__":
    sys.exit(pytest.main([__file__, "-v"]))
