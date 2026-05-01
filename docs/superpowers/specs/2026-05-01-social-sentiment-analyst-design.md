# Social Sentiment Analyst — Design Spec

**Date:** 2026-05-01
**Skill name:** `social-sentiment-analyst`
**Skill location:** `.claude/skills/social-sentiment-analyst/`

---

## Overview

A skill for detecting retail sentiment, trending narratives, and rumor-driven moves across social media and alternative sources. Designed as a **filter layer**, not an alpha generator — it surfaces structured context about the rumor layer around a ticker so that other signals (price action, options flow, VCP setup, fundamentals) can be interpreted with full awareness of what retail is doing and why.

Primary use case: options-first swing trading (1–14 day holds), with intraday capability as secondary mode.

Rumor signals are treated as **situational awareness** inputs with a built-in sentiment regime classifier that determines whether to use the signal for momentum confirmation (Early-stage) or contrarian fade (Peak/Decaying). The tool does not pick direction — it tells you where in the rumor cycle a narrative sits and what that implies for structure.

---

## Architecture

**Approach:** Pure WebSearch + LLM synthesis. No Python scripts, no social media APIs.

**Rationale:** The hard tasks — distinguishing organic chatter from coordinated pumps, classifying regime state, extracting the dominant thesis — are interpretive, not counting tasks. Scripts produce false precision; LLMs produce genuine judgment over linguistic content. Social media APIs are actively adversarial (Twitter/X $100+/month, Reddit banned third-party access in 2023). WebSearch routes through Google/Bing infrastructure that handles adversarial scraping.

**Structure:**
```
.claude/skills/social-sentiment-analyst/
  SKILL.md
  references/
    output_schema.md           # Contract: exact fields, order, format, null-output path
    source_weighting.md        # Source tiers, search strategy, fallback hierarchy, breadth scale
    regime_taxonomy.md         # 5-state model with positive + negative named examples
    noise_filter_criteria.md   # Classic pump tells + AI-generated content markers
    temporal_framework.md      # 4 causal scenarios, freshness rating, trigger-context inputs
    thesis_patterns.md         # 9 thesis types with options structure implications
    iv_integration.md          # IV rank × regime → options direction guidance
```

**Implementation order:** Write `output_schema.md` first (it is the contract everything else serves), then `SKILL.md`, then the six analytical reference files. This ensures analytical files are written knowing exactly which output fields they need to populate.

---

## Source Coverage

### Priority Tiers

**Tier 1 — High signal (primary breadth measurement):**
- Reddit: r/wallstreetbets, r/options, r/stocks, r/investing, r/CryptoCurrency
- X / Twitter: retail traders, financial influencers, trending $tickers
- StockTwits: purpose-built ticker-level sentiment

**Tier 2 — Medium signal:**
- YouTube: large-following financial influencers (creates predictable retail flow)
- Crypto Twitter + Telegram: essential for COIN, MSTR, MARA, crypto options

**Tier 3 — Confirmation / slower signal:**
- Seeking Alpha comments (fundamentals-aware retail)
- InvestorHub
- Unusual Whales / FlowAlgo discussion threads (flow discussion often contains rumor theses)
- Substack / financial newsletters (tracks which names are getting newsletter pumps)

**Special cases:**
- Discord: scout where accessible; many active options communities have migrated here
- 4chan /biz/: very low weight, very high noise — flag only; occasionally surfaces small-cap pumps early
- TruthSocial: DJT-specific tickers and politically-tied names only

### Cross-Source Breadth Scale (Tier 1 only)

The breadth score is categorical, not continuous. Tier 1 has exactly three sources:

- **0–1 sources active = Insufficient breadth** — likely single-community echo chamber or coordinated single-platform pump
- **2 sources active = Partial breadth** — developing signal, not yet confirmed cross-source
- **3 sources active = Full breadth** — genuine cross-source coherence; highest-conviction signal

