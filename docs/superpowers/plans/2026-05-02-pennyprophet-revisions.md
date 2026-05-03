# PennyProphet Revisions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the 10 approved revisions from `docs/superpowers/specs/2026-05-02-pennyprophet-revisions-design.md` — hardening signal decay, social baseline, per-signal minimums, bracket blacklist, EDGAR compliance, and operational discipline.

**Architecture:** Infrastructure types first (config, DecayEntry, CandidateScore), then each signal service migrates to those types, then the aggregator applies per-signal minimums and blacklist, then controller endpoints, then wiring, then trading rules and config. Every layer depends on the one below; work top-down.

**Tech Stack:** Go (existing), Gin HTTP router, Alpaca market data API (calendar endpoint), logrus, testify-free table tests in standard `testing` package.

---

## File Map

| File | Action | Change |
|---|---|---|
| `config/config.go` | Modify | Add `OperatorEmail` + fail-fast validation |
| `services/penny_types.go` | Modify | Add `DecayEntry`, expand `CandidateScore` |
| `services/penny_types_test.go` | Modify | Add `DecayEntry.EffectiveScore` tests |
| `services/penny_universe_service.go` | Modify | ADV $300K→$500K, three-speed calendar refresh |
| `services/penny_universe_service_test.go` | Modify | ADV boundary tests, `isMarketHours` tests |
| `services/penny_screener_service.go` | Modify | `TechnicalEntry` → `DecayEntry`, meaningful-change anchor |
| `services/penny_screener_service_test.go` | Modify | Anchor update tests |
| `services/sec_edgar_service.go` | Modify | `operatorEmail`, `DecayEntry`, max-rule upsert, timestamp parse, fallback rate |
| `services/sec_edgar_service_test.go` | Create | Max-rule + timestamp tests |
| `services/social_signal_service.go` | Modify | 7-day ring-buffer baseline, new scoring, `DecayEntry`, universe cleanup |
| `services/social_signal_service_test.go` | Create | Baseline, velocity, 72h guard, cleanup tests |
| `services/penny_signal_aggregator.go` | Modify | Per-signal minimums, `BracketBlacklist`, `GetCandidates` eligibility filter |
| `services/penny_signal_aggregator_test.go` | Modify | Update fixtures + add eligibility/blacklist tests |
| `controllers/penny_controller.go` | Modify | Two DELETE blacklist endpoints |
| `cmd/bot/main.go` | Modify | Wire `OperatorEmail` → `NewSECEdgarService`; Alpaca creds → `NewPennyUniverseService` |
| `data/agent-config.json` | Modify | `sbx_a788a4e3` permissions: `allowOptions:false`, `maxPositionPct:8`, `maxDeployedPct:60`, `maxToolRoundsPerBeat:18` |
| `TRADING_RULES_PENNY.md` | Modify | Prepend 7 operational sections; update positions, universe, social exit, blacklist note |

---

## Task 1: Config — OperatorEmail with Fail-Fast Validation

**Files:**
- Modify: `config/config.go`

- [ ] **Step 1: Write the failing test**

