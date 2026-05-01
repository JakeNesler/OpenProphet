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
| **Full** | 3 of 3 | Genuine cross-source coherence. Required for Emergency alert tier. |

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
| **High** | All 3 Tier 1 sources returned results with timestamps; 10+ substantive posts total across all tiers |
| **Medium** | 2 of 3 Tier 1 sources returned results; or all 3 but thin (5–10 posts across all tiers); or timestamp data partially missing |
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