Only Full breadth (3/3) crosses the coherence threshold for Emergency alert tier.

### Source Fallback Hierarchy

WebSearch source availability degrades as platforms change. If Tier 1 sources return thin results:
- Reddit thin → search Seeking Alpha comments and InvestorHub as partial substitute
- X/Twitter thin → search financial YouTube and Substack for same narrative
- StockTwits thin → search Reddit options-specific subreddits

Output must include a **Search Yield** rating (High / Medium / Low) reflecting actual source availability during the invocation. Low search yield bounds the confidence ceiling at Medium regardless of other factors.

---

## Regime Taxonomy (5 States)

### Pre-emergent / Noise
Low volume, uncoordinated, absent or incoherent thesis. Often from known pump accounts, bots, or isolated community chatter. Does not yet qualify as a tradeable signal.

### Early / Gaining Traction
Rising mention velocity, cross-source coherence beginning to form (≥2 Tier 1 sources), thesis becoming articulate and specific. This is the entry window for momentum plays.

### Peak / Saturating
Mainstream visibility (CNBC, MarketWatch, Google Trends spike), parabolic mention volume, everyone is aware. Narrative is likely priced in. Entry window for premium selling / fade plays.

### Decaying-elevated
Price still elevated, original chatter slowing, thesis fragmented across multiple competing narratives. **IV crush danger zone for long premium holders.** Holders have not capitulated. Do not initiate new long premium positions.

### Decayed / Capitulated
Narrative dead, price returned to pre-rumor range or consolidated. Watch for mean reversion opportunity or fresh catalyst reset. IV collapsing — defined-risk structures only.

### Examples
Each state in `regime_taxonomy.md` must include 2–3 named concrete positive examples and 1–2 named negative examples ("looked like X, was actually Y because of these specific tells"). Use real cases from the last 18 months where possible. Mark the date of each example so future refresh cycles know what is getting stale.

Good examples: GME January 2021 (Early → Peak → Decayed), DJT March 2024, specific biotech FDA plays, MSTR during Bitcoin moves.

---

## Noise Filter Criteria

### Classic Coordinated Pump Tells
- Identical or near-identical phrasing across multiple posts (copy-paste detection)
- New/low-karma accounts posting exclusively about this ticker
- Recycled meme templates from prior failed pumps (visual and textual)
- Suspicious unanimity — organic chatter always has skeptics; coordinated pumps are unnaturally bullish
- Volume without substance: many posts, zero specific thesis, DD, or numbers
- Manufactured urgency markers ("LFG 🚀🚀" with no reasoning) vs. organic excitement ("noticed the setup because [specific observation]")

### AI-Generated Content Markers (post-2024)
Classic copy-paste tells miss LLM-generated pump content. Specific markers:
- Overly polished prose for the platform — WSB does not write in measured paragraphs
- Generic bullish framing without specific numbers, dates, or named sources
- Suspicious uniformity of post length across multiple accounts
- Absence of platform-native slang, memes, or in-jokes that an organic community member would naturally use
- Hedged but bullish tone (LLMs are trained to be measured; organic pump posts are not)
- Lack of specific catalyst dates or ticker-specific technical observations

---

## Temporal Framework

### Trigger-Context Inputs

When invoked from the alerting system, the following context should be passed:

| Input | If provided | If missing |
|-------|-------------|------------|
| Trigger timestamp | Use as temporal anchor for causal scenario classification | Note "no trigger context — causal scenario classification skipped" |
| Trigger directional bias (bullish/bearish/neutral) | Cross-check against chatter sentiment; flag divergence explicitly | Note as unavailable |
| Lookback horizon | Use as search window (default: 7 days) | Default to 7 days |

When trigger context is missing (manual lookup), the skill must explicitly state this in the output rather than silently producing lower-quality causal classification.

### Four Causal Scenarios

