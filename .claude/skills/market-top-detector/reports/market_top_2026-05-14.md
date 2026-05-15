# Market Top Detector Report — 2026-05-14

**Note on data sourcing:** The bundled Python script returned `403 Legacy Endpoint` from FMP (historical-price endpoints retired Aug 31 2025). Per-component scoring below uses the methodology in `references/market_top_methodology.md` against web-sourced data captured 2026-05-12/14.

---

## Executive Summary

| | |
|---|---|
| **Composite Score** | **64 / 100** |
| **Risk Zone** | **🟥 Red — High Probability of Top** |
| **Risk Budget** | **40–55%** |
| **Strongest Signal** | Component 4 (Breadth Divergence) and Component 6 (Sentiment / Speculation) |
| **Closest Historical Pattern** | Late 2021 / Jan 2022 — narrow leadership at index highs with breadth quietly breaking |

**One-line view:** SPY printed a record 7,444 on 2026-05-13 with VIX at 18 and Put/Call at 0.49 — index strength + low fear + low protection. Underneath, **only 46.52% of S&P 500 stocks are above their 50DMA** while the all-market uptrend ratio sits at **24.62% RED with negative slope** and breadth has dead-crossed (8MA below 200MA). Five of six components are firing yellow-or-worse. This is a 2–8 week tactical top profile.

---

## 6-Component Scorecard

| # | Component | Weight | Component Score (0–100) | Weighted | Status |
|---|---|---|---|---|---|
| 1 | Distribution Day Count | 25% | 55 | 13.75 | Yellow-Orange |
| 2 | Leading Stock Health | 20% | 65 | 13.00 | Orange |
| 3 | Defensive Sector Rotation | 15% | 70 | 10.50 | Orange |
| 4 | Market Breadth Divergence | 15% | 85 | 12.75 | Red |
| 5 | Index Technical Condition | 15% | 35 | 5.25 | Yellow |
| 6 | Sentiment & Speculation | 10% | 85 | 8.50 | Red |
| | **Composite** | **100%** | | **63.75 → 64** | **🟥 Red Zone** |

---

## Component Detail

### 1. Distribution Day Count — 55 / 100  (Weight 25%)
- **Reading:** IBD-style distribution day count not directly published in available web sources for May 14 2026, but the May 12 inflation print produced a confirmed institutional-selling day on both S&P and Nasdaq, and May 13 trading was characterized as "S&P 500 down on jump in inflation" before a rebound.
- **Inference:** With breadth deteriorating into the May 13 inflation reaction and a positive May 14 close on AI/semi rebound, the trailing 25-day distribution count likely sits in the 4–5 range — the threshold zone (4 = warning, 5+ = elevated, 6+ = correction zone).
- **Score:** **55** — Yellow-to-orange. If distribution count is verified at 5+, score moves to 70+.

### 2. Leading Stock Health — 65 / 100  (Weight 20%)
- **Reading:** May 13 narrative explicitly notes "tech valuations came under pressure" and the May 14 record was driven by an "AI/semiconductor stocks rebound" — implying these names had been weakening before the bounce.
- **Sector breadth:** Technology 36.8% **in downtrend** per CSV; Communication Services 23.4% in downtrend; Consumer Cyclical 11.5% in downtrend.
- **Inference:** Growth-leadership ETFs (ARKK / SOXX / IGV / SMH proxies) are likely under their 21DMAs given the broad tech-sector breakdown, even with the May 14 bounce.
- **Score:** **65** — Orange. Leadership group is broken even when index is at highs.

### 3. Defensive Sector Rotation — 70 / 100  (Weight 15%)
- **Reading:** Direct quote from web: "as tech valuations came under pressure and tariff uncertainty rattled markets, prompting investors to **rotate into defensives**."
- XLP **+1.28%** on May 12; consumer staples back in focus in 2026.
- Healthcare in confirmed uptrend per breadth CSV.
- Energy "overbought" (52.4%) — late-cycle inflation hedge.
- XLY (cyclical) weighed down by AMZN/TSLA softness.
- **Score:** **70** — Orange. The XLP > XLY relative bid is one of the cleanest top-precursor signals in the framework.

### 4. Market Breadth Divergence — 85 / 100  (Weight 15%) — **STRONGEST SIGNAL**
- **S&P 500 50DMA breadth:** 46.52% (May 14) — **below 50%, while index at record**. Classic divergence.
- **S&P 500 200DMA breadth (CSV):** 59.72% — narrow rally band, below the healthy 60% threshold.
- **8MA breadth:** 55.86%, **dead-crossed below 200MA** (−3.86 pt). Strategic sell-trigger active.
- **All-market uptrend ratio:** 24.62% **RED**, slope negative.
- **9 of 11 sectors in downtrend.**
- **Score:** **85** — Red. This is the single most damning component — index at all-time high while fewer than half of constituents are above 50DMA is the defining feature of distribution tops (2007, 2015, late-2021, Jul-2023).

### 5. Index Technical Condition — 35 / 100  (Weight 15%)
- **Reading:** SPY at $742.31 (record 7,444 on S&P), made new high on May 14 per TheStreet. No failed-rally pattern, no lower highs.
- The price action itself is healthy — the divergence problem sits in components 4 and 6, not in price.
- **Score:** **35** — Yellow. Price has not confirmed the breadth/sentiment warning yet. This is what makes the setup "warning" rather than "active correction".

