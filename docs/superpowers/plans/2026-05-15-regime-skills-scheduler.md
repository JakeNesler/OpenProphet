# Regime-Skills Scheduler Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add four new scheduler jobs (`macro_regime_skill`, `breadth_skill`, `market_top_skill`, `bubble_skill`) to `agent/analysis-scheduler.js` so the upstream regime-gate inputs are produced automatically each pre-market.

**Architecture:** Hybrid invocation. `macro_regime_skill` is a direct python spawn (the script is fully autonomous and writes the JSON itself). The other three are LLM-driven via `_runSkill` with per-skill AUTOMATED RUN appendix builders that direct the LLM to write a specific filename with the keys `compute_daily_regime_score.py` extracts. State, locks, and startup healing mirror the existing `regime_gate_compute` patterns.

**Tech Stack:** Node.js (ESM), `node:test`, `child_process.spawn`, existing scheduler primitives (`_runSkill`, `_runOneshotOpencode`).

**Spec:** `docs/superpowers/specs/2026-05-15-regime-skills-scheduler-design.md`.

---

## File Structure

- Modify: `agent/analysis-scheduler.js` — add constants, 4 exported pure helpers, 4 state fields with load/save/getStatus plumbing, 4 lock-key cases, 4 validJobs entries, 4 triggerJob branches, 4 runner methods, 1 scheduled-trigger block, 1 startup-heal step.
- Modify: `agent/analysis-scheduler.test.mjs` — append tests for the 4 helpers, the new state surface, and the new lock-key cases.

No new files. No changes to the four skill folders. No changes to `scripts/compute_daily_regime_score.py`.

---

## Task 1: Append failing tests

**Files:**
- Modify: `agent/analysis-scheduler.test.mjs` (append at end of file)

- [ ] **Step 1: Extend the existing import line at the top of the test file**

The current test file imports only `buildRegimeComputeArgv`. The new tests need `AnalysisScheduler` (the class) plus the four new helpers. Find this line near the top of `agent/analysis-scheduler.test.mjs`:

```js
import { buildRegimeComputeArgv } from './analysis-scheduler.js';
```

Replace it with:

```js
import {
  buildRegimeComputeArgv,
  buildMacroRegimeArgv,
  buildBreadthSkillAppendix,
  buildMarketTopSkillAppendix,
  buildBubbleSkillAppendix,
  AnalysisScheduler,
} from './analysis-scheduler.js';
```

- [ ] **Step 2: Append the new test block at the end of the file**

Append the following to `agent/analysis-scheduler.test.mjs` (no further imports — `test`, `assert`, `AnalysisScheduler`, and the four builders are now all in scope from Step 1):

