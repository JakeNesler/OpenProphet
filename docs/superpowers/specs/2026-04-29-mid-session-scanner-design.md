---
name: Mid-Session Market Intelligence Scanner
description: Design for a centralized mid-session news scanner that detects breaking market events and delivers emergency heartbeat interrupts to all trading agents
type: project
---

# Mid-Session Market Intelligence Scanner — Design

**Date:** 2026-04-29  
**Status:** Approved

## Problem

Trading agents were blind to intra-day news events. The daily briefing (6 AM ET) was the only structured news read, leaving a gap of 6–10+ hours during live trading. The OpenAI revenue-miss incident (April 28, 2026) — which dragged Oracle -5%, SoftBank -10%, AMD -3%, Broadcom -4% — was not seen by any agent because: (a) the news tools existed but weren't called on heartbeat, (b) there was no automated mid-session polling, and (c) no push mechanism existed to interrupt an agent's sleep cycle.

## Goals

1. Detect market-moving news every 15 minutes during market hours (9:30–4:00 PM ET)
2. Deliver breaking alerts as emergency heartbeats to all running agents with no manual intervention
3. Preserve existing per-agent heartbeat logic — only interrupt when genuinely warranted (score ≥ 7/10)
4. Prevent alert stacking on volatile days via single-slot emergency queue

## Architecture

Four components, all wired at server startup:

### 1. `AnalysisScheduler._runMidSessionScan()` — Scanner

- `setInterval` every 15 minutes, fires only during market hours (weekday, 9:30–16:00 ET)
- Skips if previous scan still running (`_scanActive` guard)
- Spawns a one-shot opencode subprocess with the scanner prompt
- Scanner calls `get_marketwatch_bulletins` + `get_marketwatch_realtime`, scores significance 1–10
- If score ≥ 7: writes `data/reports/market_alert_YYYYMMDD_HHMMSS.json`
- Parent process detects the new file post-subprocess and calls `onEmergencyWake(alert_summary)`

**Significance scoring:**
- 8–10: Systemic (FOMC surprise, index move >1%, banking crisis, major war escalation, large-cap earnings miss with sector contagion)
- 6–7: Cross-asset (single sector >3%, large-cap earnings, commodity spike >3%)
- 4–5: Notable (moderate earnings, in-line econ data)
- 1–3: Routine — no file written

### 2. `AgentHarness.emergencyWake(reason)` — Interrupt Mechanism

```
emergencyWake(reason):
  if not running or paused: ignore
  _emergencyQueued = reason   // single slot — replace, don't append
  if _beating: return         // current beat will see it after finishing
  cancel _timer
  setImmediate(_runBeatCycle) // fire at delay=0
```

`_runBeatCycle()` is extracted from `_scheduleNext()`'s callback. It is the single shared path for both normal and emergency beats:
- Drains `_emergencyQueued` → `_emergencyReason` before calling `_beat()`
- Clears `_emergencyReason` after `_beat()` returns
- If a new emergency was queued during the beat, schedules next cycle immediately; else calls `_scheduleNext()` normally

`_beat()` prefixes the heartbeat prompt with emergency context when `_emergencyReason` is set:
> [EMERGENCY ALERT] The mid-session scanner detected a breaking development: {reason}. Review before routine duties and assess whether immediate action is required.

### 3. `AgentOrchestrator.triggerEmergencyHeartbeat(reason)` — Fan-out

Iterates `this.runtimes`, skips any harness that is stopped or paused, calls `harness.emergencyWake(reason)` on each.

### 4. `server.js` wiring — Callback Bridge

```javascript
const scheduler = new AnalysisScheduler({
  model: ...,
  onEmergencyWake: (reason) => {
    if (harness?.state?.running && !harness?.state?.paused) harness.emergencyWake(reason);
    orchestrator.triggerEmergencyHeartbeat(reason);
  },
});
```

This covers both harness tracks: the standalone active `harness` variable AND all `orchestrator.runtimes`.

## Data Flow

```
MarketWatch RSS → scanner subprocess → significance score
  score < 7: no-op
  score ≥ 7: write market_alert_*.json
             → onEmergencyWake(summary)
               → harness.emergencyWake()    (active sandbox)
               → orchestrator.triggerEmergencyHeartbeat()
                 → each runtime harness.emergencyWake()
                   → _runBeatCycle() at delay=0
                     → _beat() with [EMERGENCY ALERT] prefix
```

## Alert File Format

`data/reports/market_alert_YYYYMMDD_HHMMSS.json`

```json
{
  "generated_at": "<UTC ISO>",
  "significance_score": 8,
  "alert_summary": "OpenAI Q1 revenue missed consensus by 15%; Oracle, AMD, Broadcom showing pre-market weakness due to AI infrastructure spending concern.",
  "affected_tickers": ["ORCL", "AMD", "AVGO"],
  "affected_sectors": ["Technology", "Semiconductors"],
  "direction": "bearish",
  "headlines": [{"headline": "...", "source": "MarketWatch", "impact": "..."}]
}
```

Readable via `read_latest_report("market_alert")` — the `market_alert_` prefix is added to the `prefixMap` in `mcp-server.js`.

## DTE-Aware Context

The scanner produces a single alert regardless of agent type. Each agent interprets urgency relative to its own open positions:
- Penny/scalp agents (2–5 DTE): treat any score ≥ 7 alert as requiring immediate reassessment
- Swing agents (50–120 DTE): use alert to reassess macro thesis at next heartbeat; intra-day price blips generally don't require closing early

## Known Limitations (v1)

- No position-specific routing: all alerts go to all agents; each agent decides relevance
- No cross-scan deduplication: the same story may trigger alerts on two consecutive 15-min scans (mitigated by single-slot queue — second scan replaces first)
- Fixed 15-min interval regardless of phase — future work could shorten interval at open/close
