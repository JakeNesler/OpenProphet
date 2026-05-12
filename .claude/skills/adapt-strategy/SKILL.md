---
name: adapt-strategy
description: Analyze recent trading performance across every sandbox running the Prophet agent (`default`), identify what rules are drifting or broken, and propose + apply targeted edits to whatever strategy that agent currently points at. This is the primary learning loop — run it weekly or after any bad stretch.
allowed-tools: Read Glob
---

You are closing the learning loop for the Prophet trading agent. Your job is to read what the agent actually did, compare it to what the strategy says it should do, find the gaps, and propose concrete rule changes — then apply the ones the user approves.

## Step 1 — Resolve target agent, strategy, and sandboxes

This skill targets the **`default`** agent (name "Prophet"). Sandboxes are resolved by agent — never by sandbox name. Activity from every sandbox running this agent is aggregated so the strategy is tuned against the full history.

1. Read `data/agent-config.json`.
2. In `agents[]`, find the entry with `id === 'default'` (fallback: `name` containing `"Prophet"` case-insensitive, excluding `"PennyProphet"` and `"TrendProphet"`). Take its `strategyId` — this is the strategy this skill will edit.
3. In `strategies[]`, find the entry with that `id`. Extract `id`, `name`, and the full `customRules` text. State the strategy name + id in one line before continuing — this is the ground truth you will be editing.
4. Iterate `sandboxes` (object map). Keep every entry whose `agent.activeAgentId === 'default'`. For each kept entry, record `(name, accountId)`. Call this list `<PROPHET_DIRS>`.
5. If `<PROPHET_DIRS>` is empty, stop and tell the user: "No sandbox currently uses agent `default`. Assign it to a sandbox first." Do not proceed.

State the resolved sandbox list (sandbox name → accountId directory) before continuing. Steps 3 and 4 below glob across **every** directory in `<PROPHET_DIRS>` and merge results.

## Step 3 — Load recent decisions (last 30 days, all Prophet sandboxes)

For each `<DIR>` in `<PROPHET_DIRS>`: glob `data/sandboxes/<DIR>/decisive_actions/*.json`. Merge all matched files into one list, sort by file mtime descending, read the **60 most recent overall** (not 60 per sandbox). For each, extract:
- `timestamp`
- `sandboxId` (record which directory it came from — useful for gap analysis if a pattern is sandbox-specific)
- `action` (BUY / SELL / HOLD / etc.)
- `symbol`
- `reasoning` (full text)

## Step 4 — Load recent P&L context (all Prophet sandboxes)

For each `<DIR>` in `<PROPHET_DIRS>`: glob `data/sandboxes/<DIR>/activity_logs/activity_*.json`. Read the **8 most recent per sandbox**. From each `summary`:
- winning_trades, losing_trades, total_pnl, largest_win, largest_loss
- Tag the row with its sandbox name

Compute aggregate profit factor across all loaded days from all sandboxes combined. Also note per-sandbox profit factor — large divergences (one sandbox profitable, another deeply red) are themselves a finding worth surfacing in Step 5.

## Step 5 — Gap analysis

For each section of the strategy rules, ask: does the agent's actual behavior match the rule?

Work through these categories:

**Entry discipline**
- Are positions being sized within 15%?
- Are scalps truly ≤5 DTE?
- Are swing positions in the 50–120 DTE / delta 0.40–0.70 band?
- Is the agent using limit orders? (Look for "limit" vs. absence of it in reasoning)

**Exit discipline**
- Are losers being cut at -15%? Or are stops being moved?
- Are scalps being closed EOD?
- Are profits being taken at +25–50%?

**Loss-review protocol**
- After a bad stretch, does the agent pause entries and run stats?
- Is it re-entering same symbols within 2 hours (revenge trading)?

**Position concentration**
- Any sector exceeding 40%?
- More than 10 simultaneous positions?

**Behavioral drift**
- Reasoning that sounds emotional ("hoping", "giving it more time", "should bounce")
- Thesis changes mid-hold without acknowledging the shift

For each gap you find, write:
> **Gap [N]**: [category] — [what the rule says] vs. [what the agent actually did, with timestamp and quote]

## Step 6 — Propose specific rule edits

For each significant gap (ignore one-offs; focus on patterns appearing 2+ times), propose a rule change using this format:

---
**Proposed Edit [N]** — [Category]

**Current rule:**
> [exact quote from customRules]

**Proposed replacement:**
> [your revised text]

**Rationale:** [1–2 sentences explaining what behavior this fixes and what evidence from the decision log supports it]

---

If a gap suggests adding a *new* rule rather than changing an existing one, say so explicitly and write the full new rule text.

## Step 7 — Present and confirm

Show the user all proposed edits clearly. Ask which ones to apply. Do not modify any file until the user confirms specific edits.

## Step 8 — Apply approved edits

For each approved edit:
1. Re-read `data/agent-config.json` to get the freshest version.
2. In the `strategies` array, find the entry by **the `id` you resolved in Step 1** (do NOT look up by name — names can drift, the id is the link from the agent to the strategy).
3. Edit `customRules` — replace the old rule text with the new rule text exactly as proposed. Preserve all surrounding content.
4. Update `updatedAt` on the strategy entry to now (ISO string).
5. Write the file back.

After all edits are applied, show the final diff of what changed in the strategy's `customRules`. Remind the user the changes take effect on the next heartbeat of **every** sandbox using agent `default` — all of them share this strategy.
