---
name: trend-parameter-review
description: Quarterly review of TrendProphet's Donchian-trend numerical parameters (Donchian-100 entry length, Donchian-50 trail length, ATR-20 floor, SMA-200 regime filter, initial-hard-stop multiple, segment cap, position-size factor, max open positions, aggregate risk cap, daily circuit breaker) plus per-ticker performance for TLT/GLD/USO/DBC/UUP/EEM against the prior 6 months of completed entry/exit pairs. Run quarterly. Hard sample-size guard: needs ≥ 6 completed trades in the window or it exits without proposing changes.
allowed-tools: Read Glob Edit
---

You are running the quarterly parameter review for the TrendProphet agent. TrendProphet is mechanical, long-only, and very low-frequency — a single ticker can sit in cash for months between breakouts. Three months of data may show fewer than five completed trades, which is why this review runs quarterly with a 6-month look-back, not weekly with a 30-day window.

Your job is to evaluate, with evidence, whether any of the strategy's numerical parameters should move and whether any individual ticker should be removed from the universe (a parametric universe-trim is in scope; adding new tickers is structural and out of scope — escalate per Step 8).

You may NOT change the strategy structure (long-only, ETF-only, daily-bar, Donchian+SMA-only signal, persisted-ledger architecture, heartbeat scheduling structure, cold-start mechanism). Only numerical parameters and universe-trim decisions are in scope.

## Step 1 — Load current parameters

Read `TRADING_RULES_TREND.md`. Extract live values for the parameters below (these are the only knobs you may propose edits to). The values shown are the snapshot at skill-creation time — re-read on every run.

| Parameter | Current location in rules | Current value |
|---|---|---|
| Donchian entry length | Glossary + Signal Definitions | 100 |
| Donchian trail length | Glossary + Exit signals | 50 |
| ATR-20 vol floor | Entry signal | 0.005 (0.5%) |
| SMA regime length | Entry signal | 200 |
| Initial-hard-stop ATR multiple | Position Sizing | 2 |
| Initial-hard-stop active window | Glossary + Exit signals | 20 trading days |
| Risk-per-position | Position Sizing | 0.5% of portfolio |
| Max position size cap | Position Sizing | 4% of portfolio |
| Segment-deployed cap | Risk Management | 18% of portfolio |
| Max open trend positions | Risk Management | 5 |
| Aggregate trend-risk cap | Risk Management | 2.5% of portfolio |
| Daily circuit breaker | Risk Management | trend-segment P&L ≤ −2% |
| Limit-order entry cushion | Heartbeat Step 3 | last_close × 1.005 |
| Cold-start proximity filter | Cold Start Behavior | ≤ 1 ATR (one-time, structural — do not propose) |
| Universe | Universe table | TLT, GLD, USO, DBC, UUP, EEM |

## Step 2 — Load 6 months of trend activity

1. Glob `data/sandboxes/37393fb0/activity_logs/activity_*.json`. Read every file dated within the last 180 calendar days.
2. Glob `data/sandboxes/37393fb0/decisive_actions/*.json`. Read every file with a `timestamp` within the last 180 calendar days.
3. Read `data/sandboxes/37393fb0/prophet_trader.db` if you can. The trend ledger is persisted to disk per `TRADING_RULES_TREND.md` "Persisted Ledger" section. Verify exact table name and column names by inspecting the schema first; expected columns:
   - ticker, entry_date, entry_price, shares, atr_at_entry, initial_stop, donchian_100_high_at_entry
   - exit_date, exit_price, exit_reason ∈ {trailing_stop, initial_hard_stop, manual, circuit_breaker}
   - realized_pnl, status ∈ {open, closed, pending_fill}
   - cold_start_completed flag

If the DB is unreadable, fall back to reconstructing the trade log from the decisive_actions stream (entries log entry_price/atr/Donchian-100/Donchian-50; exits log exit_reason and realized_pnl). DB is preferred — note in the report when a fallback was used.

## Step 3 — Sample-size guard (MANDATORY, NON-NEGOTIABLE)

Count **completed entry/exit pairs** across the prior 6 months (status = closed in the ledger, or any decisive action that records both an entry and the corresponding exit within the window).

**If count < 6:**

