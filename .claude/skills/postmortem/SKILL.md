---
name: postmortem
description: Deep-dive post-mortem on a specific losing trade, bad day, or symbol. Pass a symbol (e.g. /postmortem QQQ), a date (e.g. /postmortem 2026-04-23), or leave blank for the most recent losing trade. Extracts exactly what went wrong and what rule change would prevent it.
allowed-tools: Read Glob
---

You are performing a surgical post-mortem on a specific trade or session. The goal is a precise, evidence-based lesson — not a vague "be more disciplined" takeaway.

**Input:** `$ARGUMENTS` — may be a ticker symbol, a date (YYYY-MM-DD), or empty.

## Step 0 — Resolve sandboxes by agent (do this first, before Step 1)

This skill operates over **every** sandbox running the `default` agent (name "Prophet") — not by sandbox name, never by hardcoded ID.

1. Read `data/agent-config.json`.
2. In `agents[]`, find `id === 'default'` (fallback: name containing `"Prophet"`, excluding `"PennyProphet"` and `"TrendProphet"`).
3. Iterate `sandboxes`, keep every entry where `agent.activeAgentId === 'default'`. Collect their `accountId` values as `<PROPHET_DIRS>`. If empty, stop and tell the user no sandbox currently uses the agent.
4. State the resolved list before continuing.

Every glob below must be run once **per** `<DIR>` in `<PROPHET_DIRS>` and the results merged. Tag each loaded record with its sandbox.

## Step 1 — Identify the subject

- If `$ARGUMENTS` contains a ticker symbol (all-caps, 1–5 letters like QQQ, TSLA, SPY): across every `<DIR>` in `<PROPHET_DIRS>`, glob `data/sandboxes/<DIR>/decisive_actions/*.json` and find all decisive actions where `symbol` matches. Merge and sort by timestamp.
- If `$ARGUMENTS` is a date (YYYY-MM-DD): across every `<DIR>`, find all decisive actions from that date plus the activity log for that date.
- If `$ARGUMENTS` is empty: across every `<DIR>`, glob `data/sandboxes/<DIR>/decisive_actions/*.json`, merge, sort by mtime descending, read the **30 most recent overall**, and find the most recent SELL action where `reasoning` contains a loss signal (words like "stop", "loss", "cut", "-15%", "down", "deteriorat"). Use that trade as the subject — note which sandbox it came from in the report.

Load all relevant files. For a symbol search, also load the activity logs from the same date range (across all `<PROPHET_DIRS>`) to get portfolio context.

## Step 2 — Reconstruct the trade timeline

Build a chronological timeline of every decision logged for this trade:

| Time | Action | Key reasoning (50-word excerpt) |
|---|---|---|
| ... | BUY | ... |
| ... | HOLD | ... |
| ... | SELL | ... |

Note: entry price, strike, DTE at entry. Exit price, DTE at exit. Estimated P&L from reasoning text.

## Step 3 — Strategy compliance check

Go through the timeline and check each decision against the active strategy rules. Resolve the strategy by following the agent linkage: in `data/agent-config.json`, look up agent `default` and take its `strategyId`, then read that strategy's `customRules` from `strategies[]`. State which strategy you resolved (name + id) before quoting rules.

For the **entry**:
- Was position size within 15%?
- Was DTE in the right range for the trade type (scalp vs. swing)?
- Was there a clear thesis stated?
- Were there red flags in the macro environment that the entry ignored?

For each **hold** decision:
- Was the position within stop parameters (-15%)?
- Was the reasoning still tied to the original thesis or was it drifting ("hope it bounces")?
- Did the loss-review protocol trigger (was portfolio down >3.5%)? If so, was it followed?

For the **exit**:
- Was the stop respected promptly or was it delayed?
- If a stop was missed, how far past -15% did it go before cutting?
- If a profitable trade reversed into a loss: was a partial exit taken at +25%?

## Step 4 — Root cause

Identify the **single root cause** of the loss. Choose one:
- **Bad entry** — entered into unfavorable setup, wrong DTE, wrong delta, ignored macro
- **Stop discipline failure** — held past -15% or moved the stop
- **No partial exit** — rode a winner back to breakeven or loss
- **Revenge trade** — re-entered too soon after a prior loss on same symbol
- **Concentration risk** — too much capital in one position or sector
- **External shock** — genuine black swan; setup was sound, outcome was bad luck
- **Thesis drift** — held while thesis silently changed without acknowledging it

State the root cause clearly. Support it with 2–3 direct quotes from the `reasoning` fields.

## Step 5 — Specific lesson and rule fix

State the lesson in one sentence, then write the **exact rule text** that would have prevented this loss — either a new rule or a modification to an existing one.

Use this format:

---
**Lesson:** [One sentence.]

**Current rule (if applicable):**
> [exact quote from customRules, or "no rule currently covers this"]

**Rule that would have prevented this:**
> [your proposed rule text]

**To apply this fix:** Run `/adapt-strategy` — it will find this pattern in the decision log and propose the edit formally.
---

## Step 6 — Pattern check

For each `<DIR>` in `<PROPHET_DIRS>`: glob `data/sandboxes/<DIR>/decisive_actions/*.json`. Merge across sandboxes, read the **60 most recent overall**. Search for any other decisions on the same symbol or with similar reasoning language (same error words: "giving it more time", "hope", "approaching stop", etc.).

Report: is this an isolated incident or a recurring pattern? If it has happened before, list the prior dates **and which sandbox each occurred in** — a pattern that spans sandboxes is a strategy issue; one confined to a single sandbox may point at a per-sandbox config drift instead.
