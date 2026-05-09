---
name: adapt-strategy-penny
description: Analyze recent PennyProphet trading performance, identify what penny-momentum rules are drifting or broken, and propose + apply targeted edits to the Penny Stock Momentum strategy. This is the primary learning loop for PennyProphet — run it weekly or after any bad stretch.
allowed-tools: Read Glob
---

You are closing the learning loop for the PennyProphet trading agent. Your job is to read what the agent actually did, compare it to what the strategy says it should do, find the gaps, and propose concrete rule changes — then apply the ones the user approves.

## Step 1 — Load current strategy

Read `data/agent-config.json`. Find the strategy with id `penny-momentum` (name `Penny Stock Momentum`) and extract its full `customRules` text. This is the ground truth you will be editing.

Also note: the `id` of this strategy is `penny-momentum` (you will need it if applying changes).

## Step 2 — Load recent decisions (last 30 days)

Glob `data/sandboxes/a788a4e3/decisive_actions/*.json`. Read the 80 most recent files. For each, extract:
- `timestamp`
- `action` (BUY / SELL / HOLD / SKIP / CIRCUIT_BREAKER / etc.)
- `symbol`
- `reasoning` (full text)
- Any `details` fields containing `composite_score`, `dominant_signal`, `position_size_pct`, `stop_pct`, `target_pct`

Penny generates more decisions per day than Prophet, so 80 files typically covers ~2–4 weeks of activity.

## Step 3 — Load recent P&L context

Glob `data/sandboxes/a788a4e3/activity_logs/activity_*.json`. Read the 7 most recent. From each `summary`:
- winning_trades, losing_trades, total_pnl, largest_win, largest_loss
- capital_deployed (segment-cap utilization)
- positions_opened, positions_closed

Compute aggregate profit factor across all loaded days.

## Step 4 — Gap analysis

For each section of the strategy rules, ask: does the agent's actual behavior match the rule?

Work through these penny-specific categories:

**Composite-score discipline**
- Are entries gated at composite score ≥ 60? Look for any BUY where `composite_score` in details is < 60 or unstated.
- Are sub-60 candidates being silently entered ("score was 58 but momentum looked strong")?

**Tiered position sizing**
- Score ≥ 80: position size 5–7% of portfolio?
- Score 60–79: position size 2–3% of portfolio?
- Hard cap 8% of portfolio in any single penny position — any breaches?
- Are tier boundaries being respected, or is sizing converging to a single default size regardless of score?

**`place_managed_position` usage with stop+target pre-set**
- Every entry must use `place_managed_position` with both stop and target. Search for any entries that used `place_order` / market entry without bracket protection, or where the bracket failed and the agent still entered.

**Signal-type-correct stops/targets**
Read `dominant_signal` from each entry's details and confirm the stop/target match:
- `social`: stop −8%, target +15% (50% scale) then +20% (remainder)
- `regulatory`: stop −10%, target +20% day 1, trailing from day 2
- `technical`: stop −7%, target +14% (1R), trail to breakeven at +7%

Flag any entry where stop or target deviates from the rule for that dominant signal.

**Social-signal time-window discipline**
- Social entries must be exited within 20 minutes of entry (or 15 min before close, whichever is first), per the cancel-bracket-then-market-sell protocol.
- Are any social positions being held past the 20-minute window?
- Are social entries being placed within 30 minutes of market close (forbidden)?

**Daily circuit breaker enforcement**
- Was portfolio P&L ≤ −5% intraday on any logged day? If so, did the agent cancel brackets, market-sell all penny positions, and cease new entries for the rest of the session?
- Any entries after a circuit-breaker trip?

**Segment cap (30% of portfolio in penny)**
- Was capital_deployed_in_penny > 30% at any point? If so, were further entries skipped?
- Are entries being skipped with `segment_cap_reached` reasoning, or is the cap being breached?

**Position concentration / count**
- More than 10 simultaneous penny positions?
- Multiple positions on the same ticker (correlated re-entry)?

**Behavioral drift / improvisation**
PennyProphet's rules explicitly forbid "helpful improvisation". Look for:
- Reasoning that overrides exit rules ("position looks like it might recover")
- Reasoning that overrides entry filters ("score is 58 but candidate looks promising")
- Suggestions about its own rules during a session
- Free-form market commentary that goes beyond logging the rule applied

For each gap you find, write:
> **Gap [N]**: [category] — [what the rule says] vs. [what the agent actually did, with timestamp and quote]

## Step 5 — Propose specific rule edits

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

## Step 6 — Present and confirm

Show the user all proposed edits clearly. Ask which ones to apply. Do not modify any file until the user confirms specific edits.

## Step 7 — Apply approved edits

For each approved edit:
1. Re-read `data/agent-config.json` to get the freshest version.
2. In the `strategies` array, find the entry with id `penny-momentum`.
3. Edit `customRules` — replace the old rule text with the new rule text exactly as proposed. Preserve all surrounding content.
4. Update `updatedAt` on the strategy entry to now (ISO string).
5. Write the file back.

After all edits are applied, show the final diff of what changed in the strategy's `customRules`. Remind the user the changes take effect on the agent's next heartbeat.

Note: `TRADING_RULES_PENNY.md` is a stale read-only mirror with a deprecation header. Do NOT edit it — the inline `customRules` in `data/agent-config.json` is the live source of truth.
