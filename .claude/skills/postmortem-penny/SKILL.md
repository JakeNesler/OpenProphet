---
name: postmortem-penny
description: Deep-dive post-mortem on a specific PennyProphet losing trade, bad day, or symbol. Pass a symbol (e.g. /postmortem-penny ABCD), a date (e.g. /postmortem-penny 2026-04-23), or leave blank for the most recent losing penny trade. Extracts exactly what went wrong and what penny-momentum rule change would prevent it.
allowed-tools: Read Glob
---

You are performing a surgical post-mortem on a specific PennyProphet trade or session. The goal is a precise, evidence-based lesson — not a vague "be more disciplined" takeaway.

**Input:** `$ARGUMENTS` — may be a ticker symbol, a date (YYYY-MM-DD), or empty.

## Step 1 — Identify the subject

- If `$ARGUMENTS` contains a ticker symbol (all-caps, 1–5 letters): find all decisive actions where `symbol` matches, sorted by timestamp.
- If `$ARGUMENTS` is a date (YYYY-MM-DD): find all decisive actions from that date, and the activity log for that date.
- If `$ARGUMENTS` is empty: glob `data/sandboxes/a788a4e3/decisive_actions/*.json`, read the 50 most recent, find the most recent SELL or CIRCUIT_BREAKER action where `reasoning` contains a loss signal (words like "stop", "circuit breaker", "score dropped", "social fade", "loss", "cut", "−5%", "−7%", "−8%", "−10%", "down", "deteriorat", "20-minute", "time exit"). Use that trade as the subject.

Load all relevant files. For a symbol search, also load the activity logs from the same date range to get portfolio context.

## Step 2 — Reconstruct the trade timeline

Build a chronological timeline of every decision logged for this trade:

| Time | Action | Key reasoning (50-word excerpt) |
|---|---|---|
| ... | BUY | ... |
| ... | HOLD/MANAGE | ... |
| ... | SELL/STOP/TIME_EXIT | ... |

Note: entry price, composite score at entry, dominant signal type, position size %, stop %, target %. Exit price, time-in-position, estimated P&L from reasoning text or details fields.

## Step 3 — Strategy compliance check

Go through the timeline and check each decision against the active strategy rules (read `data/agent-config.json`, find strategy with id `penny-momentum`, use its `customRules`):

For the **entry**:
- Was composite score ≥ 60? Was the score-tier sizing rule respected (5–7% for ≥80, 2–3% for 60–79, hard cap 8%)?
- Was the dominant signal type identified, and was the stop/target set per the signal-type rules?
  - social: −8% stop, +15%/+20% target
  - regulatory: −10% stop, +20% day 1
  - technical: −7% stop, +14% target, breakeven trail at +7%
- Was `place_managed_position` used with stop and target pre-set? (Required — no entries without bracket protection.)
- For social entries, was there ≥30 minutes to market close? (Forbidden inside the last 30 min.)
- Was the segment cap (30% deployed) respected at entry time?

For each **hold / manage** decision:
- Was the position still within the signal-typed stop?
- For social: was the 20-minute time-window respected, or did the agent hold past it?
- Was the reasoning still tied to the rule ("bracket active, time remaining"), or was it improvising ("position looks like it might recover", "score still strong")?
- Did the daily circuit breaker trigger (portfolio P&L ≤ −5%)? If so, was it followed?

For the **exit**:
- For a stop hit: was the bracket the executor (clean), or did the agent intervene?
- For a social time-exit: was the cancel-bracket-then-market-sell protocol followed cleanly within the 20-min window?
- For a circuit breaker close: were ALL penny positions closed and was new-entry evaluation halted for the rest of the session?
- If a profitable trade reversed into a loss: was the breakeven-trail rule (technical) or partial-scale rule (social +15%) honored?

## Step 4 — Root cause

Identify the **single root cause** of the loss. Choose one:
- **Bad entry** — sub-60 score override, wrong dominant-signal classification, segment cap breach, social entry too close to close
- **Wrong stop/target for signal** — applied a different signal type's stop/target than the dominant signal warranted
- **Stop discipline failure** — bracket cancelled or moved, held past signal-typed stop
- **Social time-window failure** — held a social position past the 20-minute window
- **Bracket protection failure** — entered without `place_managed_position`, or entered after a bracket rejection
- **Circuit breaker ignored** — new entries after −5% intraday trip, or positions not flat-closed
- **Score / signal fade** — composite or dominant signal decayed below threshold mid-hold without exit
- **Re-entry on faded signal** — re-entered the same ticker too soon after a stop
- **External shock** — genuine black swan; setup was sound, outcome was bad luck
- **Improvisation drift** — reasoning explicitly overrode a rule with free-form judgment

State the root cause clearly. Support it with 2–3 direct quotes from the `reasoning` fields.

## Step 5 — Specific lesson and rule fix

State the lesson in one sentence, then write the **exact rule text** that would have prevented this loss — either a new rule or a modification to an existing one in `customRules` for `penny-momentum`.

Use this format:

---
**Lesson:** [One sentence.]

**Current rule (if applicable):**
> [exact quote from customRules, or "no rule currently covers this"]

**Rule that would have prevented this:**
> [your proposed rule text]

**To apply this fix:** Run `/adapt-strategy-penny` — it will find this pattern in the decision log and propose the edit formally.
---

## Step 6 — Pattern check

Glob `data/sandboxes/a788a4e3/decisive_actions/*.json`. Read the 80 most recent. Search for any other decisions on the same symbol or with similar reasoning language (same error words: "stop", "circuit breaker", "score dropped", "social fade", "might recover", "20-minute", "still strong"). Also search by `dominant_signal` in details to see if this is a signal-type-specific failure pattern (e.g. all social-time-window violations, or all sub-60 entries).

Report: is this an isolated incident or a recurring pattern? If it has happened before, list the prior dates and (where possible) the dominant signal involved.