```js

test('buildMacroRegimeArgv includes script, output-dir, and api-key when key provided', () => {
  const argv = buildMacroRegimeArgv(
    '/abs/path/to/macro_regime_detector.py',
    'data/reports',
    'fmp_test_key',
  );
  assert.equal(argv[0], '/abs/path/to/macro_regime_detector.py');
  // --output-dir and --api-key both present, with their values.
  assert.deepEqual(
    argv.filter((v) => v.startsWith('--')).sort(),
    ['--api-key', '--output-dir'],
  );
  assert.equal(argv[argv.indexOf('--output-dir') + 1], 'data/reports');
  assert.equal(argv[argv.indexOf('--api-key') + 1], 'fmp_test_key');
});

test('buildMacroRegimeArgv omits --api-key when key is null/undefined', () => {
  // We still call the script — it falls back to the FMP_API_KEY env var inside
  // the python process. The scheduler-level guard logs a warning before reaching
  // the builder if the env var is also missing.
  const argv = buildMacroRegimeArgv('/script.py', 'data/reports', null);
  assert.ok(!argv.includes('--api-key'), `argv should not contain --api-key, got ${argv}`);
  assert.ok(argv.includes('--output-dir'), 'argv must still contain --output-dir');
});

test('buildBreadthSkillAppendix directs LLM to write breadth_<date>.json with current_value_percent', () => {
  const appendix = buildBreadthSkillAppendix('2026-05-15');
  // The dateslug form (no dashes) matches the daily_briefing convention used
  // elsewhere in the file (data/reports/daily_brief_YYYYMMDD.json).
  assert.match(appendix, /data\/reports\/breadth_20260515\.json/, 'appendix must name the target filename');
  assert.match(appendix, /current_value_percent/, 'appendix must require the current_value_percent key');
  assert.match(appendix, /AUTOMATED RUN/, 'appendix must mark itself as an automated run override');
});

test('buildMarketTopSkillAppendix directs LLM to write market_top_<date>.json with composite.composite_score', () => {
  const appendix = buildMarketTopSkillAppendix('2026-05-15');
  assert.match(appendix, /data\/reports\/market_top_20260515\.json/);
  // The key path the regime-gate writer extracts: nested under "composite".
  assert.match(appendix, /composite_score/);
  assert.match(appendix, /composite/);
  assert.match(appendix, /AUTOMATED RUN/);
});

test('buildBubbleSkillAppendix directs LLM to write bubble_<date>.json with percentage', () => {
  const appendix = buildBubbleSkillAppendix('2026-05-15');
  assert.match(appendix, /data\/reports\/bubble_20260515\.json/);
  assert.match(appendix, /percentage/);
  // The bubble scorer is run with --scores '<json>' --output json — the appendix
  // must reference the script so the LLM doesn't skip the scoring step.
  assert.match(appendix, /bubble_scorer\.py/);
  assert.match(appendix, /AUTOMATED RUN/);
});

test('getStatus exposes the four new regime-skill last-run dates', () => {
  const scheduler = new AnalysisScheduler();
  const status = scheduler.getStatus();
  // All four start null on a fresh instance — no state file loaded.
  assert.equal(status.lastBreadthDate, null);
  assert.equal(status.lastMacroRegimeDate, null);
  assert.equal(status.lastMarketTopDate, null);
  assert.equal(status.lastBubbleDate, null);
});

test('_getLockKey produces stable dateslug-suffixed keys for each new job', () => {
  const scheduler = new AnalysisScheduler();
  assert.equal(scheduler._getLockKey('macro_regime_skill', '2026-05-15'), 'macro_regime_skill_20260515');
  assert.equal(scheduler._getLockKey('breadth_skill', '2026-05-15'),      'breadth_skill_20260515');
  assert.equal(scheduler._getLockKey('market_top_skill', '2026-05-15'),   'market_top_skill_20260515');
  assert.equal(scheduler._getLockKey('bubble_skill', '2026-05-15'),       'bubble_skill_20260515');
});
```

- [ ] **Step 3: Run the tests and verify all 7 new tests fail**

Run: `node --test agent/analysis-scheduler.test.mjs`

Expected: the existing tests continue to pass; the 7 new tests fail. Likely failure mode is `SyntaxError: The requested module './analysis-scheduler.js' does not provide an export named 'buildMacroRegimeArgv'` — that's fine, it confirms the TDD red phase.

---

## Task 2: Implement constants, exported helpers, state, and lock keys

**Files:**
- Modify: `agent/analysis-scheduler.js`

After Task 2 every test from Task 1 passes. The runner methods and scheduled-trigger glue (Task 3) have no unit tests; they make the feature actually fire daily.

- [ ] **Step 1: Add the module-level constants**

In `agent/analysis-scheduler.js`, find the existing `const REGIME_COMPUTE_SCRIPT = ...` line near the top of the file (around line 42). After the `REGIME_GATE_OUTPUT` line, add:

