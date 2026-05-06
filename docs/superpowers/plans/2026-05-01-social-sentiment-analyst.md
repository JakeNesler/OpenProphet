# Social Sentiment Analyst Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the `social-sentiment-analyst` skill — a 7-reference-file + SKILL.md skill that detects retail sentiment, trending narratives, and rumor-driven moves across social platforms and outputs a structured 13-field analysis for options trading decisions.

**Architecture:** Pure WebSearch + LLM synthesis. No scripts, no APIs. Eight markdown files total (SKILL.md + 7 reference files). Output is pinned to an exact 13-field schema defined in `output_schema.md`, which is written first as the contract everything else serves.

**Tech Stack:** Markdown only. No code. Verification steps use PowerShell `Select-String` to confirm required sections exist in each file.

**Spec:** `docs/superpowers/specs/2026-05-01-social-sentiment-analyst-design.md`

---

## File Map

| File | Responsibility |
|------|---------------|
| `.claude/skills/social-sentiment-analyst/references/output_schema.md` | Contract: exact 13-field output format, null-output path, alert tier logic |
| `.claude/skills/social-sentiment-analyst/SKILL.md` | Skill entry point: frontmatter, workflow, enforcement language, reference index |
| `.claude/skills/social-sentiment-analyst/references/source_weighting.md` | Source tiers, breadth categorical scale, fallback hierarchy, search yield rating |
| `.claude/skills/social-sentiment-analyst/references/regime_taxonomy.md` | 5-state taxonomy with named positive + negative real-world examples |
| `.claude/skills/social-sentiment-analyst/references/noise_filter_criteria.md` | Classic pump tells + AI-generated content markers with named examples |
| `.claude/skills/social-sentiment-analyst/references/temporal_framework.md` | 4 causal scenarios, freshness rating, trigger-context input handling |
| `.claude/skills/social-sentiment-analyst/references/thesis_patterns.md` | 10 thesis types with options structure implications |
| `.claude/skills/social-sentiment-analyst/references/iv_integration.md` | IV rank × regime → options direction guidance |

---

## Task 1: Create `references/output_schema.md`

**File:** Create `.claude/skills/social-sentiment-analyst/references/output_schema.md`

This is the contract. Write it first. Every other file is written knowing exactly what fields it must populate.

- [ ] **Step 1.1: Create the file with full content**

Create `.claude/skills/social-sentiment-analyst/references/output_schema.md` with this exact content:

```markdown
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
```

- [ ] **Step 1.2: Verify required sections exist**

Run:
```powershell
$file = ".claude\skills\social-sentiment-analyst\references\output_schema.md"
$checks = @("Null-Output Path", "Full Output Format", "Alert Tier Logic", "Enforcement Rule", "Cross-Source Breadth", "Last refreshed")
$checks | ForEach-Object { if (Select-String -Path $file -Pattern $_ -Quiet) { "PASS: $_" } else { "FAIL: $_" } }
```
Expected: 6 PASS lines.

- [ ] **Step 1.3: Commit**

```bash
git add .claude/skills/social-sentiment-analyst/references/output_schema.md
git commit -m "feat: add social-sentiment-analyst output_schema reference"
```

---

## Task 2: Create `SKILL.md`

**File:** Create `.claude/skills/social-sentiment-analyst/SKILL.md`

- [ ] **Step 2.1: Create the file with full content**

Create `.claude/skills/social-sentiment-analyst/SKILL.md` with this exact content:

```markdown
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

If Search Yield = Low AND fewer than 5 substantive posts found across all tiers:
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
3. Classify Causal Scenario (1–4) using identification criteria in the reference file
4. If trigger directional bias was provided, check chatter sentiment alignment — flag explicit divergence

**If no trigger timestamp:**
→ Record "Skipped — no trigger context provided (manual lookup)" in Field 4

Assess Freshness (Active / Recent / Stale) from most recent substantive post timestamps.

### Step 6: Regime Classification

Using `references/regime_taxonomy.md`, classify regime state and trajectory.

Pattern-match to named examples in the taxonomy. Pay special attention to negative examples — if the situation resembles a case that looked like Early but was Noise, apply that lesson and downgrade confidence.

Assign confidence (Low / Medium / High) with any automatic caps from Step 1.

**Justification required.** Classification without justification is invalid output.

### Step 7: Extract Thesis and Score Breadth

Using `references/thesis_patterns.md`:
- Identify which of the 10 thesis types describes the dominant narrative (or Vague/undefined if none fit)
- Write a 1–2 sentence narrative fingerprint

Score Cross-Source Breadth (Tier 1 only, categorical, using `references/source_weighting.md`):
- Count how many of the 3 Tier 1 sources had meaningful activity
- Apply categorical scale: 0–1 = Insufficient, 2 = Partial, 3 = Full

### Step 8: Generate Structured Output

Using `references/output_schema.md` as the exact template:
1. Complete all 13 fields in specified order
2. Apply alert tier logic from `references/output_schema.md`
3. Cross-reference IV rank with regime using `references/iv_integration.md` for Field 10
4. Confirm every classification field has its required justification
5. Output the formatted result

## Reference Files

- `references/output_schema.md` — output contract (13 fields, null-output path, alert tier logic)
- `references/source_weighting.md` — source priority tiers, breadth scale, fallback hierarchy
- `references/regime_taxonomy.md` — 5-state taxonomy with named real-world examples
- `references/noise_filter_criteria.md` — pump tells and AI-content detection markers
- `references/temporal_framework.md` — 4 causal scenarios, freshness rating, trigger-context handling
- `references/thesis_patterns.md` — 10 thesis types with options structure implications
- `references/iv_integration.md` — IV rank × regime → options direction guidance
```

- [ ] **Step 2.2: Verify required sections exist**

```powershell
$file = ".claude\skills\social-sentiment-analyst\SKILL.md"
$checks = @("name: social-sentiment-analyst", "Enforcement Rule", "Classifications without justification are invalid output", "Step 1:", "Step 8:", "Null-Output Check", "references/output_schema.md")
$checks | ForEach-Object { if (Select-String -Path $file -Pattern $_ -Quiet) { "PASS: $_" } else { "FAIL: $_" } }
```
Expected: 7 PASS lines.

