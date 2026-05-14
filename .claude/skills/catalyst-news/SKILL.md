---
name: catalyst-news
description: Fetches the last 24h of ticker-filtered news for Prophet's liquid optionable universe from FMP, classifies items into M&A or earnings-whisper buckets, and emits up to 3 ranked catalysts for the daily brief. Use when surfacing ticker-specific catalysts that MarketWatch's general feed tends to miss — deal news, profit warnings, guidance cuts/raises, preannouncements.
---

# Catalyst News Skill

## Purpose

Surface ticker-specific catalysts on Prophet's universe that the general MarketWatch scan in the daily brief tends to miss. Deliberately narrow scope: only M&A activity and earnings-whisper events (preannouncements, guidance moves, profit warnings, beat/miss headlines).

General sector and sentiment pieces are filtered out — they overlap with `market_headlines` from MarketWatch and would just inflate brief size without adding signal.

## Universe

Reads the shared static file at `.claude/skills/analyst-actions/universe.txt` (single source of truth) and adds the same FMP top-volume top-up as `analyst-actions`.

## Output Schema

JSON list of up to 3 ranked events:

```json
[
  {
    "ticker": "NVDA",
    "event_type": "ma",
    "headline": "NVIDIA agrees to acquire Arm for $40B",
    "source": "Reuters",
    "url": "https://...",
    "published": "2026-05-14T12:30:00+00:00"
  },
  {
    "ticker": "TSLA",
    "event_type": "earnings",
    "headline": "Tesla preannounces Q3 deliveries above consensus",
    "source": "Bloomberg",
    "url": "https://...",
    "published": "2026-05-14T14:15:00+00:00"
  }
]
```

`event_type` is one of `ma` | `earnings`.

## Classification

Word-boundary regex matching against title + snippet:

- **M&A** (takes precedence): "to acquire", "agrees to buy", "acquires/acquired/acquisition", "merger", "buyout", "takeover", "hostile bid", "tender offer", "divest"
- **Earnings whispers**: "preannounce", "profit warning", "raises/cuts guidance", "warns on/about", "beats/tops/crushes estimates", "misses/trails estimates"

## CLI

```sh
python scripts/fetch_catalyst_news.py \
    --top-up 15 \
    --lookback-hours 24 \
    --limit 3
```

Soft-fail behavior:
- Missing `FMP_API_KEY` → emits `[]` and exits 0.
- FMP news fetch failure → emits `[]`, logged to stderr.
- FMP screener failure → static universe used alone.

## Ranking

Sort by `(keyword_weight, recency)` descending. Dedup by `(ticker, event_type)` so a single ticker can contribute at most one M&A item AND one earnings item — never two of the same type.

## FMP Endpoints

- `/stable/news/stock` (v3 fallback: `/stock_news`)
- `/stable/company-screener` (v3 fallback: `/stock-screener`) — universe top-up only

## Testing

```sh
python -m pytest .claude/skills/catalyst-news/scripts/tests/ -v
```
