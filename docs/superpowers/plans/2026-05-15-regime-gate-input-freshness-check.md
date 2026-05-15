# Regime Gate Input-Freshness Check Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Detect stale upstream regime-skill input files in `scripts/compute_daily_regime_score.py` and treat them as missing (neutral 50, `present=false`, stderr warning), preventing silent staleness when an operator stops running an upstream skill.

**Architecture:** Add a module-level `INPUT_STALE_AFTER_HOURS = 36` default constant (distinct from the existing output-side `STALE_AFTER_HOURS = 29`) and a `--input-stale-after-hours` CLI flag that overrides it. The threshold is threaded through `build_payload` to `_read_score`. In `_read_score`, after the `exists()` check, compare the file's `mtime` against the `now` reference used for the payload. If the age exceeds the threshold, take the existing fail-soft path (neutral 50, `present=false`) and emit a stderr warning naming the file and its age. Pure addition — no other fail-soft path changes. Fail-OPEN remains correct: this layer feeds the Go service, which owns the closed-side `tier=UNKNOWN` decision via its own stale window.

**Tech Stack:** Python 3.11+, `pytest`, `pathlib`, `os.utime` (for test mtime control), `datetime`.

---

## File Structure

- Modify: `scripts/compute_daily_regime_score.py`
  - Add `INPUT_STALE_AFTER_HOURS = 36` constant near `STALE_AFTER_HOURS`.
  - Add `now: datetime` and `stale_threshold_hours: float` parameters to `_read_score`; thread both from `build_payload`.
  - Add `input_stale_after_hours: float = INPUT_STALE_AFTER_HOURS` parameter to `build_payload`.
  - Add `--input-stale-after-hours` CLI flag in `main()`; pass it into `build_payload`.
  - Insert mtime check between `exists()` and JSON read.
  - Update module docstring's "Fail-soft" paragraph and the Usage block.
- Modify: `scripts/tests/test_compute_daily_regime_score.py`
  - Add helper to backdate a file's mtime by N hours.
  - Add four tests: stale input → neutral, fresh input → unchanged, warning content, CLI flag override.

No new files.

---

## Task 1: Add stale-input tests (TDD red phase)

**Files:**
- Modify: `scripts/tests/test_compute_daily_regime_score.py` (append at end of file)

- [ ] **Step 1: Write the failing tests**

Append the following to `scripts/tests/test_compute_daily_regime_score.py`:

