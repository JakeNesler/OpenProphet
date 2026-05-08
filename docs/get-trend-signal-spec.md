# `get_trend_signal` Endpoint Spec

**Status:** Draft for review
**Owner:** TBD
**Updated:** 2026-05-07

## Purpose

Provide TrendProphet (and its pre-flight predicate) with deterministic signal values for a given ETF. Computation is performed in Go alongside the existing `get_harvest_state` and `get_penny_*` endpoints. The agent never computes Donchian, ATR, or SMA itself — see TRADING_RULES_TREND.md for the rationale.

## Endpoint

```
GET /api/v1/trend/signal/:symbol
```

### Path parameters

| Param | Type | Notes |
|---|---|---|
| `symbol` | string | Uppercase ticker symbol. Validated against the TrendProphet universe; symbols outside the universe return 400. |

### Query parameters

None for v1. The lookback windows are fixed: 100-day Donchian high, 50-day Donchian low, 200-day SMA, 20-day ATR. If lookback windows ever need to be configurable, add them as optional query params in v2.

### Response — 200 OK

```json
{
  "ticker": "TLT",
  "as_of": "2026-05-06T16:00:00-04:00",
  "bars_count": 252,
  "last_close": 95.42,
  "donchian_100_high": 95.10,
  "donchian_50_low": 91.85,
  "sma_200": 92.34,
  "atr_20": 1.21
}
```