```js
// Upstream regime-gate skill scripts. macro_regime is a direct python spawn;
// breadth/market_top/bubble are invoked through opencode + their SKILL.md
// because they require an LLM in the loop (data collection, schema reshape,
// or manual scoring inputs). See docs/superpowers/specs/2026-05-15-regime-skills-scheduler-design.md.
const MACRO_REGIME_SCRIPT = path.join(PROJECT_ROOT, '.claude', 'skills', 'macro-regime-detector', 'scripts', 'macro_regime_detector.py');
```

- [ ] **Step 2: Add the four exported pure helpers**

In `agent/analysis-scheduler.js`, find the existing `export function buildRegimeComputeArgv(...)` (around line 63). Immediately AFTER its closing brace, add:

```js
/**
 * Build the spawn argv for macro_regime_skill. Pure function — exported for tests.
 * `apiKey` may be null/undefined; in that case `--api-key` is omitted and the
 * python script falls back to its own FMP_API_KEY env var lookup.
 */
export function buildMacroRegimeArgv(scriptPath, outputDir, apiKey) {
  const argv = [scriptPath, '--output-dir', outputDir];
  if (apiKey) argv.push('--api-key', apiKey);
  return argv;
}

/**
 * Build the AUTOMATED RUN appendix for breadth_skill. The breadth-chart-analyst
 * skill's autonomous python (fetch_breadth_csv.py) emits a different schema than
 * compute_daily_regime_score.py expects, so the LLM must reshape: run the CSV
 * fetcher, take the uptrend-ratio current value as `current_value_percent`, and
 * Write the result to data/reports/breadth_<dateslug>.json.
 */
export function buildBreadthSkillAppendix(date) {
  const slug = date.replace(/-/g, '');
  return `**AUTOMATED RUN**: This skill was triggered automatically by the scheduler to produce a regime-gate input. Do not run image-based analysis or generate the long-form markdown report. Instead:
1. Run \`python .claude/skills/breadth-chart-analyst/scripts/fetch_breadth_csv.py --json\` and capture its stdout.
2. Use the Write tool to save a JSON file at exactly this path: data/reports/breadth_${slug}.json
3. The saved JSON MUST contain a top-level integer field \`current_value_percent\` (0-100). Use the current uptrend-ratio value from the CSV fetch output, rounded to the nearest integer. Other fields from the CSV output may be preserved alongside it for forensics, but \`current_value_percent\` is required.
4. Do NOT prompt for user confirmation. Do NOT write any other files.`;
}

/**
 * Build the AUTOMATED RUN appendix for market_top_skill. Tells the LLM to collect
 * the WebSearch-sourced inputs market_top_detector.py needs (put-call, VIX-term,
 * margin-debt-yoy) and run the script with --output-dir so it writes the canonical
 * market_top_*.json with composite.composite_score directly.
 */
export function buildMarketTopSkillAppendix(date) {
  const slug = date.replace(/-/g, '');
  return `**AUTOMATED RUN**: This skill was triggered automatically by the scheduler to produce a regime-gate input. Do not generate the long-form markdown report. Instead:
1. Follow the SKILL.md's data-collection steps to gather: CBOE equity put/call ratio, VIX term structure state, margin debt year-over-year change. Breadth values may be auto-fetched via the script's default behavior.
2. Run \`python .claude/skills/market-top-detector/scripts/market_top_detector.py --output-dir data/reports --put-call <value> --vix-term <value> --margin-debt-yoy <value>\`, supplying the values collected in step 1. Include any other CLI args the SKILL.md instructs you to pass.
3. Verify a new file appears in data/reports/ matching \`market_top_${slug}*.json\` (the script appends its own timestamp). The file MUST contain a nested integer field \`composite.composite_score\` (0-100); this is the field compute_daily_regime_score.py extracts.
4. Do NOT prompt for user confirmation. Do NOT write any other files.`;
}

/**
 * Build the AUTOMATED RUN appendix for bubble_skill. bubble_scorer.py is a pure
 * scoring engine — the LLM must collect the 12 indicators from public sources,
 * run the scorer with --scores '<json>' --output json, capture stdout, and Write
 * the result to data/reports/bubble_<dateslug>.json.
 */
