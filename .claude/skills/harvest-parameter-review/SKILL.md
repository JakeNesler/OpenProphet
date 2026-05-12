---
name: harvest-parameter-review
description: Monthly review of Harvest's iron-condor numerical parameters (target delta, DTE window, IVR threshold, profit-take %, stop %, deployed-buying-power cap, wing widths, position sizing) against the prior 90 days of realized condor outcomes. Mechanical agents drift in parameters, not in behavior — this is the parameter-tuning learning loop. Run monthly. Hard sample-size guard: needs ≥ 10 closed condors in the window or it exits without proposing changes.
allowed-tools: Read Glob Edit
---

You are running the monthly parameter review for the Harvest iron-condor agent. Harvest is mechanical — it does not have behavioral drift like a discretionary agent. What it has is **parameter staleness**: the IVR threshold, delta target, DTE window, profit-take, stop multiple, and deployed-BP cap may have been calibrated for a different vol regime than the one the agent has been operating in. Your job is to evaluate, with evidence, whether any of those numerical parameters should move.

You may NOT change the strategy structure. You may ONLY propose numerical edits. If the structure itself looks broken, escalate per Step 7.

## Step 1 — Load current parameters

Read `TRADING_RULES_HARVEST.md`. Extract the live values of the following parameters (these are the only knobs you may propose edits to):

| Parameter | Current location in rules | Current value |
|---|---|---|
| Target delta | Step 3 entry checks | 0.16 (tolerance [0.12, 0.20]) |
| DTE entry window | Step 3 expirations | [35, 55] |
| Time-exit DTE | Step 2 Priority 1 | ≤ 21 |
| IVR threshold (entry) | Step 3 IVR check | ≥ 30 |
| Profit target (close at) | Step 2 Priority 3 | cost ≤ 0.50× credit |
| Loss stop (close at) | Step 2 Priority 2 | cost ≥ 2.00× credit |
| Deployed-buying-power cap | Step 1 + Step 3 | 12.0% |
| Max open condors | Step 1 + Step 3 | 5 |
| Wing widths | Step 3 | SPY $5, QQQ $5, IWM $2, GLD $2, TLT $1 |
| Position-size factor | Step 3 contracts formula | 0.015 (1.5%) |
| Min-credit gate (wing fraction) | Step 3 credit check | wing_width / 3 |
| Min-credit floor ($) | Step 3 credit check | $0.30 |

Pull the actual numbers as they appear in the file today (the values above are the snapshot at skill-creation time — re-read on every run).

## Step 2 — Resolve sandboxes by agent and load 90 days of harvest activity

This skill aggregates condor history from **every** sandbox running the `harvest` agent — not by sandbox name, never by hardcoded ID.

1. Read `data/agent-config.json`. In `agents[]`, find `id === 'harvest'` (fallback: name matching `/harvest/i`).
2. Iterate `sandboxes`, keep every entry where `agent.activeAgentId === 'harvest'`. Collect their `accountId` values as `<HARVEST_DIRS>`. State the resolved list before continuing. If empty, stop and tell the user no sandbox currently uses the agent.

Then, for each `<DIR>` in `<HARVEST_DIRS>`:

a. Glob `data/sandboxes/<DIR>/activity_logs/activity_*.json`. Read every file with a date within the last 90 calendar days.
b. Glob `data/sandboxes/<DIR>/decisive_actions/*.json`. Read every file with a `timestamp` within the last 90 calendar days.
c. Read `data/sandboxes/<DIR>/prophet_trader.db` if you can — the `harvest_condors` table is the authoritative source for realized condor P&L. Per-row fields you care about (verify exact column names by inspecting the table schema first):
   - underlying, opened_at, closed_at, status (CLOSED is the cohort you analyze)
   - credit_received, cost_to_close, realized_pnl
   - dte_at_entry, dte_at_exit
   - short_put_delta_at_entry, short_call_delta_at_entry
   - ivr_at_entry
   - wing_width, contracts
   - exit_reason (which priority fired: time / loss / profit)

Tag every loaded condor row with the sandbox it came from. The closed-condor cohort and parameter analyses in Steps 3-5 are computed on the **merged** cohort across all `<HARVEST_DIRS>`. Per-sandbox breakdowns are reported only when a finding diverges by sandbox (e.g. one sandbox has all the IWM losers).

If the DB is unreadable for a sandbox, fall back to reconstructing that sandbox's condor history from `decisive_actions` `reasoning` + `market_data` fields (entries log credit/strikes/DTE/IVR; exits log cost-to-close and exit reason). DB is preferred — fall back only when necessary, and note in the report which sandbox(es) required a fallback.

## Step 3 — Sample-size guard (MANDATORY, NON-NEGOTIABLE)

Count CLOSED condors over the prior 90 days (status = CLOSED in the DB, or all condors with a logged close action in the decisive-action stream).