Output the following block exactly and EXIT WITHOUT PROPOSING ANY CHANGES:

```
INSUFFICIENT_SAMPLE: <N> completed trades over 6mo (need ≥6).

Parameter snapshot (for the historical record):
  donchian_entry_length:           <value>
  donchian_trail_length:           <value>
  atr_vol_floor:                   <value>
  sma_regime_length:               <value>
  initial_hard_stop_atr_multiple:  <value>
  initial_hard_stop_window_days:   <value>
  risk_per_position_pct:           <value>
  max_position_pct:                <value>
  segment_deployed_cap_pct:        <value>
  max_open_positions:              <value>
  aggregate_risk_cap_pct:          <value>
  daily_circuit_breaker_pct:       <value>
  limit_entry_cushion_pct:         <value>
  universe:                        <value>
  observed_completed_count:        <N>
  window_start:                    <YYYY-MM-DD>
  window_end:                      <YYYY-MM-DD>

Trend strategies are very low-frequency by design; below 6 completed trades,
per-ticker statistics are pure noise. No parameter edits will be proposed
this cycle. Re-run next quarter.
```

Then stop. Do not edit `TRADING_RULES_TREND.md`. Do not propose changes. The user has explicitly forbidden relaxing this threshold.

If count ≥ 6, proceed to Step 4.

## Step 4 — Per-ticker analysis (TLT, GLD, USO, DBC, UUP, EEM)

For each of the six universe tickers, even those with zero or one trade in the window, produce this row in a single table:

| Ticker | Trades | Win % | Avg hold (trading days) | Avg $ P&L | Best $ | Worst $ | Max single-trade DD ($, % of portfolio) | Time-in-position vs trail-50 effectiveness |
|---|---|---|---|---|---|---|---|---|

Definitions:
- **Trades** = completed entry/exit pairs in the 6mo window (open trades not counted but noted in a footnote)
- **Win %** = % of trades with realized_pnl > 0
- **Avg hold** = mean trading-day count from entry_date to exit_date
- **Avg / Best / Worst $ P&L** = realized_pnl mean / max / min
- **Max single-trade DD** = largest unrealized drawdown observed during the life of any single trade for this ticker (computed from the daily-bar trajectory between entry and exit; if not directly logged, approximate from `entry_price` and the lowest close between entry and exit). Express both in dollars and as a fraction of portfolio at entry.
- **Trail-50 effectiveness** = qualitative read of when the trailing stop fired vs. price action. If trades closed at the Donchian-50 trail well after the position had given back a large share of its peak unrealized P&L, the 50-bar trail is "loose" — capturing trends late. If trades closed soon after a minor pullback that turned out to be a continuation, the trail is "tight" — chopping out of trends prematurely. State this as one of: TIGHT / BALANCED / LOOSE / N/A (insufficient data).

Footnotes for any ticker:
- "Open at end of window: <details>" if there's an active position
- "Zero trades in window" if the ticker never qualified — flag this for the universe-trim discussion in Step 6

## Step 5 — Parameter evidence

For each parameter, compute the following and state the qualitative finding (BINDING / NON-BINDING / TUNED-RIGHT / OFF-REGIME):

**Donchian entry length (100)**
- For each completed losing trade, look at what happened in the bars before entry. Was the entry on a fresh new high (clean breakout into open space) or was it on a marginal break that immediately reversed (chopped)?
- Compute "entries chopped" = entries where the position hit either the initial hard stop or trailing stop within 10 trading days of entry. If "entries chopped" is a high share of all entries (>50%), the 100-bar entry length may be too short for the current regime — consider 150 or 200. Report the count.
- If "entries chopped" share is small but trends are also short (avg hold < 30 days), the entry signal may be late — but do not propose lengthening past 100 without strong evidence; long lengths increase signal lag and reduce trade count further.

**Donchian trail length (50)**
- This drives the TIGHT/BALANCED/LOOSE classification in Step 4. If multiple tickers cluster as TIGHT, propose lengthening (e.g. 50 → 65). If multiple cluster as LOOSE, propose shortening (e.g. 50 → 35). If mixed, leave at 50.