### 6. Sentiment & Speculation — 85 / 100  (Weight 10%) — **STRONGEST SIGNAL**
- **CBOE Equity Put/Call:** **0.49** (May 12) and 0.53 (May 8). Sub-0.55 is historically late-cycle complacency; sub-0.50 is rare and typically clusters near tops (early 2022, July 2023).
- **VIX:** 17.99–18.01 — moderate-low, but not extreme low. Still in the "complacency-permitted" zone.
- **VIX term structure:** Contango (assumed normal-shape). Not in flat/backwardation that would mark active fear.
- **Margin debt:** +36% YoY at peak (Aug 2025), and at March 2026 reading is just 4.5% off its all-time high. Two consecutive monthly declines = topping signal in the leverage cycle.
- **Score:** **85** — Red. Put/Call sub-0.50 with margin debt rolling over from a +36% YoY peak is the textbook leverage/complacency cocktail that precedes corrections.

---

## Composite Score & Risk Zone

```
Composite = 64 / 100  →  🟥 Red — High Probability of Top
```

| Score | Zone | Risk Budget | Action |
|---|---|---|---|
| 0–20 | Green | 100% | Normal |
| 21–40 | Yellow | 80–90% | Tighten stops |
| 41–60 | Orange | 60–75% | Profit-taking weak names |
| **61–80** | **Red** | **40–55%** | **Aggressive profit-taking** ← we are here |
| 81–100 | Critical | 20–35% | Maximum defense |

---

## Historical Comparison

The closest historical pattern to the current setup:

**Late 2021 / Jan 2022 — Pre-correction profile**
- Index at records, mega-cap leadership, breadth quietly degrading
- Put/Call sub-0.55, low VIX, margin debt at peak
- Distribution days accumulating through hot CPI/Fed re-pricing
- Defensive rotation underway in Q4 2021
- Resolution: 25%+ correction in QQQ over 2022, S&P −20%

**Other parallels:**
- **Jul 2023:** Similar P/C extreme + breadth divergence preceded an Aug-Oct -10% correction.
- **Jan 2018:** Vol-spike correction off similar low-vol/high-margin-debt setup.
- **Less applicable:** 2007 and 2000 had additional credit deterioration not present today.

---

## What-If Scenarios (Sensitivity)

| Change | Composite Move | New Zone |
|---|---|---|
| If Put/Call jumps to 0.85 (de-risking begins) | −8 | 56 → Orange |
| If 50DMA breadth recovers above 60% | −12 | 52 → Orange |
| If distribution days confirm at 6+ | +6 | 70 → Red (deeper) |
| If VIX spikes through 22 | +4 (initial) then likely sets up bounce | 68 → Red |
| If a weekly Follow-Through Day fires after a 5%+ pullback | reset to 35–45 | Yellow |

---

## Recommended Actions (Red Zone Playbook)

### Aggressive profit-taking
- Trim long-term holdings 25–40%, prioritizing weakest sectors (Tech, Consumer Cyclical, Comm Services, Real Estate, Financials).
- Keep core in **uptrend sectors only** (Healthcare, Basic Materials).
- Hedge remaining exposure with SPY/QQQ put spreads; volatility is cheap (VIX 18) — favorable for buying protection.

### New entries
- Default = **no new aggressive longs**.
- Exception: high-quality breakouts in Healthcare/Materials with tight stops, half size.

### Stops
- Tighten trailing stops to 1.5× ATR (vs. 2× normal).
- For positions in downtrend sectors, switch to fixed stops at 1× ATR.

### Cash
- Build cash to 25–35%. Goal: ammunition for a real capitulation entry (Uptrend Ratio <10%, 8MA breadth <23%).

### Hedging
- Net short is **not yet justified** — composite is 64, not 81+. Wait for deeper deterioration or for index price to confirm with a 5%+ break.
- Defined-risk shorts only (puts, put spreads), small size.

---

## Follow-Through Day (FTD) Status
- Not applicable. Market is at highs in a "warning" regime, not in a rally attempt off a correction low.
- Track: if a 5–10% pullback occurs over the next 2–8 weeks, watch for FTD on day 4+ to time re-entry.

---

## Delta vs Previous Run
- No prior run on file in this directory; this is the baseline reading.

---

## Caveats
1. **Distribution Day count is inferred, not directly verified** — the IBD count for May 14 2026 was not directly available in the web sources. If verified at <4, component 1 score drops to ~35 and composite moves to ~60 (still high end of Orange).
2. **FMP script unavailable** — the calibrated 6-component scoring against historical distributions is approximated by methodology rules in this report rather than the script's exact thresholds.
3. **Index strength is the offset** — component 5 (Index Technical) is keeping the composite below the 80+ Critical zone. A confirmed breakdown in price would push the composite to 75–85.

---

**Bottom line:** **Five of six components are flashing yellow-to-red.** The breadth and sentiment signals (the two most reliable historical top-precursors) are both at red-zone readings. The only thing keeping the score out of Critical is that price hasn't confirmed yet. **Reduce equity exposure to the 40–55% risk budget. Build cash. Buy cheap protection. Don't wait for the price break to confirm what breadth and sentiment are already saying.**