**Scenario 1 — Narrative-Led Flow**
Chatter was building hours-to-days before the trigger fired. The flow or breakout is the result of the narrative reaching a tipping point. Highest-conviction setup for momentum continuation. Retail participation is already established and thesis-driven.
*Identification:* Narrative inflection timestamp is 6+ hours before trigger. Posts reference specific catalysts (FDA date, earnings setup, M&A speculation, sector theme) without referencing recent unusual flow.

**Scenario 2 — Coincident Catalyst**
Narrative and trigger emerge simultaneously, both reacting to a real underlying event (news, sector rotation, macro). High conviction but duration is uncertain — you don't know the catalyst's full magnitude. Use tighter stops, shorter expirations.
*Identification:* Narrative inflection within ±1 hour of trigger. Posts and flow both reference the same news event.

**Scenario 3 — Flow-Led Narrative**
Trigger fired first; retail noticed the flow and started chattering about it. The narrative is reflexive, not predictive — retail is reacting to your trigger signal, not to independent information. Often fails within 1–3 days because the "rumor" has no underlying substance.
*Identification:* Narrative inflection clearly after trigger. Posts contain phrases like "look at the unusual flow on $XYZ", "someone knows something", "huge call buying just hit." These are reflexivity tells.

**Scenario 4 — Decoupled**
Rumor chatter exists but has no clear relationship to the trigger event. Treat the trigger as a clean technical/flow signal without rumor amplification.
*Identification:* Chatter topic is unrelated to trigger direction or thesis. No temporal correlation.

### Temporal Sequence Detection

The LLM should search with explicit time-bounded queries (last 2 hours, last 24 hours, last 7 days) and note the earliest substantive mentions and the inflection point where chatter accelerated. WebSearch timestamps are often imprecise — use ranges ("narrative appears to have accelerated 18–36 hours before trigger") and lower confidence accordingly. Never fabricate precise timestamps.

### Freshness Rating

- **Active** — Significant chatter in last 2 hours. Narrative live and evolving. Trade thesis may shift within hours.
- **Recent** — Meaningful chatter in last 24 hours, now cooling. Swing setup window.
- **Stale** — Last substantive chatter 24+ hours ago. High risk of being late to the move.

---

## Thesis Patterns (10 Types)

| Thesis Type | Description | Options Implication |
|-------------|-------------|---------------------|
| Short squeeze setup | High short interest, borrow cost, float squeeze narrative | High IV expected; spreads > naked long; gamma risk real |
| Earnings catalyst | Beat/miss speculation, guidance revision, whisper number | Check IV rank first; calendar spreads if IV low; avoid long premium into high IV |
| FDA / regulatory binary | Approval or rejection event, PDUFA date | Straddle or defined risk; do not pick direction; very high IV normal |
| M&A speculation | Acquisition rumor, strategic review, private equity interest | Calls; watch for IV spike on confirmation leak |
| Activist investor / 13D | Activist taking stake, board changes, strategic demands | Multi-month thesis arc; longer-dated options; less IV crush risk |
| Bankruptcy / restructuring | Distress signals, missed payments, restructuring chatter | Puts, put spreads; extreme halt risk; defined risk only; retail often wrong by buying calls |
| Sector theme rotation | Macro-driven sector narrative (AI, defense, energy transition) | Lower conviction on individual names; longer duration; smaller size per name |
| Crypto-correlated | COIN, MSTR, MARA, RIOT driven by BTC/ETH narrative | Correlated to crypto vol; adjust for spillover; Bitcoin moves drive these non-linearly |
| Political / policy-tied | DJT, defense contractors, tariff-exposed names | Event-driven; high IV; defined risk only; TruthSocial + X primary sources |
| Vague / undefined thesis | Chatter exists but doesn't fit a recognizable pattern | Lower conviction; treat as noise-adjacent; smaller size or skip; do not force-fit |

---

## IV Integration