**ATR-20 vol floor (0.005)**
- Across the window, count entries the agent skipped due to "atr_floor" (ATR/price below 0.005). Were the skipped tickers later good trends that the floor excluded? You can't know counterfactuals perfectly, but you can read the post-skip price path from the daily-bar data on those tickers and note whether a trade would have produced a winner.
- If the floor is rejecting many entries on a ticker that looks like it would have won, the floor may be too high for that asset's vol regime. If almost no entries are rejected by the floor, it's non-binding.

**SMA-200 regime filter (length 200)**
- Across all SKIP-on-entry decisions, how many were filtered by `last_close ≤ sma_200`? Of those, did the underlying go on to make a Donchian-100 high without re-asserting above SMA-200, or did the SMA filter correctly avoid a fakeout?
- If the filter rejected >75% of entries that would otherwise have triggered, but the rejected setups would have been losers, the SMA-200 is doing its job — leave as is.
- Changing the SMA length itself (e.g. 200 → 150) is parametric and proposable. Removing the filter entirely is structural — escalate per Step 8.

**Initial-hard-stop multiple (2× ATR) and active window (20 days)**
- Of trades that exited via initial_hard_stop, how often did the position then go on to recover above entry within the next 30 days? If "stop-then-recover" is common, the 2× multiple may be too tight (or the 20-day window may be too long, keeping the hard stop active when the trailing stop would have given more room).
- If initial_hard_stop firings rarely happen, the parameter is non-binding — no signal.

**Risk-per-position (0.5%) and aggregate risk cap (2.5%)**
- Were these caps ever the binding constraint? How often (% of heartbeats) was aggregate_risk near 2.5%?
- Changes here have a portfolio-risk impact and should not be proposed without strong evidence across multiple quarters. Default = no change. If you do propose a change, do so cautiously and explicitly note the portfolio-risk implications.

**Segment-deployed cap (18%) and max-open (5)**
- Was either ever the binding constraint? If neither, the caps are non-binding — no signal, do not propose changes.
- If frequently binding and the agent was skipping entries because of them, that's evidence the strategy is finding more setups than budgeted — note as a sizing/cap question for the user.

**Daily circuit breaker (−2%)**
- Did it trip in the window? If yes, look at what happened next session: did the trend regime continue (the breaker was the wrong call) or reverse (the breaker was correct)? With sample sizes this small, a single firing isn't enough to propose a change — note the finding but default to no change.

**Limit-order entry cushion (0.5% above last close)**
- Of entries placed, how many were "missed entry — gap above limit"? If many, the cushion may be too tight; consider 0.75% or 1.0%. If none missed, no signal.

## Step 6 — Universe trim assessment

For each ticker in [TLT, GLD, USO, DBC, UUP, EEM], decide one of:

- **KEEP** — produced at least one completed trade in the window, win rate or avg P&L is acceptable, or zero trades but the ticker is a structurally important bucket (rates / metals / EM / FX / etc.)
- **WATCH** — produced losing trades only, or zero trades with no structural rationale to retain
- **REMOVE** — at least 3 completed trades AND all losers AND avg per-trade P&L < 0 by a margin that's unlikely to reverse

To propose REMOVE, all three conditions must hold. A single bad trade does not warrant removal. A ticker with zero trades is WATCH at most — never REMOVE without evidence. Removing the only ticker in an asset bucket eliminates the diversification rationale for that bucket; flag this explicitly in the proposal.

The user has not authorized adding new tickers. Adding new tickers is structural — escalate per Step 8.

## Step 7 — Propose specific parameter edits (only when warranted)

Translate evidence into specific numerical or universe-trim proposals. Use the format from `adapt-strategy`:

---
**Proposed Parameter Edit [N]** — [Parameter name or "Universe trim: <ticker>"]

**Current rule text:**
> [exact quote from `TRADING_RULES_TREND.md`]

**Proposed replacement text:**
> [your revised text — must be a numerical change to the existing rule, or a single-ticker universe removal]

**Evidence:** [Specific stats from Steps 4–6 — trade count, win %, avg P&L per ticker, chop rate, stop-then-recover rate. Must cite at least one number from the cohort. No vibes-based proposals.]

**Predicted effect:** [What you expect this change to do — e.g. "lengthening the trail from 50 → 65 is expected to capture an additional ~3% of move per winning trade based on the LOOSE/TIGHT analysis above; trade count is unaffected since the trail length only governs exits."]