- [ ] **Step 2.3: Commit**

```bash
git add .claude/skills/social-sentiment-analyst/SKILL.md
git commit -m "feat: add social-sentiment-analyst SKILL.md with 8-step workflow"
```

---

## Task 3: Create `references/source_weighting.md`

**File:** Create `.claude/skills/social-sentiment-analyst/references/source_weighting.md`

- [ ] **Step 3.1: Create the file with full content**

```markdown
---
Last refreshed: 2026-Q2
---

# Source Weighting

Defines source priority tiers, cross-source breadth measurement, fallback hierarchy, and search yield rating.

## Priority Tiers

### Tier 1 — High Signal (Primary Breadth Measurement)

These three sources form the breadth scale. Breadth is measured against Tier 1 only.

| Source | Why high signal | Primary subreddits / areas |
|--------|----------------|---------------------------|
| **Reddit** | Highest volume retail options discussion; WSB drives real retail flow | r/wallstreetbets, r/options, r/stocks, r/investing, r/CryptoCurrency |
| **X / Twitter** | Fastest signal propagation; breaking rumors hit here first; financial influencers | Verified financial accounts, trending $tickers, options traders |
| **StockTwits** | Purpose-built ticker-level sentiment; high signal-to-noise vs. general Twitter | Ticker pages, trending stocks |

### Tier 2 — Medium Signal

| Source | Why medium signal | Notes |
|--------|------------------|-------|
| **YouTube** | Large-following influencer coverage creates predictable retail flow; slower than Tier 1 | Track view count and recency; a 500K-view video pumping a name matters |
| **Crypto Twitter + Telegram** | Essential for crypto-correlated equities | Use for COIN, MSTR, MARA, RIOT, CLSK and direct crypto options; Bitcoin narrative propagates here first |

### Tier 3 — Confirmation / Slower Signal

| Source | Why Tier 3 | Notes |
|--------|-----------|-------|
| **Seeking Alpha comments** | Fundamentals-aware retail; slower but higher quality thesis discussion | Good for confirming narrative has reached more serious retail |
| **InvestorHub** | Community platform; mid-signal | Supplement Tier 1 and 2 |
| **Unusual Whales / FlowAlgo discussion** | Flow aggregator discussion often contains rumor-driven theses | Reflexivity risk: check if narrative references the flow itself (Scenario 3 tell) |
| **Substack / newsletters** | Tracks which names are getting newsletter pumps | Pump here = narrative has mainstream retail reach |

### Special Cases

| Source | Weight | When to use |
|--------|--------|------------|
| **Discord** | Medium-Tier 2 | Scout where accessible; many active options communities migrated here from Reddit; harder to search via WebSearch |
| **4chan /biz/** | Very low | Small-cap pumps only; extremely high noise; flag only, never cite as primary signal |
| **TruthSocial** | Context-specific | DJT ticker and politically-tied names only (defense contractors, tariff-exposed names during policy events) |

## Cross-Source Breadth Scale

Breadth is measured against Tier 1 sources only (Reddit, X/Twitter, StockTwits). The scale is **categorical, not continuous**. Tier 1 has exactly three sources.

| Breadth Level | Tier 1 Sources Active | Interpretation |
|---------------|----------------------|----------------|
| **Insufficient** | 0–1 of 3 | Likely single-community echo chamber or coordinated single-platform pump. Do not classify as genuine cross-source signal. |
| **Partial** | 2 of 3 | Developing signal, not yet confirmed. Can qualify for Standard alert tier. |
| **Full** | 3 of 3 | Genuine cross-source coherence. Required for Emergency alert tier. Your father's Bitcoin case had Full breadth across crypto Tier 1 equivalents. |

**Important:** A signal appearing on 10 Tier 2/3 sources but only 1 Tier 1 source is still Insufficient breadth. Tier 2/3 sources confirm and amplify; they do not substitute for Tier 1 coherence.

## Source Fallback Hierarchy

WebSearch visibility into social platforms degrades as platforms change their access policies. When Tier 1 sources return thin results, apply these substitutions and **lower Search Yield accordingly**:

| Tier 1 Source Degraded | Partial Substitute | Substitute Quality |
|------------------------|-------------------|--------------------|
| Reddit thin results | Seeking Alpha comments + InvestorHub | Weaker; more fundamentals-aware, less retail-sentiment |
| X/Twitter thin results | Financial YouTube + Substack for same narrative | Slower signal; check post dates carefully |
| StockTwits thin results | Reddit options-specific subreddits | Acceptable substitute; same retail crowd |

If all three Tier 1 sources are returning thin results simultaneously, assess Search Yield as Low regardless of Tier 2/3 activity.

## Search Yield Rating

Assess at the end of Step 2 (parallel WebSearch) and report in Field 2 of every output.

| Rating | Criteria |
|--------|---------|
| **High** | All 3 Tier 1 sources returned results with timestamps; 10+ substantive posts total |
| **Medium** | 2 of 3 Tier 1 sources returned results; or all 3 but thin (5–10 posts); or timestamp data partially missing |
| **Low** | 0–1 Tier 1 sources returned meaningful results; fewer than 5 substantive posts total |

**Low Search Yield caps Overall Confidence at Medium** regardless of other factors.

## Source Credibility Within Tiers

Not all posts within a tier carry equal weight. Within Tier 1:

**Higher credibility signals:**
- Posts with specific data (float size, short interest %, specific catalyst date, option strike/expiry)
- Accounts with posting history in this sector/ticker (not first-time posters)
- Skeptical or nuanced posts alongside bullish ones (organic chatter has bears)
- Posts that appeared before any price movement or flow detection

**Lower credibility signals:**
- Posts with no thesis (just ticker + rocket emoji)
- Accounts with no history
- Posts that appeared after unusual flow was publicly noted
- Posts referencing flow/options activity as their primary evidence (Scenario 3 reflexivity tell)
```