| Regime | IV Rank | Recommended Options Approach |
|--------|---------|------------------------------|
| Early / Gaining | Low (<30) | Long premium directionally (highest conviction setup) |
| Early / Gaining | Medium (30–60) | Smaller size or vertical spreads; premium becoming expensive |
| Early / Gaining | High (>60) | Spreads or calendar structures; avoid naked long premium |
| Peak / Saturating | High (>60) | Sell premium (narrative likely priced in); consider short straddle/strangle with defined risk |
| Peak / Saturating | Medium (30–60) | Light premium selling; risk of continued squeeze |
| Decaying-elevated | Any | Avoid long premium; IV crush + directional risk active |
| Decayed / Capitulated | Collapsing | Mean reversion possible; defined risk only; small size |

---

## Output Schema (13 Fields)

The output schema is the contract. All analytical reference files serve it. The implementation order begins here.

### Null-Output Path

If Search Yield = Low AND fewer than 5 substantive posts found across all tiers, skip regime classification entirely and output:

> **No meaningful chatter detected.** Clean technical/flow signal recommended — the rumor layer adds no information for this ticker at this time.

This is a feature: it tells the trading system to trust the technical/flow trigger without rumor overlay.

### Full Output Format

```
══════════════════════════════════════════════
SOCIAL SENTIMENT ANALYSIS: $[TICKER]
══════════════════════════════════════════════

Reference Freshness: [oldest example date across loaded files]
[If any reference >2 quarters old: "⚠ Knowledge base partially stale — confidence capped at Medium"]

Search Yield: [High / Medium / Low]
[If Low: see null-output path above]

──────────────────────────────────────────────
REGIME
──────────────────────────────────────────────
State: [Pre-emergent/Noise | Early/Gaining | Peak/Saturating | Decaying-elevated | Decayed/Capitulated]
Trajectory: [Stable | Transitioning-up | Transitioning-down]
Confidence: [Low | Medium | High]
→ Justification: [1–2 sentences citing specific evidence from searched sources.
   Classifications without justification are invalid output.]

──────────────────────────────────────────────
CAUSAL SCENARIO
──────────────────────────────────────────────
Scenario: [1–Narrative-Led | 2–Coincident | 3–Flow-Led | 4–Decoupled | Skipped–no trigger context]
→ Justification: [1–2 sentences citing temporal sequence and content evidence.
   Classifications without justification are invalid output.]

──────────────────────────────────────────────
FRESHNESS
──────────────────────────────────────────────
Rating: [Active | Recent | Stale]
→ Justification: [1–2 sentences with timestamp range. Use ranges if exact timestamps unavailable.
   Classifications without justification are invalid output.]

──────────────────────────────────────────────
DOMINANT THESIS
──────────────────────────────────────────────
Type: [category from thesis_patterns.md]
→ Summary: [1–2 sentence narrative fingerprint describing the specific thesis driving chatter]

──────────────────────────────────────────────
SOURCE BREAKDOWN
──────────────────────────────────────────────
Tier 1: Reddit [X posts/mentions] | X/Twitter [Y] | StockTwits [Z]
Tier 2: YouTube [if active] | Crypto Telegram/Twitter [if active]
Tier 3: Seeking Alpha [if active] | Unusual Whales discussion [if active] | Other [specify]
Cross-Source Breadth: [0–1 = Insufficient | 2 = Partial | 3 = Full] ([N]/3 Tier 1 sources)

──────────────────────────────────────────────
VELOCITY
──────────────────────────────────────────────
Assessment: [qualitative description of mention acceleration rate and timeframe]

──────────────────────────────────────────────
OPTIONS CONTEXT
──────────────────────────────────────────────
IV Rank: [if passed in] / [Not provided — obtain before trading]
Options Guidance: [from iv_integration.md × thesis_patterns.md — specific structure recommendation]

──────────────────────────────────────────────
RED FLAGS
──────────────────────────────────────────────
[List any noise-filter criteria that triggered but did not disqualify the signal, with brief explanation]
[Or: None]

──────────────────────────────────────────────
ALERT TIER
──────────────────────────────────────────────
Tier: [Emergency | Standard | Daily Digest | Suppress]
Reason: [1 sentence]

──────────────────────────────────────────────
OVERALL CONFIDENCE
──────────────────────────────────────────────
[Low | Medium | High]
[Automatic caps: Medium if Search Yield = Low, or if any reference file >2 quarters old]
══════════════════════════════════════════════
```