```python
def _backdate(path: Path, hours: float) -> None:
    """Set the file's mtime to `hours` in the past, leaving content intact.
    Used to simulate an upstream skill that stopped writing — the scheduler
    keeps re-picking this file as 'latest' because it's the only match."""
    import os
    import time
    past = time.time() - hours * 3600
    os.utime(path, (past, past))


def test_stale_input_uses_neutral_default(tmp_path):
    # Simulates the silent-staleness failure mode: upstream skill stopped
    # running, scheduler keeps picking the same weeks-old file forever.
    # The script must treat a too-old input as missing (neutral 50,
    # present=false) so the operator gets a loud signal and downstream
    # Go service eventually flips tier via its own stale window.
    inputs = _make_inputs(tmp_path, breadth=70, macro=80, top=30, bubble=50)
    # Default threshold is 36h; 40h is comfortably past it.
    _backdate(inputs["--breadth"], hours=40)

    output_path = _run_script(tmp_path, inputs)
    payload = json.loads(output_path.read_text())

    # breadth contributes 50 (neutral) instead of 70, mirroring the
    # missing-breadth test: score drops by 0.35 * (70-50) = 7 → 52 - 7 = 45.
    assert payload["score"] == 45
    assert payload["components"]["breadth"]["present"] is False
    assert payload["components"]["breadth"]["value"] == 50
    # source still recorded — operator needs the filename to investigate.
    assert payload["components"]["breadth"]["source"] == "breadth.json"
    # Other components unaffected.
    assert payload["components"]["macro"]["present"] is True


def test_fresh_input_is_not_flagged_stale(tmp_path):
    # Boundary check: a file written just now must not be flagged stale.
    # Without this, a typo turning ">" into ">=" or a wrong threshold
    # value silently neutralizes every component.
    inputs = _make_inputs(tmp_path, breadth=70, macro=80, top=30, bubble=50)
    # No backdating — mtime is "now".
    output_path = _run_script(tmp_path, inputs)
    payload = json.loads(output_path.read_text())

    assert payload["score"] == 52  # same as full-inputs happy path
    for key in ("breadth", "macro", "top_risk", "bubble"):
        assert payload["components"][key]["present"] is True, (
            f"{key} wrongly flagged not-present despite fresh mtime"
        )


def test_stale_input_warning_names_file_and_age(tmp_path):
    # Operator's only signal is the stderr warning. It must include
    # the filename (so they know which skill stopped) and the age
    # (so they know how stale — distinguishes "one missed day" from
    # "skill has been broken for a month").
    inputs = _make_inputs(tmp_path, breadth=70, macro=80, top=30, bubble=50)
    _backdate(inputs["--bubble"], hours=72)

    output_path = tmp_path / "regime_gate.json"
    cmd = [sys.executable, str(SCRIPT_PATH), "--output", str(output_path)]
    for flag, path in inputs.items():
        cmd += [flag, str(path)]
    result = subprocess.run(cmd, capture_output=True, text=True)
    assert result.returncode == 0, result.stderr

    stderr = result.stderr
    assert "bubble.json" in stderr, f"warning missing filename: {stderr!r}"
    assert "stale" in stderr.lower(), f"warning missing 'stale' keyword: {stderr!r}"
    # Age must appear as a number (we don't pin exact format — implementation
    # detail — but a digit followed by 'h' or 'hour' is the minimum signal).
    assert re.search(r"\d+(\.\d+)?\s*h", stderr), (
        f"warning missing age in hours: {stderr!r}"
    )


def test_cli_flag_overrides_default_threshold(tmp_path):
    # Operator-tunable threshold via --input-stale-after-hours. We pin the
    # threshold to 1h and backdate breadth by 2h — under the default 36h
    # this would be fresh, but with the override it must be flagged stale.
    # This proves the flag actually plumbs through to _read_score rather
    # than being silently ignored.
    inputs = _make_inputs(tmp_path, breadth=70, macro=80, top=30, bubble=50)
    _backdate(inputs["--breadth"], hours=2)

    output_path = _run_script(
        tmp_path, inputs, input_stale_after_hours=1
    )
    payload = json.loads(output_path.read_text())

    # breadth neutralized → same arithmetic as the missing-breadth case.
    assert payload["score"] == 45
    assert payload["components"]["breadth"]["present"] is False
    # And the converse sanity check: default threshold would NOT flag a
    # 2h-old file (covered already by test_fresh_input_is_not_flagged_stale
    # so we don't repeat the assertion here — this test is purely about
    # the flag mechanism).
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `python -m pytest scripts/tests/test_compute_daily_regime_score.py -v`

Expected: the four new tests fail (the existing five still pass). The mtime-driven tests fail because no check exists yet; `test_cli_flag_overrides_default_threshold` fails because the flag isn't defined (argparse will reject `--input-stale-after-hours`).

---

## Task 2: Add the input-freshness check + CLI flag

**Files:**
- Modify: `scripts/compute_daily_regime_score.py:40` (add constant)
- Modify: `scripts/compute_daily_regime_score.py:71-109` (`_read_score` signature + mtime branch)
- Modify: `scripts/compute_daily_regime_score.py:126-156` (`build_payload` signature + threading)
- Modify: `scripts/compute_daily_regime_score.py:168-190` (`main` adds `--input-stale-after-hours` flag)

- [ ] **Step 1: Add the constant**

Edit `scripts/compute_daily_regime_score.py`. Find:

```python
NEUTRAL_VALUE = 50  # used when an input is missing / unparseable
STALE_AFTER_HOURS = 29  # must match services/regime_gate_service_test.go convention
```

Replace with:

```python
NEUTRAL_VALUE = 50  # used when an input is missing / unparseable
STALE_AFTER_HOURS = 29  # OUTPUT freshness window. Must match services/regime_gate_service_test.go convention.
INPUT_STALE_AFTER_HOURS = 36  # INPUT freshness window. If an upstream skill's file mtime is older than this, treat the input as missing (neutral 50). Looser than the output window so a late upstream run doesn't immediately neutralize the score, but tight enough that a skill that's been broken for >1.5 days is loudly flagged. Distinct from STALE_AFTER_HOURS, which governs how long the Go service trusts our output.
```

- [ ] **Step 2: Add mtime check to `_read_score` (now takes `now` and `stale_threshold_hours`)**

Find the current `_read_score` signature and the `exists()` block:

```python
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
```

Replace with:

```python
def _read_score(
    component: str,
    file_path: Path | None,
    now: datetime,
    stale_threshold_hours: float,
) -> tuple[int, str | None, bool]:
    """Return (value, source_basename, present). Falls back to neutral 50 with
    `present=False` on any failure mode (missing, stale, unreadable, schema
    mismatch, non-numeric)."""
    if file_path is None:
        return NEUTRAL_VALUE, None, False
    if not file_path.exists():
        print(
            f"warn: {component} input not found: {file_path} — using neutral {NEUTRAL_VALUE}",
            file=sys.stderr,
        )
        return NEUTRAL_VALUE, file_path.name, False
    # mtime check: catches silent-staleness where the scheduler keeps picking
    # the same weeks-old "latest" file because the upstream skill stopped
    # writing new ones. We compare against the caller-supplied `now` (the
    # same reference used for as_of) so tests are deterministic.
    mtime = datetime.fromtimestamp(file_path.stat().st_mtime, tz=timezone.utc)
    age_hours = (now - mtime).total_seconds() / 3600
    if age_hours > stale_threshold_hours:
        print(
            f"warn: {component} input stale ({age_hours:.1f}h old > "
            f"{stale_threshold_hours}h threshold): {file_path} — "
            f"using neutral {NEUTRAL_VALUE}",
            file=sys.stderr,
        )
        return NEUTRAL_VALUE, file_path.name, False
    try:
        raw = json.loads(file_path.read_text())