**If count < 10:**

Output the following block exactly and EXIT WITHOUT PROPOSING ANY CHANGES:

```
INSUFFICIENT_SAMPLE: <N> closed condors over 90d (need ≥10).

Parameter snapshot (for the historical record):
  target_delta:           <value>
  dte_entry_window:       <value>
  time_exit_dte:          <value>
  ivr_threshold:          <value>
  profit_target_multiple: <value>
  loss_stop_multiple:     <value>
  deployed_bp_cap_pct:    <value>
  max_open_condors:       <value>
  wing_widths:            <value>
  position_size_factor:   <value>
  min_credit_wing_frac:   <value>
  min_credit_floor:       <value>
  observed_closed_count:  <N>
  window_start:           <YYYY-MM-DD>
  window_end:             <YYYY-MM-DD>

Below 10 closed trades, win/loss-rate estimates are pure noise. No parameter
edits will be proposed this cycle. Re-run next month.
```

Then stop. Do not edit `TRADING_RULES_HARVEST.md`. Do not propose changes. The user has explicitly forbidden relaxing this threshold.

If count ≥ 10, proceed to Step 4.

## Step 4 — Per-parameter evidence

For each parameter, compute:

**Target delta (0.16) — vs. realized outcomes**
- Distribution of `short_put_delta_at_entry` and `short_call_delta_at_entry` across the cohort.
- Among CLOSED condors that hit the loss stop: what was the average entry delta? If higher than 0.16, that's a "wings too tight / too close to ATM" signal.
- Hit rate at profit target vs. at loss stop, broken out by entry delta tier (0.12–0.14 vs 0.14–0.18 vs 0.18–0.20).

**DTE entry window ([35, 55])**
- Distribution of `dte_at_entry`. Average days held = `dte_at_entry - dte_at_exit`.
- If the average held duration is materially shorter than the entry-DTE midpoint (e.g. 18 days held vs ~45 DTE entry midpoint), the time-exit at DTE ≤ 21 is firing earlier than the credit-decay curve probably warrants — flag this.
- Time-exit firings as a share of all closes vs. profit-target firings. If time-exits dominate and average realized P&L on time-exits is small (or negative), the DTE entry window may be too short, OR the time-exit DTE may need to move down.

**IVR threshold (≥ 30)**
- Distribution of `ivr_at_entry` for entries that closed at the profit target vs. those that hit the loss stop.
- If profit-target closures cluster at IVR > 50 and loss-stop closures cluster at IVR 30–40, the IVR floor may be too permissive (admitting too many low-vol-regime trades that produced thin credits).
- If IVR distribution at entry is consistently bunched right at 30–35 (and few entries pass at higher IVR), the threshold may be filtering nothing in the current regime — note that as a "non-binding constraint, no signal."

**Profit target (0.50× credit) and Loss stop (2.0× credit)**
- Hit rate: % of CLOSED condors that closed via profit target, via loss stop, via time exit. These three plus any "hard stop" forced closes should sum to ~100%.
- Ratio: a healthy short-vol book at 16Δ / 50%-profit / 2× stop should run ~70–85% profit-target closures historically. Materially below that range in the cohort is a red flag.
- Average dollar P&L per close-type. Compare profit-target $ won × profit count vs. loss-stop $ lost × loss count. Flag if the loss side's dollar magnitude is exceeding the profit side's despite a higher profit hit rate (indicates the 2× stop is letting individual losses run too far).

**Deployed-buying-power cap (12.0%) and Max open condors (5)**
- Time-series of `deployed_buying_power_pct` and `open_condors` across the window. Was the cap ever the binding constraint? How often (% of heartbeats) was deployed_pct ≥ 10%?
- If the cap was rarely the binding constraint (e.g. always < 8%), the cap is non-binding — no signal, do not propose changing it.
- If the cap was binding frequently and entries were being skipped because of it, that's evidence the agent is finding more setups than the budget allows — note as a sizing/cap question for the user.

**Wing widths and position-size factor (0.015)**
- Realized P&L variance per ticker. If a single ticker (e.g. IWM) shows variance materially different from its peers relative to its wing width, the wing width may need adjustment.
- Position-size factor: changes here have a portfolio-risk impact and should not be proposed without strong evidence of under- or over-sizing across multiple months. Default = no change.

**Min-credit gates (wing/3 and $0.30 floor)**
- How many entries were skipped for "credit below minimum"? If a meaningful number, are the skipped entries ones the user would have wanted (i.e. were they post-hoc winners)? You can't know the counterfactual perfectly, but you can note that {N} entries were filtered by the credit gate over the window.

## Step 5 — IVR-at-entry vs. realized-vol relationship

For each CLOSED condor, compute (or extract from the DB if logged) the realized vol over the holding period of the underlying. Plot conceptually: x = `ivr_at_entry`, y = realized P&L per contract.

