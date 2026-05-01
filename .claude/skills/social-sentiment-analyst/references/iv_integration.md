---
Last refreshed: 2026-Q2
---

# IV Integration

Defines how to combine IV rank with regime classification to produce options structure guidance. This file is crossed with thesis_patterns.md to produce Field 10 (Options Guidance) of the output.

## When IV Rank Is Not Provided

If IV rank was not passed in at invocation, note in Field 10: "IV rank not provided — obtain current IV rank via your broker before executing. Options guidance below assumes moderate IV (30–60 rank). Adjust structure if actual IV rank is significantly higher or lower."

Then provide guidance based on the moderate IV assumption, clearly labeled as an assumption.

## IV Rank × Regime Guidance Table

| Regime State | IV Rank | Recommended Options Structure |
|-------------|---------|------------------------------|
| **Early / Gaining** | Low (<30) | Long premium directionally. This is the highest-conviction setup — low IV means options are cheap and the move hasn't been priced. Buy calls (bullish thesis) or puts (bearish thesis). Consider simple long options rather than spreads to maximize upside capture. |
| **Early / Gaining** | Medium (30–60) | Smaller size than low-IV case. Vertical debit spreads reduce premium risk while preserving directional exposure. Avoid naked long premium at full position size. |
| **Early / Gaining** | High (>60) | IV is expensive; the move may already be partially priced. Use defined-risk debit spreads with a favorable risk/reward ratio, or wait for IV compression before entering. Do not buy naked long premium. |
| **Peak / Saturating** | High (>60) | Narrative likely priced in. **Sell premium.** Short straddle, iron condor, or credit spread captures IV collapse as the narrative saturates. Manage risk with defined-width spreads. This is the clearest premium-selling setup this skill produces. |
| **Peak / Saturating** | Medium (30–60) | Light premium selling via credit spread. Leave room for continued squeeze — naked short premium is risky if the narrative hasn't fully peaked. |
| **Peak / Saturating** | Low (<30) | Unusual — peak regime with low IV suggests the market hasn't priced the narrative. Re-examine whether regime is truly Peak or just Early with high visibility. If Peak is confirmed, wait for IV expansion before positioning. |
| **Decaying-elevated** | Any | **Avoid long premium entirely.** You are in the IV crush zone — IV is compressing while price is uncertain. Both directional and long-premium risk are elevated. If you must trade: short premium via tight credit spreads. Cash or short premium are the only rational structures. |
| **Decayed / Capitulated** | Collapsing | Mean reversion trade possible if price has overshot to the downside. Defined-risk structures only (debit spreads or narrow straddles). IV is low — long premium is cheap but the move must happen quickly. Thesis: fresh catalyst or technical mean reversion, not narrative continuation. |
| **Pre-emergent / Noise** | Any | No options position based on social sentiment. If trading, use only technical/flow signals. Any options position should be smallest possible size and defined risk. |

## Thesis × IV Integration

Certain thesis types override the general table. When there is a conflict between the table above and `thesis_patterns.md`, apply this precedence:

| Thesis Override | Rule |
|----------------|------|
| **FDA Binary** | Regardless of IV rank or regime: straddle or defined risk. Do not pick direction. IV will be extreme — calculate break-even move from options premium before trading. |
| **Bankruptcy / Restructuring** | Regardless of IV rank or regime: puts or put spreads only. Defined risk only. No calls. |
| **Activist / 13D** | Use longer-dated options (3–6 months) regardless of current IV. Short-dated options miss the thesis arc. |
| **Vague / Undefined** | Maximum 25% of normal position size regardless of IV rank. Debit spread if entering at all. |

## IV Rank Interpretation

If IV rank is provided, interpret as:

| IV Rank | Context |
|---------|---------|
| <20 | Historically cheap options; premium buyers have structural advantage |
| 20–40 | Below average; options are reasonably priced |
| 40–60 | Near average; neither premium buyer nor seller has structural advantage |
| 60–80 | Above average; premium sellers have structural advantage unless expecting very large move |
| >80 | Extremely elevated; premiums are expensive; straddles require very large moves to be profitable; credit spreads highly attractive |

## Complete Field 10 Generation

To generate Field 10 of the output:
1. Identify regime state (from Field 3)
2. Identify IV rank (from invocation input, or note as unavailable)
3. Look up the cell in the IV Rank × Regime table above
4. Check for any thesis override from thesis_patterns.md
5. Apply thesis override if present; otherwise use table guidance
6. Write the recommendation as: "[Structure] because [regime] + [IV rank context] + [thesis consideration if applicable]"

Example output for Field 10:
> IV Rank: 28 (low)
> Options Guidance: Long calls on the 45-strike expiring in 3 weeks. IV is cheap (rank 28) and regime is Early/Gaining with Full breadth — this is the optimal premium-buying setup. Short squeeze thesis means spreads are preferable to naked longs due to gamma risk; use a 45/50 call debit spread to limit downside if IV expands faster than price moves.