- [ ] **Step 3.2: Verify required sections**

```powershell
$file = ".claude\skills\social-sentiment-analyst\references\source_weighting.md"
$checks = @("Tier 1", "Tier 2", "Tier 3", "Cross-Source Breadth Scale", "Insufficient", "Partial", "Full", "Fallback Hierarchy", "Search Yield Rating", "Last refreshed")
$checks | ForEach-Object { if (Select-String -Path $file -Pattern $_ -Quiet) { "PASS: $_" } else { "FAIL: $_" } }
```
Expected: 10 PASS lines.

- [ ] **Step 3.3: Commit**

```bash
git add .claude/skills/social-sentiment-analyst/references/source_weighting.md
git commit -m "feat: add social-sentiment-analyst source_weighting reference"
```

---

## Task 4: Create `references/regime_taxonomy.md`

**File:** Create `.claude/skills/social-sentiment-analyst/references/regime_taxonomy.md`

This is the most critical reference file. It must contain named real-world examples — not abstract templates.

- [ ] **Step 4.1: Create the file with full content**

```markdown
---
Last refreshed: 2026-Q2
Examples current as of: 2026-Q2 (oldest case: GME Jan 2021)
---

# Regime Taxonomy

Defines the 5-state sentiment regime cycle with named positive and negative examples. Pattern-match incoming signals to these examples. Pay particular attention to negative examples — they illustrate the most common classification mistakes.

## Regime States

### State 1: Pre-emergent / Noise

**Definition:** Low volume, uncoordinated, absent or incoherent thesis. Often originates from known pump accounts, bots, isolated community chatter, or recycled memes with no new catalyst. Not a tradeable signal.

**Distinguishing features:**
- Mention volume low and flat (no acceleration)
- Cross-source breadth: Insufficient (0–1/3 Tier 1)
- Thesis absent, vague ("moon soon"), or recycled from prior failed pump
- Account quality low (new accounts, pump history)
- Skeptics absent not because bulls are unanimous but because the thread has no engagement

**Positive example — correctly classified as Noise:**
AMC, September 2022 (case date: Sep 15–30, 2022). After the AMC preferred share conversion, sporadic WSB chatter resumed about "another squeeze." Posts recycled the original "apes" meme vocabulary with no new thesis beyond hope and momentum references to the 2021 event. StockTwits activity was flat. X/Twitter had only a handful of posts from known meme-stock accounts. Cross-source breadth: Insufficient (1/3). No price follow-through. Correct classification: Pre-emergent/Noise. Outcome: AMC continued declining.

**Negative example — looked like Noise, was actually Early:**
NVDA, October–November 2022 (case date: Oct 20 – Nov 15, 2022). Initial low-volume chatter on r/stocks about AI-driven server demand was easy to dismiss as noise — mention volume was modest and tone was measured rather than manic. However, thesis quality was high (specific product cycle details, data center capex arguments) and the same thesis appeared across r/stocks, X/Twitter analyst accounts, and Seeking Alpha simultaneously. Cross-source breadth was Partial (2/3 Tier 1). **Lesson:** When thesis quality is high and cross-source is ≥2/3, do not dismiss as noise based on volume alone. This was Early/Gaining with quiet velocity.

---

### State 2: Early / Gaining Traction

**Definition:** Rising mention velocity, cross-source coherence beginning to form (≥2 Tier 1 sources), thesis becoming articulate and specific. Entry window for momentum plays. This is the highest-conviction entry point for long premium if IV is low.

**Distinguishing features:**
- Velocity accelerating from baseline (posts per hour increasing)
- Cross-source breadth: Partial or Full (≥2/3 Tier 1)
- Thesis is specific — references dates, data, catalyst, or mechanism (not just "moon")
- Some skeptics present (organic, not coordinated)
- No mainstream media coverage yet

**Positive example 1 — correctly classified as Early:**
SMCI (Super Micro Computer), January–February 2024 (case date: Jan 15 – Feb 8, 2024). AI infrastructure thesis building simultaneously on r/stocks and X/Twitter, with specific discussion of SMCI's GPU server rack business and Nvidia partnership. StockTwits began showing activity by week 2. Posts cited specific revenue estimates and data center buildout timelines. Cross-source breadth reached Full (3/3) within 2 weeks. Causal scenario: Narrative-Led (chatter began before the institutional analyst upgrades). Correct classification: Early → eventually Peak during earnings. Long call holders who entered Early saw significant gains before IV crush at Peak.

**Positive example 2 — correctly classified as Early:**
GME, January 11–18, 2021 (case date: Jan 11–18, 2021). Short squeeze thesis on WSB was becoming articulate and data-driven — specific short interest percentages, float analysis, and gamma squeeze mechanics were being discussed. X/Twitter (then Twitter) was picking up the WSB narrative. StockTwits showed rising activity. Cross-source breadth: Full (3/3). Thesis was specific and falsifiable. Correct classification: Early/Gaining. The following week brought Peak/Saturating as CNBC coverage began.

**Negative example — looked like Early, was coordinated pump:**
BBBY, September 2022 (case date: Sep 1–15, 2022). After Ryan Cohen's exit, a brief spike in WSB and Twitter chatter attempted to revive the squeeze narrative. Posts reached Partial breadth (2/3) but thesis quality was weak — "squeeze incoming" without specific short interest data, float analysis, or catalyst. Account ages skewed new. No meaningful StockTwits activity. Coordinated pump tell: the same generic phrasing appeared across multiple posts within hours. **Lesson:** Partial breadth alone is not sufficient; thesis quality matters. This was a coordinated pump attempt masquerading as organic Early-stage, and it failed within 5 days.

---

### State 3: Peak / Saturating

**Definition:** Mainstream visibility, parabolic mention volume, everyone is aware. The narrative is likely priced in. Entry window for premium selling and fade plays. Long premium positions entered here face IV crush risk.

**Distinguishing features:**
- CNBC, MarketWatch, or Google Trends coverage
- Mention velocity parabolic (10x+ baseline)
- Cross-source breadth: Full (3/3) plus Tier 2/3 saturation
- Thesis now widely repeated without new information added
- Discord and YouTube influencers covering the name
- New retail accounts flooding in (account quality drops)

**Positive example 1:**
GME, January 27–28, 2021 (case date: Jan 27–28, 2021). CNBC ran live coverage. WSB grew from 2M to 6M members in days. Robinhood restricted trading. Every financial media outlet covered it. Mention velocity was parabolic. StockTwits, Twitter, Reddit, YouTube — Full breadth plus Tier 2/3 saturation. Options IV was at extreme levels. Short premium strategies (e.g., selling covered calls, put spreads) were appropriate for new positions. Correct classification: Peak/Saturating.

**Positive example 2:**
MSTR, November 2024 (case date: Nov 10–22, 2024). During Bitcoin's ATH run, MSTR's BTC accumulation strategy saturated financial media. Michael Saylor appearing on CNBC repeatedly. Reddit crypto-adjacent subreddits, X/Twitter crypto accounts, and StockTwits all at Full breadth. Mainstream Bloomberg and Reuters coverage. Long premium holders who bought during Early (October–early November) were sitting on large gains; new entrants at Peak faced unfavorable risk/reward. IV was elevated. Correct classification: Peak/Saturating.

---

### State 4: Decaying-elevated

**Definition:** Price still elevated from the run-up, but original chatter slowing and thesis fragmenting. Holders have not capitulated. **This is the IV crush danger zone for long premium.** Do not initiate new long premium positions here.

**Distinguishing features:**
- Price above pre-narrative range but velocity declining
- Original thesis fragmenting into competing narratives ("hold for $1000" vs "take profits" vs "it's over")
- Skeptics reappearing and gaining engagement
- Tier 1 breadth may still be Full but post quality and thesis coherence declining
- New information absent — posts are reactions, not new analysis
- IV still elevated but beginning to compress

**Positive example:**
GME, February 4–20, 2021 (case date: Feb 4–20, 2021). Price was $40–100 (still above the pre-squeeze $5–10 range). WSB chatter had fragmented: "hold for $1000" posts competed with "I took profits" confession posts and "this is over" reality-check threads. StockTwits showed declining velocity. The original short-squeeze thesis (specific short interest %, gamma ramp) had been replaced by vague "just hold" messaging. IV was compressing from peak levels. Long premium holders entering here got hit by both directional uncertainty and vol crush. **Correct classification: Decaying-elevated.** Short premium or cash was appropriate.

**Key diagnostic for Decaying-elevated vs. Peak:** In Peak, the narrative is uniform and amplifying. In Decaying-elevated, the narrative has fractured — you can find both "this is the top" and "hold for X" posts simultaneously, and the bears are getting upvotes.

---

### State 5: Decayed / Capitulated

**Definition:** Narrative dead. Price has returned to pre-rumor range or consolidated at a lower level. Chatter minimal. Watch for mean reversion opportunity or fresh catalyst reset. IV collapsing — defined-risk structures only.

**Distinguishing features:**
- Minimal substantive chatter across all tiers
- Price at or near pre-narrative baseline
- Any remaining posts are "I told you so" or "when next squeeze" with no engagement
- IV collapsing toward historical norms
- Cross-source breadth: Insufficient (0–1/3)

**Positive example 1:**
GME, March 2021 (case date: Mar 1–31, 2021). Price settled around $40–60. WSB still had GME posts but engagement was low and thesis was absent. StockTwits quiet. X/Twitter coverage only in "what happened?" retrospectives. IV collapsing. Mean reversion to pre-squeeze fundamentals ($5–15) was the eventual direction. Correct classification: Decayed/Capitulated.

**Positive example 2:**
MARA/RIOT, December 2021 – January 2022 (case date: Dec 2021 – Jan 2022). After Bitcoin's peak at ~$69K, the crypto mining stock narrative died rapidly. Retail chatter on all platforms collapsed. MARA and RIOT lost 70%+ from peak over the following months. IV compressed significantly. Correct classification: Decayed/Capitulated. New long premium at this stage without a fresh catalyst had asymmetric downside.

---

## Classification Checklist

Before finalizing regime classification, verify:

- [ ] Have I checked velocity trend (accelerating, flat, decelerating)?
- [ ] Have I scored breadth categorically (not counted raw mentions)?
- [ ] Have I assessed thesis quality, not just volume?
- [ ] Have I checked for noise filter red flags that would override apparent Early classification?
- [ ] Have I pattern-matched to at least one named example above?
- [ ] Is my trajectory assessment (Stable / Transitioning-up / Transitioning-down) consistent with the velocity trend?
- [ ] Have I written a 1–2 sentence justification citing specific evidence?
```

