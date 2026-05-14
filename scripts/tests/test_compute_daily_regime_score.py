"""Unit tests for compute_daily_regime_score.

The script consolidates four upstream regime-skill outputs into a single
regime_gate.json snapshot consumed by services/regime_gate_service.go.
Tests use fixture JSON files so the script stays decoupled from upstream
skill internals — if a skill changes its output schema, this test file is
the place the breakage surfaces.
"""
from __future__ import annotations

import json
import re
import subprocess
import sys
from datetime import datetime, timedelta, timezone
from pathlib import Path

import pytest

SCRIPT_PATH = Path(__file__).parent.parent / "compute_daily_regime_score.py"


def _write(path: Path, payload: dict) -> Path:
    path.write_text(json.dumps(payload))
    return path


def _make_inputs(tmp_path: Path, *, breadth=70, macro=80, top=30, bubble=50) -> dict:
    """Write the four fixture input files in the schemas the upstream skills
    actually emit today. Returns {flag_name: path} for the CLI invocation."""
    inputs = {}
    if breadth is not None:
        # breadth-chart-analyst's detect_uptrend_ratio.py emits a top-level
        # `current_value_percent` field (0-100 float representing % of stocks
        # in uptrend). Higher = healthier breadth.
        inputs["--breadth"] = _write(
            tmp_path / "breadth.json", {"current_value_percent": breadth}
        )
    if macro is not None:
        # macro-regime-detector emits composite.composite_score (0-100 int).
        # Polarity is RISK: high = regime is transitioning/unstable. The
        # compute script inverts it (uses 100-macro).
        inputs["--macro"] = _write(
            tmp_path / "macro.json", {"composite": {"composite_score": macro}}
        )
    if top is not None:
        # market-top-detector emits composite.composite_score (0-100 int).
        # Polarity is RISK: high = top imminent. Inverted in the formula.
        inputs["--top"] = _write(
            tmp_path / "top.json", {"composite": {"composite_score": top}}
        )
    if bubble is not None:
        # us-market-bubble-detector emits a top-level `percentage` field
        # (0-100 float). Polarity is RISK: high = bubble. Inverted in formula.
        inputs["--bubble"] = _write(tmp_path / "bubble.json", {"percentage": bubble})
    return inputs


def _run_script(tmp_path: Path, inputs: dict, **extra_args) -> Path:
    """Invoke the script as a subprocess (matches scheduler invocation pattern)
    and return the output JSON path."""
    output_path = tmp_path / "regime_gate.json"
    cmd = [sys.executable, str(SCRIPT_PATH), "--output", str(output_path)]
    for flag, path in inputs.items():
        cmd += [flag, str(path)]
    for flag, value in extra_args.items():
        cmd += [f"--{flag.replace('_', '-')}", str(value)]
    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        raise AssertionError(
            f"script failed with code {result.returncode}\n"
            f"stdout: {result.stdout}\nstderr: {result.stderr}"
        )
    return output_path


def test_full_inputs_computes_correct_score(tmp_path):
    # Formula: 0.35*breadth + 0.30*(100-macro) + 0.20*(100-top) + 0.15*(100-bubble)
    # With breadth=70, macro=80, top=30, bubble=50:
    #   = 0.35*70 + 0.30*20 + 0.20*70 + 0.15*50
    #   = 24.5 + 6.0 + 14.0 + 7.5 = 52.0 → rounds to 52
    inputs = _make_inputs(tmp_path, breadth=70, macro=80, top=30, bubble=50)
    output_path = _run_script(tmp_path, inputs)

    payload = json.loads(output_path.read_text())
    assert payload["score"] == 52, f"score: want 52, got {payload['score']}"
    # Components must record raw upstream values (not transformed). The
    # transformation (100-x) is only applied inside the weighting; auditors
    # reading regime_gate.json see what each upstream skill actually said.
    assert payload["components"]["breadth"]["value"] == 70
    assert payload["components"]["macro"]["value"] == 80
    assert payload["components"]["top_risk"]["value"] == 30
    assert payload["components"]["bubble"]["value"] == 50