If high-IVR entries are systematically realizing high P&L and low-IVR entries are systematically realizing zero or negative P&L, that's signal to raise the IVR threshold. If there's no relationship, the threshold is probably fine where it is. State the qualitative pattern in the report.

If realized vol is not directly computable, use a proxy (the `cost_to_close / credit` trajectory across the holding period, or post-hoc daily underlying returns from a market-data tool you have available).

## Step 6 — Propose specific parameter edits (only when warranted)

Translate evidence into specific numerical proposals. Use the format from `adapt-strategy`:

---
**Proposed Parameter Edit [N]** — [Parameter name]

**Current rule text:**
> [exact quote from `TRADING_RULES_HARVEST.md`]

**Proposed replacement text:**
> [your revised text — must be a numerical change to the existing rule, not a structural change]

**Evidence:** [Specific stats from Step 4 — hit rate, avg P&L, sample count. Must cite at least one number from the cohort. No vibes-based proposals.]

**Predicted effect:** [What you expect this change to do — e.g. "raises IVR floor; expected to reduce entry count by ~30% and improve avg credit per entry by ~40%, based on the IVR-tier breakdown above."]

---

Examples of legitimate proposals (illustrative):
- "Raise IVR threshold from 30 → 40 (loss-stop firings clustered at IVR 30–40 with only 18 of 47 closures hitting target; raising the floor would have excluded 12 of those 18 losers based on entry-IVR distribution)."
- "Widen wings: IWM from $2 → $3 (IWM stop hit rate was 45% over the cohort vs 18% for SPY/QQQ — likely too tight for IWM's vol regime in the window)."
- "Lower DTE entry midpoint: change [35, 55] → [30, 50] (average days-held was 18 vs ~45 DTE midpoint entry; time-exit firings were 60% of all closures with avg realized P&L ~breakeven — closer-DTE entries would harvest the same credit decay over a shorter capital lock-up)."
- "Tighten loss stop: 2.0× credit → 1.75× credit (loss-stop closures averaged $1.85 lost per contract vs profit-target $0.78 won; the dollar asymmetry is wider than the win-rate asymmetry compensates for)."

Examples of proposals you must NOT make (these are structural, not parametric):
- Adding a new exit priority
- Adding new universe tickers (universe is structural — escalate per Step 7)
- Changing the order of Step 1/2/3 in the heartbeat
- Adding a new tool call
- Adding adjustment / rolling logic (the rules explicitly forbid this)

If your evidence points at a structural problem rather than a parameter problem, do NOT propose a parameter edit. Go to Step 7.

## Step 7 — Structural escalation (do NOT silently change structure)

If the cohort evidence suggests the strategy structure itself is broken — for example:
- The win rate is below 50% across all parameter tiers (something more fundamental than parameter tuning is wrong)
- A specific underlying is consistently producing losses regardless of delta/IVR (suggests universe should be revisited)
- Time exits are dominating closures and the credit-decay curve looks structurally adverse (suggests the strategy itself isn't capturing premium efficiently in this regime)
- Multiple parameter changes that, taken together, would amount to a different strategy

Do NOT propose parametric edits to paper over structural issues. Instead, write a file `STRUCTURAL_REVIEW_NEEDED.md` at the repo root with:

```
# Harvest Structural Review Needed

Triggered by: harvest-parameter-review on <YYYY-MM-DD>
Window: <90-day window>
Sample size: <N closed condors>

## What the data shows

[Bullet list of the structural-looking findings.]

## Why this is structural, not parametric

[Why no combination of legal numerical edits would address what the data shows.]

## Recommended operator decisions

[1–3 specific questions the operator needs to answer before any parameter changes are made — e.g., "should IWM stay in the universe?", "should the agent take directional skews?", etc.]
```

Then ask the user explicitly whether they want to discuss structural changes. Do not edit `TRADING_RULES_HARVEST.md` until that conversation has happened.

## Step 8 — Present and confirm

Show the user all proposed parameter edits clearly (or the structural-escalation report if Step 7 fired). Ask which numerical edits to apply. Do not modify any file until the user confirms specific edits.

## Step 9 — Apply approved edits

For each approved numerical edit:
1. Re-read `TRADING_RULES_HARVEST.md` to get the freshest version.
2. Use Edit to replace the old rule text with the new rule text exactly as proposed. Preserve all surrounding content.
3. After all edits, show the final diff of what changed.

Remind the user the changes take effect on Harvest's next heartbeat — no agent restart required, since Harvest reads `TRADING_RULES_HARVEST.md` from disk (verify by checking the active strategy's `rulesFile` reference; if the strategy was recently migrated to inline `customRules` in `data/agent-config.json`, edit there instead and update the strategy `updatedAt`).

Note: do NOT touch the universe, the heartbeat-step structure, the hard-stops list, or the "What You Do Not Do" list. Those are structural. Parameters only.