Add to `config/config_test.go` (create if it doesn't exist):

```go
package config

import (
	"os"
	"testing"
)

func TestLoad_MissingOperatorEmail_ReturnsError(t *testing.T) {
	os.Unsetenv("OPERATOR_EMAIL")
	err := Load()
	if err == nil {
		t.Fatal("expected error when OPERATOR_EMAIL is unset, got nil")
	}
}

func TestLoad_WithOperatorEmail_Succeeds(t *testing.T) {
	os.Setenv("OPERATOR_EMAIL", "test@example.com")
	defer os.Unsetenv("OPERATOR_EMAIL")
	err := Load()
	if err != nil {
		t.Fatalf("expected no error with OPERATOR_EMAIL set, got: %v", err)
	}
	if AppConfig.OperatorEmail != "test@example.com" {
		t.Errorf("expected OperatorEmail=test@example.com, got %q", AppConfig.OperatorEmail)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```
go test ./config/... -run TestLoad -v
```

Expected: FAIL — `AppConfig` has no `OperatorEmail` field yet.

- [ ] **Step 3: Implement OperatorEmail in config.go**

Add to `Config` struct after `DataRetentionDays`:

```go
OperatorEmail string // SEC EDGAR User-Agent contact; set via OPERATOR_EMAIL env var
```

Add to `AppConfig = &Config{...}` block:

```go
OperatorEmail: os.Getenv("OPERATOR_EMAIL"),
```

Change `Load()` return from `return nil` to:

```go
if AppConfig.OperatorEmail == "" {
    return fmt.Errorf("OPERATOR_EMAIL must be set — SEC EDGAR policy requires a real contact address in the User-Agent header. Set OPERATOR_EMAIL=your@email.com in .env")
}
return nil
```

Add `"fmt"` to imports.

- [ ] **Step 4: Run test to verify it passes**

```
go test ./config/... -run TestLoad -v
```

Expected: PASS

- [ ] **Step 5: Run full config tests**

```
go test ./config/... -v
```

Expected: all PASS

- [ ] **Step 6: Commit**

```
git add config/config.go config/config_test.go
git commit -m "feat: add OperatorEmail config with fail-fast validation for EDGAR User-Agent"
```

---

## Task 2: Types — Add DecayEntry and Expand CandidateScore

**Files:**
- Modify: `services/penny_types.go`
- Modify: `services/penny_types_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `services/penny_types_test.go`:

```go
func TestDecayEntry_EffectiveScore_AtZero(t *testing.T) {
	d := DecayEntry{BaseScore: 40.0, EventTime: time.Now(), HalfLifeHrs: 2.0}
	got := d.EffectiveScore()
	if got < 39.9 || got > 40.0 {
		t.Errorf("expected ~40.0 at t=0, got %f", got)
	}
}

func TestDecayEntry_EffectiveScore_AtHalfLife(t *testing.T) {
	d := DecayEntry{BaseScore: 40.0, EventTime: time.Now().Add(-2 * time.Hour), HalfLifeHrs: 2.0}
	got := d.EffectiveScore()
	if got < 19.5 || got > 20.5 {
		t.Errorf("expected ~20.0 at half-life, got %f", got)
	}
}

func TestDecayEntry_EffectiveScore_Floor(t *testing.T) {
	// 9h with 2h half-life: 40 * 0.5^4.5 ≈ 1.77, < 5% of 40 (=2.0) → 0
	d := DecayEntry{BaseScore: 40.0, EventTime: time.Now().Add(-9 * time.Hour), HalfLifeHrs: 2.0}
	got := d.EffectiveScore()
	if got != 0 {
		t.Errorf("expected 0 at decay floor, got %f", got)
	}
}

func TestDecayEntry_EffectiveScore_JustAboveFloor(t *testing.T) {
	// 8h with 2h half-life: 40 * 0.5^4 = 2.5, > 5% of 40 (=2.0) → not floored
	d := DecayEntry{BaseScore: 40.0, EventTime: time.Now().Add(-8 * time.Hour), HalfLifeHrs: 2.0}
	got := d.EffectiveScore()
	if got <= 0 {
		t.Errorf("expected positive score at 4 half-lives (above floor), got %f", got)
	}
}

func TestDecayEntry_EffectiveScore_ZeroBaseScore(t *testing.T) {
	d := DecayEntry{BaseScore: 0, EventTime: time.Now(), HalfLifeHrs: 2.0}
	got := d.EffectiveScore()
	if got != 0 {
		t.Errorf("expected 0 for zero base score, got %f", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```
go test ./services/... -run TestDecayEntry -v
```

Expected: FAIL — `DecayEntry` undefined.

- [ ] **Step 3: Add DecayEntry to penny_types.go**

Add after the imports block, before `UniverseSymbol`:

```go
// DecayEntry is the canonical in-memory representation of a decaying signal.
// All three signal services store one DecayEntry per ticker.
type DecayEntry struct {
	BaseScore   float64
	EventTime   time.Time
	HalfLifeHrs float64
}

// EffectiveScore returns the decayed score, floored to zero below 5% of base.
func (d DecayEntry) EffectiveScore() float64 {
	if d.BaseScore == 0 {
		return 0
	}
	elapsed := time.Since(d.EventTime).Hours()
	lambda := math.Log(2) / d.HalfLifeHrs
	decayed := d.BaseScore * math.Exp(-lambda*elapsed)
	if decayed < 0.05*d.BaseScore {
		return 0
	}
	return decayed
}
```

- [ ] **Step 4: Expand CandidateScore struct**

Replace the existing `CandidateScore` struct with:

```go
// CandidateScore is the aggregated signal score for one symbol.
type CandidateScore struct {
	Ticker              string    `json:"ticker"`
	Price               float64   `json:"price"`
	CompositeScore      float64   `json:"composite_score"`
	SignalCount         int       `json:"signal_count"`
	CompositeEligible   bool      `json:"composite_eligible"`

	TechnicalScore      float64   `json:"technical_score"`
	TechnicalEffective  float64   `json:"technical_effective"`
	RegulatoryScore     float64   `json:"regulatory_score"`
	RegulatoryEffective float64   `json:"regulatory_effective"`
	SocialScore         float64   `json:"social_score"`
	SocialEffective     float64   `json:"social_effective"`

	DominantSignal   string    `json:"dominant_signal"`
	TechnicalContext string    `json:"technical_context,omitempty"`
	RegulatoryEvent  string    `json:"regulatory_event,omitempty"`
	SocialContext    string    `json:"social_context,omitempty"`
	LastUpdated      time.Time `json:"last_updated"`
}
```

- [ ] **Step 5: Run all types tests**

```
go test ./services/... -run "TestDecayEntry|TestScoreWithDecay|TestDominantSignal" -v
```

Expected: all PASS. (`scoreWithDecay` is unchanged; existing tests still pass.)

- [ ] **Step 6: Verify build compiles**

```
go build ./...
```

Expected: no errors (new fields on `CandidateScore` are additive; existing code that constructs `CandidateScore` by name still compiles — the aggregator will need updating in Task 7).

- [ ] **Step 7: Commit**

```
git add services/penny_types.go services/penny_types_test.go
git commit -m "feat: add DecayEntry type with EffectiveScore; expand CandidateScore with effective fields"
```

---

## Task 3: PennyUniverseService — ADV Threshold + Market-Hours Refresh

**Files:**
- Modify: `services/penny_universe_service.go`
- Modify: `services/penny_universe_service_test.go`

- [ ] **Step 1: Write failing tests for ADV threshold**

Read `services/penny_universe_service_test.go` first to understand existing test structure, then add:

```go
func TestUniverseFilter_ADV_BoundaryExcluded(t *testing.T) {
	svc := NewPennyUniverseService("key", "", "", "", nil)
	items := []fmpScreenerItem{
		{Symbol: "LOW", CompanyName: "Low Vol", MarketCap: 100_000_000, Price: 5.0,
			Volume: 99_999, ExchangeShortName: "NASDAQ"}, // dollarVol = 499,995 < 500,000
	}
	result := svc.filter(items)
	if len(result) != 0 {
		t.Errorf("expected ADV $499,995 to be excluded, got %d results", len(result))
	}
}

func TestUniverseFilter_ADV_BoundaryIncluded(t *testing.T) {
	svc := NewPennyUniverseService("key", "", "", "", nil)
	items := []fmpScreenerItem{
		{Symbol: "OK", CompanyName: "OK Vol", MarketCap: 100_000_000, Price: 5.0,
			Volume: 100_000, ExchangeShortName: "NASDAQ"}, // dollarVol = 500,000 ≥ 500,000
	}
	result := svc.filter(items)
	if len(result) != 1 {
		t.Errorf("expected ADV $500,000 to be included, got %d results", len(result))
	}
}

func TestIsMarketHours_Open(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	cal := AlpacaCalendarEntry{Date: "2026-05-02", Open: "09:30", Close: "16:00", SessionOpen: "0400", SessionClose: "2000"}
	// 10:00 ET on the calendar date
	now := time.Date(2026, 5, 2, 10, 0, 0, 0, loc)
	got := isMarketHours(now, cal)
	if got != "open" {
		t.Errorf("expected 'open' at 10:00 ET, got %q", got)
	}
}

func TestIsMarketHours_PreMarket(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	cal := AlpacaCalendarEntry{Date: "2026-05-02", Open: "09:30", Close: "16:00", SessionOpen: "0400", SessionClose: "2000"}
	now := time.Date(2026, 5, 2, 6, 0, 0, 0, loc)
	got := isMarketHours(now, cal)
	if got != "pre" {
		t.Errorf("expected 'pre' at 06:00 ET, got %q", got)
	}
}

func TestIsMarketHours_AfterHours(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	cal := AlpacaCalendarEntry{Date: "2026-05-02", Open: "09:30", Close: "16:00", SessionOpen: "0400", SessionClose: "2000"}
	now := time.Date(2026, 5, 2, 17, 0, 0, 0, loc)
	got := isMarketHours(now, cal)
	if got != "after" {
		t.Errorf("expected 'after' at 17:00 ET, got %q", got)
	}
}

func TestIsMarketHours_Closed_WrongDate(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	cal := AlpacaCalendarEntry{Date: "2026-05-02", Open: "09:30", Close: "16:00", SessionOpen: "0400", SessionClose: "2000"}
	// next day
	now := time.Date(2026, 5, 3, 10, 0, 0, 0, loc)
	got := isMarketHours(now, cal)
	if got != "closed" {
		t.Errorf("expected 'closed' for date mismatch, got %q", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```
go test ./services/... -run "TestUniverseFilter_ADV|TestIsMarketHours" -v
```

Expected: FAIL — `AlpacaCalendarEntry` undefined, constructor signature mismatch.

- [ ] **Step 3: Add AlpacaCalendarEntry and helpers to penny_universe_service.go**

Add after the import block:

```go
// AlpacaCalendarEntry holds one trading-day entry from the Alpaca /v2/calendar endpoint.
type AlpacaCalendarEntry struct {
	Date         string `json:"date"`          // "YYYY-MM-DD"
	Open         string `json:"open"`          // "HH:MM" regular market open ET
	Close        string `json:"close"`         // "HH:MM" regular market close ET
	SessionOpen  string `json:"session_open"`  // "HHMM" extended-hours open ET
	SessionClose string `json:"session_close"` // "HHMM" extended-hours close ET
}

// isMarketHours returns "open", "pre", "after", or "closed" based on now vs the calendar entry.
func isMarketHours(now time.Time, cal AlpacaCalendarEntry) string {
	loc, _ := time.LoadLocation("America/New_York")
	if cal.Date == "" {
		return staticMarketPhase(now, loc)
	}
	nowET := now.In(loc)
	calDate, err := time.ParseInLocation("2006-01-02", cal.Date, loc)
	if err != nil {
		return staticMarketPhase(now, loc)
	}
	y1, m1, d1 := nowET.Date()
	y2, m2, d2 := calDate.Date()
	if y1 != y2 || m1 != m2 || d1 != d2 {
		return "closed"
	}
	open := parseTimeOnCalDate("15:04", cal.Open, calDate, loc)
	close_ := parseTimeOnCalDate("15:04", cal.Close, calDate, loc)
	sessOpen := parseTimeOnCalDate("1504", cal.SessionOpen, calDate, loc)
	sessClose := parseTimeOnCalDate("1504", cal.SessionClose, calDate, loc)

	if nowET.Before(sessOpen) || nowET.After(sessClose) {
		return "closed"
	}
	if nowET.Before(open) {
		return "pre"
	}
	if nowET.After(close_) {
		return "after"
	}
	return "open"
}

func parseTimeOnCalDate(layout, timeStr string, calDate time.Time, loc *time.Location) time.Time {
	combined := calDate.Format("2006-01-02") + " " + timeStr
	t, err := time.ParseInLocation("2006-01-02 "+layout, combined, loc)
	if err != nil {
		return calDate
	}
	return t
}

func staticMarketPhase(now time.Time, loc *time.Location) string {
	nowET := now.In(loc)
	wd := nowET.Weekday()
	if wd == time.Saturday || wd == time.Sunday {
		return "closed"
	}
	h, m, _ := nowET.Clock()
	total := h*60 + m
	switch {
	case total < 4*60 || total > 20*60:
		return "closed"
	case total < 9*60+30:
		return "pre"
	case total > 16*60:
		return "after"
	default:
		return "open"
	}
}
```

- [ ] **Step 4: Update PennyUniverseService struct and constructor**

Replace the struct definition:

```go
type PennyUniverseService struct {
	httpClient      *http.Client
	fmpAPIKey       string
	fmpBaseURL      string
	alpacaAPIKey    string
	alpacaSecretKey string
	alpacaBaseURL   string
	mu              sync.RWMutex
	universe        []UniverseSymbol
	calEntry        AlpacaCalendarEntry
	calDate         time.Time
	logger          *logrus.Logger
}
```

Replace `NewPennyUniverseService`:

```go
func NewPennyUniverseService(fmpAPIKey, alpacaAPIKey, alpacaSecretKey, alpacaBaseURL string, httpClient *http.Client) *PennyUniverseService {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	if alpacaBaseURL == "" {
		alpacaBaseURL = "https://paper-api.alpaca.markets"
	}
	logger := logrus.New()
	logger.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})
	return &PennyUniverseService{
		httpClient:      httpClient,
		fmpAPIKey:       fmpAPIKey,
		fmpBaseURL:      "https://financialmodelingprep.com",
		alpacaAPIKey:    alpacaAPIKey,
		alpacaSecretKey: alpacaSecretKey,
		alpacaBaseURL:   alpacaBaseURL,
		logger:          logger,
	}
}
```

- [ ] **Step 5: Update ADV threshold from 300_000 to 500_000**

In `filter()`, change:

```go
if dollarVol < 300_000 {
```

to:

```go
if dollarVol < 500_000 {
```

- [ ] **Step 6: Add calendar fetch and three-speed Start loop**

Add after the `filter()` function:

```go
func (s *PennyUniverseService) maybeRefreshCalendar(now time.Time) {
	s.mu.RLock()
	sameDay := !s.calDate.IsZero() && s.calDate.Year() == now.Year() &&
		s.calDate.Month() == now.Month() && s.calDate.Day() == now.Day()
	s.mu.RUnlock()
	if sameDay {
		return
	}
	cal, err := s.fetchAlpacaCalendar(now)
	if err != nil {
		s.logger.WithError(err).Warn("PennyUniverseService: calendar fetch failed, using static phase fallback")
		return
	}
	s.mu.Lock()
	s.calEntry = cal
	s.calDate = now
	s.mu.Unlock()
}

func (s *PennyUniverseService) fetchAlpacaCalendar(now time.Time) (AlpacaCalendarEntry, error) {
	date := now.Format("2006-01-02")
	url := fmt.Sprintf("%s/v2/calendar?start=%s&end=%s", s.alpacaBaseURL, date, date)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return AlpacaCalendarEntry{}, err
	}
	req.Header.Set("APCA-API-KEY-ID", s.alpacaAPIKey)
	req.Header.Set("APCA-API-SECRET-KEY", s.alpacaSecretKey)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return AlpacaCalendarEntry{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return AlpacaCalendarEntry{}, fmt.Errorf("alpaca calendar returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return AlpacaCalendarEntry{}, err
	}
	var entries []AlpacaCalendarEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		return AlpacaCalendarEntry{}, err
	}
	if len(entries) == 0 {
		return AlpacaCalendarEntry{}, fmt.Errorf("alpaca calendar: no entries for %s (non-trading day)", date)
	}
	return entries[0], nil
}

func (s *PennyUniverseService) getCalEntry() AlpacaCalendarEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.calEntry
}
```

Replace the `Start` method:

```go
func (s *PennyUniverseService) Start(ctx context.Context) {
	s.maybeRefreshCalendar(time.Now())
	s.refresh()
	for {
		cal := s.getCalEntry()
		phase := isMarketHours(time.Now(), cal)
		var interval time.Duration
		switch phase {
		case "open":
			interval = 5 * time.Minute
		case "pre":
			interval = 30 * time.Minute
		case "after":
			interval = 60 * time.Minute
		default:
			interval = 60 * time.Minute
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
			now := time.Now()
			s.maybeRefreshCalendar(now)
			cal = s.getCalEntry()
			if isMarketHours(now, cal) != "closed" {
				s.refresh()
			}
		}
	}
}
```

Add `"encoding/json"` and `"io"` to imports (they're already present — verify).

- [ ] **Step 7: Remove the now-unused constant**

Delete `const universeRefreshInterval = 15 * time.Minute` (no longer used).

- [ ] **Step 8: Run tests**

```
go test ./services/... -run "TestUniverseFilter|TestIsMarketHours" -v
```

Expected: all PASS

- [ ] **Step 9: Build check**

```
go build ./...
```

Expected: compile error in `cmd/bot/main.go` because `NewPennyUniverseService` signature changed — that's expected; fix in Task 9.

- [ ] **Step 10: Run only services tests to confirm no regressions**

```
go test ./services/... -v
```

Expected: all pass except any test that calls `NewPennyUniverseService` with the old signature — update those now in the test file to pass `""` for the three new params.

- [ ] **Step 11: Commit**

```
git add services/penny_universe_service.go services/penny_universe_service_test.go
git commit -m "feat: raise ADV threshold to $500K; add three-speed market-hours refresh via Alpaca calendar"
```

---

## Task 4: PennyScreenerService — TechnicalEntry Migration + Meaningful-Change Anchor

**Files:**
- Modify: `services/penny_screener_service.go`
- Modify: `services/penny_screener_service_test.go`

- [ ] **Step 1: Write failing tests**

Read `services/penny_screener_service_test.go` to understand existing patterns, then add:

```go
func TestUpdateAnchor_FirstObservation(t *testing.T) {
	before := time.Now()
	base, anchor := updateAnchor(30.0, 0, time.Time{}, false)
	after := time.Now()
	if base != 30.0 {
		t.Errorf("first obs: expected base=30.0, got %f", base)
	}
	if anchor.Before(before) || anchor.After(after) {
		t.Errorf("first obs: anchor should be ~now, got %v", anchor)
	}
}

func TestUpdateAnchor_PriorZero_NewPositive(t *testing.T) {
	before := time.Now()
	base, anchor := updateAnchor(25.0, 0, time.Now().Add(-1*time.Hour), true)
	after := time.Now()
	if base != 25.0 {
		t.Errorf("prior zero: expected base=25.0, got %f", base)
	}
	if anchor.Before(before) || anchor.After(after) {
		t.Error("prior zero: anchor should reset to now")
	}
}

func TestUpdateAnchor_SmallChange_PreservesAnchor(t *testing.T) {
	oldAnchor := time.Now().Add(-2 * time.Hour)
	// 5% change (below 10% threshold)
	base, anchor := updateAnchor(31.5, 30.0, oldAnchor, true)
	if base != 30.0 {
		t.Errorf("small change: expected base preserved at 30.0, got %f", base)
	}
	if !anchor.Equal(oldAnchor) {
		t.Errorf("small change: expected anchor preserved at %v, got %v", oldAnchor, anchor)
	}
}

func TestUpdateAnchor_LargeChange_UpdatesAnchor(t *testing.T) {
	oldAnchor := time.Now().Add(-2 * time.Hour)
	// 20% change (above 10% threshold)
	before := time.Now()
	base, anchor := updateAnchor(24.0, 20.0, oldAnchor, true)
	after := time.Now()
	if base != 24.0 {
		t.Errorf("large change: expected base=24.0, got %f", base)
	}
	if anchor.Before(before) || anchor.After(after) {
		t.Errorf("large change: expected anchor ~now, got %v", anchor)
	}
}

func TestUpdateAnchor_ExactlyTenPercent_UpdatesAnchor(t *testing.T) {
	// Exactly 10% change: 10% relative → meaningful (> not >=, but spec says > 0.10)
	// 30.0 * 1.10 = 33.0 → |33-30|/30 = 0.10, NOT > 0.10 → preserve
	oldAnchor := time.Now().Add(-1 * time.Hour)
	base, anchor := updateAnchor(33.0, 30.0, oldAnchor, true)
	if base != 30.0 {
		t.Errorf("exactly 10%%: expected base preserved, got %f", base)
	}
	if !anchor.Equal(oldAnchor) {
		t.Error("exactly 10%: expected anchor preserved (not strictly > 10%)")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```
go test ./services/... -run "TestUpdateAnchor" -v
```

Expected: FAIL — `updateAnchor` undefined.

- [ ] **Step 3: Add updateAnchor helper and update TechnicalEntry**

In `penny_screener_service.go`, replace:

```go
type TechnicalEntry struct {
	Score       float64
	VolumeRatio float64
	GapPct      float64
	Context     string
	UpdatedAt   time.Time
}
```

with:

```go
type TechnicalEntry struct {
	Entry       DecayEntry // BaseScore=computed score, EventTime=last meaningful change, HalfLifeHrs=2.0
	VolumeRatio float64
	GapPct      float64
	Context     string
}

// updateAnchor applies the meaningful-change rule: anchor resets only on first observation,
// prior-zero-to-positive, or >10% relative change. Package-private; accessible to same-package tests.
func updateAnchor(newScore, priorBase float64, priorAnchor time.Time, hasPrior bool) (base float64, anchor time.Time) {
	if !hasPrior || priorBase == 0 {
		return newScore, time.Now()
	}
	relChange := math.Abs(newScore-priorBase) / priorBase
	if relChange > 0.10 {
		return newScore, time.Now()
	}
	return priorBase, priorAnchor
}
```

- [ ] **Step 4: Update GetTechnicalScore to use EffectiveScore**

Replace:

```go
func (s *PennyScreenerService) GetTechnicalScore(ticker string) (float64, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.scores[ticker]
	if !ok {
		return 0, ""
	}
	score := scoreWithDecay(e.Score, e.UpdatedAt, 2.0)
	return score, e.Context
}
```

with:

```go
func (s *PennyScreenerService) GetTechnicalScore(ticker string) (float64, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.scores[ticker]
	if !ok {
		return 0, ""
	}
	return e.Entry.EffectiveScore(), e.Context
}
```

- [ ] **Step 5: Update computeEntry to use DecayEntry and anchor logic**

Replace `computeEntry`:

```go
func (s *PennyScreenerService) computeEntry(ticker string, snap *alpacaMarket.Snapshot) TechnicalEntry {
	if snap == nil || snap.DailyBar == nil || snap.PrevDailyBar == nil {
		prior, hasPrior := s.scores[ticker]
		if hasPrior {
			return prior
		}
		return TechnicalEntry{Entry: DecayEntry{HalfLifeHrs: 2.0, EventTime: time.Now()}}
	}

	var volumeRatio float64
	if snap.PrevDailyBar.Volume > 0 {
		volumeRatio = float64(snap.DailyBar.Volume) / float64(snap.PrevDailyBar.Volume)
	}
	var gapPct float64
	if snap.PrevDailyBar.Close > 0 {
		gapPct = (snap.DailyBar.Open - snap.PrevDailyBar.Close) / snap.PrevDailyBar.Close * 100
	}
	var breakoutBonus float64
	if snap.DailyBar.High > 0 {
		if (snap.DailyBar.High-snap.DailyBar.Close)/snap.DailyBar.High <= 0.02 {
			breakoutBonus = 1.0
		}
	}

	total := math.Min(volumeRatio/5.0, 1.0)*20.0 +
		math.Min(math.Abs(gapPct)/5.0, 1.0)*10.0 +
		breakoutBonus*10.0
	signalSummary := fmt.Sprintf("vol_ratio=%.1fx gap=%.1f%% breakout_near=%v", volumeRatio, gapPct, breakoutBonus > 0)

	prior, hasPrior := s.scores[ticker]
	var priorBase float64
	var priorAnchor time.Time
	if hasPrior {
		priorBase = prior.Entry.BaseScore
		priorAnchor = prior.Entry.EventTime
	}
	base, anchor := updateAnchor(total, priorBase, priorAnchor, hasPrior)

	return TechnicalEntry{
		Entry:       DecayEntry{BaseScore: base, EventTime: anchor, HalfLifeHrs: 2.0},
		VolumeRatio: volumeRatio,
		GapPct:      gapPct,
		Context:     signalSummary,
	}
}
```

- [ ] **Step 6: Run tests**

```
go test ./services/... -run "TestUpdateAnchor" -v
```

Expected: all PASS

- [ ] **Step 7: Update existing screener tests**

In `penny_screener_service_test.go`, update any test that constructs `TechnicalEntry` with the old struct (e.g., `TechnicalEntry{Score: x, UpdatedAt: y}`) to use `TechnicalEntry{Entry: DecayEntry{BaseScore: x, EventTime: y, HalfLifeHrs: 2.0}}`.

- [ ] **Step 8: Run full services tests**

```
go test ./services/... -v
```

Expected: all PASS

- [ ] **Step 9: Commit**

```
git add services/penny_screener_service.go services/penny_screener_service_test.go
git commit -m "feat: migrate TechnicalEntry to DecayEntry; add meaningful-change anchor logic"
```

---

## Task 5: SECEdgarService — User-Agent, DecayEntry Migration, Max-Rule Upsert, Timestamps

**Files:**
- Modify: `services/sec_edgar_service.go`
- Create: `services/sec_edgar_service_test.go`

- [ ] **Step 1: Write failing tests**

Create `services/sec_edgar_service_test.go`:

```go
package services

import (
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func newTestEdgar() *SECEdgarService {
	return &SECEdgarService{
		entries: make(map[string]regulatoryEntry),
		logger:  logrus.New(),
	}
}

func TestSECEdgar_UpsertEntry_FirstEntry(t *testing.T) {
	svc := newTestEdgar()
	svc.mu.Lock()
	svc.upsertEntry("TICK", 40.0, time.Now(), "8-K filed")
	svc.mu.Unlock()

	score, desc := svc.GetRegulatoryScore("TICK")
	if score < 39.9 || score > 40.0 {
		t.Errorf("expected ~40.0 for fresh entry, got %f", score)
	}
	if desc != "8-K filed" {
		t.Errorf("expected desc '8-K filed', got %q", desc)
	}
}

func TestSECEdgar_UpsertEntry_MaxRule_OldWins(t *testing.T) {
	// 8-K at -2h scores 40 with 24h half-life: decayed ≈ 39.4 > new 25 → old wins
	svc := newTestEdgar()
	oldEventTime := time.Now().Add(-2 * time.Hour)
	svc.mu.Lock()
	svc.upsertEntry("TICK", 40.0, oldEventTime, "old 8-K")
	svc.upsertEntry("TICK", 25.0, time.Now(), "PR wire")
	svc.mu.Unlock()

	score, desc := svc.GetRegulatoryScore("TICK")
	if score < 38.0 || score > 40.0 {
		t.Errorf("expected decayed old score ~39.4, got %f", score)
	}
	if desc != "old 8-K" {
		t.Errorf("expected old desc preserved, got %q", desc)
	}
}

func TestSECEdgar_UpsertEntry_MaxRule_NewWins(t *testing.T) {
	// 8-K at -25h scores 40: decayed ≈ 19.3 < new 40 → new wins
	svc := newTestEdgar()
	oldEventTime := time.Now().Add(-25 * time.Hour)
	svc.mu.Lock()
	svc.upsertEntry("TICK", 40.0, oldEventTime, "old 8-K")
	svc.upsertEntry("TICK", 40.0, time.Now(), "new 8-K")
	svc.mu.Unlock()

	score, desc := svc.GetRegulatoryScore("TICK")
	if score < 39.5 || score > 40.0 {
		t.Errorf("expected ~40 (new wins), got %f", score)
	}
	if desc != "new 8-K" {
		t.Errorf("expected new desc, got %q", desc)
	}
}

func TestSECEdgar_ParseAtomDate_Valid(t *testing.T) {
	ts := "2026-05-02T14:30:00-04:00"
	parsed, fallback := parseAtomDate(ts)
	if fallback {
		t.Error("expected no fallback for valid RFC3339 timestamp")
	}
	// 14:30 EDT = 18:30 UTC
	if parsed.UTC().Hour() != 18 || parsed.UTC().Minute() != 30 {
		t.Errorf("expected 18:30 UTC, got %v", parsed.UTC())
	}
}

func TestSECEdgar_ParseAtomDate_Invalid_Fallback(t *testing.T) {
	_, fallback := parseAtomDate("not-a-date")
	if !fallback {
		t.Error("expected fallback for invalid timestamp")
	}
}

func TestSECEdgar_ParseRSSDate_Valid(t *testing.T) {
	ts := "Fri, 02 May 2026 14:30:00 -0400"
	parsed, fallback := parseRSSDate(ts)
	if fallback {
		t.Error("expected no fallback for valid RFC1123Z timestamp")
	}
	if parsed.UTC().Hour() != 18 || parsed.UTC().Minute() != 30 {
		t.Errorf("expected 18:30 UTC, got %v", parsed.UTC())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```
go test ./services/... -run "TestSECEdgar" -v
```

Expected: FAIL — `regulatoryEntry` struct mismatch, `parseAtomDate`/`parseRSSDate` undefined.

- [ ] **Step 3: Update regulatoryEntry struct**

Replace:

```go
type regulatoryEntry struct {
	BaseScore  float64
	DetectedAt time.Time
	EventDesc  string
}
```

with:

```go
type regulatoryEntry struct {
	Entry     DecayEntry
	EventDesc string
}
```

- [ ] **Step 4: Update SECEdgarService struct and constructor**

Add `operatorEmail string` to the struct. Replace `NewSECEdgarService`:

```go
func NewSECEdgarService(universe *PennyUniverseService, httpClient *http.Client, operatorEmail string) *SECEdgarService {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	logger := logrus.New()
	logger.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})
	return &SECEdgarService{
		httpClient:    httpClient,
		universe:      universe,
		operatorEmail: operatorEmail,
		entries:       make(map[string]regulatoryEntry),
		logger:        logger,
	}
}
```

Add `operatorEmail string` field to `SECEdgarService` struct.

- [ ] **Step 5: Update User-Agent in fetchAtom and fetchRSS**

In both `fetchAtom` and `fetchRSS`, replace:

```go
req.Header.Set("User-Agent", "ProphetBot/1.0 (contact: trading@example.com)")
```

with:

```go
req.Header.Set("User-Agent", fmt.Sprintf("PennyProphet Trading Bot %s", s.operatorEmail))
```

- [ ] **Step 6: Update GetRegulatoryScore to use EffectiveScore**

Replace:

```go
return scoreWithDecay(e.BaseScore, e.DetectedAt, regulatoryHalfLifeHours), e.EventDesc
```

with:

```go
return e.Entry.EffectiveScore(), e.EventDesc
```

- [ ] **Step 7: Add timestamp parse helpers**

Add before `pollEdgar`:

```go
func parseAtomDate(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Now(), true
	}
	return t, false
}

func parseRSSDate(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC1123Z, s)
	if err != nil {
		t, err = time.Parse("Mon, 02 Jan 2006 15:04:05 MST", s)
		if err != nil {
			return time.Now(), true
		}
	}
	return t, false
}
```

- [ ] **Step 8: Rewrite upsertEntry with max rule**

Replace:

```go
func (s *SECEdgarService) upsertEntry(ticker string, base float64, now time.Time, desc string) {
	existing, ok := s.entries[ticker]
	if !ok || base > existing.BaseScore {
		s.entries[ticker] = regulatoryEntry{BaseScore: base, DetectedAt: now, EventDesc: desc}
	}
}
```

with:

```go
// upsertEntry implements the max rule: replace only when new_base > existing decayed score.
// Caller must hold mu.Lock.
func (s *SECEdgarService) upsertEntry(ticker string, newBase float64, eventTime time.Time, desc string) {
	existing, ok := s.entries[ticker]
	if !ok {
		s.entries[ticker] = regulatoryEntry{
			Entry:     DecayEntry{BaseScore: newBase, EventTime: eventTime, HalfLifeHrs: regulatoryHalfLifeHours},
			EventDesc: desc,
		}
		return
	}
	if newBase > existing.Entry.EffectiveScore() {
		s.entries[ticker] = regulatoryEntry{
			Entry:     DecayEntry{BaseScore: newBase, EventTime: eventTime, HalfLifeHrs: regulatoryHalfLifeHours},
			EventDesc: desc,
		}
	}
}
```

- [ ] **Step 9: Update poll to track fallback rate; update pollEdgar and pollGlobeNewswire**

Replace `poll()`:

```go
func (s *SECEdgarService) poll() {
	tickers := tickerSet(s.universe.GetTickers())
	fb1, tot1 := s.pollEdgar(tickers)
	fb2, tot2 := s.pollGlobeNewswire(tickers)
	total := tot1 + tot2
	fallbacks := fb1 + fb2
	if total > 0 && float64(fallbacks)/float64(total) > 0.50 {
		s.logger.WithField("pct", fmt.Sprintf("%.0f%%", float64(fallbacks)/float64(total)*100)).
			Error("decay anchor fallback rate — EDGAR feed format may have changed")
	}
}
```

Replace `pollEdgar` signature and body:

```go
func (s *SECEdgarService) pollEdgar(tickers map[string]bool) (fallbacks, total int) {
	const edgarURL = "https://www.sec.gov/cgi-bin/browse-edgar?action=getcurrent&type=8-K&dateb=&owner=include&count=40&search_text=&output=atom"
	entries, err := s.fetchAtom(edgarURL)
	if err != nil {
		s.logger.WithError(err).Warn("SECEdgarService: EDGAR poll failed")
		return 0, 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range entries {
		total++
		ticker := extractTickerFromTitle(entry.Title, tickers)
		if ticker == "" {
			continue
		}
		eventTime, isFallback := parseAtomDate(entry.Updated)
		if isFallback {
			fallbacks++
			s.logger.Warnf("decay anchor: observation fallback used for %s", ticker)
		}
		desc := fmt.Sprintf("8-K filed %s", eventTime.Format("15:04 ET"))
		s.upsertEntry(ticker, 40.0, eventTime, desc)
	}
	return fallbacks, total
}
```

Add `PubDate string \`xml:"pubDate"\`` to `rssItem` struct.

Replace `pollGlobeNewswire` signature and body:

```go
func (s *SECEdgarService) pollGlobeNewswire(tickers map[string]bool) (fallbacks, total int) {
	const gnwURL = "https://www.globenewswire.com/RssFeed/country/US"
	items, err := s.fetchRSS(gnwURL)
	if err != nil {
		s.logger.WithError(err).Warn("SECEdgarService: GlobeNewswire poll failed")
		return 0, 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range items {
		combined := strings.ToUpper(item.Title + " " + item.Description)
		for ticker := range tickers {
			if strings.Contains(combined, ticker) {
				total++
				eventTime, isFallback := parseRSSDate(item.PubDate)
				if isFallback {
					fallbacks++
					s.logger.Warnf("decay anchor: observation fallback used for %s", ticker)
				}
				desc := fmt.Sprintf("PR wire mention %s", eventTime.Format("15:04 ET"))
				s.upsertEntry(ticker, 25.0, eventTime, desc)
			}
		}
	}
	return fallbacks, total
}
```

- [ ] **Step 10: Run all tests**

```
go test ./services/... -run "TestSECEdgar" -v
```

Expected: all PASS

- [ ] **Step 11: Update aggregator test helper (if it references old regulatoryEntry)**

In `penny_signal_aggregator_test.go`, find `aggregatorForTest` and update the `edgar.entries` initialization:

```go
// OLD:
edgar.entries[t] = regulatoryEntry{BaseScore: regScore, DetectedAt: time.Now(), EventDesc: "test event"}
// NEW:
edgar.entries[t] = regulatoryEntry{
    Entry:     DecayEntry{BaseScore: regScore, EventTime: time.Now(), HalfLifeHrs: regulatoryHalfLifeHours},
    EventDesc: "test event",
}
```

- [ ] **Step 12: Run full services tests**

```
go test ./services/... -v
```

Expected: all PASS

- [ ] **Step 13: Commit**

```
git add services/sec_edgar_service.go services/sec_edgar_service_test.go services/penny_signal_aggregator_test.go
git commit -m "feat: add operatorEmail User-Agent; migrate SECEdgar to DecayEntry; implement max-rule upsert with timestamp parsing"
```

---

## Task 6: SocialSignalService — 7-Day Rolling Baseline, New Scoring, DecayEntry

**Files:**
- Modify: `services/social_signal_service.go`
- Create: `services/social_signal_service_test.go`

- [ ] **Step 1: Write failing tests**

Create `services/social_signal_service_test.go`:

```go
package services

import (
	"testing"
	"time"
)

func bucketIdx(t time.Time) int {
	return int(t.Unix()/1800) % 336
}

func TestMentionBaseline_Advance_AccumulatesInSameBucket(t *testing.T) {
	bl := &mentionBaseline{firstSeen: time.Now().Add(-73 * time.Hour)}
	now := time.Now()
	bl.lastBucket = bucketIdx(now)
	bl.advance(now, 5)
	bl.advance(now, 3)
	if bl.total != 8 {
		t.Errorf("expected total=8, got %d", bl.total)
	}
	if bl.buckets[bucketIdx(now)] != 8 {
		t.Errorf("expected current bucket=8, got %d", bl.buckets[bucketIdx(now)])
	}
}

func TestMentionBaseline_Advance_ZerosPassedBuckets(t *testing.T) {
	bl := &mentionBaseline{firstSeen: time.Now().Add(-73 * time.Hour)}
	// Seed an old bucket
	old := time.Now().Add(-2 * time.Hour)
	bl.lastBucket = bucketIdx(old)
	oldIdx := bucketIdx(old)
	bl.buckets[oldIdx] = 10
	bl.total = 10

	// Advance to now (4 buckets later)
	now := time.Now()
	bl.advance(now, 3)

	if bl.buckets[oldIdx] != 0 {
		t.Errorf("expected old bucket zeroed, got %d", bl.buckets[oldIdx])
	}
	if bl.total != 3 {
		t.Errorf("expected total=3 after clearing old bucket, got %d", bl.total)
	}
}

func TestMentionBaseline_BaselinePer30min_Floor(t *testing.T) {
	bl := &mentionBaseline{total: 0, firstSeen: time.Now().Add(-73 * time.Hour)}
	got := bl.baselinePer30min()
	if got < 0.5 {
		t.Errorf("expected floor 0.5, got %f", got)
	}
}

func TestMentionBaseline_BaselinePer30min_BelowFloor(t *testing.T) {
	// total=10 / 336 ≈ 0.03 < 0.5 → floor applies
	bl := &mentionBaseline{total: 10, firstSeen: time.Now().Add(-73 * time.Hour)}
	got := bl.baselinePer30min()
	if got != 0.5 {
		t.Errorf("expected 0.5 floor for low total, got %f", got)
	}
}

func TestSocialService_NewTicker_72hGuard(t *testing.T) {
	svc := &SocialSignalService{
		entries:   make(map[string]socialEntry),
		baselines: make(map[string]*mentionBaseline),
		logger:    newTestLogger(),
		universe:  &PennyUniverseService{},
	}
	now := time.Now()
	// Ticker first seen < 72h ago
	counts := map[string]int{"NEW": 50}
	svc.recomputeRedditScores(now, counts)

	entry, ok := svc.entries["NEW"]
	if !ok {
		t.Fatal("expected entry for NEW")
	}
	if entry.MentionPts != 0 {
		t.Errorf("expected MentionPts=0 for new ticker (<72h), got %f", entry.MentionPts)
	}
}

func TestSocialService_UniverseExitCleanup(t *testing.T) {
	universe := &PennyUniverseService{}
	universe.universe = []UniverseSymbol{{Ticker: "KEEP"}}
	svc := &SocialSignalService{
		entries:   make(map[string]socialEntry),
		baselines: make(map[string]*mentionBaseline),
		logger:    newTestLogger(),
		universe:  universe,
	}
	svc.baselines["KEEP"] = &mentionBaseline{firstSeen: time.Now().Add(-73 * time.Hour)}
	svc.baselines["GONE"] = &mentionBaseline{firstSeen: time.Now().Add(-73 * time.Hour)}

	now := time.Now()
	svc.recomputeRedditScores(now, map[string]int{"KEEP": 5})

	if _, ok := svc.baselines["GONE"]; ok {
		t.Error("expected GONE removed from baselines after universe exit cleanup")
	}
	if _, ok := svc.baselines["KEEP"]; !ok {
		t.Error("expected KEEP preserved in baselines")
	}
}

func newTestLogger() *logrus.Logger {
	return logrus.New()
}
```

Add `"github.com/sirupsen/logrus"` import.

- [ ] **Step 2: Run tests to verify they fail**

```
go test ./services/... -run "TestMentionBaseline|TestSocialService" -v
```

Expected: FAIL — `mentionBaseline` undefined, `recomputeRedditScores` signature mismatch.

- [ ] **Step 3: Add mentionBaseline struct**

In `social_signal_service.go`, replace:

```go
type mentionRecord struct {
	Ticker    string
	Timestamp time.Time
}

type socialEntry struct {
	BaseScore  float64
	MentionPts float64
	DetectedAt time.Time
	Context    string
}
```

with:

```go
type mentionBaseline struct {
	buckets    [336]int
	total      int
	firstSeen  time.Time
	lastBucket int
}

func (b *mentionBaseline) advance(now time.Time, newCount int) {
	currentBucket := int(now.Unix()/1800) % 336
	if currentBucket != b.lastBucket {
		passed := (currentBucket - b.lastBucket + 336) % 336
		if passed >= 336 {
			b.total = 0
			for i := range b.buckets {
				b.buckets[i] = 0
			}
		} else {
			for i := 1; i <= passed; i++ {
				idx := (b.lastBucket + i) % 336
				b.total -= b.buckets[idx]
				b.buckets[idx] = 0
			}
		}
		b.lastBucket = currentBucket
	}
	b.buckets[currentBucket] += newCount
	b.total += newCount
}

func (b *mentionBaseline) baselinePer30min() float64 {
	avg := float64(b.total) / 336.0
	if avg < 0.5 {
		return 0.5
	}
	return avg
}

type socialEntry struct {
	Entry      DecayEntry
	MentionPts float64
	Context    string
}
```

- [ ] **Step 4: Update SocialSignalService struct**

Replace `mentionWindow []mentionRecord` with `baselines map[string]*mentionBaseline` in the struct:

```go
type SocialSignalService struct {
	httpClient *http.Client
	universe   *PennyUniverseService
	mu         sync.RWMutex
	entries    map[string]socialEntry
	baselines  map[string]*mentionBaseline
	logger     *logrus.Logger
}
```

Update `NewSocialSignalService` to initialize `baselines`:

```go
return &SocialSignalService{
	httpClient: httpClient,
	universe:   universe,
	entries:    make(map[string]socialEntry),
	baselines:  make(map[string]*mentionBaseline),
	logger:     logger,
}
```

- [ ] **Step 5: Update GetSocialScore**

Replace:

```go
return scoreWithDecay(e.BaseScore, e.DetectedAt, socialHalfLifeHours), e.Context
```

with:

```go
return e.Entry.EffectiveScore(), e.Context
```

- [ ] **Step 6: Rewrite pollReddit**

Replace `pollReddit`:

```go
func (s *SocialSignalService) pollReddit() {
	subreddits := []string{"pennystocks", "RobinHoodPennyStocks"}
	tickers := tickerSet(s.universe.GetTickers())
	now := time.Now()
	counts := make(map[string]int)

	for _, sub := range subreddits {
		url := fmt.Sprintf("https://www.reddit.com/r/%s/new.json?limit=100", sub)
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "ProphetBot/1.0 (contact: trading@example.com)")
		resp, err := s.httpClient.Do(req)
		if err != nil {
			s.logger.WithError(err).Warnf("SocialSignalService: Reddit r/%s failed", sub)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			s.logger.WithField("status", resp.StatusCode).Warnf("SocialSignalService: Reddit r/%s non-200", sub)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var listing redditListing
		if err := json.Unmarshal(body, &listing); err != nil {
			continue
		}
		for _, child := range listing.Data.Children {
			combined := strings.ToUpper(child.Data.Title + " " + child.Data.Selftext)
			for _, m := range tickerRegex.FindAllStringSubmatch(combined, -1) {
				if len(m) >= 2 && tickers[m[1]] {
					counts[m[1]]++
				}
			}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.recomputeRedditScores(now, counts)
}
```

- [ ] **Step 7: Rewrite recomputeRedditScores**

Replace the existing `recomputeRedditScores` and `pruneWindow`:

```go
func (s *SocialSignalService) recomputeRedditScores(now time.Time, counts map[string]int) {
	for ticker, count := range counts {
		bl, ok := s.baselines[ticker]
		if !ok {
			bl = &mentionBaseline{firstSeen: now, lastBucket: int(now.Unix()/1800) % 336}
			s.baselines[ticker] = bl
		}
		bl.advance(now, count)

		var mentionVelocityPts float64
		if time.Since(bl.firstSeen) >= 72*time.Hour {
			velocity := math.Min(float64(count)/bl.baselinePer30min(), 2.0)
			mentionVelocityPts = velocity * 5.0
		}

		var sentimentPts float64
		if existing, ok := s.entries[ticker]; ok {
			sentimentPts = existing.Entry.BaseScore - existing.MentionPts
			if sentimentPts < 0 {
				sentimentPts = 0
			}
		}

		score := math.Min(mentionVelocityPts+sentimentPts, 20.0)
		ctx := fmt.Sprintf("mentions=%d", count)
		if time.Since(bl.firstSeen) >= 72*time.Hour {
			ctx = fmt.Sprintf("mentions=%d velocity=%.1fx", count, float64(count)/bl.baselinePer30min())
		}
		s.entries[ticker] = socialEntry{
			Entry:      DecayEntry{BaseScore: score, EventTime: now, HalfLifeHrs: socialHalfLifeHours},
			MentionPts: mentionVelocityPts,
			Context:    ctx,
		}
	}

	// Evict entries for tickers with no current mentions
	for ticker := range s.entries {
		if _, ok := counts[ticker]; !ok {
			delete(s.entries, ticker)
		}
	}

	// Universe-exit cleanup: remove baselines for tickers no longer in universe
	universeTickers := tickerSet(s.universe.GetTickers())
	for ticker := range s.baselines {
		if !universeTickers[ticker] {
			delete(s.baselines, ticker)
		}
	}
}
```

Delete `pruneWindow` (no longer used).

- [ ] **Step 8: Update fetchStockTwits to use new socialEntry**

Replace the entry construction at the bottom of `fetchStockTwits`:

```go
s.mu.Lock()
defer s.mu.Unlock()
existing := s.entries[ticker]
newScore := min64(existing.MentionPts+sentimentPts, 20.0)
signalCtx := fmt.Sprintf("%s st_bullish=%.0f%%", existing.Context, ratio*100)
s.entries[ticker] = socialEntry{
	Entry:      DecayEntry{BaseScore: newScore, EventTime: existing.Entry.EventTime, HalfLifeHrs: socialHalfLifeHours},
	MentionPts: existing.MentionPts,
	Context:    signalCtx,
}
```

Update `pollStockTwitsForTopMentioned` to use `e.Entry.EffectiveScore()`:

```go
decayed := e.Entry.EffectiveScore()
```

- [ ] **Step 9: Delete the now-unused constants and types**

Remove `mentionWindowDuration` constant and `mentionRecord` type (already replaced).

- [ ] **Step 10: Run tests**

```
go test ./services/... -run "TestMentionBaseline|TestSocialService" -v
```

Expected: all PASS

- [ ] **Step 11: Update aggregator test helper for new socialEntry**

In `penny_signal_aggregator_test.go`, update `aggregatorForTest`:

```go
// OLD:
social.entries[t] = socialEntry{BaseScore: socScore, MentionPts: socScore, DetectedAt: time.Now(), Context: "test ctx"}
// NEW:
social.entries[t] = socialEntry{
    Entry:      DecayEntry{BaseScore: socScore, EventTime: time.Now(), HalfLifeHrs: socialHalfLifeHours},
    MentionPts: socScore,
    Context:    "test ctx",
}
```

Also update `screener.scores` initialization:

```go
// OLD:
screener.scores[t] = TechnicalEntry{Score: techScore, UpdatedAt: time.Now()}
// NEW:
screener.scores[t] = TechnicalEntry{Entry: DecayEntry{BaseScore: techScore, EventTime: time.Now(), HalfLifeHrs: 2.0}}
```

- [ ] **Step 12: Run full services tests**

```
go test ./services/... -v
```

Expected: all PASS

- [ ] **Step 13: Commit**

```
git add services/social_signal_service.go services/social_signal_service_test.go services/penny_signal_aggregator_test.go
git commit -m "feat: add 7-day per-ticker rolling baseline to SocialSignalService; migrate to DecayEntry; add 72h new-ticker guard"
```

---

## Task 7: PennySignalAggregator — Per-Signal Minimums + Eligibility Gate

**Files:**
- Modify: `services/penny_signal_aggregator.go`
- Modify: `services/penny_signal_aggregator_test.go`

- [ ] **Step 1: Write failing tests**

Add to `penny_signal_aggregator_test.go`:

```go
func TestAggregator_SingleSignalBlocked_HighReg(t *testing.T) {
	// tech=10 (<15 min), reg=30 (≥25), social=5 (<10 min) → only reg contributes → single → not eligible
	agg := aggregatorForTest(10.0, 30.0, 5.0, []string{"TICK"})
	agg.aggregate()
	candidates := agg.GetCandidates(0)
	if len(candidates) != 0 {
		t.Errorf("single-signal candidate should not appear in GetCandidates, got %d", len(candidates))
	}
	detail := agg.GetSignalDetail("TICK")
	if detail == nil {
		t.Fatal("single-signal candidate should still be stored internally")
	}
	if detail.CompositeEligible {
		t.Error("expected CompositeEligible=false for single-signal")
	}
	if detail.SignalCount != 1 {
		t.Errorf("expected SignalCount=1, got %d", detail.SignalCount)
	}
}

func TestAggregator_TwoSignalPasses(t *testing.T) {
	// tech=20 (≥15), reg=30 (≥25), social=5 (<10) → tech+reg contribute → eligible
	agg := aggregatorForTest(20.0, 30.0, 5.0, []string{"TICK"})
	agg.aggregate()
	candidates := agg.GetCandidates(0)
	if len(candidates) != 1 {
		t.Errorf("two-signal candidate should appear in GetCandidates, got %d", len(candidates))
	}
	if candidates[0].SignalCount != 2 {
		t.Errorf("expected SignalCount=2, got %d", candidates[0].SignalCount)
	}
	if !candidates[0].CompositeEligible {
		t.Error("expected CompositeEligible=true")
	}
	// composite = techEff(20) + regEff(30) + socEff(0) = 50
	if candidates[0].CompositeScore < 49 || candidates[0].CompositeScore > 51 {
		t.Errorf("expected composite ~50, got %f", candidates[0].CompositeScore)
	}
}

func TestAggregator_ThreeSignalMaxCapped(t *testing.T) {
	// tech=40, reg=40, social=20 → composite = min(100, 100) = 100
	agg := aggregatorForTest(40.0, 40.0, 20.0, []string{"TICK"})
	agg.aggregate()
	candidates := agg.GetCandidates(0)
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	if candidates[0].CompositeScore != 100.0 {
		t.Errorf("expected composite=100, got %f", candidates[0].CompositeScore)
	}
	if candidates[0].SignalCount != 3 {
		t.Errorf("expected SignalCount=3, got %d", candidates[0].SignalCount)
	}
}

func TestAggregator_DominantSignal_UsesEffective(t *testing.T) {
	// tech=10 (below min → techEff=0), reg=30 (≥25 → regEff=30), social=15 (≥10 → socEff=15)
	// dominant from effective: reg=30/40=0.75, soc=15/20=0.75 → tie → regulatory wins (priority)
	agg := aggregatorForTest(10.0, 30.0, 15.0, []string{"TICK"})
	agg.aggregate()
	detail := agg.GetSignalDetail("TICK")
	if detail == nil {
		t.Fatal("expected detail")
	}
	if detail.DominantSignal != "regulatory" {
		t.Errorf("expected dominant=regulatory, got %q", detail.DominantSignal)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```
go test ./services/... -run "TestAggregator_SingleSignal|TestAggregator_TwoSignal|TestAggregator_ThreeSignal|TestAggregator_DominantSignal_UseEff" -v
```

Expected: FAIL — `CompositeEligible` and `SignalCount` not yet set.

- [ ] **Step 3: Update aggregate() with per-signal minimums**

Replace `aggregate()`:

```go
func (a *PennySignalAggregator) aggregate() {
	universe := a.universe.GetUniverse()
	now := time.Now()

	a.mu.Lock()
	defer a.mu.Unlock()

	for _, u := range universe {
		techScore, techCtx := a.screener.GetTechnicalScore(u.Ticker)
		regScore, regEvent := a.edgar.GetRegulatoryScore(u.Ticker)
		socScore, socCtx := a.social.GetSocialScore(u.Ticker)

		techEff := techScore
		if techScore < 15 {
			techEff = 0
		}
		regEff := regScore
		if regScore < 25 {
			regEff = 0
		}
		socEff := socScore
		if socScore < 10 {
			socEff = 0
		}

		signalCount := 0
		if techEff > 0 {
			signalCount++
		}
		if regEff > 0 {
			signalCount++
		}
		if socEff > 0 {
			signalCount++
		}

		composite := math.Min(techEff+regEff+socEff, 100.0)
		eligible := signalCount >= 2

		if composite < evictionThreshold {
			delete(a.candidates, u.Ticker)
			continue
		}

		if !eligible {
			a.logger.WithFields(logrus.Fields{
				"ticker":    u.Ticker,
				"composite": composite,
			}).Debug("single-signal candidate, below confluence requirement")
		}

		a.candidates[u.Ticker] = CandidateScore{
			Ticker:              u.Ticker,
			Price:               u.Price,
			CompositeScore:      composite,
			SignalCount:         signalCount,
			CompositeEligible:   eligible,
			TechnicalScore:      techScore,
			TechnicalEffective:  techEff,
			RegulatoryScore:     regScore,
			RegulatoryEffective: regEff,
			SocialScore:         socScore,
			SocialEffective:     socEff,
			DominantSignal:      dominantSignal(techEff, regEff, socEff),
			TechnicalContext:    techCtx,
			RegulatoryEvent:     regEvent,
			SocialContext:       socCtx,
			LastUpdated:         now,
		}
	}
}
```

Add `"github.com/sirupsen/logrus"` to import if needed (it's already present).

- [ ] **Step 4: Update GetCandidates to filter by CompositeEligible**

Replace:

```go
func (a *PennySignalAggregator) GetCandidates(minScore float64) []CandidateScore {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var out []CandidateScore
	for _, c := range a.candidates {
		if c.CompositeScore >= minScore {
			out = append(out, c)
		}
	}
	...
}
```

with:

```go
func (a *PennySignalAggregator) GetCandidates(minScore float64) []CandidateScore {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var out []CandidateScore
	for _, c := range a.candidates {
		if !c.CompositeEligible || c.CompositeScore < minScore {
			continue
		}
		if a.blacklist.IsBlacklisted(c.Ticker) {
			continue
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CompositeScore > out[j].CompositeScore
	})
	return out
}
```

(The blacklist field is added next step — add a placeholder `a.blacklist` for now by adding the `BracketBlacklist` type and field simultaneously below.)

- [ ] **Step 5: Add BracketBlacklist type and wire into aggregator**

Add before `PennySignalAggregator` struct:

```go
// BracketBlacklistEntry records a session-scoped bracket-rejection for one ticker.
type BracketBlacklistEntry struct {
	Ticker       string
	RejectedAt   time.Time
	RejectReason string
	AttemptCount int
}

type BracketBlacklist struct {
	mu      sync.RWMutex
	entries map[string]BracketBlacklistEntry
}

func newBracketBlacklist() *BracketBlacklist {
	return &BracketBlacklist{entries: make(map[string]BracketBlacklistEntry)}
}

func (b *BracketBlacklist) IsBlacklisted(ticker string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	_, ok := b.entries[ticker]
	return ok
}
```

Add `blacklist *BracketBlacklist` field to `PennySignalAggregator`. Initialize it in `NewPennySignalAggregator`:

```go
return &PennySignalAggregator{
	universe:   universe,
	screener:   screener,
	edgar:      edgar,
	social:     social,
	candidates: make(map[string]CandidateScore),
	blacklist:  newBracketBlacklist(),
	logger:     logger,
}
```

Add aggregator methods:

```go
func (a *PennySignalAggregator) AddToBlacklist(ticker, reason string) {
	a.blacklist.mu.Lock()
	defer a.blacklist.mu.Unlock()
	if entry, ok := a.blacklist.entries[ticker]; ok {
		entry.AttemptCount++
		entry.RejectReason = reason
		a.blacklist.entries[ticker] = entry
	} else {
		a.blacklist.entries[ticker] = BracketBlacklistEntry{
			Ticker:       ticker,
			RejectedAt:   time.Now(),
			RejectReason: reason,
			AttemptCount: 1,
		}
	}
	a.logger.WithField("ticker", ticker).WithField("reason", reason).
		Info("PennySignalAggregator: added to bracket blacklist")
}

func (a *PennySignalAggregator) RemoveFromBlacklist(ticker string) {
	a.blacklist.mu.Lock()
	defer a.blacklist.mu.Unlock()
	delete(a.blacklist.entries, ticker)
}

func (a *PennySignalAggregator) ClearBlacklist() {
	a.blacklist.mu.Lock()
	defer a.blacklist.mu.Unlock()
	a.blacklist.entries = make(map[string]BracketBlacklistEntry)
}
```

- [ ] **Step 6: Update existing aggregator tests to reflect new minimums**

In `TestAggregator_Composite`, the fixture uses `tech=30, reg=20, social=10`. With new minimums: reg=20 < 25 → regEff=0. Composite = 30+0+10 = 40 with signalCount=2 (eligible). Update the test expectation:

```go
// OLD:
if c.CompositeScore < 59 || c.CompositeScore > 61 {
    t.Errorf("expected composite ~60, got %f", c.CompositeScore)
}
// NEW:
// tech=30 (≥15), reg=20 (<25 → 0), social=10 (≥10) → composite=40, eligible=true
if c.CompositeScore < 39 || c.CompositeScore > 41 {
    t.Errorf("expected composite ~40, got %f", c.CompositeScore)
}
if !c.CompositeEligible {
    t.Error("expected CompositeEligible=true (tech+social both contribute)")
}
```

- [ ] **Step 7: Run all aggregator tests**

```
go test ./services/... -run "TestAggregator" -v
```

Expected: all PASS

- [ ] **Step 8: Run full services tests**

```
go test ./services/... -v
```

Expected: all PASS

- [ ] **Step 9: Commit**

```
git add services/penny_signal_aggregator.go services/penny_signal_aggregator_test.go
git commit -m "feat: add per-signal minimums and CompositeEligible gate; add BracketBlacklist to aggregator"
```

---

## Task 8: Controller — Blacklist DELETE Endpoints

**Files:**
- Modify: `controllers/penny_controller.go`
- Modify: router registration file (find by grepping for `HandleGetCandidates`)

- [ ] **Step 1: Find route registration**

```
grep -r "HandleGetCandidates\|penny_controller\|PennyController" --include="*.go" -l
```

Note the file(s) returned — that's where you'll register the new DELETE routes.

- [ ] **Step 2: Write failing test**

Read `controllers/penny_controller_test.go` (if it exists) for test patterns. Add (or create `controllers/penny_controller_test.go`):

```go
package controllers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"prophet-trader/services"

	"github.com/gin-gonic/gin"
)

func TestHandleClearBlacklist_Returns200(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// nil service args: NewPennySignalAggregator only touches services during aggregate();
	// blacklist operations are safe with nil service pointers.
	agg := services.NewPennySignalAggregator(nil, nil, nil, nil)
	agg.AddToBlacklist("TICK", "bracket rejection test")
	pc := NewPennyController(agg)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("DELETE", "/api/v1/penny/blacklist", nil)
	pc.HandleClearBlacklist(c)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if agg.IsBlacklisted("TICK") {
		t.Error("expected blacklist cleared after HandleClearBlacklist")
	}
}

func TestHandleRemoveFromBlacklist_Returns200(t *testing.T) {
	gin.SetMode(gin.TestMode)
	agg := services.NewPennySignalAggregator(nil, nil, nil, nil)
	agg.AddToBlacklist("RMVD", "test")
	agg.AddToBlacklist("KEEP", "test")
	pc := NewPennyController(agg)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "ticker", Value: "RMVD"}}
	c.Request = httptest.NewRequest("DELETE", "/api/v1/penny/blacklist/RMVD", nil)
	pc.HandleRemoveFromBlacklist(c)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if agg.IsBlacklisted("RMVD") {
		t.Error("expected RMVD removed from blacklist")
	}
	if !agg.IsBlacklisted("KEEP") {
		t.Error("expected KEEP still blacklisted")
	}
}
```

- [ ] **Step 3: Add handler methods to penny_controller.go**

Add at the end of `penny_controller.go`:

```go
// HandleClearBlacklist clears the entire bracket-rejection blacklist for this session.
// DELETE /api/v1/penny/blacklist
func (pc *PennyController) HandleClearBlacklist(c *gin.Context) {
	pc.aggregator.ClearBlacklist()
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// HandleRemoveFromBlacklist removes one ticker from the bracket-rejection blacklist.
// DELETE /api/v1/penny/blacklist/:ticker
func (pc *PennyController) HandleRemoveFromBlacklist(c *gin.Context) {
	ticker := c.Param("ticker")
	pc.aggregator.RemoveFromBlacklist(ticker)
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
```

- [ ] **Step 4: Register routes**

In the router registration file found in Step 1, add alongside existing penny routes:

```go
penny.DELETE("/blacklist", pennyController.HandleClearBlacklist)
penny.DELETE("/blacklist/:ticker", pennyController.HandleRemoveFromBlacklist)
```

- [ ] **Step 5: Build check**

```
go build ./...
```

Expected: successful (except `cmd/bot/main.go` with changed constructor signatures — that's expected until Task 9).

- [ ] **Step 6: Run controller tests**

```
go test ./controllers/... -v
```

Expected: all PASS

- [ ] **Step 7: Commit**

```
git add controllers/penny_controller.go
git commit -m "feat: add DELETE /blacklist and DELETE /blacklist/:ticker operator endpoints"
```

---

## Task 9: main.go Wiring — OperatorEmail + Alpaca Creds

**Files:**
- Modify: `cmd/bot/main.go`

- [ ] **Step 1: Read main.go**

```
Read cmd/bot/main.go
```

Find all calls to `NewSECEdgarService` and `NewPennyUniverseService`.

- [ ] **Step 2: Update NewSECEdgarService call**

Find the line calling `NewSECEdgarService(universe, httpClient)` and add the third argument:

```go
// OLD:
edgar := services.NewSECEdgarService(universe, nil)
// NEW:
edgar := services.NewSECEdgarService(universe, nil, config.AppConfig.OperatorEmail)
```

- [ ] **Step 3: Update NewPennyUniverseService call**

Find the line calling `NewPennyUniverseService(fmpKey, httpClient)` and add the Alpaca params:

```go
// OLD:
universe := services.NewPennyUniverseService(config.AppConfig.FMPAPIKey, nil)
// NEW:
universe := services.NewPennyUniverseService(
    config.AppConfig.FMPAPIKey,
    config.AppConfig.AlpacaAPIKey,
    config.AppConfig.AlpacaSecretKey,
    config.AppConfig.AlpacaBaseURL,
    nil,
)
```

- [ ] **Step 4: Full build**

```
go build ./...
```

Expected: successful (no compile errors)

- [ ] **Step 5: Run all tests**

```
go test ./... -v
```

Expected: all PASS

- [ ] **Step 6: Commit**

```
git add cmd/bot/main.go
git commit -m "fix: wire OperatorEmail to SECEdgarService; wire Alpaca creds to PennyUniverseService"
```

---

## Task 10: TRADING_RULES_PENNY.md — All Trading Rules Changes

**Files:**
- Modify: `TRADING_RULES_PENNY.md`

This task has no code compilation — verify changes by reading the file after each edit.

- [ ] **Step 1: Prepend 7 operational discipline sections after Core Philosophy**

After the `## Core Philosophy` section (line ~14) and before `## Signal-Gated Entry`, insert these sections verbatim from `potential additions/PENNYPROPHET_REVISIONS.md` Revision 10:

```markdown
## How You Operate

You are PennyProphet, a signal-gated penny stock momentum trading agent. You
are not a reasoning agent. You are a rule executor wrapped in a language
model. Your job is to apply the rules below mechanically against the candidate
data provided by the signal pipeline.

Your outputs are limited to:
  1. Actions specified by your rules (enter, exit, manage, skip, halt)
  2. Structured logs via log_activity and log_decision
  3. Mechanical tool calls to fetch data and execute trades

You do not:
  - Produce free-form market commentary or opinions
  - Override exit rules because a position "looks like it might recover"
  - Override entry filters because a candidate "looks promising despite low score"
  - Suggest improvements to your own rules during a session
  - Improvise responses to situations not covered by your rules

If a situation arises that your rules do not cover, your only valid action is:
  - Halt new entries
  - Continue managing existing positions per their dominant-signal exit rules
  - Log "uncovered situation: {description}" via log_decision
  - Wait for operator instruction

Helpful improvisation is the failure mode. The signal pipeline does the
analysis. You execute against its output.

## Rule Boundary Handling

Numeric thresholds are inclusive unless explicitly stated otherwise:
  - "composite score ≥ 60" includes exactly 60
  - "P&L ≤ −5%" includes exactly −5%
  - "−8% from entry" stops at exactly −8%

For genuinely ambiguous situations not covered by rules:
  - Default to the more conservative action
  - Conservative for entries: skip
  - Conservative for exits: do not exit early; let the dominant-signal rules play out
  - Always log the ambiguity via log_decision

## When Data Is Missing or Inconsistent

  - get_penny_candidates returns empty: do nothing, log "no candidates above threshold"
  - get_penny_signal_detail returns stale data (>60s): skip that ticker, log
  - get_account fails or returns inconsistent state: halt entries, log
  - Quote at entry-time check is stale (>30s): skip that trade, log
  - Position state in get_positions doesn't match expected state: halt all
    activity, log "reconciliation mismatch — operator review required"

## Hard Stops That Override Everything

These conditions halt all trading activity immediately and require operator
action to resume:

  - Broker connection failure or authentication error
  - Trade rejection by broker for reason other than bracket-order limitation
  - Account risk warning or margin call from broker
  - Position reconciliation mismatch (internal state ≠ broker state)
  - Quote staleness exceeding 5 minutes during market hours
  - Multiple consecutive (3+) failed orders within a single heartbeat
  - Any error condition not covered by your rules

In these cases:
  - Cease all new entries
  - Do NOT attempt to manage existing positions (broker may have closed them
    or position state may be unknown)
  - Log the condition with full diagnostic detail via log_decision
  - Do not retry until operator confirms reset

This is not a rule violation — these are signals that something has broken
and continuing operation could cause harm.

## Startup / Restart Behavior

On agent startup or after a restart:

  1. Call get_positions to fetch current state from broker
  2. Compare against last known internal state (if any)
  3. If reconciliation succeeds:
       - Resume normal heartbeat operation
       - Log "session start" with full position inventory
  4. If reconciliation fails:
       - Halt all trading activity
       - Log "startup reconciliation failed — operator review required"
       - Wait for operator
  5. If no prior internal state exists (fresh start):
       - Adopt broker positions as starting state
       - Log "fresh start — adopted N broker positions"
       - Resume normal operation

The bracket-rejection blacklist is empty on every startup (cleared by
session boundary).

The daily circuit breaker is reset on startup if the prior session has ended.
If startup occurs mid-session and the breaker was tripped, it remains tripped
until the next session boundary.

## Circuit Breaker Behavior

Trigger: portfolio P&L ≤ −5% intraday (Harvest positions excluded; this is
PennyProphet-scoped P&L only).

On trigger:
  - Cancel all open bracket orders for penny positions
  - Place market sell orders for all open penny positions
  - Cease evaluating new candidates for the rest of the session
  - Continue emitting heartbeat-alive logs every interval (so operator can
    confirm agent is alive vs. crashed)
  - Do NOT poll signals or call get_penny_candidates while breaker is tripped
    (reduces unnecessary API load)

Reset: at the next market open following the trip. Breaker state is
session-scoped, not persistent across days.

Manual override: operator can reset mid-session via dashboard if conditions
warrant. Manual reset logs operator identity and timestamp.

## Glossary

  Composite score:        Sum of effective signal scores; max 100
  Effective signal score: Raw signal score if above per-signal minimum, else 0
  Dominant signal:        Highest effective signal normalized by its max
  Multi-signal confluence: At least 2 signals contributing non-zero
  Decay anchor:           Timestamp from which decay is computed
  Decay floor:            5% of base score; below this, signal is fully decayed
  ADV:                    Average daily dollar volume = avg shares × avg price
  Bracket order:          Order with stop and target legs, atomic execution
  Session:                One trading day, market open to close
  1R:                     One unit of risk; for −8% stop, 1R = +8% target
  Universe:               Set of tickers eligible for signal evaluation
  Candidate:              Universe ticker with composite score above threshold
```

- [ ] **Step 2: Update Position Sizing section**

Replace the three `**Rule:**` lines under `## Position Sizing`:

```markdown
**Rule:** Maximum 8% of portfolio in any single penny position, regardless of score.
**Rule:** Maximum 10 open penny positions simultaneously.
**Rule:** Maximum 60% of portfolio deployed in penny positions at any time.
```

Add the narrative note after those rules:

```markdown
**Note:** The deployed cap (60%) typically binds before the position count cap (10). At 6% average sizing, 10 positions = 60% deployed — both hit simultaneously.
```

- [ ] **Step 3: Update Pre-Trade Checklist position count**

Change:

```markdown
- [ ] Total open penny positions < 12?
```

to:

```markdown
- [ ] Total open penny positions < 10?
```

- [ ] **Step 4: Update Universe section in Core Philosophy**

Change:

```markdown
- **Universe** — $2.00–$10.00 price, $50M–$500M market cap, ≥$300K daily dollar volume
```

to:

```markdown
- **Universe** — $2.00–$10.00 price, $50M–$500M market cap, ≥$500K daily dollar volume (ADV = avg_volume_30d × avg_price_30d)
```

- [ ] **Step 5: Replace social exit rule**

Replace the `### dominant_signal = "social"` section:

```markdown
### `dominant_signal = "social"` (Reddit/StockTwits momentum)

ENTRY:
  - Use place_managed_position with stop and target
  - Stop: −8% from entry
  - Target: +15% (50% scale) then +20% (remaining)

TIME-BASED EXIT (overrides bracket if not yet filled):

  At 20 minutes post-entry (or 15 minutes before market close, whichever first):

  1. Cancel the active bracket order via cancel_order
  2. Confirm cancellation succeeded:
     - If cancel succeeded → proceed to step 3
     - If cancel failed because bracket already filled → log and stop (the
       position is already closed by the bracket)
     - If cancel failed for any other reason → halt agent, log
       "social-exit cancel failure, operator review required"
  3. Place market sell order for full position size
  4. Confirm fill within 60 seconds:
     - If filled → log exit, mark position closed
     - If not filled within 60 seconds → halt agent, log
       "social-exit market order stalled, operator review required"

RACE CONDITION HANDLING:

  If the bracket's stop or target leg fires before the cancel completes, the
  position is closed by the bracket — this is fine. Always confirm final
  position state via get_positions after the protocol completes.

ENTRY GATING:
  - Do not enter social positions < 30 minutes before market close
  - Social signals expiring during the last 30 minutes of trading are skipped
```

- [ ] **Step 6: Add Bracket Blacklist note after Bracket Order Requirement section**

After the `## Bracket Order Requirement` section, add:

```markdown
## Bracket Order Blacklist

If place_managed_position rejects a symbol due to bracket-order limitations,
that symbol is automatically blacklisted for the remainder of the session by
the backend — the agent does not need to take any action. Blacklisted tickers
will not appear in get_penny_candidates results during the session.

The agent must NEVER attempt to enter a position without a bracket order,
even if a candidate appears highly attractive. If place_managed_position
fails for any reason, skip the trade and log.
```

- [ ] **Step 7: Update document header date**

Change `**Updated:** 2026-04-27` to `**Updated:** 2026-05-02`.

- [ ] **Step 8: Commit**

```
git add TRADING_RULES_PENNY.md
git commit -m "docs: apply all 10 revisions to TRADING_RULES_PENNY.md — operational discipline, limits, social exit, blacklist note"
```

---

## Task 11: Agent Config — PennyTrades Permission Updates

**Files:**
- Modify: `data/agent-config.json`

- [ ] **Step 1: Update sbx_a788a4e3 permissions block**

In `data/agent-config.json`, find the `"sbx_a788a4e3"` sandbox. In its `"permissions"` object, make these changes:

| Field | From | To |
|---|---|---|
| `"allowOptions"` | `true` | `false` |
| `"maxPositionPct"` | `12` | `8` |
| `"maxDeployedPct"` | `80` | `60` |
| `"maxToolRoundsPerBeat"` | `25` | `18` |

The result should be:

```json
"permissions": {
  "allowLiveTrading": true,
  "maxPositionPct": 8,
  "maxDeployedPct": 60,
  "maxDailyLoss": 5,
  "maxOpenPositions": 10,
  "maxOrderValue": 0,
  "allowedTools": [],
  "blockedTools": [],
  "allowOptions": false,
  "allowStocks": true,
  "allow0DTE": false,
  "requireConfirmation": false,
  "maxToolRoundsPerBeat": 18
}
```

- [ ] **Step 2: Verify JSON is valid**

```
go run -e "import _ \"encoding/json\"" 2>/dev/null || python -c "import json; json.load(open('data/agent-config.json'))"
```

Or just: open the file in any JSON validator. Confirm no parse errors.

- [ ] **Step 3: Commit**

```
git add data/agent-config.json
git commit -m "config: update PennyTrades sandbox — allowOptions:false, maxPositionPct:8, maxDeployedPct:60, maxToolRoundsPerBeat:18"
```

---

## Final Validation

- [ ] **Full build**

```
go build ./...
```

Expected: successful

- [ ] **Full test suite**

```
go test ./... -v -count=1
```

Expected: all PASS; no skipped tests.

- [ ] **Confirm OPERATOR_EMAIL fail-fast works**

```
OPERATOR_EMAIL= go run ./cmd/bot/... 2>&1 | head -5
```

Expected: output contains `OPERATOR_EMAIL must be set`

- [ ] **Smoke check: run tests with race detector**

```
go test -race ./services/... -timeout 60s
```

Expected: PASS with no data races detected.

---

## Self-Review Checklist

**Spec coverage:**

| Revision | Tasks |
|---|---|
| Rev 1: composite minimums | Task 7 |
| Rev 2: decay spec | Tasks 2, 4, 5, 6 |
| Rev 3: social velocity | Task 6 |
| Rev 4: social exit + cancel protocol | Task 10 |
| Rev 5: universe race / calendar refresh | Task 3 |
| Rev 6: position limit reconciliation | Tasks 10, 11 |
| Rev 7: ADV $300K → $500K | Tasks 3, 10 |
| Rev 8: EDGAR User-Agent + latency | Tasks 5, 9 |
| Rev 9: bracket blacklist | Tasks 7, 8, 10 |
| Rev 10: operational discipline | Task 10 |

**Known gaps (acceptable):**
- Design spec annotations (§8 of revisions spec) updating `2026-04-27-pennyprophet-design.md` are documentation-only and not required for functional correctness. Apply them separately if desired.
- `SeedCandidateForTest` in the aggregator constructs `CandidateScore` by field name — the added fields (`SignalCount`, `CompositeEligible`, etc.) have zero values which won't break existing callers but tests using it may want to set `CompositeEligible: true` explicitly.