- [ ] **Step 4.2: Verify required sections**

```powershell
$file = ".claude\skills\social-sentiment-analyst\references\regime_taxonomy.md"
$checks = @("Pre-emergent", "Early / Gaining", "Peak / Saturating", "Decaying-elevated", "Decayed / Capitulated", "GME", "SMCI", "MSTR", "Negative example", "Last refreshed", "Classification Checklist")
$checks | ForEach-Object { if (Select-String -Path $file -Pattern $_ -Quiet) { "PASS: $_" } else { "FAIL: $_" } }
```
Expected: 11 PASS lines.

- [ ] **Step 4.3: Commit**

```bash
git add .claude/skills/social-sentiment-analyst/references/regime_taxonomy.md
git commit -m "feat: add social-sentiment-analyst regime_taxonomy with named examples"
```

---

## Task 5: Create `references/noise_filter_criteria.md`

**File:** Create `.claude/skills/social-sentiment-analyst/references/noise_filter_criteria.md`

- [ ] **Step 5.1: Create the file with full content**

```markdown
---
Last refreshed: 2026-Q2
Examples current as of: 2026-Q2
---

# Noise Filter Criteria

Criteria for distinguishing organic retail chatter from coordinated pumps and AI-generated content. Red flags do not automatically disqualify a signal — they lower confidence and must appear in Field 11 (Red Flags) of the output. Three or more red flags with no cross-source coherence = classify Pre-emergent/Noise regardless of mention volume.

## Classic Coordinated Pump Tells

### 1. Identical or near-identical phrasing across posts
Multiple posts using the same phrase, sentence structure, or hashtag within a short window. Not just the same ticker — the same specific wording. Example: four posts in two hours all containing the phrase "massive catalyst incoming this week" with different usernames.

### 2. New or low-history accounts posting exclusively about this ticker
Accounts with fewer than 3 months of history, or accounts with thousands of posts but only in pump/penny-stock threads. Legitimate retail traders have diverse posting history.

### 3. Recycled meme templates from prior failed pumps
Visual memes or narrative framing directly copied from a previous pump attempt on the same ticker or a similar ticker. Example: using GME squeeze meme templates for an unrelated stock with no float or short-interest similarity.

### 4. Suspicious unanimity — absence of organic skeptics
Organic chatter always includes skeptics, bears, and "I don't see it" responses. A thread with 50 bullish posts and zero pushback is a red flag. Real communities argue. Coordinated pumps don't allow dissent to gain traction.

### 5. Volume without substance
High post count, zero specific thesis. Posts contain only: ticker symbol, emoji (🚀💎🙌), generic "to the moon" framing, or references to prior gains — but no float data, no catalyst date, no short interest figures, no specific product or event. Volume ≠ signal.

### 6. Manufactured urgency without rationale
"Last chance to buy before [vague date]" or "this is it, the moment is NOW" without a specific catalyst or date. Organic urgency cites a specific event (FDA date, earnings, regulatory decision). Manufactured urgency is deliberately vague.

### 7. Timing cluster: burst posting
All posts appearing within a 30–60 minute window, then silence. Organic community discussion spreads over hours and days. Coordinated pumps often have a push window.

## AI-Generated Content Markers (Post-2024)

AI-generated pump posts bypass classic copy-paste detection. They are not identical — they are generated fresh by an LLM instructed to write a bullish post. Markers:

### 1. Overly polished prose for the platform
WSB, 4chan /biz/, and StockTwits do not write in measured, grammatically correct paragraphs. If a post on WSB reads like a hedge fund pitch deck summary, it was not written by a WSB user. Marker: formal sentence structure, complete sentences, no abbreviations or slang.

### 2. Generic bullish framing without specific numbers or named sources
"The risk/reward profile appears favorable given the upcoming catalyst" — this sounds rigorous but cites nothing. Organic retail posts either cite specific data OR make wild unsubstantiated claims. The LLM-generated middle ground (sounds analytical, cites nothing specific) is a tell.

### 3. Suspicious uniformity of post length across multiple accounts
If five posts from different accounts all run 150–200 words with similar structure (brief setup, bullish argument, call to action), they were likely generated by the same prompt. Length uniformity is a statistical tell that manual posting wouldn't produce.

### 4. Absence of platform-native slang, memes, or in-jokes
Each platform has a vocabulary: WSB says "tendies," "retard," "YOLO," "autist." StockTwits has its own shorthand. Discord trading communities have server-specific memes. An account supposedly posting from WSB for 2 years but using none of the platform vocabulary was not written by a WSB user.

### 5. Hedged but bullish tone
LLMs are trained to be measured and to include hedges ("of course, do your own research," "this is not financial advice," "there are risks"). Organic pump posts are not hedged — they are maximally confident. A post that is 80% bullish but ends with "as always, assess your own risk tolerance" was likely written by an LLM.

### 6. Absence of any ticker-specific technical observations
Organic traders who follow a stock notice specific things: a specific chart pattern, an unusual spread, a specific recent SEC filing, a specific analyst note. AI-generated posts pump the general narrative without demonstrating any knowledge of the specific ticker's current state.

**Named example (AI-generated pump pattern, 2025):**
Several small-cap biotech plays in Q1–Q2 2025 showed this pattern: 8–12 accounts across Reddit and StockTwits posting 160–220 word analyses of an upcoming FDA PDUFA date. Each post was unique (different wording) but shared: polished grammar, no WSB slang, generic risk disclosure language at the end, no specific clinical trial data cited. The posts appeared in a 90-minute window. Cross-source breadth was Insufficient (StockTwits only, no X/Twitter traction). All were correctly classified as coordinated AI-generated pump; none of the stocks moved materially.

## Red Flag Scoring

Count the number of red flags (from Classic + AI-Generated sections above) that fire on the collected content.

| Red Flag Count | Impact on Classification |
|----------------|-------------------------|
| 0–1 | Note in Red Flags field; proceed with analysis |
| 2 | Lower confidence by one level; flag in output |
| 3+ with no cross-source coherence | Classify Pre-emergent/Noise regardless of volume |
| 3+ WITH cross-source coherence | Classify with Low confidence; flag prominently |

**Never suppress Red Flags from the output.** Even if the signal passes noise filter and gets classified as Early, the Red Flags field must list what fired. This creates an auditable record.
```

