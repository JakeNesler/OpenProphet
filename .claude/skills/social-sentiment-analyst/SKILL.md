---
name: social-sentiment-analyst
description: Detect retail sentiment, trending narratives, and rumor-driven moves across social media sources (Reddit, X/Twitter, StockTwits, YouTube, Telegram, Discord, Seeking Alpha, Substack, and others). Use when analyzing social chatter around a specific ticker, evaluating whether unusual options flow has a retail narrative behind it, checking if a VCP breakout has rumor amplification, or assessing what the retail crowd is saying and doing around a name. Outputs a structured 13-field analysis including regime state (5-state cycle), causal scenario, dominant thesis, cross-source breadth, and options structure guidance. Primary use: options-first swing trading with intraday capability as secondary.
---

# Social Sentiment Analyst

## Overview

This skill analyzes retail sentiment, trending narratives, and rumor-driven moves across social media platforms. It is a **filter layer, not an alpha generator** — it tells you where in the rumor cycle a narrative sits and what that implies for how to structure a trade.

Rumor signals are treated as situational awareness alongside price action, options flow, VCP setups, and fundamentals. The skill will tell you when the rumor layer adds no information (null-output) so you can trade the technical signal cleanly.

**Primary use case:** Options-first swing trading (1–14 day holds). Intraday capability as secondary.

## When to Use This Skill

**Explicit triggers:**
- "What is retail saying about $XYZ?"
- "Is there social chatter behind this unusual flow?"
- "Check the sentiment on $XYZ before I put on this trade"
- "Is $XYZ in a rumor cycle right now?"
- "What thesis is driving $XYZ options volume?"
- "Should I fade the hype on $XYZ or ride it?"

**Implicit triggers:**
- Unusual options flow detected → check if retail narrative explains or predates it
- VCP breakout forming → check if rumor amplification is present
- Reviewing an open position → check if held name is entering Peak/Decaying regime
- News event → check if retail is building a narrative around it

**When NOT to use:**
- Macro/institutional news analysis → use `market-news-analyst`
- Fundamental stock deep-dive → use `us-stock-analysis`
- Market-wide breadth → use `uptrend-analyzer`

## Invocation Inputs

Provide when invoking. All optional except Ticker, but analysis quality degrades when trigger context is absent.

```
Ticker: $[SYMBOL]
Trigger type: [unusual flow / VCP breakout / price alert / manual lookup]
Trigger timestamp: [HH:MM ET — omit if manual lookup]
Trigger directional bias: [bullish / bearish / neutral — omit if unknown]
Lookback horizon: [days — default 7]
IV rank: [0–100 — omit if unavailable; note this affects options guidance quality]
```

## Enforcement Rule

**For each classification (Regime, Causal Scenario, Freshness, Dominant Thesis), the output must include a 1–2 sentence justification citing specific evidence from the searched sources. Classifications without justification are invalid output. Do not summarize or truncate justifications under time pressure or for brevity.**

## Workflow

### Step 1: Parse Inputs and Load Reference Files

Parse invocation inputs. Note missing optional fields:
- Missing trigger timestamp → Causal Scenario = "Skipped — no trigger context"
- Missing IV rank → Options Guidance notes "obtain IV rank before trading"

Load all reference files:
- `references/output_schema.md` — always (read this first; it is the output contract)
- `references/source_weighting.md` — always
- `references/regime_taxonomy.md` — always
- `references/noise_filter_criteria.md` — always
- `references/temporal_framework.md` — always
- `references/thesis_patterns.md` — always
- `references/iv_integration.md` — always

**Do not begin Step 2 until all 7 reference files above have been read and their content is in working context.** If any file cannot be found, stop and report which file is missing.

**Check Reference Freshness:** Note the oldest `Last refreshed:` date across all files. If any file is older than 2 quarters from today's date, confidence ceiling is capped at Medium for this invocation. Surface this in Field 1 of the output.

### Step 2: Run Parallel WebSearch Queries

Execute these searches simultaneously. Use time-bounded language ("past 7 days", "recent", "this week") to surface fresh content.

**Tier 1 — High signal:**
1. `"$TICKER" site:reddit.com wallstreetbets options` — WSB and r/options
2. `reddit "$TICKER" stock options discussion` — broader Reddit
3. `"$TICKER" stocktwits` — StockTwits ticker page
4. `"$TICKER" stock twitter options rumor` — X/Twitter via web search

