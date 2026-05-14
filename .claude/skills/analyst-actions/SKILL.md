---
name: analyst-actions
description: Fetches recent analyst rating changes and price-target updates from FMP for Prophet's liquid optionable universe, ranks them by firm tier and PT-move magnitude, and emits a JSON summary for the daily brief. Use when surfacing catalysts that move mega-cap underlyings intraday — sell-side actions from tier-1 banks (GS, MS, JPM, BofA, Citi, Wells Fargo) on names Prophet trades or could trade.
---

# Analyst Actions Skill

## Purpose

Surface fast-moving sell-side catalysts (PT raises/cuts, rating up/downgrades) on names in Prophet's liquid optionable universe, ranked so the most impactful events bubble to the top of the daily brief.

Tier-1 bank actions on mega caps (e.g., Goldman PT raise on NVDA) move the underlying intraday and frequently catch options positions off-guard. This skill captures that signal class with a 24-hour lookback aligned to the daily-briefing cadence.

## Universe

Static curated floor (~40 names) at `universe.txt` plus ~15 FMP top-volume names filtered by price >$20 and market cap >$5B. Shared with `catalyst-news` as the single source of truth.

## Output Schema

JSON list of ranked events (max 15 by default):

```json
[
  {
    "ticker": "NVDA",
    "type": "pt_change",
    "firm": "Goldman Sachs",
    "action": "raised",
    "from": 175.0,
    "to": 210.0,
    "date": "2026-05-14T12:30:00+00:00"
  },
  {
    "ticker": "TSLA",
    "type": "rating_change",
    "firm": "Morgan Stanley",
    "action": "upgrade",
    "from": "Hold",
    "to": "Buy",
    "date": "2026-05-14T14:15:00+00:00"
  }
]
```

`type` is one of `pt_change` | `rating_change`.
`action` for PT changes: `raised` | `lowered` | `set` | `reiterated`.
`action` for rating changes: `upgrade` | `downgrade` | `initiated` | `reiterated`.

## CLI

```sh
python scripts/fetch_analyst_actions.py \
    --top-up 15 \
    --lookback-hours 24 \
    --limit 15
```

Soft-fail behavior:
- Missing `FMP_API_KEY` → emits `[]` and exits 0 (daily-brief pipeline keeps running).
- Per-ticker FMP failure → logged to stderr; partial output returned.
- FMP screener failure → static universe used alone.

## Ranking

`score = firm_tier_weight × action_magnitude`

- Firm tier: 1 (GS/MS/JPM/BAC/Citi/WF) = 3.0; tier 2 (Barclays, UBS, Jefferies, etc.) = 1.5; other = 1.0.
- PT change weight scales with |Δ| / from up to 3.0 (20% PT move ≈ 2.0).
- Rating change weight: upgrade/downgrade = 2.0, initiated = 1.2, reiterated = 0.6.

## FMP Endpoints

- `/stable/grades-historical` (v3 fallback: `/historical-rating/<symbol>`)
- `/stable/price-target-news` (v3 fallback: `/price-target/<symbol>`)
- `/stable/company-screener` (v3 fallback: `/stock-screener`) — universe top-up only

## Testing

```sh
python -m pytest .claude/skills/analyst-actions/scripts/tests/ -v
```
