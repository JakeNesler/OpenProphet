# Regime-Skills Scheduler Design

**Status:** Approved 2026-05-15

**Goal:** Automate the four upstream regime-gate skills (breadth, macro, top, bubble) so they produce fresh `data/reports/*.json` daily before `regime_gate_compute` runs, eliminating the operator's manual python-script invocation.

**Background:** Item 2 of the risk-and-diversification plan (`docs/superpowers/plans/2026-05-14-risk-and-diversification-plan.md:402`) called out the upstream-skill dependency as a deferred cross-cutting task. Without daily skill runs, the input-freshness check (commit `4715d78`, `INPUT_STALE_AFTER_HOURS=36`) eventually drops each component to neutral 50 and the regime tier becomes uninformative.

---

## 1. Architecture

Four new scheduler jobs in `agent/analysis-scheduler.js`, **hybrid invocation** (revised 2026-05-15 after deeper script research):

- **`macro_regime_skill`** — direct python spawn via `child_process.spawn`, mirroring `_runRegimeGateCompute`. `macro_regime_detector.py` is fully autonomous (FMP-fetched, takes `--output-dir` + `--api-key`, writes `macro_regime_<timestamp>.json` matching the schema `compute_daily_regime_score.py` expects).

- **`breadth_skill`, `market_top_skill`, `bubble_skill`** — LLM-driven via `_runSkill`, mirroring `harvest_parameter_review`. Each skill's python scripts require an LLM-in-the-loop for either data collection (market_top needs put/call, VIX-term, margin-debt from WebSearch; bubble needs 12 indicator scores) or output reshaping (breadth's autonomous `fetch_breadth_csv.py` emits `breadth_200ma`/`uptrend_ratio` but the regime-gate writer extracts `current_value_percent`; LLM bridges the schema gap). Each gets an AUTOMATED RUN appendix that directs the LLM to write `data/reports/<prefix>_<YYYYMMDD>.json` with the exact keys `compute_daily_regime_score.py` extracts.

State persisted in `data/scheduler-state.json`. Locks via `.running` files in `data/reports/`.

## 2. Jobs and entrypoint scripts

Four new entries added to `triggerJob`'s `validJobs` list. Three call the python-spawn helper; one calls `_runSkill` with a custom appendix.

