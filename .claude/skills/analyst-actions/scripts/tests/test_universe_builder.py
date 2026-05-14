"""Tests for universe_builder.

Cover the static loader edge cases (comments, blanks, casing) and the
merge logic with mocked FMP screener responses including the soft-fail path.
"""

import sys
from pathlib import Path

import pytest

SCRIPTS_DIR = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(SCRIPTS_DIR))

from universe_builder import (  # noqa: E402
    build_universe,
    fetch_dynamic_topup,
    load_static_universe,
)


class FakeFMP:
    def __init__(self, screener_result):
        self._screener_result = screener_result

    def screen_liquid_universe(self, **_kwargs):
        return self._screener_result


def write_universe(tmp_path: Path, content: str) -> Path:
    p = tmp_path / "universe.txt"
    p.write_text(content, encoding="utf-8")
    return p


def test_load_static_strips_comments_and_blanks(tmp_path: Path):
    p = write_universe(
        tmp_path,
        "\n".join(
            [
                "# header comment",
                "SPY",
                "",
                "  QQQ  ",
                "AAPL # inline comment",
                "# another",
                "msft",
            ]
        ),
    )
    assert load_static_universe(p) == ["SPY", "QQQ", "AAPL", "MSFT"]


def test_load_static_missing_file_returns_empty(tmp_path: Path):
    assert load_static_universe(tmp_path / "does_not_exist.txt") == []


def test_dynamic_topup_sorted_by_volume():
    fmp = FakeFMP(
        screener_result=[
            {"symbol": "AAA", "volume": 1_000_000},
            {"symbol": "BBB", "volume": 50_000_000},
            {"symbol": "CCC", "volume": 10_000_000},
        ]
    )
    out = fetch_dynamic_topup(fmp, top_up_count=2)
    assert out == ["BBB", "CCC"]


def test_dynamic_topup_handles_missing_volume_key():
    fmp = FakeFMP(
        screener_result=[
            {"symbol": "AAA"},  # no volume — should sort last
            {"symbol": "BBB", "averageVolume": 8_000_000},
            {"symbol": "CCC", "volume": 12_000_000},
        ]
    )
    out = fetch_dynamic_topup(fmp, top_up_count=3)
    assert out[0] == "CCC"  # highest volume
    assert "BBB" in out
    assert "AAA" in out


def test_dynamic_topup_empty_response():
    assert fetch_dynamic_topup(FakeFMP(screener_result=None), top_up_count=10) == []
    assert fetch_dynamic_topup(FakeFMP(screener_result=[]), top_up_count=10) == []


def test_build_universe_merges_static_first_then_dynamic(tmp_path: Path):
    p = write_universe(tmp_path, "SPY\nNVDA\nAAPL\n")
    fmp = FakeFMP(
        screener_result=[
            {"symbol": "TSLA", "volume": 80_000_000},
            {"symbol": "NVDA", "volume": 70_000_000},  # duplicate — must be deduped
            {"symbol": "AMD", "volume": 60_000_000},
        ]
    )
    result = build_universe(static_path=p, top_up_count=5, client=fmp)
    assert result["tickers"][:3] == ["SPY", "NVDA", "AAPL"]
    # NVDA appears in static; should not be re-added from dynamic.
    assert result["tickers"].count("NVDA") == 1
    assert "TSLA" in result["tickers"]
    assert "AMD" in result["tickers"]
    assert result["static_count"] == 3
    assert result["dynamic_count"] == 2


def test_build_universe_soft_fails_when_fmp_unavailable(tmp_path: Path):
    p = write_universe(tmp_path, "SPY\nQQQ\n")
    # No client => no dynamic top-up; static-only fallback.
    result = build_universe(static_path=p, top_up_count=5, client=None)
    assert result["tickers"] == ["SPY", "QQQ"]
    assert result["dynamic_count"] == 0


def test_build_universe_soft_fails_when_fmp_raises(tmp_path: Path):
    p = write_universe(tmp_path, "SPY\n")

    class BoomFMP:
        def screen_liquid_universe(self, **_kwargs):
            raise RuntimeError("api down")

    result = build_universe(static_path=p, top_up_count=5, client=BoomFMP())
    assert result["tickers"] == ["SPY"]
    assert result["dynamic_count"] == 0


def test_build_universe_disables_topup_when_count_zero(tmp_path: Path):
    p = write_universe(tmp_path, "SPY\nQQQ\n")
    fmp = FakeFMP(screener_result=[{"symbol": "TSLA", "volume": 99}])
    result = build_universe(static_path=p, top_up_count=0, client=fmp)
    assert result["tickers"] == ["SPY", "QQQ"]


if __name__ == "__main__":
    sys.exit(pytest.main([__file__, "-v"]))