export function buildBubbleSkillAppendix(date) {
  const slug = date.replace(/-/g, '');
  return `**AUTOMATED RUN**: This skill was triggered automatically by the scheduler to produce a regime-gate input. Do not prompt for manual scoring inputs. Instead:
1. Follow the SKILL.md's Phase 1 data-collection workflow to gather the 12 quantitative indicator values from public sources (Put/Call ratio, VIX, margin debt, breadth, IPO data, etc.).
2. Score each indicator 0-2 per the SKILL.md guidelines and assemble the scores into a JSON object matching the format \`bubble_scorer.py\` expects.
3. Run \`python .claude/skills/us-market-bubble-detector/scripts/bubble_scorer.py --scores '<JSON_FROM_STEP_2>' --output json\` and capture its stdout.
4. Use the Write tool to save the captured stdout JSON at exactly this path: data/reports/bubble_${slug}.json
5. The saved JSON MUST contain a top-level numeric field \`percentage\` (0-100); this is the field compute_daily_regime_score.py extracts. The scorer should produce it by default.
6. Do NOT prompt for user confirmation. Do NOT write any other files.`;
}
```

- [ ] **Step 3: Add state fields to the constructor**

Find the constructor's existing `this._lastRegimeGateDate = null;` line (around line 107). Immediately after it, add:

```js
    this._lastBreadthDate = null;       // YYYY-MM-DD (daily breadth-chart-analyst run)
    this._lastMacroRegimeDate = null;   // YYYY-MM-DD (daily macro-regime-detector run)
    this._lastMarketTopDate = null;     // YYYY-MM-DD (daily market-top-detector run)
    this._lastBubbleDate = null;        // YYYY-MM-DD (daily us-market-bubble-detector run)
```

- [ ] **Step 4: Expose the four new fields in `getStatus()`**

Find the `getStatus()` return object (around line 128). After the existing `lastRegimeGateDate: this._lastRegimeGateDate,` line, add:

```js
      lastBreadthDate: this._lastBreadthDate,
      lastMacroRegimeDate: this._lastMacroRegimeDate,
      lastMarketTopDate: this._lastMarketTopDate,
      lastBubbleDate: this._lastBubbleDate,
```

- [ ] **Step 5: Load the four new fields from state file**

Find `_loadState()` (around line 573). After the existing `this._lastRegimeGateDate = s.lastRegimeGateDate || null;` line, add:

```js
      this._lastBreadthDate = s.lastBreadthDate || null;
      this._lastMacroRegimeDate = s.lastMacroRegimeDate || null;
      this._lastMarketTopDate = s.lastMarketTopDate || null;
      this._lastBubbleDate = s.lastBubbleDate || null;
```

- [ ] **Step 6: Persist the four new fields in `_saveState()`**

Find `_saveState()` (around line 596). In the `JSON.stringify` object, after the existing `lastRegimeGateDate: this._lastRegimeGateDate,` line, add:

```js
        lastBreadthDate: this._lastBreadthDate,
        lastMacroRegimeDate: this._lastMacroRegimeDate,
        lastMarketTopDate: this._lastMarketTopDate,
        lastBubbleDate: this._lastBubbleDate,
```

- [ ] **Step 7: Add lock-key cases**

Find `_getLockKey()`'s switch statement (around line 502). After the existing `case 'trend_parameter_review':` line, BEFORE the `default:` case, add:

```js
      case 'macro_regime_skill': return `macro_regime_skill_${dateSlug}`;
      case 'breadth_skill':      return `breadth_skill_${dateSlug}`;
      case 'market_top_skill':   return `market_top_skill_${dateSlug}`;
      case 'bubble_skill':       return `bubble_skill_${dateSlug}`;