- [ ] **Step 5.2: Verify required sections**

```powershell
$file = ".claude\skills\social-sentiment-analyst\references\noise_filter_criteria.md"
$checks = @("Classic Coordinated Pump Tells", "AI-Generated Content Markers", "identical or near-identical", "Overly polished prose", "platform-native slang", "Red Flag Scoring", "Named example", "Last refreshed")
$checks | ForEach-Object { if (Select-String -Path $file -Pattern $_ -Quiet) { "PASS: $_" } else { "FAIL: $_" } }
```
Expected: 8 PASS lines.

- [ ] **Step 5.3: Commit**

```bash
git add .claude/skills/social-sentiment-analyst/references/noise_filter_criteria.md
git commit -m "feat: add social-sentiment-analyst noise_filter_criteria with AI-content detection"
```

---

## Task 6: Create `references/temporal_framework.md`

**File:** Create `.claude/skills/social-sentiment-analyst/references/temporal_framework.md`

- [ ] **Step 6.1: Create the file with full content**

```markdown
---
Last refreshed: 2026-Q2
---

# Temporal Framework

Defines how to handle trigger-context inputs, classify causal scenarios, rate narrative freshness, and identify temporal sequence from WebSearch results.

## Trigger-Context Input Handling

The calling system may pass trigger context when invoking the skill. Handle each input as follows:

| Input | If provided | If missing |
|-------|-------------|------------|
| **Trigger timestamp** | Use as temporal anchor for causal scenario classification. Compare to narrative inflection point. | Note "No trigger context provided — causal scenario classification skipped" in Field 4. Do not guess or estimate. |
| **Trigger directional bias** (bullish/bearish/neutral) | Cross-check against dominant chatter sentiment. Explicitly flag any divergence (e.g., "trigger is bullish but chatter is net bearish — divergence flagged"). | Note as unavailable in Field 4 justification. |
| **Lookback horizon** | Use this as the WebSearch time window. | Default to 7 days. |
| **No trigger context at all** | If none of the above are provided (manual lookup), note "Manual lookup — causal scenario classification skipped" in Field 4. The skill still produces all other fields. |

**Never silently downgrade quality.** If trigger context is missing, the output must state this explicitly. A reader should always know whether causal scenario was classified or skipped, and why.

## Four Causal Scenarios

### Scenario 1: Narrative-Led Flow

**What it means:** Chatter was building hours-to-days before the trigger fired. The unusual flow, breakout, or price alert is the result of the retail narrative reaching a tipping point. **This is the highest-conviction setup for momentum continuation** — retail participation is already established and thesis-driven.

**Identification criteria:**
- Narrative inflection point is **6+ hours before** the trigger timestamp
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
```