| Job name | Mechanism | Entrypoint | Output (data/reports/) | FMP |
|---|---|---|---|---|
| `macro_regime_skill` | python spawn | `.claude/skills/macro-regime-detector/scripts/macro_regime_detector.py` | `macro_regime_<YYYY-MM-DD_HHMMSS>.json` (script writes) | Yes |
| `breadth_skill` | LLM via `_runSkill` | `breadth-chart-analyst/SKILL.md` + AUTOMATED RUN appendix | `breadth_<YYYYMMDD>.json` (LLM Write tool, must include `current_value_percent`) | No |
| `market_top_skill` | LLM via `_runSkill` | `market-top-detector/SKILL.md` + AUTOMATED RUN appendix | `market_top_<YYYYMMDD>.json` (LLM Write tool, must include `composite.composite_score`) | Yes (FMP via script's `--api-key`) |
| `bubble_skill` | LLM via `_runSkill` | `us-market-bubble-detector/SKILL.md` + AUTOMATED RUN appendix | `bubble_<YYYYMMDD>.json` (LLM Write tool, must include `percentage`) | No (uses public sources) |

**FMP guard:** Only `macro_regime_skill` strictly requires `FMP_API_KEY` at the scheduler level (it's a python spawn and we check before invoking). Skip with a `warning` log if missing — matching `_runDailyBriefing`'s pattern (`analysis-scheduler.js:849-850`). `market_top_skill` also needs FMP but goes through the LLM, which surfaces its own errors via `_runOneshotOpencode`. Operator has an FMP Starter-tier key; daily call volume is well under the tier's limit.

## 3. Timing, sequencing, startup healing

**Scheduled trigger** — single condition in `_checkSchedule` at **5:00 AM ET weekdays** (revised: three LLM runs need 30+ min budget before `regime_gate_compute` fires at 5:50):

```js
if (isWeekday && hour === 5 && minute === 0) {
  if (this._lastMacroRegimeDate !== isoDate)  await this.triggerJob('macro_regime_skill').catch(() => {});
  if (this._lastBreadthDate !== isoDate)      await this.triggerJob('breadth_skill').catch(() => {});
  if (this._lastMarketTopDate !== isoDate)    await this.triggerJob('market_top_skill').catch(() => {});
  if (this._lastBubbleDate !== isoDate)       await this.triggerJob('bubble_skill').catch(() => {});
}
```

Order: macro_regime first (python, <1 min), then the three LLM-driven skills sequentially. Each LLM run uses a 15-minute timeout (matching `harvest_parameter_review`). The `_activeJob` mutex enforces serial execution. Worst-case timing: macro done by 5:01, breadth by 5:16, market_top by 5:31, bubble by 5:46 — still inside the 5:50 ET deadline before `regime_gate_compute`. Typical case is much faster (LLM runs often finish in 5–10 min). If any LLM stalls past 5:50, `regime_gate_compute` still fires with whatever JSONs exist; the 36h input-freshness window covers yesterday's file for the stalled component.

```
5:00 AM ET → macro_regime_skill (python) → breadth_skill (LLM) → market_top_skill (LLM) → bubble_skill (LLM)
5:50 AM ET → regime_gate_compute (reads the four JSONs)
6:00 AM ET → daily_briefing
```

Each branch is independently gated on its own `_lastXxxDate !== isoDate`, so a transient failure of one skill does not block the others within the same morning chain.

**Startup healing** — new step **1.4** in `runStartupChecks`, inserted **before** the existing step 1.5 (`regime_gate_compute` heal). Same `if (isWeekday && hour < 16 && _lastXxxDate !== isoDate)` pattern, awaited per-skill so all four JSONs exist by the time step 1.5 runs. Covers two cases: bot offline at the 5:00 ET trigger, or bot started mid-morning after `snapshot.ps1` swept `data/reports/`.

**No explicit dependency between `regime_gate_compute` and the four skills.** The `INPUT_STALE_AFTER_HOURS=36` window already tolerates one missed day, and 1.4-before-1.5 startup ordering covers the bot-restart case. Adding a hard wait would couple jobs that fail-soft well on their own.

## 4. State, locks, failure handling

**State** — four new fields in `data/scheduler-state.json`, persisted alongside `_lastRegimeGateDate`:

```js
this._lastBreadthDate = null;
this._lastMacroRegimeDate = null;
this._lastMarketTopDate = null;
this._lastBubbleDate = null;
```

Added to `getStatus()`, `_loadState()`, `_saveState()` symmetrically with the existing `_lastRegimeGateDate`.

**Locks** — four new keys in `_getLockKey`'s switch statement: `breadth_skill_YYYYMMDD`, `macro_regime_skill_YYYYMMDD`, `market_top_skill_YYYYMMDD`, `bubble_skill_YYYYMMDD`. Acquired/released by `triggerJob` like every other job.

**Failure handling (asymmetric):**
- **Script exits non-zero:** log `error` with stderr (mirrors `_runRegimeGateCompute:829-834`), set `_lastXxxDate = isoDate` anyway. Rationale: scheduler should not thrash retrying every minute. Operator sees the failure in the log; the next morning's run will retry naturally.
- **FMP_API_KEY missing for an FMP-requiring skill:** log `warning` and skip without setting `_lastXxxDate`. Rationale: this isn't "we tried and failed" — it's "we can't try at all." Leaving the date null lets the operator fix the env var and trigger via the `triggerJob` API the same day.

One skill failing never blocks the others. Downstream `regime_gate_compute` fail-softs on missing inputs, and the input-freshness check covers the stale-from-old-failure case.

## 5. Testing

Following the existing `agent/analysis-scheduler.test.mjs` pattern (`buildRegimeComputeArgv` test at lines 28-90).

**Unit-level (node:test):**
- Export `buildMacroRegimeArgv(scriptPath, outputDir, apiKey)` helper from `analysis-scheduler.js`. Tests:
  - argv contains the script path, `--output-dir data/reports`, and `--api-key <value>`.
  - argv omits `--api-key` when `apiKey` is null/undefined.
- Export three appendix builders — `buildBreadthSkillAppendix(date)`, `buildMarketTopSkillAppendix(date)`, `buildBubbleSkillAppendix(date)`. Each test verifies the appendix:
  - directs the LLM to Write the output to `data/reports/<prefix>_<YYYYMMDD>.json` (where `<YYYYMMDD>` is the dateslug of the supplied date).
  - explicitly requires the output JSON to contain the field `compute_daily_regime_score.py` extracts (breadth: `current_value_percent`; market_top: nested `composite.composite_score`; bubble: `percentage`).
  - tells the LLM to skip any user-confirmation step in the underlying SKILL.md.

**Integration-level (also `analysis-scheduler.test.mjs`):**
- `getStatus()` returns the four new `lastXxxDate` fields, initially `null`.
- `triggerJob('breadth_skill')` (and the three others) does NOT return the "Unknown job" error — guards against an incomplete edit.

**Out of scope:**
- Direct unit tests of the four runner methods (subprocess / LLM-spawning — covered by manual smoke-test the next morning).
- End-to-end "scheduled job fires at 5:00 ET" — existing scheduler tests don't cover wall-clock `_checkSchedule` triggers either.
- New python-side tests — the four skill scripts are unchanged; their internal correctness is the skills' concern.

---

## Files affected

- Modify: `agent/analysis-scheduler.js`
- Modify: `agent/analysis-scheduler.test.mjs`
- No changes to the four skill folders.
- No changes to `scripts/compute_daily_regime_score.py`.

## Risks

- **Skill CLI changes silently.** If a skill author renames `--output-dir` or removes `--api-key`, our spawn breaks. Mitigation: argv-builder unit tests pin the flag names; a renamed flag fails CI before production.
- **`snapshot.ps1` semantics unknown.** Files disappeared from `data/reports/` after the operator ran it yesterday. Out of scope for this design — startup healing (1.4) covers the resulting gap. Worth documenting separately at some point.
- **Future skill that needs more than `--output-dir` + `--api-key`.** The generic helper handles the current four cleanly. If a fifth skill has a different shape, refactor at that point — premature abstraction otherwise.
