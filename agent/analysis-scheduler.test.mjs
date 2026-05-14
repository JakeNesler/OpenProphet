// Tests for the regime_gate_compute scheduler hook. Only exercises the pure
// argv builder — the spawn side is integration territory and would couple
// the test to platform-specific Python launching.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import path from 'path';

import { buildRegimeComputeArgv } from './analysis-scheduler.js';

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