- [ ] **Step 6.2: Verify required sections**

```powershell
$file = ".claude\skills\social-sentiment-analyst\references\temporal_framework.md"
$checks = @("Trigger-Context Input Handling", "Scenario 1", "Scenario 2", "Scenario 3", "Scenario 4", "Freshness Rating", "Reflexivity Tells", "Never fabricate", "Last refreshed")
$checks | ForEach-Object { if (Select-String -Path $file -Pattern $_ -Quiet) { "PASS: $_" } else { "FAIL: $_" } }
```
Expected: 9 PASS lines.

- [ ] **Step 6.3: Commit**

```bash
git add .claude/skills/social-sentiment-analyst/references/temporal_framework.md
git commit -m "feat: add social-sentiment-analyst temporal_framework reference"
```

---

## Task 7: Create `references/thesis_patterns.md`

**File:** Create `.claude/skills/social-sentiment-analyst/references/thesis_patterns.md`

- [ ] **Step 7.1: Create the file with full content**

```markdown
---
Last refreshed: 2026-Q2
---

# Thesis Patterns

Maps the 10 recognizable retail thesis types to their optimal options structure implications. The dominant thesis should be extracted from the content of the top posts, not from the ticker's general category. A tech stock can be in an "M&A speculation" thesis; a biotech can be in a "short squeeze" thesis.

## Thesis Identification

Read the top 10–15 most-engaged posts. The dominant thesis is the specific argument the most posts are making. If no single argument has plurality, use "Vague / undefined thesis."

## The 10 Thesis Types

### 1. Short Squeeze Setup
**Description:** Community discussing high short interest, increasing borrow cost, low float, and potential for a squeeze trigger. Posts reference specific short interest percentages, days-to-cover, and gamma ramp mechanics.
**Identification keywords:** "short interest," "borrow rate," "float," "gamma squeeze," "short ladder attack," "FTDs," "days to cover"
**Options implication:** High IV is likely already embedded or will be shortly. Spreads are preferable to naked long calls. Gamma risk is real — be aware of expiration dynamics. If IV is already elevated (IV rank >60), selling premium into the anticipated squeeze is viable with defined risk. Watch for halt risk in small-caps.
**Duration:** 1–10 days for short-duration squeezes; multi-week for sustained institutional shorts.

### 2. Earnings Catalyst
**Description:** Speculation about an upcoming earnings beat, miss, or guidance change. May include whisper numbers, channel checks, credit card data leaks, or insider tone analysis.
**Identification keywords:** "earnings," "beat," "miss," "guidance," "whisper number," "EPS estimate," "[quarter] results"
**Options implication:** Check IV rank before any position — earnings are the most common IV crush event. If IV rank is low (<30), long straddle or directional calls/puts are viable. If IV rank is high (>60), selling premium via iron condor or credit spread is typically better risk/reward. Never buy naked long premium into high IV earnings without strong directional conviction backed by non-social signals.
**Duration:** Options should expire shortly after earnings; avoid holding through the event with long premium in high IV.

### 3. FDA / Regulatory Binary
**Description:** An upcoming FDA decision, PDUFA date, or regulatory approval/rejection. Common in biotech and pharma. The outcome is binary and unpredictable.
**Identification keywords:** "FDA," "PDUFA," "approval," "rejection," "trial results," "advisory committee," "NDA," "BLA"
**Options implication:** Do not pick direction on binary events without exceptional edge. Straddle or strangle (long both sides) captures the move regardless of direction. IV will be extreme — calculate the break-even move required by the options premium and assess whether the historical move magnitude exceeds it. Defined risk is mandatory; halts are possible.
**Duration:** Typically resolved in one session; IV collapses immediately post-decision.

### 4. M&A Speculation
**Description:** Acquisition rumor, strategic review, leveraged buyout speculation, or private equity interest. Retail is speculating that the company will be bought out at a premium.
**Identification keywords:** "acquisition," "buyout," "takeover," "merger," "PE interest," "strategic review," "rumor," "bid"
**Options implication:** Long calls (out-of-the-money) capture upside if the deal is real. Watch for IV spike on any confirmation leak — if IV is already elevated, the market may have partially priced the rumor. Avoid puts unless the rumor is clearly false and you expect mean reversion. Risk: if the rumor is baseless, the stock returns to pre-rumor price quickly.
**Duration:** Rumored deals can resolve in days (quick denial) to months (extended strategic review).

### 5. Activist Investor / 13D Speculation
**Description:** Chatter about an activist investor taking or building a position, upcoming board changes, or demands for strategic alternatives. Often surfaces from 13D/13G filing analysis or informed speculation.
**Identification keywords:** "activist," "13D," "board seat," "shareholder letter," "strategic alternatives," specific activist fund names (Elliott, Starboard, Icahn, etc.)
**Options implication:** Multi-month thesis arc — do not use near-term options. Longer-dated calls (3–6 months out) provide time for the activist campaign to develop. IV crush risk is lower than binary events because activist campaigns unfold over time. Consider LEAPS for high-conviction cases.
**Duration:** Activist campaigns typically run 3–12 months from initial filing to resolution.

### 6. Bankruptcy / Restructuring
**Description:** Speculation about financial distress, debt default, missed payments, bankruptcy filing, or restructuring. Retail often incorrectly buys calls on "bottom-fishing" or "turnaround" theses.
**Identification keywords:** "bankruptcy," "Chapter 11," "debt," "default," "restructuring," "liquidity," "going concern," "dilution"
**Options implication:** Puts and put spreads are the appropriate structure. Halt risk is extreme — options may become worthless overnight if a halt occurs around a filing. Defined risk is mandatory. Do not buy calls on bankruptcy speculation regardless of how compelling the "bottom" looks; retail is historically wrong on these. Even in Chapter 11 reorganizations, equity often goes to zero. If already in a halt, do not open new positions.
**Duration:** Can resolve in days (liquidation) to years (reorganization).

### 7. Sector Theme Rotation
**Description:** A macro-driven narrative pushing an entire sector rather than a specific catalyst on one name. The stock is rising because "AI," "defense," "energy transition," or "reshoring" is in rotation.
**Identification keywords:** Sector terms without company-specific catalysts; "riding the [theme] wave," "all [sector] stocks are moving," references to ETFs or peer companies moving together
**Options implication:** Lower per-name conviction — the individual stock is a sector proxy. Use longer duration, smaller size per name, and consider ETF options instead of single-name options to reduce idiosyncratic risk. IV tends to be lower than event-driven plays.
**Duration:** Sector themes can last weeks to months; individual stock participation varies.

### 8. Crypto-Correlated
**Description:** The stock price is driven by Bitcoin or Ethereum narrative rather than company-specific news. Common for COIN, MSTR, MARA, RIOT, CLSK, and other crypto-adjacent equities.
**Identification keywords:** References to BTC/ETH price, "Bitcoin proxy," "MSTR is just leveraged Bitcoin," "miners move with hash rate," specific Bitcoin price targets driving equity price targets
**Options implication:** Adjust IV expectations for crypto-level volatility. Movements are non-linear — MSTR can move 2–5x the BTC percentage move due to leverage embedded in the balance sheet. Check BTC/ETH trend independently before taking a position. Crypto sentiment from Telegram and X/Twitter is Tier 2 for these names (not just supplementary). Halts are unlikely but crypto markets trade 24/7, creating gap risk on Monday opens.
**Duration:** Follows BTC/ETH cycle timing; can be intraday to multi-week.

### 9. Political / Policy-Tied
**Description:** The stock's move is driven by political statements, policy announcements, tariff news, or regulatory decisions with political dimensions. DJT is the canonical example, but defense contractors, tariff-exposed manufacturers, and pharmaceutical pricing names also qualify.
**Identification keywords:** Politician names, "tariff," "executive order," "policy," "regulation," campaign-related terms, TruthSocial references
**Options implication:** Event-driven, high IV, unpredictable duration. Defined risk is mandatory — political events can gap violently in either direction. For DJT specifically, TruthSocial is a primary source (Tier 1 equivalent for that ticker). Use credit spreads or iron condors rather than naked premium for new positions. Duration depends on the political cycle — can resolve in hours (statement retracted) or persist for months (policy implementation).
**Duration:** Highly variable; intraday to multi-month.

### 10. Vague / Undefined Thesis
**Description:** Chatter exists but does not fit any of the above patterns. Posts are bullish (or bearish) without a specific, falsifiable thesis. May indicate early noise, a pre-emergent catalyst not yet identifiable, or a coordinated pump with no real thesis.
**Identification criteria:** Cannot identify a specific catalyst, mechanism, or event driving the thesis after reading 10+ posts. Bullishness is general rather than specific.
**Options implication:** Lower conviction. Treat as noise-adjacent. Smaller size if trading at all — no more than 25% of normal position. If cross-source breadth is Insufficient, skip entirely. Do not force-fit to another thesis category to justify a trade.
**Duration:** Unpredictable; resolve by waiting for the thesis to become identifiable (if it does).

## Thesis vs. Regime Interaction

The thesis type shapes what the appropriate trade structure is; the regime state determines timing and conviction:

| Thesis | Best Regime for Entry | Entry Approach |
|--------|----------------------|----------------|
| Short Squeeze | Early (before IV explosion) | Long call spreads; avoid naked longs |
| Earnings | Early or Peak (sell premium) | Direction-dependent or iron condor |
| FDA Binary | Any regime (binary, not cyclical) | Straddle/strangle pre-event |
| M&A Speculation | Early (before IV spike on confirmation) | Long OTM calls |
| Activist / 13D | Early | Long-dated calls or LEAPS |
| Bankruptcy | Peak or Decaying (puts on the slide) | Put spreads, defined risk |
| Sector Theme | Early | Longer-dated directional, smaller size |
| Crypto-Correlated | Early (follow BTC trend independently) | Directional, monitor BTC |
| Political / Policy | Any | Credit spreads, defined risk only |
| Vague / Undefined | Never (skip or minimum size) | If trading: debit spread only |
```