```

- [ ] **Step 3: Thread `now` and threshold through `build_payload`**

Find the `build_payload` signature and the component loop:

```python
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
```

Replace with:

```python
def build_payload(
    *,
    breadth_path: Path | None,
    macro_path: Path | None,
    top_path: Path | None,
    bubble_path: Path | None,
    now: datetime | None = None,
    input_stale_after_hours: float = INPUT_STALE_AFTER_HOURS,
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
        value, source, present = _read_score(name, path, now, input_stale_after_hours)
        components[name] = {"value": value, "source": source, "present": present}
```

- [ ] **Step 4: Add CLI flag in `main()`**

Find the `main()` function's argparse block and the `build_payload` call:

```python
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
```

Replace with:

```python
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
    parser.add_argument(
        "--input-stale-after-hours",
        type=float,
        default=INPUT_STALE_AFTER_HOURS,
        help=(
            "treat an input file as missing if its mtime is older than this "
            f"many hours (default {INPUT_STALE_AFTER_HOURS}). Distinct from "
            "the output-side STALE_AFTER_HOURS the Go service checks."
        ),
    )
    args = parser.parse_args()

    payload = build_payload(
        breadth_path=args.breadth,
        macro_path=args.macro,
        top_path=args.top,
        bubble_path=args.bubble,
        input_stale_after_hours=args.input_stale_after_hours,
    )
```

- [ ] **Step 5: Run the new tests and verify they pass**

Run: `python -m pytest scripts/tests/test_compute_daily_regime_score.py::test_stale_input_uses_neutral_default scripts/tests/test_compute_daily_regime_score.py::test_fresh_input_is_not_flagged_stale scripts/tests/test_compute_daily_regime_score.py::test_stale_input_warning_names_file_and_age scripts/tests/test_compute_daily_regime_score.py::test_cli_flag_overrides_default_threshold -v`

Expected: all four PASS.

- [ ] **Step 6: Run the full test file and verify nothing regressed**

Run: `python -m pytest scripts/tests/test_compute_daily_regime_score.py -v`

Expected: all 9 tests pass (5 original + 4 new).

---

## Task 3: Update the module docstring

**Files:**
- Modify: `scripts/compute_daily_regime_score.py:16-19` (fail-soft paragraph)

- [ ] **Step 1: Update the docstring**

Find:

```python
Fail-soft: any input that is missing, unreadable, or malformed contributes a
neutral 50. The components block records `present: false` for forensics.
Operator gets the loud signal via stderr warning; downstream Go service
keeps trading on the most recent good value.
```

Replace with:

```python
Fail-soft: any input that is missing, stale (mtime older than
INPUT_STALE_AFTER_HOURS — default 36h, overridable via
--input-stale-after-hours), unreadable, or malformed contributes a neutral
50. The components block records `present: false` for forensics. Operator
gets the loud signal via stderr warning; downstream Go service keeps
trading on the most recent good value until its own STALE_AFTER_HOURS
output window closes and it flips to tier=UNKNOWN. The stale-input check
exists because the scheduler picks the lexicographically latest matching
file, so a skill that stops running has its last file silently re-used
forever without this guard.
```

- [ ] **Step 2: Sanity-check the script still imports/parses**

Run: `python -c "import scripts.compute_daily_regime_score as m; print(m.INPUT_STALE_AFTER_HOURS)"`

Expected output: `36`

Alternatively (if `scripts/` isn't a package on the Python path), run:

`python scripts/compute_daily_regime_score.py --help`

Expected: argparse usage prints without traceback.

- [ ] **Step 3: Run the full test file one more time**

Run: `python -m pytest scripts/tests/test_compute_daily_regime_score.py -v`

Expected: all 9 tests pass.

---

## Task 4: Squash-commit the change

**Files:**
- All changes staged together.

- [ ] **Step 1: Inspect the diff**

Run: `git status` and `git diff scripts/compute_daily_regime_score.py scripts/tests/test_compute_daily_regime_score.py`

Confirm only these two files changed (plus any pre-existing modifications already on the branch — leave those alone for this commit if they are unrelated; otherwise stop and ask).

- [ ] **Step 2: Stage and commit**

```bash
git add scripts/compute_daily_regime_score.py scripts/tests/test_compute_daily_regime_score.py
git commit -m "$(cat <<'EOF'
feat(regime-gate): treat stale upstream input files as missing

Add INPUT_STALE_AFTER_HOURS=36 mtime check to compute_daily_regime_score.py
with --input-stale-after-hours CLI override. If an upstream skill stops
running, the scheduler keeps picking the same weeks-old "latest" file
forever and regime_gate.json silently freezes against a stale market read.
The new check files such an input under the existing missing/unreadable
fail-soft path: neutral 50, present=false, stderr warning naming the file
and its age.

Distinct from the output-side STALE_AFTER_HOURS=29 the Go service uses —
this is fail-OPEN at the writer, while the Go service still owns the
closed-side tier=UNKNOWN decision via its own stale window.

Tests added for stale input, fresh-input boundary, warning content, and
CLI flag override.
EOF
)"
```

- [ ] **Step 3: Confirm commit landed**

Run: `git log -1 --stat`

Expected: one commit, two files changed.

---

## Verification

- [ ] All 9 tests in `scripts/tests/test_compute_daily_regime_score.py` pass.
- [ ] Existing `agent/analysis-scheduler.test.mjs` still passes (no scheduler-side changes, but sanity check): `node --test agent/analysis-scheduler.test.mjs`.
- [ ] `python scripts/compute_daily_regime_score.py --help` exits 0 and shows `--input-stale-after-hours` in the output.
- [ ] Single new commit on the current branch.