```

- [ ] **Step 8: Run the tests and verify the 7 new tests now pass**

Run: `node --test agent/analysis-scheduler.test.mjs`

Expected: all tests pass — both the original ones and the 7 new ones from Task 1.

---

## Task 3: Wire validJobs, triggerJob branches, runner methods, scheduled trigger, and startup heal

After this task the feature actually fires daily. No new tests in this task — these pieces are subprocess-spawning glue covered by manual smoke-test the next morning.

**Files:**
- Modify: `agent/analysis-scheduler.js`

- [ ] **Step 1: Add four entries to the `validJobs` array in `triggerJob`**

Find the `validJobs` array (around line 149). After the existing `'regime_gate_compute',` entry, add:

```js
      'macro_regime_skill', 'breadth_skill', 'market_top_skill', 'bubble_skill',
```

- [ ] **Step 2: Add four `triggerJob` branches**

Find the `else if (jobName === 'regime_gate_compute') { ... }` block in `triggerJob` (around line 213). Immediately AFTER its closing brace (before the existing `else if (jobName === 'trend_parameter_review')` branch), add:

```js
      } else if (jobName === 'macro_regime_skill') {
        // Spec: missing FMP_API_KEY skips WITHOUT advancing _lastMacroRegimeDate
        // so the operator can fix the env var and re-trigger same-day. Other
        // failure modes (spawn error, non-zero exit) DO advance the date to
        // avoid thrash retry — handled inside the runner.
        if (!process.env.FMP_API_KEY) {
          this._log('macro_regime_skill: FMP_API_KEY not set — skipping; regime_gate_compute will fall back to neutral 50 for macro. Date not advanced so operator can re-trigger after setting key.', 'warning');
          return { skipped: true, reason: 'FMP_API_KEY missing', job: jobName };
        }
        this._lastMacroRegimeDate = isoDate;
        await this._runMacroRegimeSkill(isoDate);
        await this._saveState();
      } else if (jobName === 'breadth_skill') {
        this._lastBreadthDate = isoDate;
        await this._runBreadthSkill(isoDate);
        await this._saveState();
      } else if (jobName === 'market_top_skill') {
        this._lastMarketTopDate = isoDate;
        await this._runMarketTopSkill(isoDate);
        await this._saveState();
      } else if (jobName === 'bubble_skill') {
        this._lastBubbleDate = isoDate;
        await this._runBubbleSkill(isoDate);
        await this._saveState();
```

(Mirrors the existing `regime_gate_compute` branch: set the date BEFORE the await so we don't thrash retry on failure, then save state after.)

- [ ] **Step 3: Add the macro-regime python runner**

Find `_runRegimeGateCompute(date)` (around line 806). Immediately BEFORE it, add a new method:

```js
  // macro_regime_skill: direct python spawn. The script fetches its own data from
  // FMP and writes data/reports/macro_regime_<timestamp>.json. The FMP_API_KEY
  // presence check happens in triggerJob (skips without advancing date so the
  // operator can fix and re-trigger). On script error we log and move on —
  // regime_gate_compute's own fail-soft + the 36h input-freshness window cover
  // the gap.
  async _runMacroRegimeSkill(date) {
    this._log(`Starting macro_regime_skill for ${date}...`, 'info');
    this.emit('scheduler_job_start', { job: 'macro_regime_skill', date });

    const argv = buildMacroRegimeArgv(MACRO_REGIME_SCRIPT, REPORTS_DIR, process.env.FMP_API_KEY);

    await new Promise((resolve) => {
      const child = spawn(PYTHON_BIN, argv, { stdio: ['ignore', 'pipe', 'pipe'] });
      let stderr = '';
      child.stderr.on('data', (chunk) => { stderr += chunk.toString(); });
      child.on('error', (err) => {
        this._log(`macro_regime_skill spawn failed: ${err.message}`, 'error');
        resolve();
      });
      child.on('close', (code) => {
        if (code === 0) {
          this._log(`macro_regime_skill complete → ${REPORTS_DIR}/macro_regime_<timestamp>.json`, 'success');
        } else {
          this._log(`macro_regime_skill exited ${code}; stderr: ${stderr.trim()}`, 'error');
        }
        resolve();
      });
    });

    this.emit('scheduler_job_end', { job: 'macro_regime_skill', date, output: REPORTS_DIR });
  }
```

- [ ] **Step 4: Add the three LLM-driven runners**

Immediately AFTER `_runMacroRegimeSkill`, add:

```js
  // breadth_skill: LLM-driven via _runSkill so the LLM can run the CSV fetcher
  // and reshape the output to the schema compute_daily_regime_score.py expects.
  async _runBreadthSkill(date) {
    await this._runSkill('breadth-chart-analyst', date, null, 15 * 60 * 1000, buildBreadthSkillAppendix(date));
  }

  // market_top_skill: LLM-driven so the LLM can WebSearch put/call, VIX-term,
  // and margin-debt-yoy inputs that market_top_detector.py requires.
  async _runMarketTopSkill(date) {
    await this._runSkill('market-top-detector', date, null, 15 * 60 * 1000, buildMarketTopSkillAppendix(date));
  }

  // bubble_skill: LLM-driven so the LLM can collect the 12 indicator values
  // and pass them as --scores '<json>' to bubble_scorer.py.
  async _runBubbleSkill(date) {
    await this._runSkill('us-market-bubble-detector', date, null, 15 * 60 * 1000, buildBubbleSkillAppendix(date));
  }
```

- [ ] **Step 5: Add the scheduled-trigger block in `_checkSchedule`**

Find the existing `regime_gate_compute` scheduled trigger in `_checkSchedule` (around line 758). Immediately BEFORE it, add:

```js
    // Daily upstream regime-gate skills. Run sequentially at 5:00 AM ET so the
    // four input JSONs are present by the time regime_gate_compute fires at
    // 5:50. macro_regime is fast (python); the other three are LLM-driven and
    // each gets a 15-min timeout. Worst-case the chain finishes by ~5:46 —
    // still inside the 5:50 deadline. If something stalls, regime_gate_compute
    // fail-softs (and the 36h input-freshness window covers yesterday's file).
    if (isWeekday && hour === 5 && minute === 0) {
      if (this._lastMacroRegimeDate !== isoDate) await this.triggerJob('macro_regime_skill').catch(() => {});
      if (this._lastBreadthDate !== isoDate)     await this.triggerJob('breadth_skill').catch(() => {});
      if (this._lastMarketTopDate !== isoDate)   await this.triggerJob('market_top_skill').catch(() => {});
      if (this._lastBubbleDate !== isoDate)      await this.triggerJob('bubble_skill').catch(() => {});
    }
```

- [ ] **Step 6: Add startup-heal step 1.4 in `runStartupChecks`**

Find the existing "1.5 Regime gate compute" block in `runStartupChecks` (around line 261). Immediately BEFORE it (i.e. inserting between step 1 daily-briefing and step 1.5 regime-gate), add:

```js
    // 1.4 Regime-gate upstream skills (state-based). Catches the case where the
    // bot was offline at the 5:00 AM ET trigger. Heals up to market close (4 PM
    // ET) so the four input JSONs are present when step 1.5 (regime_gate_compute)
    // fires next. Sequential so the LLM-driven ones don't fight over the
    // _activeJob mutex.
    if (isWeekday && hour < 16) {
      if (this._lastMacroRegimeDate !== isoDate) {
        if (await this._isLocked(this._getLockKey('macro_regime_skill', isoDate))) {
          this._log('macro_regime_skill already running in another process — skipping startup trigger.', 'info');
        } else {
          this._log('No macro_regime_skill for today — triggering now...', 'info');
          await this.triggerJob('macro_regime_skill').catch(() => {});
        }
      }
      if (this._lastBreadthDate !== isoDate) {
        if (await this._isLocked(this._getLockKey('breadth_skill', isoDate))) {
          this._log('breadth_skill already running in another process — skipping startup trigger.', 'info');
        } else {
          this._log('No breadth_skill for today — triggering now...', 'info');
          await this.triggerJob('breadth_skill').catch(() => {});
        }
      }
      if (this._lastMarketTopDate !== isoDate) {
        if (await this._isLocked(this._getLockKey('market_top_skill', isoDate))) {
          this._log('market_top_skill already running in another process — skipping startup trigger.', 'info');
        } else {
          this._log('No market_top_skill for today — triggering now...', 'info');
          await this.triggerJob('market_top_skill').catch(() => {});
        }
      }
      if (this._lastBubbleDate !== isoDate) {
        if (await this._isLocked(this._getLockKey('bubble_skill', isoDate))) {
          this._log('bubble_skill already running in another process — skipping startup trigger.', 'info');
        } else {
          this._log('No bubble_skill for today — triggering now...', 'info');
          await this.triggerJob('bubble_skill').catch(() => {});
        }
      }
    }
```

- [ ] **Step 7: Run all tests and verify nothing regressed**

Run: `node --test agent/analysis-scheduler.test.mjs`

Expected: all tests pass (originals + 7 new ones from Task 1).

- [ ] **Step 8: Sanity-check that the module still imports**

Run: `node -e "import('./agent/analysis-scheduler.js').then(m => console.log('exports:', Object.keys(m).join(',')))"`

Expected output includes: `buildRegimeComputeArgv,buildMacroRegimeArgv,buildBreadthSkillAppendix,buildMarketTopSkillAppendix,buildBubbleSkillAppendix,AnalysisScheduler` (order may vary).

---

## Task 4: Squash-commit

**Files:** scripts staged together.

- [ ] **Step 1: Inspect the diff**

Run: `git status` and `git diff agent/analysis-scheduler.js agent/analysis-scheduler.test.mjs`

Confirm only these two files changed. Pre-existing modifications already on the branch (`cmd/bot/main.go`, `config/config.go`, `config/config_test.go`, untracked plan/spec/activity files) should NOT be staged.

- [ ] **Step 2: Stage and commit**

```bash
git add agent/analysis-scheduler.js agent/analysis-scheduler.test.mjs
git commit -m "$(cat <<'EOF'
feat(scheduler): automate 4 upstream regime-gate skills

Add macro_regime_skill, breadth_skill, market_top_skill, and bubble_skill
to agent/analysis-scheduler.js so regime_gate_compute has fresh inputs each
pre-market. Hybrid invocation: macro_regime is a direct python spawn
(autonomous FMP fetch). breadth, market_top, and bubble are LLM-driven via
_runSkill + AUTOMATED RUN appendix because each needs an LLM in the loop
(data collection from WebSearch, manual scoring, or schema reshaping).

Scheduled at 5:00 AM ET weekdays, sequential. Startup heal runs all four
on bot start if missing for today (up to 4 PM ET cutoff) so the chain is
robust to overnight downtime and snapshot.ps1 sweeping data/reports/.

Tests: 7 new node:tests covering the 4 exported helpers, the new
getStatus surface, and the new lock-key cases.

Spec: docs/superpowers/specs/2026-05-15-regime-skills-scheduler-design.md
EOF
)"
```

- [ ] **Step 3: Confirm commit landed**

Run: `git log -1 --stat`

Expected: one commit, two files changed.

---

## Verification

- [ ] All `node --test agent/analysis-scheduler.test.mjs` tests pass (originals + 7 new).
- [ ] `node -e "import('./agent/analysis-scheduler.js').then(...)"` exports include all four new helpers + the existing `buildRegimeComputeArgv`.
- [ ] Single new commit on the current branch.
- [ ] Operator smoke-test the next morning: confirm scheduler logs four `Starting <skill>_skill` lines around 5:00 AM ET and that `data/reports/` contains fresh `macro_regime_*.json`, `breadth_<date>.json`, `market_top_*.json`, and `bubble_<date>.json` before 5:50 AM ET.