| Field | Type | Definition |
|---|---|---|
| `ticker` | string | Echo of the requested symbol |
| `as_of` | RFC 3339 timestamp | The close timestamp of the most recent completed daily bar used in the computation |
| `bars_count` | int | Number of daily bars in the data window. Always ≥ 250 for a successful response |
| `last_close` | float64 | Close of the most recent completed daily bar (`close[t-1]` in TrendProphet's notation) |
| `donchian_100_high` | float64 | Max close over `close[t-101 .. t-2]` (the 100 completed bars before `last_close`) |
| `donchian_50_low` | float64 | Min close over `close[t-51 .. t-2]` |
| `sma_200` | float64 | Simple mean of `close[t-201 .. t-2]` |
| `atr_20` | float64 | Wilder ATR over the last 20 bars (definition below) |

### Response — 404 Not Found

Returned when the symbol is in the universe but Alpaca has no bar data for it (delisting, data outage). Body:

```json
{ "error": "no bar data available for TLT" }
```

### Response — 400 Bad Request

Returned for invalid symbol shape (lowercase, special chars) OR symbol not in the TrendProphet universe. Body:

```json
{ "error": "symbol BMY not in trend universe", "universe": ["TLT","GLD","USO","DBC","UUP","EEM"] }
```

The universe check is done at the controller, not the service, so a single config constant in `controllers/trend_controller.go` is the source of truth.

### Response — 422 Unprocessable Entity

Returned when bars exist but `bars_count < 250`. The agent's rules already say to skip the ticker in this case. Body:

```json
{ "error": "insufficient history for TLT", "bars_count": 142, "minimum_required": 250 }
```

### Response — 500 Internal Server Error

Returned for upstream Alpaca failures. Body:

```json
{ "error": "alpaca data fetch failed: connection timeout" }
```

## Computation specifics

### Bar window

Fetch 260 trading days of daily bars ending at the most recent completed close. The 260-day window provides 10 bars of headroom over the 250-bar minimum, which absorbs Alpaca returning slightly fewer bars near holidays. If Alpaca returns more than 260, only the most recent 260 are used.

The "most recent completed close" is the latest 4:00 PM ET bar. For requests during regular market hours (before 4:00 PM ET), `last_close` is yesterday's close. For requests after 4:00 PM ET on a trading day, `last_close` is today's close.

### Donchian-N high

```
donchian_N_high = max(close[i] for i in [n - N - 1, n - 2])
```

where `n = len(closes)` and indices are 0-based. If `bars_count` is exactly 250, the Donchian-100 window spans `close[148 .. 248]` (100 bars), which excludes `close[249]` (the most recent close). The rule "excludes today" is enforced by indexing up to `n - 2`, never including `n - 1`.

Note: this is a Donchian channel computed from **closes**, not high-of-day prices. The TrendProphet rules use closes for entry signals (close-above-Donchian-high is a stricter, more reliable breakout signal than intraday-high-above-Donchian-high). Implementation must use close, not high. A unit test should pin this.

### Donchian-N low

```
donchian_N_low = min(close[i] for i in [n - N - 1, n - 2])
```

Same indexing rule.

### SMA-N

```
sma_N = mean(close[i] for i in [n - N - 1, n - 2])
```

Same indexing rule. For N=200, requires 200 prior closes, so `bars_count ≥ 201` is the absolute minimum (250 is required by the rules but the computation itself only needs 201).

### ATR-N (Wilder smoothing)

True Range:

```
TR[i] = max(
  high[i] - low[i],
  abs(high[i] - close[i-1]),
  abs(low[i]  - close[i-1])
)
```

Seed value (simple mean of first N true ranges):

```
ATR_seed = mean(TR[1 .. N])
```

Wilder recursion for subsequent bars:

```
ATR[i] = (ATR[i-1] * (N - 1) + TR[i]) / N
```

The returned value is `ATR[n - 1]` — the ATR through the most recent completed bar. For N=20, the seed at `i=20` requires `TR[1..20]` which requires `close[0..20]` and `high[20], low[20]`. So 21 bars are the absolute minimum for ATR-20; 250-bar requirement makes this trivially satisfied.

**Wilder vs. simple ATR is non-negotiable.** Several public ATR implementations use a simple moving average (SMA) of TR instead of Wilder's recursive smoothing; results differ meaningfully and the rule file specifies Wilder explicitly. Tests must pin Wilder.

### Numerical precision

All math in `float64`. The endpoint returns four-decimal precision in JSON for prices and ATR (consistent with how `get_harvest_state` returns prices). No rounding logic in the computation — return the raw `float64` and let JSON serialization handle representation.

## Code structure

Mirrors the harvest pattern.

### `services/trend_signal_service.go`

```go
package services

type TrendSignalService struct {
    dataSvc *AlpacaDataService
}

type TrendSignal struct {
    Ticker          string  `json:"ticker"`
    AsOf            string  `json:"as_of"`
    BarsCount       int     `json:"bars_count"`
    LastClose       float64 `json:"last_close"`
    Donchian100High float64 `json:"donchian_100_high"`
    Donchian50Low   float64 `json:"donchian_50_low"`
    SMA200          float64 `json:"sma_200"`
    ATR20           float64 `json:"atr_20"`
}

func NewTrendSignalService(dataSvc *AlpacaDataService) *TrendSignalService { ... }

func (s *TrendSignalService) GetSignal(ctx context.Context, symbol string) (*TrendSignal, error) {
    bars, err := s.dataSvc.GetHistoricalBars(ctx, symbol, /* 260 trading days back */, time.Now(), "1Day")
    if err != nil { return nil, err }
    if len(bars) < 250 { return nil, ErrInsufficientHistory }
    closes := make([]float64, len(bars))
    highs  := make([]float64, len(bars))
    lows   := make([]float64, len(bars))
    for i, b := range bars { closes[i], highs[i], lows[i] = b.Close, b.High, b.Low }
    return &TrendSignal{
        Ticker:          symbol,
        AsOf:            bars[len(bars)-1].Timestamp.Format(time.RFC3339),
        BarsCount:       len(bars),
        LastClose:       closes[len(closes)-1],
        Donchian100High: donchianHigh(closes, 100),
        Donchian50Low:   donchianLow(closes, 50),
        SMA200:          sma(closes, 200),
        ATR20:           wilderATR(highs, lows, closes, 20),
    }, nil
}
```

Pure functions for the math (`donchianHigh`, `donchianLow`, `sma`, `wilderATR`) live in the same file and are easy to unit test in isolation.

### `controllers/trend_controller.go`

```go
package controllers

var TrendUniverse = []string{"TLT", "GLD", "USO", "DBC", "UUP", "EEM"}

type TrendController struct {
    signalSvc *services.TrendSignalService
}

func NewTrendController(signalSvc *services.TrendSignalService) *TrendController {
    return &TrendController{signalSvc: signalSvc}
}

func (tc *TrendController) HandleGetSignal(c *gin.Context) {
    symbol := strings.ToUpper(c.Param("symbol"))
    if !inUniverse(symbol) {
        c.JSON(http.StatusBadRequest, gin.H{
            "error":    fmt.Sprintf("symbol %s not in trend universe", symbol),
            "universe": TrendUniverse,
        })
        return
    }
    ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
    defer cancel()
    signal, err := tc.signalSvc.GetSignal(ctx, symbol)
    if errors.Is(err, services.ErrInsufficientHistory) {
        c.JSON(http.StatusUnprocessableEntity, gin.H{
            "error":            fmt.Sprintf("insufficient history for %s", symbol),
            "minimum_required": 250,
        })
        return
    }
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
        return
    }
    c.JSON(http.StatusOK, signal)
}
```

### Wire-up in `cmd/bot/main.go`

Add to the existing service initialization block (around line 178-200):

```go
trendSignalSvc := services.NewTrendSignalService(alpacaDataSvc)
trendController := controllers.NewTrendController(trendSignalSvc)
```

Add a route group in `setupRouter` (around line 338, alongside the harvest group):

```go
trend := api.Group("/trend")
{
    trend.GET("/signal/:symbol", trendController.HandleGetSignal)
}
```

Update `setupRouter`'s parameter list to accept `trendController *controllers.TrendController` and pass it from the call site at line 210.

### MCP tool registration

The MCP server (`mcp-server.js`) exposes Go endpoints as tools to the agent. Add a tool definition that mirrors the existing `get_harvest_state` registration, calling `GET /api/v1/trend/signal/:symbol` and returning the JSON body. The tool name `get_trend_signal` matches what TRADING_RULES_TREND.md specifies.

## Caching

**No caching in v1.** Each request recomputes from a fresh Alpaca fetch. Reasoning:

- Bars update once per day at 4:00 PM ET. Caching a daily-resolution signal between calls within the same day saves a single Alpaca request and adds invalidation logic.
- Alpaca historical-bars requests are cheap and fast (<200ms typical) and the call volume is low: ~6 calls/day for TrendProphet's heartbeat plus ~6 calls/heartbeat for pre-flight on agents that include it. At a 30-minute pre-flight cadence over a 6.5-hour session, that's ~78 calls/day total. Negligible.
- Caching would need to flush at 4:05 PM ET when new bars become available, which adds cron/timing complexity for marginal benefit.

If pre-flight expands to other agents or cadence increases, revisit. A 60-second TTL cache keyed by `(symbol, current_minute)` would be a low-risk addition.

## Tests

### Unit tests in `services/trend_signal_service_test.go`

Pin the math against a known fixture. Use a fixed bar series (250 bars of synthetic prices) committed alongside the test, with expected Donchian/SMA/ATR values computed by hand or by a reference implementation.

Required test cases:
- `TestDonchianHigh_ExcludesLastBar` — prove the window is `[n-N-1, n-2]`, not `[n-N, n-1]`. Construct a series where the last bar is the all-time high; assert `donchian_100_high < last_close`.
- `TestDonchianLow_ExcludesLastBar` — symmetric.
- `TestSMA200_StableUpTrend` — series of `close[i] = 100 + i`; `sma_200` over the prior 200 bars should equal a known value (mean of 200 evenly-spaced numbers).
- `TestWilderATR_KnownFixture` — pin against a documented Wilder ATR example. The Stockcharts reference series for AAPL is a common public fixture.
- `TestWilderATR_NotEqualToSimpleATR` — sanity test: with a TR series that has known divergence between simple-mean and Wilder, assert the result matches Wilder, not simple-mean. Catches the most common implementation mistake.
- `TestGetSignal_InsufficientHistory` — construct a 100-bar series; assert `ErrInsufficientHistory`.
- `TestGetSignal_StaleBar` — construct a series whose last bar timestamp is older than the previous trading day; the service should still return a result (with the older `as_of`) and let the caller decide. Tests that staleness handling lives in the agent rules, not the service.

### Controller tests in `controllers/trend_controller_test.go`

Mirror `controllers/penny_controller_test.go` patterns. Mock the service interface, exercise the four response codes (200, 400, 422, 500). Pin the universe check.

### Integration test

A single end-to-end test that hits a running bot with a real Alpaca call against TLT (or a deterministic fixture in CI). Validates wire-up of service → controller → router. Skip in CI by default if no Alpaca key is available; make available via `go test -tags=integration`.

## Performance

A single signal computation:
- ~250 bars × float64 = 6 KB working set, easily fits in L1 cache
- Donchian-100 = 100 comparisons; Donchian-50 = 50 comparisons; SMA-200 = 200 additions; Wilder ATR = ~250 max/abs/divisions
- Total: <1 ms CPU, <200 ms when including the Alpaca round-trip

The 6-ticker universe means a full pre-flight check is 6 parallel requests, ~200 ms total. Acceptable for a daily heartbeat. Acceptable as part of a heartbeat preflight for any sub-30-second cadence.

## Open questions

1. **Source of truth for "today's close."** Alpaca's daily bars include the most recent partial bar during regular market hours. The spec assumes the bar at `time.Now()` is the most recent *completed* bar — but during regular hours, Alpaca may return today's in-progress bar with a partial close. Confirm Alpaca's behavior and either filter the in-progress bar in `GetHistoricalBars` or document that pre-market and intraday calls return yesterday's close as `last_close`. TrendProphet's heartbeat is at 5:00 PM ET, after the close, so the daily bar is settled — but the pre-flight on other agents may run during regular hours.

2. **Holidays and half-days.** A 250-trading-day window spans roughly 12 calendar months, which can cross holidays unevenly. The 260-bar request gives some headroom but assumes Alpaca handles holiday gaps cleanly. Verify against a January or July request that crosses holiday weeks.

3. **Universe vs. operator override.** The hardcoded universe in `controllers/trend_controller.go` matches TRADING_RULES_TREND.md. If the rule file changes and the controller is not updated, the endpoint will reject valid symbols. Two options: read the universe from a shared constant in `models/`, or add an environment override (`TREND_UNIVERSE=TLT,GLD,USO,...`) for emergency operator changes. Recommendation: shared constant. The universe is small enough that drift is unlikely.

4. **Cold-start signal availability.** Tickers in the universe may have insufficient history if the ETF is newly listed (rare for the chosen 6) or if Alpaca's historical data is incomplete. The 422 response handles this gracefully — the agent's rules already require skipping. No action needed unless we expand the universe.

5. **Versioning.** If the computation changes (e.g., Donchian switches to high-of-day instead of close, or ATR window changes from 20 to 14), older agent decisions logged before the change will reference different signal definitions. Add a `signal_version` field to the response (`"v1"` initially) so log analysis can attribute decisions to the right computation. Cheap to add up-front.