- [ ] **Step 7.2: Verify required sections**

```powershell
$file = ".claude\skills\social-sentiment-analyst\references\thesis_patterns.md"
$checks = @("Short Squeeze", "Earnings Catalyst", "FDA", "M&A Speculation", "Activist", "Bankruptcy", "Sector Theme", "Crypto-Correlated", "Political", "Vague / Undefined", "Last refreshed")
$checks | ForEach-Object { if (Select-String -Path $file -Pattern $_ -Quiet) { "PASS: $_" } else { "FAIL: $_" } }
```
Expected: 11 PASS lines.

- [ ] **Step 7.3: Commit**

```bash
git add .claude/skills/social-sentiment-analyst/references/thesis_patterns.md
git commit -m "feat: add social-sentiment-analyst thesis_patterns reference"
```

---

## Task 8: Create `references/iv_integration.md`

**File:** Create `.claude/skills/social-sentiment-analyst/references/iv_integration.md`

- [ ] **Step 8.1: Create the file with full content**

```markdown
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
```

- [ ] **Step 8.2: Verify required sections**

```powershell
$file = ".claude\skills\social-sentiment-analyst\references\iv_integration.md"
$checks = @("Early / Gaining", "Peak / Saturating", "Decaying-elevated", "Decayed / Capitulated", "IV Rank", "Thesis Override", "FDA Binary", "Bankruptcy", "Field 10", "Last refreshed")
$checks | ForEach-Object { if (Select-String -Path $file -Pattern $_ -Quiet) { "PASS: $_" } else { "FAIL: $_" } }
```
Expected: 10 PASS lines.