### Alert Tier Logic

**Emergency:**
(Early regime OR Regime trajectory = Transitioning-up toward Peak) AND Active freshness AND Scenario 1 AND Cross-Source Breadth = Full (3/3)

**Standard:**
New ticker entering Early regime with Partial or Full breadth (≥2 Tier 1 sources)

**Daily Digest:**
Regime transitions on watched tickers; Peak saturation warnings; Decaying-elevated state on open positions

**Suppress:**
- Scenario 3 + Stale freshness (reflexive narrative, already old)
- Pre-emergent / Noise with no red-flag override
- Decayed / Capitulated regime (move is over)
- Scenario 4 + Overall Confidence = Low (chatter decoupled and weak)

---

## Enforcement Language (SKILL.md)

The following language must appear verbatim in SKILL.md:

> For each classification (Regime, Causal Scenario, Freshness, Dominant Thesis), the output must include a 1–2 sentence justification citing specific evidence from the searched sources. **Classifications without justification are invalid output.** Do not summarize or truncate justifications under time pressure or for brevity.

This prevents confidence drift across sessions and makes all outputs auditable.

---

## Reference File Freshness Protocol

- Each reference file carries a `Last refreshed: YYYY-QX` header
- If any reference file's examples are older than 2 quarters, Overall Confidence is automatically capped at Medium and the Reference Freshness warning is shown at the top of every output
- `output_schema.md` surfaces the oldest example date in field 1 so staleness is visible before the analysis, not after
- Quarterly review: replace oldest named case examples with recent ones from the past 18 months; mark date of each example inline

---

## Acceptance Test Case

Before implementation is accepted, run the skill on one concrete real-world case and verify all 13 output fields match the expected values below.

**Recommended test case:** [To be filled in before implementation — ideally the Bitcoin trade that prompted this skill, or a recent options-name with verifiable FDA/earnings chatter and known outcome.]

For each field, write the expected value before running the skill. If the skill's output matches on regime, causal scenario, freshness, thesis type, and alert tier, the implementation is correct. Mismatches on these five fields require reference file revision before deployment.

**Test case template:**
```
Ticker: $___
Trigger type: [unusual flow / breakout / manual]
Trigger timestamp: ___
Trigger bias: [bullish / bearish / neutral]
Expected Regime: ___
Expected Causal Scenario: ___
Expected Freshness: ___
Expected Dominant Thesis: ___
Expected Alert Tier: ___
```

---

## Integration with Existing System

The skill functions as a **filter layer** called by other triggers:
- Unusual options flow detected → invoke skill on that ticker
- VCP breakout confirmed → invoke skill to check rumor overlay
- Manual lookup → invoke with no trigger context (skill notes "causal scenario skipped")

The skill does not replace price action, options flow, or technical analysis. It answers one question: *what is the retail/social layer doing around this ticker, and what does that imply for how to structure the trade?*

---

## Files to Create (Implementation Order)

1. `references/output_schema.md` — the contract
2. `SKILL.md` — the workflow + enforcement language
3. `references/source_weighting.md`
4. `references/regime_taxonomy.md` — requires named real cases, dates marked
5. `references/noise_filter_criteria.md` — requires named real cases, AI-content section
6. `references/temporal_framework.md`
7. `references/thesis_patterns.md`
8. `references/iv_integration.md`