def test_missing_breadth_uses_neutral_default(tmp_path):
    # Absent input must not brick the daily run. The script substitutes a
    # neutral 50 and flags `present: false` so operators can investigate.
    inputs = _make_inputs(tmp_path, breadth=None, macro=80, top=30, bubble=50)
    output_path = _run_script(tmp_path, inputs)

    payload = json.loads(output_path.read_text())
    # breadth contributes 50 instead of 70: change is 0.35 * (70-50) = 7
    # Previous score was 52, so new is 52 - 7 = 45
    assert payload["score"] == 45
    assert payload["components"]["breadth"]["present"] is False
    assert payload["components"]["breadth"]["value"] == 50  # neutral default
    assert payload["components"]["macro"]["present"] is True


def test_score_clamped_to_0_100(tmp_path):
    # All inputs at worst → score floor is 0, not negative.
    inputs = _make_inputs(tmp_path, breadth=0, macro=100, top=100, bubble=100)
    output_path = _run_script(tmp_path, inputs)
    payload = json.loads(output_path.read_text())
    assert payload["score"] == 0

    # All inputs at best → score ceiling is 100.
    inputs = _make_inputs(tmp_path, breadth=100, macro=0, top=0, bubble=0)
    output_path = _run_script(tmp_path, inputs)
    payload = json.loads(output_path.read_text())
    assert payload["score"] == 100


def test_output_schema_matches_go_regimegatefile(tmp_path):
    # The Go service (services/regime_gate_service.go regimeGateFile struct)
    # reads score (int), as_of (RFC3339), stale_after (RFC3339). The output
    # must always include these three fields with the expected types so the
    # Go side parses without a schema-version negotiation.
    inputs = _make_inputs(tmp_path)
    output_path = _run_script(tmp_path, inputs)
    payload = json.loads(output_path.read_text())

    assert isinstance(payload["score"], int)
    assert "as_of" in payload
    assert "stale_after" in payload
    # Go's time.Time RFC3339 parser accepts 2026-05-14T08:30:00Z and offsets.
    # The "Z" or "+HH:MM" suffix must be present.
    assert re.match(
        r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$",
        payload["as_of"],
    ), f"as_of not RFC3339: {payload['as_of']}"

    # components block is metadata (Go side ignores) but is the operator's
    # audit trail. Every component must be present, even when source file isn't.
    for key in ("breadth", "macro", "top_risk", "bubble"):
        assert key in payload["components"]
        assert "value" in payload["components"][key]
        assert "present" in payload["components"][key]


def test_stale_after_is_29h_from_as_of(tmp_path):
    # The Go service's regime_gate_service_test.go uses a 29h stale window
    # (24h trading + 5h buffer). The Python writer must match so a fresh
    # daily computation isn't flagged stale before the next one writes.
    inputs = _make_inputs(tmp_path)
    output_path = _run_script(tmp_path, inputs)
    payload = json.loads(output_path.read_text())

    as_of = datetime.fromisoformat(payload["as_of"].replace("Z", "+00:00"))
    stale_after = datetime.fromisoformat(payload["stale_after"].replace("Z", "+00:00"))
    delta = stale_after - as_of
    assert delta == timedelta(hours=29), f"delta: want 29h, got {delta}"


def test_invalid_json_input_uses_neutral_default(tmp_path):
    # Upstream skill writes corrupted JSON → script must not crash. Treat
    # the input as missing (neutral 50) and emit a warning. Matches Go side's
    # fail-open philosophy: bad data shouldn't silently halt all trading.
    bad_path = tmp_path / "bubble_corrupt.json"
    bad_path.write_text("{this is not valid json")
    inputs = _make_inputs(tmp_path, breadth=70, macro=80, top=30, bubble=None)
    inputs["--bubble"] = bad_path

    output_path = _run_script(tmp_path, inputs)
    payload = json.loads(output_path.read_text())
    # bubble falls back to 50; score is same as the missing-bubble case
    # (which would have been: 0.35*70 + 0.30*20 + 0.20*70 + 0.15*50 = 52)
    assert payload["score"] == 52
    assert payload["components"]["bubble"]["present"] is False
    assert payload["components"]["bubble"]["value"] == 50
