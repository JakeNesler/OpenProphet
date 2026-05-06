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
