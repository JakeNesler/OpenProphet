// Tests for the regime_gate_compute scheduler hook. Only exercises the pure
// argv builder — the spawn side is integration territory and would couple
// the test to platform-specific Python launching.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import path from 'path';

import {
  buildRegimeComputeArgv,
  buildMacroRegimeArgv,
  buildBreadthSkillAppendix,
  buildMarketTopSkillAppendix,
  buildBubbleSkillAppendix,
  AnalysisScheduler,
} from './analysis-scheduler.js';

// Path separators differ across platforms (\ on Windows, / on POSIX).
// Use path.join in expectations so this suite passes on both.
const p = (...parts) => path.join(...parts);

test('buildRegimeComputeArgv: picks latest of each prefix lexicographically', () => {
  // Skills emit timestamped filenames (e.g., macro_regime_2026-05-14_083000.json).
  // Lexicographic order matches chronological order for these formats, so the
  // last-sorted file is the latest. No mtime stats needed (cheaper, deterministic).
  const files = [
    'macro_regime_2026-05-13_083000.json',
    'macro_regime_2026-05-14_083000.json',
    'market_top_20260513.json',
    'market_top_20260514.json',
    'breadth_20260514.json',
    'bubble_20260514.json',
    'daily_brief_20260514.json',   // unrelated file — must be ignored
    'random_other.json',           // unrelated file — must be ignored
  ];
  const argv = buildRegimeComputeArgv(
    'data/reports',
    'scripts/compute_daily_regime_score.py',
    'data/reports/regime_gate.json',
    files,
  );

  // Script path is the first non-flag argument; node:child_process.spawn
  // receives this plus the flags.
  assert.equal(argv[0], 'scripts/compute_daily_regime_score.py');

  // Output flag is always present.
  const outIdx = argv.indexOf('--output');
  assert.notEqual(outIdx, -1, 'argv must include --output');
  assert.equal(argv[outIdx + 1], 'data/reports/regime_gate.json');

  // Each input flag must point to the LATEST file of that prefix.
  function valueOf(flag) {
    const i = argv.indexOf(flag);
    return i === -1 ? null : argv[i + 1];
  }
  assert.equal(valueOf('--breadth'), p('data/reports', 'breadth_20260514.json'));
  assert.equal(valueOf('--macro'), p('data/reports', 'macro_regime_2026-05-14_083000.json'));
  assert.equal(valueOf('--top'), p('data/reports', 'market_top_20260514.json'));
  assert.equal(valueOf('--bubble'), p('data/reports', 'bubble_20260514.json'));
});

test('buildRegimeComputeArgv: omits flags for prefixes with no matching files', () => {
  // The Python script fails soft on absent inputs (neutral 50). The scheduler
  // mirrors that by simply not passing the flag, rather than passing a fake
  // path that would emit a "file not found" warning to stderr.
  const files = [
    'macro_regime_2026-05-14_083000.json',
    'daily_brief_20260514.json',
  ];
  const argv = buildRegimeComputeArgv(
    'data/reports',
    'scripts/compute_daily_regime_score.py',
    '/tmp/regime_gate.json',
    files,
  );

  assert.equal(argv.includes('--breadth'), false, '--breadth flag must be absent when no breadth file');
  assert.equal(argv.includes('--top'), false);
  assert.equal(argv.includes('--bubble'), false);
  assert.equal(argv.includes('--macro'), true, '--macro must be present when a macro file exists');
});

test('buildRegimeComputeArgv: works with empty file list', () => {
  // On a fresh install with no reports yet, the script still runs (writes
  // neutral 50 across all components). Verify argv is well-formed in that
  // degenerate case.
  const argv = buildRegimeComputeArgv(
    'data/reports',
    'scripts/compute_daily_regime_score.py',
    '/tmp/regime_gate.json',
    [],
  );
  assert.equal(argv[0], 'scripts/compute_daily_regime_score.py');
  assert.deepEqual(
    argv.filter((v) => v.startsWith('--')),
    ['--output'],
  );
});

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