**Tier 2 — Medium signal:**
5. `"$TICKER" youtube financial` — influencer coverage
6. `"$TICKER" telegram crypto` — only if crypto-adjacent ticker (COIN, MSTR, MARA, RIOT, etc.)

**Tier 3 — Confirmation:**
7. `site:seekingalpha.com "$TICKER" comments` — fundamentals-aware retail
8. `"$TICKER" "unusual whales" OR "flowAlgo" discussion` — flow discussion forums
9. `"$TICKER" substack newsletter` — newsletter pump tracking

**Special (conditional):**
10. `site:4chan.org/biz "$TICKER"` — only if small-cap; extreme low weight
11. `"$TICKER" discord options trading` — community scout
12. `"$TICKER" truthsocial` — only for DJT, defense stocks, politically-tied names

For each search result: capture approximate timestamps, post volume estimate, content samples, and platform.

### Step 3: Null-Output Check

Count substantive posts across all tiers. Substantive = contains a thesis or specific claim, not just a ticker mention or emoji-only post.

If fewer than 5 substantive posts found across all tiers (which produces Search Yield = Low):
→ Output the null-output format from `references/output_schema.md` and stop.

Otherwise: assess Search Yield (High / Medium / Low) based on actual result volume and proceed.

### Step 4: Apply Noise Filter

Using `references/noise_filter_criteria.md`, assess collected content for:
- Classic coordinated pump tells (copy-paste phrases, new accounts, suspicious unanimity, volume without thesis)
- AI-generated content markers (polished prose, generic framing, no platform-native slang)

Document each red flag that fires. Red flags lower confidence and appear in Field 11 (Red Flags). A signal with 3+ red flags and no cross-source coherence should be classified Pre-emergent/Noise regardless of mention volume.

### Step 5: Temporal Analysis

Using `references/temporal_framework.md`:

**If trigger timestamp was provided:**
1. Identify narrative inflection point from post timestamps (use ranges if exact times unavailable)
2. Compare inflection to trigger timestamp
3. Classify Causal Scenario (1–4) using the identification criteria in the reference file
4. If trigger directional bias was provided, check chatter sentiment alignment — flag explicit divergence

**If no trigger timestamp:**
→ Record "Skipped — no trigger context provided (manual lookup)" in Field 4

Assess Freshness (Active / Recent / Stale) from most recent substantive post timestamps.

**Justification required for Causal Scenario and Freshness.** Classification without justification is invalid output.

### Step 6: Regime Classification

Using `references/regime_taxonomy.md`, classify regime state and trajectory.

Pattern-match to named examples in the taxonomy. Pay special attention to all negative examples across all 5 states — if the situation resembles any negative example in the taxonomy, apply that lesson and consider adjusting the classification or confidence accordingly.

Assign confidence (Low / Medium / High) with any automatic caps from Step 1.

**Justification required.** Classification without justification is invalid output.

### Step 7: Extract Thesis and Score Breadth

Using `references/thesis_patterns.md`:
- Identify which of the 10 thesis types describes the dominant narrative (or Vague/undefined if none fit)
- Write a 1–2 sentence narrative fingerprint

Score Cross-Source Breadth (Tier 1 only, categorical, using `references/source_weighting.md`):
- Count how many of the 3 Tier 1 sources had meaningful activity (a source counts as active if it returned at least 2 substantive posts within the lookback horizon)
- Apply categorical scale: 0–1 = Insufficient, 2 = Partial, 3 = Full

**Justification required for Dominant Thesis.** Use "Vague / undefined thesis" if no recognizable pattern — do not fabricate a category. Classification without justification is invalid output.

### Step 8: Generate Structured Output

Using `references/output_schema.md` as the exact template:
1. Complete all 13 output sections in specified order
2. Apply the waterfall alert tier logic from `references/output_schema.md`
3. Cross-reference IV rank with regime using `references/iv_integration.md` for Field 10
4. Confirm every classification field has its required 1–2 sentence justification
5. Output the formatted result

## Reference Files

- `references/output_schema.md` — output contract (13 fields, null-output path, waterfall alert tier logic)
- `references/source_weighting.md` — source priority tiers, breadth scale, fallback hierarchy
- `references/regime_taxonomy.md` — 5-state taxonomy with named real-world examples
- `references/noise_filter_criteria.md` — pump tells and AI-content detection markers
- `references/temporal_framework.md` — 4 causal scenarios, freshness rating, trigger-context handling
- `references/thesis_patterns.md` — 10 thesis types with options structure implications
- `references/iv_integration.md` — IV rank × regime → options direction guidance