---

Examples of legitimate proposals (illustrative):
- "Lengthen Donchian entry from 100 → 150 (5 of 6 entries in the window were chopped within 10 trading days; 100-bar breakouts in this regime are not delivering durable trends)."
- "Tighten Donchian trail from 50 → 35 (3 of 4 closed winners gave back >40% of peak unrealized P&L before the 50-bar trail fired — trail is too loose for the current regime)."
- "Remove USO from universe (3 completed trades, all losers, total realized P&L −$420; oil's regime in this window did not produce durable trends. EEM remains as the only commodity-adjacent ticker; rates/metals/FX buckets unchanged)."
- "Lower ATR floor from 0.005 → 0.003 (12 entry signals on UUP were rejected by the ATR floor; UUP averaged ATR/price = 0.0035 in the window — the floor is set above UUP's natural vol level for this regime)."

Examples of proposals you must NOT make (these are structural, not parametric):
- Adding new tickers to the universe
- Removing the SMA-200 regime filter entirely
- Changing the signal source (adding MACD, momentum, etc.)
- Allowing shorting or inverse ETFs
- Changing daily-bar to intraday
- Changing the persisted-ledger architecture
- Changing the heartbeat-scheduling structure (the 5:00 PM ET window)
- Changing the cold-start mechanism

If your evidence points at one of these, do NOT propose a parameter edit. Go to Step 8.

## Step 8 — Structural escalation (do NOT silently change structure)

If the cohort evidence suggests the strategy structure itself is broken — for example:
- Win rate is below 30% across all parameter tiers and all tickers (something more fundamental than parameter tuning is wrong)
- A new asset class outside the existing buckets is needed (universe addition is structural)
- The Donchian-only signal is consistently producing late entries that no length adjustment can fix
- Multiple parameter changes that, taken together, would amount to a different strategy

Do NOT propose parametric edits to paper over structural issues. Instead, write a file `STRUCTURAL_REVIEW_NEEDED_TREND.md` at the repo root with:

```
# TrendProphet Structural Review Needed

Triggered by: trend-parameter-review on <YYYY-MM-DD>
Window: <6-month window>
Sample size: <N completed trades>

## What the data shows

[Bullet list of the structural-looking findings.]

## Why this is structural, not parametric

[Why no combination of legal numerical edits or universe trims would address what the data shows.]

## Recommended operator decisions

[1–3 specific questions the operator needs to answer before any parameter changes are made — e.g., "should we add a momentum-confirmation filter?", "should the universe be expanded to include single-name commodity ETFs?", etc.]
```

Then ask the user explicitly whether they want to discuss structural changes. Do not edit `TRADING_RULES_TREND.md` until that conversation has happened.

## Step 9 — Present and confirm

Show the user the per-ticker table from Step 4, the parameter findings from Step 5, the universe-trim assessment from Step 6, and all proposed parameter edits from Step 7 (or the structural-escalation report if Step 8 fired). Ask which proposals to apply. Do not modify any file until the user confirms specific edits.

## Step 10 — Apply approved edits

For each approved numerical edit or universe trim:
1. Re-read `TRADING_RULES_TREND.md` to get the freshest version.
2. Use Edit to replace the old rule text with the new rule text exactly as proposed. Preserve all surrounding content. For a universe trim, also remove the corresponding row from the Universe table and update any rule that references the count "Six ETFs" (e.g. update to "Five ETFs" and adjust the max-open-positions discussion if needed — but **do not change the max-open-positions number itself** unless explicitly proposed and approved).
3. After all edits, show the final diff of what changed.

Remind the user the changes take effect on TrendProphet's next heartbeat (5:00 PM ET) — no agent restart required, since TrendProphet reads `TRADING_RULES_TREND.md` from disk. If the strategy is later migrated from `rulesFile`-based to inline `customRules` in `data/agent-config.json` (the Penny pattern), edit there instead and update the strategy `updatedAt` timestamp.

Note: do NOT touch the "Identity" section, the "What You Do Not Do" list, the persisted-ledger schema, the heartbeat-step structure, the hard-stops list, or the cold-start mechanism. Those are structural. Parameters and universe-trim only.
