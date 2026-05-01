---
Last refreshed: 2026-Q2
---

# Temporal Framework

Defines how to handle trigger-context inputs, classify causal scenarios, rate narrative freshness, and identify temporal sequence from WebSearch results.

## Trigger-Context Input Handling

The calling system may pass trigger context when invoking the skill. Handle each input as follows:

| Input | If provided | If missing |
|-------|-------------|------------|
| **Trigger timestamp** | Use as temporal anchor for causal scenario classification. Compare to narrative inflection point. | Note "Skipped — no trigger context" in Field 4. Do not guess or estimate. |
| **Trigger directional bias** (bullish/bearish/neutral) | Cross-check against dominant chatter sentiment. Explicitly flag any divergence (e.g., "trigger is bullish but chatter is net bearish — divergence flagged"). | Note as unavailable in Field 4 justification. |
| **Lookback horizon** | Use this as the WebSearch time window. | Default to 7 days. |
| **No trigger context at all** | If none of the above are provided (manual lookup), note "Skipped — no trigger context" in Field 4. The skill still produces all other fields. |

**Never silently downgrade quality.** If trigger context is missing, the output must state this explicitly. A reader should always know whether causal scenario was classified or skipped, and why.

## Four Causal Scenarios

### Scenario 1: Narrative-Led Flow

**What it means:** Chatter was building hours-to-days before the trigger fired. The unusual flow, breakout, or price alert is the result of the retail narrative reaching a tipping point. **This is the highest-conviction setup for momentum continuation** — retail participation is already established and thesis-driven.

**Identification criteria:**
- Narrative inflection point is **more than 1 hour before** the trigger timestamp. Confidence scales with the gap: 6+ hours before = High confidence (narrative clearly predates trigger); 1–6 hours before = Medium confidence (temporal precedence established but gap is borderline — apply Scenario 1 with Medium confidence).
- Post content references specific catalysts (FDA date, earnings setup, M&A speculation, sector theme) **without** referencing recent unusual flow or options activity
- The thesis predates the trigger and does not require it for justification
- Cross-source breadth was already ≥ Partial before the trigger fired

**Trade implication:** Momentum continuation is supported by existing retail conviction. Scenario 1 + Active freshness + Full breadth = Emergency alert tier.

**Your father's Bitcoin case lives here:** The rumor was circulating (narrative inflection) before the news hit mainstream (trigger). The thesis existed independently of any price action trigger.

### Scenario 2: Coincident Catalyst

**What it means:** Narrative and trigger emerge simultaneously, both responding to a real underlying catalyst (news event, sector rotation, macro development). High conviction but duration uncertain — you don't know the full magnitude of the catalyst yet.

**Identification criteria:**
- Narrative inflection point is **within ±1 hour** of the trigger timestamp
- Posts and flow/trigger both reference the same specific event (same news story, same data release)
- Chatter does not reference the flow or options activity — it references the underlying event

**Trade implication:** Real catalyst, but magnitude uncertain. Use tighter stops and shorter expirations than Scenario 1. Don't overstay.

### Scenario 3: Flow-Led Narrative

**What it means:** The trigger fired first; retail noticed the unusual flow/price action and started chattering about it. The narrative is **reflexive, not predictive** — retail is reacting to your trigger signal, not to independent information. Often fails within 1–3 days because the "rumor" has no underlying substance.

**Identification criteria:**
- Narrative inflection point is **clearly after** the trigger timestamp
- Post content explicitly references the flow or options activity: "look at the unusual flow on $XYZ," "someone knows something," "huge call buying just hit," "why is options volume 10x normal?"
- The thesis is vague beyond "smart money is in" — no specific catalyst cited

**Trade implication:** Dangerous for new entries. The narrative will collapse as the flow resolves. Scenario 3 + Stale freshness = Suppress. Even with Active freshness, treat with significant skepticism.

### Scenario 4: Decoupled

**What it means:** Social chatter exists for the ticker, but its content has no clear relationship to the trigger event. The chatter and the trigger are addressing different aspects of the ticker.

**Identification criteria:**
- Chatter topic is unrelated to the trigger's directional bias (chatter is about a product launch; trigger is about earnings flow)
- No temporal correlation between chatter inflection and trigger

**Trade implication:** Treat the trigger as a clean technical/flow signal. The rumor layer adds context but not amplification. Scenario 4 + Low confidence = Suppress.

## Temporal Sequence Detection

### How to Estimate Narrative Inflection

WebSearch results often have imprecise timestamps ("2 hours ago," "yesterday," relative dates). Use these heuristics:

1. Search explicitly with time-bounded queries: "past 2 hours," "past 24 hours," "past 7 days"
2. Note the earliest substantive posts (with thesis, not just ticker mention)
3. Note the post that appears to mark acceleration — when volume meaningfully increased
4. Express your estimate as a range: "narrative appears to have accelerated 18–36 hours before trigger"

**Never fabricate a precise timestamp.** If you cannot determine whether inflection was 4 hours or 12 hours before the trigger, report the range and lower confidence accordingly.

### Reflexivity Tells (Scenario 3 Identification)

The strongest Scenario 3 tell is post content that references the flow/options activity itself:

- "Someone is loading up on calls" / "huge unusual call volume just hit"
- "Dark pool activity on $XYZ" / "options sweep spotted"
- "Smart money is quietly buying" (without naming any specific news catalyst)
- "Why is volume 10x normal? Someone knows something"

These phrases prove retail is reacting to the trigger signal, not to independent information. **If you see these phrases, classify as Scenario 3 regardless of other factors.**

## Freshness Rating

Freshness measures how current the active chatter is at the time of invocation. It is independent of the regime classification — a Peak-saturating narrative can be either Active (still accelerating) or Stale (already cooling), and those have completely different trade implications.

| Rating | Criteria | Trade Implication |
|--------|---------|------------------|
| **Active** | Significant substantive chatter within the last 2 hours | Narrative live and evolving; thesis may shift within hours; monitor closely if entering |
| **Recent** | Meaningful chatter in last 24 hours, now cooling | Swing setup window; optimal for 3–10 day options |
| **Stale** | Last substantive chatter 24+ hours ago; minimal recent activity | High risk of being late; potential Suppress depending on scenario |

**Important:** Freshness + Scenario interact for alert tier:
- Scenario 1 + Active = Emergency candidate
- Scenario 1 + Stale = Daily Digest at best (you missed the entry)
- Scenario 3 + Stale = Suppress (reflexive narrative already dissolved)
