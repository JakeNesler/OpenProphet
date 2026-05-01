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

**Negative example — looked like Peak, was actually still Early:**
SMCI (Super Micro Computer), late January 2024 (case date: Jan 22–30, 2024). As SMCI began appearing in broader financial media in late January 2024, an observer might have classified it as Peak/Saturating — Bloomberg mentions, some CNBC segments, moderately elevated volume. However, checking the actual social signals: thesis quality on r/stocks and X/Twitter was still deepening with new data (forward revenue pipeline estimates, NVIDIA partnership expansion, AI server rack capacity numbers) rather than merely repeating. Velocity was still accelerating, not parabolic-then-declining. YouTube and Discord saturation had not occurred. Q4 FY2024 earnings on February 5, 2024 brought a further +30% gap-up — the thesis still had room. **Lesson:** Media mentions alone are not sufficient for Peak classification. Peak requires parabolic velocity AND thesis content that is merely repeating prior information rather than adding new analytical depth. When thesis quality is still deepening, classify as Early with high visibility, not Peak.

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

**Negative example — looked like Decaying-elevated, was actually a secondary Peak forming:**
AMC, June 1, 2021 (case date: Jun 1–3, 2021). After the first AMC squeeze in late May 2021 (AMC peaked around $72 on May 28), "I sold at the top" posts mixed with "hold for $100" posts — a textbook Decaying-elevated fragmentation pattern. However, this was not true thesis dissolution: AMC management announced that day that retail shareholders could participate in a new share offering, reigniting a specific "retail owns this company" narrative with new data. AMC retraced to the upper $60s by June 2. Those who classified June 1 as Decaying-elevated and closed positions missed the secondary peak. **Lesson:** True Decaying-elevated shows the *original mechanism failing* — the short squeeze thesis loses its specific data anchor (short interest %, gamma mechanics). Fragmentation around "sell vs. hold" alone is not sufficient if a new specific catalyst or fresh thesis angle is emerging simultaneously. Check whether any new information is being introduced or whether posts are purely reactive.

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

**Negative example — looked like Decayed, was actually Early (fresh catalyst reset):**
MSTR, early October 2024 (case date: Oct 1–14, 2024). After Bitcoin stagnated sideways from July–September 2024, MSTR social media chatter had gone quiet for weeks. The stock had pulled back significantly from prior highs, StockTwits showed minimal activity, and WSB posts were absent. Surface classification: Decayed/Capitulated. However, Bitcoin was quietly building above $60K driven by US spot ETF inflows and the post-halving supply reduction, and informed accounts on X/Twitter and Telegram were beginning to reconstruct the BTC-accumulation thesis around Saylor's continued purchasing at sub-$60K prices. Thesis quality was high (specific BTC supply data, ETF flow numbers, cost-basis analysis) even at low volume. Cross-source breadth was Insufficient (1/3 Tier 1) but thesis quality was exceptional for the volume. By mid-October MSTR entered confirmed Early/Gaining; the stock went on to 3x by late November. **Lesson:** Low chatter volume alone does not confirm Decayed/Capitulated. When (a) the underlying asset is recovering, (b) low-volume posts have high thesis quality rather than "I told you so" tone, and (c) the accounts are informed-retail rather than pump accounts, you may be at the inflection point of a fresh Early/Gaining cycle. Apply the checklist: if thesis quality is high and velocity is accelerating from zero, classify as Early with Low confidence rather than Decayed.

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