- [ ] **Step 8.3: Commit**

```bash
git add .claude/skills/social-sentiment-analyst/references/iv_integration.md
git commit -m "feat: add social-sentiment-analyst iv_integration reference"
```

---

## Task 9: Smoke Test

Run the skill manually on a real ticker to verify the output schema is followed and all 13 fields are produced.

- [ ] **Step 9.1: Invoke the skill**

In a Claude Code session, run:
```
/social-sentiment-analyst

Ticker: $MSTR
Trigger type: manual lookup
Lookback horizon: 7
```

Do not provide trigger timestamp or IV rank — this tests the missing-context handling paths.

- [ ] **Step 9.2: Verify output structure**

Check the output for:
- [ ] Reference Freshness appears as Field 1 (at the top)
- [ ] Search Yield appears as Field 2
- [ ] Regime includes State, Trajectory, Confidence, and a → Justification line
- [ ] Causal Scenario reads "Skipped — no trigger context" (since no timestamp was provided)
- [ ] Freshness includes a → Justification with a timestamp range
- [ ] Dominant Thesis matches one of the 10 types from thesis_patterns.md
- [ ] Source Breakdown lists Tier 1, Tier 2, Tier 3 activity
- [ ] Cross-Source Breadth uses categorical language (Insufficient / Partial / Full), not a raw number
- [ ] IV Rank reads "Not provided — obtain before trading"
- [ ] Options Guidance notes the moderate IV assumption explicitly
- [ ] Red Flags either lists flags or says "None"
- [ ] Alert Tier is one of: Emergency / Standard / Daily Digest / Suppress
- [ ] Overall Confidence is Low / Medium / High with any applicable cap noted

- [ ] **Step 9.3: If any field is missing or malformed**

Identify which reference file governs that field. Edit the relevant file to add clearer instruction. Re-invoke and verify. Commit the fix:

```bash
git add .claude/skills/social-sentiment-analyst/references/<file>.md
git commit -m "fix: clarify [field] output instruction in social-sentiment-analyst [file]"
```

- [ ] **Step 9.4: Fill in acceptance test case in the spec**

Open `docs/superpowers/specs/2026-05-01-social-sentiment-analyst-design.md` and fill in the test case template at the bottom with a real ticker and known outcome (ideally a recent rumor-driven move with a verifiable result). This becomes the benchmark for future skill revisions.

- [ ] **Step 9.5: Final commit when smoke test passes**

```bash
git add .claude/skills/social-sentiment-analyst/
git status  # confirm only skill files, no unintended changes
git commit -m "feat: social-sentiment-analyst skill complete and smoke-tested"
```

---

## Spec Coverage Self-Review

| Spec Requirement | Covered by Task |
|-----------------|----------------|
| 7 reference files | Tasks 1, 3–8 |
| SKILL.md with frontmatter | Task 2 |
| Implementation order (output_schema first) | Task ordering: 1→2→3–8 |
| Enforcement language verbatim | Task 2 (SKILL.md), Task 1 (output_schema.md) |
| 5-state regime taxonomy with named examples | Task 4 |
| Positive + negative named examples | Task 4 (GME, SMCI, MSTR, NVDA, BBBY) |
| AI-generated content markers | Task 5 |
| Classic pump tells | Task 5 |
| Named noise examples | Task 5 (2025 biotech AI-gen pump) |
| 4 causal scenarios with identification criteria | Task 6 |
| Trigger-context input handling (missing context path) | Task 6 |
| Timestamp range guidance (no fabrication) | Task 6 |
| Freshness × Scenario interaction | Task 6 |
| 10 thesis types with options implications | Task 7 |
| Activist/13D and Bankruptcy rows | Task 7 |
| Vague/undefined catch-all row | Task 7 |
| IV rank × regime table | Task 8 |
| Thesis override rules | Task 8 |
| Cross-source breadth categorical scale (0-1/2/3) | Tasks 1, 3 |
| Alert tier logic (Emergency/Standard/Daily/Suppress) | Task 1 |
| Suppress for Decayed and Scenario 4 + Low confidence | Task 1 |
| Reference freshness caps + quarterly headers | Tasks 1, 3–8 (all files) |
| Null-output path (<5 posts) | Task 1 |
| Search yield rating | Tasks 1, 3 |
| Fallback hierarchy for degraded sources | Task 3 |
| Smoke test | Task 9 |
| Acceptance test case template | Noted in spec; user to fill before accepting Task 9 results |
