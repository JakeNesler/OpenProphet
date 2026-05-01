---
Last refreshed: 2026-Q2
---

# Output Schema

This file defines the exact output contract for the social-sentiment-analyst skill. All 13 fields must appear in every invocation in the order specified. Use the null-output path when source data is insufficient.

## Null-Output Path

If Search Yield = Low AND fewer than 5 substantive posts found across all tiers, skip regime classification and output exactly:

```
══════════════════════════════════════════════
SOCIAL SENTIMENT ANALYSIS: $[TICKER]
══════════════════════════════════════════════
Reference Freshness: [oldest example date]
Search Yield: Low

NO MEANINGFUL CHATTER DETECTED
The rumor layer adds no information for this ticker at this time.
Clean technical/flow signal recommended — trade on price action and flow alone.
══════════════════════════════════════════════
```

## Full Output Format

```
══════════════════════════════════════════════
SOCIAL SENTIMENT ANALYSIS: $[TICKER]
══════════════════════════════════════════════

[FIELD 1] Reference Freshness: [oldest Last-refreshed date across all loaded reference files]
[If any reference file is older than 2 quarters from today: "⚠ Knowledge base partially stale — confidence capped at Medium"]

[FIELD 2] Search Yield: [High / Medium / Low]

──────────────────────────────────────────────
REGIME
──────────────────────────────────────────────
[FIELD 3] State: [Pre-emergent/Noise | Early/Gaining | Peak/Saturating | Decaying-elevated | Decayed/Capitulated]
[FIELD 3] Trajectory: [Stable | Transitioning-up | Transitioning-down]
[FIELD 3] Confidence: [Low | Medium | High]
→ Justification: [1–2 sentences citing specific evidence from searched sources.
   Classifications without justification are INVALID OUTPUT.]

──────────────────────────────────────────────
CAUSAL SCENARIO
──────────────────────────────────────────────
[FIELD 4] Scenario: [1–Narrative-Led | 2–Coincident | 3–Flow-Led | 4–Decoupled | Skipped–no trigger context]
→ Justification: [1–2 sentences citing temporal sequence and content evidence.
   Classifications without justification are INVALID OUTPUT.]

──────────────────────────────────────────────
FRESHNESS
──────────────────────────────────────────────
[FIELD 5] Rating: [Active | Recent | Stale]
→ Justification: [1–2 sentences with timestamp range. Use ranges, not fabricated exact times.
   Classifications without justification are INVALID OUTPUT.]

──────────────────────────────────────────────
DOMINANT THESIS
──────────────────────────────────────────────
[FIELD 6] Type: [one of the 10 types from thesis_patterns.md]
→ Summary: [1–2 sentence narrative fingerprint of the specific thesis driving chatter]

──────────────────────────────────────────────
SOURCE BREAKDOWN
──────────────────────────────────────────────
[FIELD 7] Tier 1: Reddit [X posts/mentions] | X/Twitter [Y] | StockTwits [Z]
          Tier 2: YouTube [if active, else omit] | Crypto Telegram/Twitter [if active, else omit]
          Tier 3: Seeking Alpha [if active] | Unusual Whales discussion [if active] | Other [specify]

[FIELD 8] Cross-Source Breadth: [Insufficient (0–1/3) | Partial (2/3) | Full (3/3)]
          → Interpretation: Insufficient = single-community echo or coordinated single-platform pump.
            Partial = developing signal, not yet confirmed. Full = genuine cross-source coherence.
          → Only Full breadth qualifies for Emergency alert tier.

──────────────────────────────────────────────
VELOCITY
──────────────────────────────────────────────
[FIELD 9] Assessment: [qualitative description of mention acceleration rate and timeframe covered]

──────────────────────────────────────────────
OPTIONS CONTEXT
──────────────────────────────────────────────
[FIELD 10] IV Rank: [value if passed in at invocation] / [Not provided — obtain before trading]
[FIELD 10] Options Guidance: [specific structure recommendation from iv_integration.md crossed with thesis_patterns.md]

──────────────────────────────────────────────
RED FLAGS
──────────────────────────────────────────────
[FIELD 11] [List any noise-filter criteria that fired but did not fully disqualify the signal, with 1-sentence explanation each]
           [Or: None]

──────────────────────────────────────────────
ALERT TIER
──────────────────────────────────────────────
[FIELD 12] Tier: [Emergency | Standard | Daily Digest | Suppress]
           Reason: [1 sentence]

──────────────────────────────────────────────
OVERALL CONFIDENCE
──────────────────────────────────────────────
[FIELD 13] [Low | Medium | High]
           [Automatic caps apply: Medium if Search Yield = Low, or if any reference file is >2 quarters old]
══════════════════════════════════════════════
```

## Alert Tier Logic

**Emergency:**
(Regime = Early/Gaining OR Trajectory = Transitioning-up) AND Freshness = Active AND Causal Scenario = 1 AND Cross-Source Breadth = Full (3/3)

**Standard:**
Ticker newly entering Early/Gaining regime AND Cross-Source Breadth = Partial or Full (≥2/3)

**Daily Digest:**
- Regime transition detected on a watched ticker
- Peak/Saturating warning on any ticker
- Decaying-elevated state on a held position

**Suppress:**
- Causal Scenario = 3 AND Freshness = Stale
- Regime = Pre-emergent/Noise AND no red-flag override
- Regime = Decayed/Capitulated (move is over, no actionable signal)
- Causal Scenario = 4 AND Overall Confidence = Low

## Enforcement Rule

For each classification (Regime, Causal Scenario, Freshness, Dominant Thesis), the output must include a 1–2 sentence justification citing specific evidence from the searched sources. **Classifications without justification are invalid output.** Do not summarize or truncate justifications under time pressure or for brevity.
