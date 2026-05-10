# PennyProphet Dilution Filter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a SEC-EDGAR-driven dilution filter that suppresses penny candidates when an issuer has filed S-1, S-3, 424B*, F-1, F-3, or a dilution-flavored 8-K within a recent trading-day window.

**Architecture:** Extend `SECEdgarService` with a parallel `dilutionBlocks` map and a second polling loop. Aggregator queries `IsDilutionBlocked` before returning candidates, mirroring the existing `BracketBlacklist` pattern. The 8-K dilution heuristic piggybacks on the existing 8-K poll. Held positions are not auto-exited.

**Tech Stack:** Go 1.21+, `sirupsen/logrus`, `golang.org/x/net/html/charset`, standard library `encoding/xml` + `net/http`. Testing via stdlib `testing` + `httptest`.

**Spec:** `docs/superpowers/specs/2026-05-10-pennyprophet-dilution-filter-design.md`

---

## File Map

**Create:**
- `services/testdata/edgar/dilution/s1-fixture.atom`
- `services/testdata/edgar/dilution/s3-shelf-fixture.atom`
- `services/testdata/edgar/dilution/s3-amendment-fixture.atom`
- `services/testdata/edgar/dilution/424b5-fixture.atom`
- `services/testdata/edgar/dilution/f1-fixture.atom`
- `services/testdata/edgar/dilution/f3-fixture.atom`
- `services/testdata/edgar/dilution/8k-item-302-fixture.atom`
- `services/testdata/edgar/dilution/8k-item-101-spa-fixture.atom`
- `services/testdata/edgar/dilution/8k-item-101-asset-purchase-fixture.atom`
- `services/testdata/edgar/dilution/8k-item-101-licensing-fixture.atom`
- `services/testdata/edgar/dilution/8k-vague-financing-fixture.atom`
- `services/testdata/edgar/dilution/non-universe-ticker-fixture.atom`

**Modify:**
- `services/penny_earnings_service.go` — add `Calendar()` getter
- `services/penny_earnings_service_test.go` — test new getter
- `services/sec_edgar_service.go` — add `nowFunc`, `dilutionBlocks`, `IsDilutionBlocked`, `pollDilutionForms`, 8-K heuristic scanner, second `Start()` goroutine; change constructor signature
- `services/sec_edgar_service_test.go` — add fixture-driven dilution tests
- `services/penny_signal_aggregator.go` — add `IsDilutionBlocked` check in `GetCandidates`; update lock-ordering comment
- `services/penny_signal_aggregator_test.go` — add suppression tests + block-doesn't-delete-data regression test
- `cmd/bot/main.go` — update `NewSECEdgarService` call to pass earnings service
- `TRADING_RULES_PENNY.md` — document dilution filter rules
- `data/agent-config.json` — mirror to `customRules` for the `penny-momentum` strategy

---

## Notes for the implementing engineer

- The package is `services`; tests live in the same package as the code (Go convention).
- Existing helpers you will reuse: `tickerSet`, `extractTickerFromTitle`, `fetchAtom`, `tradingDayDistance`, `AlpacaCalendarEntry`. **Do not re-implement these.**
- Logging uses `sirupsen/logrus`. Match the existing field style (`logger.WithField`, `logger.WithFields`).
- Lock ordering invariant in `penny_signal_aggregator.go:24-27` is mandatory — never call back into the aggregator from inside `SECEdgarService.dilutionMu`.
- Run `go test ./services/... -race` after every task that touches concurrent state.

---

### Task 1: Add `Calendar()` getter to `EarningsCalendarService`

**Files:**
- Modify: `services/penny_earnings_service.go` (add method after `WaitForFirstRefresh`, around line 164)
- Modify: `services/penny_earnings_service_test.go` (add new test)

- [ ] **Step 1: Write the failing test**

Add to `services/penny_earnings_service_test.go`:

```go
func TestEarningsCalendarService_Calendar_ReturnsSnapshot(t *testing.T) {
	svc := &EarningsCalendarService{
		entries: map[string]earningsEntry{},
		calendar: []AlpacaCalendarEntry{
			{Date: "2026-05-11"},
			{Date: "2026-05-12"},
		},
		logger: logrus.New(),
	}
	got := svc.Calendar()
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if got[0].Date != "2026-05-11" {
		t.Errorf("expected first date 2026-05-11, got %q", got[0].Date)
	}
	// Mutating the returned slice must not affect internal state.
	got[0].Date = "MUTATED"
	if svc.calendar[0].Date == "MUTATED" {
		t.Error("Calendar() returned reference to internal slice; expected defensive copy")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```
go test ./services/... -run TestEarningsCalendarService_Calendar_ReturnsSnapshot -v
```

Expected: FAIL — `svc.Calendar undefined`.

- [ ] **Step 3: Implement the getter**

Add to `services/penny_earnings_service.go` immediately after `WaitForFirstRefresh` (around line 164):

```go
// Calendar returns a defensive copy of the cached Alpaca trading calendar.
// Other services (e.g. SECEdgarService) use this to avoid duplicate FMP/Alpaca
// fetches. Returns an empty slice if the calendar has not been populated yet.
func (s *EarningsCalendarService) Calendar() []AlpacaCalendarEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.calendar) == 0 {
		return nil
	}
	out := make([]AlpacaCalendarEntry, len(s.calendar))
	copy(out, s.calendar)
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

```
go test ./services/... -run TestEarningsCalendarService_Calendar_ReturnsSnapshot -v
```

Expected: PASS.

- [ ] **Step 5: Run full earnings suite to confirm no regression**

```
go test ./services/... -run TestEarnings -v
```

Expected: all PASS.

- [ ] **Step 6: Commit**

```
git add services/penny_earnings_service.go services/penny_earnings_service_test.go
git commit -m "feat(penny): add Calendar getter to EarningsCalendarService

Defensive-copy snapshot of the cached Alpaca trading calendar. Used by
SECEdgarService for trading-day eviction without a duplicate fetch."
```

---

### Task 2: Add `nowFunc` injectable clock to `SECEdgarService`

**Files:**
- Modify: `services/sec_edgar_service.go` (struct + constructor + internal usage)
- Modify: `services/sec_edgar_service_test.go` (add test for clock injection)

- [ ] **Step 1: Write the failing test**

Add to `services/sec_edgar_service_test.go` (after the existing `newTestEdgar` helper):

```go
func TestSECEdgarService_NowFunc_DefaultIsTimeNow(t *testing.T) {
	svc := NewSECEdgarService(nil, nil, "test@example.com", nil)
	got := svc.nowFunc()
	if time.Since(got) > 5*time.Second {
		t.Errorf("default nowFunc returned %v, expected ~time.Now()", got)
	}
}

func TestSECEdgarService_NowFunc_Override(t *testing.T) {
	fixed := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	svc := NewSECEdgarService(nil, nil, "test@example.com", nil)
	svc.nowFunc = func() time.Time { return fixed }
	got := svc.nowFunc()
	if !got.Equal(fixed) {
		t.Errorf("expected fixed time, got %v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```
go test ./services/... -run TestSECEdgarService_NowFunc -v
```

Expected: FAIL on signature mismatch — `NewSECEdgarService` currently takes 3 args, not 4. (Don't worry about the 4th arg yet; it's the earnings service from Task 3, which we'll wire next. For this task add the parameter but ignore it.)

- [ ] **Step 3: Update struct and constructor**

In `services/sec_edgar_service.go`, change the `SECEdgarService` struct (around line 25) to add the field at the bottom:

```go
type SECEdgarService struct {
	httpClient    *http.Client
	universe      *PennyUniverseService
	operatorEmail string
	mu            sync.RWMutex
	entries       map[string]regulatoryEntry
	logger        *logrus.Logger
	nowFunc       func() time.Time
	earnings      *EarningsCalendarService
}
```

Update the constructor signature (around line 35). Note: the new `earnings` parameter will be wired into use in Task 3 — for now just store it.

```go
// NewSECEdgarService creates the service. The earnings parameter provides
// access to the cached Alpaca trading calendar (via earnings.Calendar()) for
// trading-day eviction in the dilution filter; pass nil only in tests that do
// not exercise dilution-filter eviction.
func NewSECEdgarService(
	universe *PennyUniverseService,
	httpClient *http.Client,
	operatorEmail string,
	earnings *EarningsCalendarService,
) *SECEdgarService {
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
		nowFunc:       time.Now,
		earnings:      earnings,
	}
}
```

- [ ] **Step 4: Update existing test helper to set nowFunc**

In `services/sec_edgar_service_test.go`, update `newTestEdgar`:

```go
func newTestEdgar() *SECEdgarService {
	return &SECEdgarService{
		entries: make(map[string]regulatoryEntry),
		logger:  logrus.New(),
		nowFunc: time.Now,
	}
}
```

Also update `TestSECEdgarService_GetRegulatoryScore_Decay` and `TestSECEdgarService_UpsertEntry_KeepsHigher` literal struct constructions to include `nowFunc: time.Now`:

```go
svc := &SECEdgarService{
    entries: make(map[string]regulatoryEntry),
    logger:  logrus.New(),
    nowFunc: time.Now,
}
```

Update the existing call to `NewSECEdgarService` in `TestSECEdgarService_FetchAtom_NonOK` (around line 77) from 3 args to 4:

```go
svc := NewSECEdgarService(nil, ts.Client(), "test@example.com", nil)
```

- [ ] **Step 5: Run all SECEdgar tests**

```
go test ./services/... -run TestSECEdgar -v
go test ./services/... -run TestExtractTickerFromTitle -v
```

Expected: all PASS, including the new `TestSECEdgarService_NowFunc_*` cases.

- [ ] **Step 6: Update main.go constructor call so the project still builds**

In `cmd/bot/main.go`, line 169:

Old:
```go
secEdgarService := services.NewSECEdgarService(pennyUniverseService, nil, cfg.OperatorEmail)
```

New:
```go
secEdgarService := services.NewSECEdgarService(pennyUniverseService, nil, cfg.OperatorEmail, earningsService)
```

(The `earningsService` variable already exists upstream — verify by `grep -n earningsService cmd/bot/main.go` before editing.)

- [ ] **Step 7: Run full build to confirm**

```
go build ./...
```

Expected: clean build, no errors.

- [ ] **Step 8: Commit**

```
git add services/sec_edgar_service.go services/sec_edgar_service_test.go cmd/bot/main.go
git commit -m "refactor(edgar): add nowFunc clock injection and earnings dependency

Adds an injectable nowFunc to SECEdgarService for deterministic eviction
tests, and threads EarningsCalendarService into the constructor so the
upcoming dilution filter can borrow the cached trading calendar without
duplicating the fetch."
```

---

### Task 3: Add dilution data model and `IsDilutionBlocked` API

**Files:**
- Modify: `services/sec_edgar_service.go` (add types, fields, public method)
- Modify: `services/sec_edgar_service_test.go` (add behavioral tests)

- [ ] **Step 1: Write the failing tests**

Add to `services/sec_edgar_service_test.go`:

```go
func newTestEdgarWithCalendar(cal []AlpacaCalendarEntry) *SECEdgarService {
	earnings := &EarningsCalendarService{
		calendar: cal,
		entries:  map[string]earningsEntry{},
		logger:   logrus.New(),
	}
	return &SECEdgarService{
		entries:        make(map[string]regulatoryEntry),
		dilutionBlocks: make(map[string]dilutionEntry),
		logger:         logrus.New(),
		nowFunc:        time.Now,
		earnings:       earnings,
	}
}

func TestIsDilutionBlocked_UnknownTicker(t *testing.T) {
	svc := newTestEdgarWithCalendar(nil)
	blocked, reason := svc.IsDilutionBlocked("ZZZZ")
	if blocked || reason != "" {
		t.Errorf("expected (false, \"\"), got (%v, %q)", blocked, reason)
	}
}

func TestIsDilutionBlocked_TakedownWithinWindow(t *testing.T) {
	cal := []AlpacaCalendarEntry{
		{Date: "2026-05-08"}, {Date: "2026-05-09"}, {Date: "2026-05-10"},
		{Date: "2026-05-11"}, {Date: "2026-05-12"},
	}
	svc := newTestEdgarWithCalendar(cal)
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	svc.nowFunc = func() time.Time { return now }
	svc.dilutionBlocks["ABCD"] = dilutionEntry{
		Ticker:    "ABCD",
		FormType:  "S-1",
		FiledAt:   time.Date(2026, 5, 9, 16, 0, 0, 0, time.UTC),
		Bucket:    "takedown",
		SourceURL: "https://www.sec.gov/x",
	}
	blocked, reason := svc.IsDilutionBlocked("ABCD")
	if !blocked {
		t.Fatal("expected ABCD to be blocked")
	}
	if !strings.Contains(reason, "S-1") {
		t.Errorf("reason should mention form type, got %q", reason)
	}
}

func TestIsDilutionBlocked_TakedownExpired(t *testing.T) {
	cal := []AlpacaCalendarEntry{
		{Date: "2026-05-04"}, {Date: "2026-05-05"}, {Date: "2026-05-06"},
		{Date: "2026-05-07"}, {Date: "2026-05-08"}, {Date: "2026-05-09"},
		{Date: "2026-05-10"},
	}
	svc := newTestEdgarWithCalendar(cal)
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	svc.nowFunc = func() time.Time { return now }
	// Filed 4 trading days ago (mon→fri = 4 trading days), 2-day takedown window.
	svc.dilutionBlocks["ABCD"] = dilutionEntry{
		Ticker:   "ABCD",
		FormType: "S-1",
		FiledAt:  time.Date(2026, 5, 4, 16, 0, 0, 0, time.UTC),
		Bucket:   "takedown",
	}
	blocked, _ := svc.IsDilutionBlocked("ABCD")
	if blocked {
		t.Error("expected ABCD to be unblocked after window expiry")
	}
	// Verify lazy eviction removed the entry.
	if _, ok := svc.dilutionBlocks["ABCD"]; ok {
		t.Error("expected expired entry to be lazily evicted")
	}
}

func TestIsDilutionBlocked_ShelfBucketLongerWindow(t *testing.T) {
	cal := []AlpacaCalendarEntry{
		{Date: "2026-05-05"}, {Date: "2026-05-06"}, {Date: "2026-05-07"},
		{Date: "2026-05-08"}, {Date: "2026-05-09"}, {Date: "2026-05-10"},
	}
	svc := newTestEdgarWithCalendar(cal)
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	svc.nowFunc = func() time.Time { return now }
	// Filed 3 trading days ago: takedown would expire (>2), shelf still active (≤5).
	svc.dilutionBlocks["ABCD"] = dilutionEntry{
		Ticker:   "ABCD",
		FormType: "S-3",
		FiledAt:  time.Date(2026, 5, 5, 16, 0, 0, 0, time.UTC),
		Bucket:   "shelf",
	}
	blocked, _ := svc.IsDilutionBlocked("ABCD")
	if !blocked {
		t.Error("expected ABCD shelf block to still be active 3 trading days in")
	}
}

func TestIsDilutionBlocked_NoCalendarFailsClosed(t *testing.T) {
	// When the trading calendar is empty, eviction can't compute correctly.
	// Fail-closed: keep blocking. Capital protection direction.
	svc := newTestEdgarWithCalendar(nil)
	svc.dilutionBlocks["ABCD"] = dilutionEntry{
		Ticker:   "ABCD",
		FormType: "S-1",
		FiledAt:  time.Now().Add(-90 * 24 * time.Hour),
		Bucket:   "takedown",
	}
	blocked, _ := svc.IsDilutionBlocked("ABCD")
	if !blocked {
		t.Error("expected fail-closed (still blocked) when calendar is empty")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```
go test ./services/... -run TestIsDilutionBlocked -v
```

Expected: FAIL — `dilutionEntry undefined`, `dilutionBlocks undefined`, `IsDilutionBlocked undefined`.

- [ ] **Step 3: Add types and field**

In `services/sec_edgar_service.go`, add after the existing `regulatoryEntry` type definition (around line 23):

```go
// dilutionEntry records a recent dilution-related SEC filing on a universe ticker.
// One entry per ticker in dilutionBlocks; replaced when a more conservative
// (takedown beats shelf) filing arrives.
type dilutionEntry struct {
	Ticker    string
	FormType  string    // "S-1", "S-3", "424B5", "8-K-3.02", etc.
	FiledAt   time.Time // best-effort from feed timestamp
	Bucket    string    // "takedown" (2-day) or "shelf" (5-day)
	SourceURL string    // EDGAR filing URL (for log audit trail)
}

const (
	dilutionTakedownWindowDays = 2
	dilutionShelfWindowDays    = 5
)
```

Add the new map and lock to the `SECEdgarService` struct. Update the struct from Task 2:

```go
type SECEdgarService struct {
	httpClient    *http.Client
	universe      *PennyUniverseService
	operatorEmail string
	mu            sync.RWMutex
	entries       map[string]regulatoryEntry
	logger        *logrus.Logger
	nowFunc       func() time.Time
	earnings      *EarningsCalendarService

	dilutionMu     sync.RWMutex
	dilutionBlocks map[string]dilutionEntry
}
```

Update the constructor's struct literal to initialize the new map:

```go
return &SECEdgarService{
    httpClient:     httpClient,
    universe:       universe,
    operatorEmail:  operatorEmail,
    entries:        make(map[string]regulatoryEntry),
    logger:         logger,
    nowFunc:        time.Now,
    earnings:       earnings,
    dilutionBlocks: make(map[string]dilutionEntry),
}
```

- [ ] **Step 4: Implement `IsDilutionBlocked`**

Add at the bottom of `services/sec_edgar_service.go`:

```go
// IsDilutionBlocked returns (true, reason) if the ticker has an unexpired
// dilution block, or (false, "") otherwise. Eviction is lazy: an expired
// entry is removed on read.
//
// Fail-closed semantics: if the trading calendar is unavailable (empty), the
// block is preserved rather than dropped. This is the safe direction for a
// capital-protection filter — we'd rather over-suppress than miss a real
// dilution event during a calendar outage.
func (s *SECEdgarService) IsDilutionBlocked(ticker string) (bool, string) {
	s.dilutionMu.RLock()
	entry, ok := s.dilutionBlocks[ticker]
	s.dilutionMu.RUnlock()
	if !ok {
		return false, ""
	}

	var calendar []AlpacaCalendarEntry
	if s.earnings != nil {
		calendar = s.earnings.Calendar()
	}
	if len(calendar) == 0 {
		// Fail-closed: keep blocking when we can't compute eviction.
		return true, dilutionReason(entry, -1)
	}

	now := s.nowFunc()
	nowDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	filedDate := time.Date(entry.FiledAt.Year(), entry.FiledAt.Month(), entry.FiledAt.Day(), 0, 0, 0, 0, entry.FiledAt.Location())
	distance := tradingDayDistance(filedDate, nowDate, calendar)
	window := dilutionTakedownWindowDays
	if entry.Bucket == "shelf" {
		window = dilutionShelfWindowDays
	}
	if distance > window {
		s.dilutionMu.Lock()
		delete(s.dilutionBlocks, ticker)
		s.dilutionMu.Unlock()
		return false, ""
	}
	return true, dilutionReason(entry, distance)
}

// dilutionReason builds a human-readable string for log lines and
// log_decision audit trails. distance < 0 means "calendar unavailable".
func dilutionReason(e dilutionEntry, distance int) string {
	if distance < 0 {
		return fmt.Sprintf("%s %s filing (calendar unavailable)", e.FormType, e.Bucket)
	}
	return fmt.Sprintf("%s %s filed %d trading days ago", e.FormType, e.Bucket, distance)
}
```

- [ ] **Step 5: Run tests**

```
go test ./services/... -run TestIsDilutionBlocked -race -v
```

Expected: all 5 PASS.

- [ ] **Step 6: Run full edgar suite**

```
go test ./services/... -run TestSECEdgar -race -v
go test ./services/... -run TestExtractTickerFromTitle -v
```

Expected: all PASS.

- [ ] **Step 7: Commit**

```
git add services/sec_edgar_service.go services/sec_edgar_service_test.go
git commit -m "feat(edgar): add dilution block data model and IsDilutionBlocked API

Per-ticker block map with lazy trading-day eviction. Takedown bucket gets
a 2-day window; shelf bucket gets 5. Fail-closed when the trading calendar
is unavailable — preserves blocks rather than dropping them, which is the
safe direction for a capital-protection filter."
```

---

### Task 4: Create dilution test fixtures

**Files:** All 11 new files under `services/testdata/edgar/dilution/`.

Each fixture is a minimal ATOM feed with one `<entry>`. The ticker `ABCD` is the universe match; `ZZZZ` is a non-universe filler.

- [ ] **Step 1: Create the directory**

```
mkdir -p services/testdata/edgar/dilution
```

- [ ] **Step 2: Create `s1-fixture.atom`**

```xml
<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Latest filings</title>
  <entry>
    <title>S-1 - ABCD CORP (0001234567) (Filer)</title>
    <updated>2026-05-09T16:42:00-04:00</updated>
    <summary>S-1 registration statement filed by ABCD CORP</summary>
  </entry>
</feed>
```

- [ ] **Step 3: Create `s3-shelf-fixture.atom`**

```xml
<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Latest filings</title>
  <entry>
    <title>S-3 - ABCD CORP (0001234567) (Filer)</title>
    <updated>2026-05-09T16:42:00-04:00</updated>
    <summary>S-3 shelf registration statement filed by ABCD CORP</summary>
  </entry>
</feed>
```

- [ ] **Step 4: Create `s3-amendment-fixture.atom`**

```xml
<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Latest filings</title>
  <entry>
    <title>S-3/A - ABCD CORP (0001234567) (Filer)</title>
    <updated>2026-05-09T16:42:00-04:00</updated>
    <summary>S-3/A amendment to shelf registration filed by ABCD CORP</summary>
  </entry>
</feed>
```

- [ ] **Step 5: Create `424b5-fixture.atom`**

```xml
<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Latest filings</title>
  <entry>
    <title>424B5 - ABCD CORP (0001234567) (Filer)</title>
    <updated>2026-05-09T16:42:00-04:00</updated>
    <summary>Prospectus supplement filed by ABCD CORP</summary>
  </entry>
</feed>
```

- [ ] **Step 6: Create `f1-fixture.atom`**

```xml
<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Latest filings</title>
  <entry>
    <title>F-1 - ABCD CORP (0001234567) (Filer)</title>
    <updated>2026-05-09T16:42:00-04:00</updated>
    <summary>F-1 registration statement filed by ABCD CORP</summary>
  </entry>
</feed>
```

- [ ] **Step 7: Create `f3-fixture.atom`**

```xml
<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Latest filings</title>
  <entry>
    <title>F-3 - ABCD CORP (0001234567) (Filer)</title>
    <updated>2026-05-09T16:42:00-04:00</updated>
    <summary>F-3 shelf registration statement filed by ABCD CORP</summary>
  </entry>
</feed>
```

- [ ] **Step 8: Create `8k-item-302-fixture.atom`**

```xml
<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Latest filings</title>
  <entry>
    <title>8-K - ABCD CORP (0001234567) (Filer)</title>
    <updated>2026-05-09T16:42:00-04:00</updated>
    <summary>Item 3.02 Unregistered Sales of Equity Securities</summary>
  </entry>
</feed>
```

- [ ] **Step 9: Create `8k-item-101-spa-fixture.atom`**

```xml
<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Latest filings</title>
  <entry>
    <title>8-K - ABCD CORP (0001234567) (Filer)</title>
    <updated>2026-05-09T16:42:00-04:00</updated>
    <summary>Item 1.01 Entry into a Securities Purchase Agreement</summary>
  </entry>
</feed>
```

- [ ] **Step 10: Create `8k-item-101-asset-purchase-fixture.atom`**

**Constraint:** title and summary must NOT contain any of: SECURITIES PURCHASE AGREEMENT, EQUITY PURCHASE, STANDBY EQUITY, ATM OFFERING, AT-THE-MARKET, REGISTERED DIRECT, SHELF TAKEDOWN, PUBLIC OFFERING, PRIVATE PLACEMENT, PIPE FINANCING, WARRANT, CONVERTIBLE NOTE, PRICING OF, COMMENCEMENT OF.

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!--
  Tests false-positive guard for the Item 1.01 keyword scan.
  This fixture must NOT contain equity-related keywords from the dilution
  pattern list (see services/sec_edgar_service.go heuristic patterns).
  Asset Purchase Agreement is non-dilutive M&A — IsDilutionBlocked must NOT fire.
-->
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Latest filings</title>
  <entry>
    <title>8-K - ABCD CORP (0001234567) (Filer)</title>
    <updated>2026-05-09T16:42:00-04:00</updated>
    <summary>Item 1.01 Entry into Asset Purchase Agreement for manufacturing facility</summary>
  </entry>
</feed>
```

- [ ] **Step 11: Create `8k-item-101-licensing-fixture.atom`**

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!--
  Additional false-positive guard. Item 1.01 entries for licensing
  agreements must not be treated as dilution events.
-->
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Latest filings</title>
  <entry>
    <title>8-K - ABCD CORP (0001234567) (Filer)</title>
    <updated>2026-05-09T16:42:00-04:00</updated>
    <summary>Item 1.01 Entry into software licensing agreement with third-party vendor</summary>
  </entry>
</feed>
```

- [ ] **Step 12: Create `8k-vague-financing-fixture.atom`**

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!--
  Documents an acknowledged false-negative: a vague 8-K title with no
  item number and no trigger keywords will not be caught by the day-1
  heuristic. This fixture proves the heuristic does not over-fire on
  ambiguous titles. v2 (8-K body fetch) would catch this.
-->
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Latest filings</title>
  <entry>
    <title>8-K - ABCD CORP (0001234567) (Filer)</title>
    <updated>2026-05-09T16:42:00-04:00</updated>
    <summary>Strategic financing update</summary>
  </entry>
</feed>
```

- [ ] **Step 13: Create `non-universe-ticker-fixture.atom`**

```xml
<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Latest filings</title>
  <entry>
    <title>S-1 - ZZZZ CORP (0009999999) (Filer)</title>
    <updated>2026-05-09T16:42:00-04:00</updated>
    <summary>S-1 registration statement filed by ZZZZ CORP</summary>
  </entry>
</feed>
```

- [ ] **Step 14: Commit fixtures**

```
git add services/testdata/edgar/dilution/
git commit -m "test(edgar): add dilution fixture feeds

Eleven minimal ATOM fixtures covering S-1, S-3, S-3/A, 424B5, F-1, F-3,
8-K item-302, 8-K item-101 (SPA, asset purchase, licensing), vague
financing 8-K, and a non-universe-ticker filing. Constraint comments
in the false-positive fixtures pin the keyword-exclusion intent."
```

---

### Task 5: Implement `pollDilutionForms` for S-1, S-3, 424, F-1, F-3

**Files:**
- Modify: `services/sec_edgar_service.go` (add poll method + mapping table)
- Modify: `services/sec_edgar_service_test.go` (fixture-driven tests)

- [ ] **Step 1: Write the failing tests**

Add to `services/sec_edgar_service_test.go`:

```go
func loadFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "edgar", "dilution", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(b)
}

func TestPollDilutionForms_S1Block(t *testing.T) {
	body := loadFixture(t, "s1-fixture.atom")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/atom+xml")
		w.Write([]byte(body))
	}))
	defer ts.Close()

	svc := newTestEdgarWithCalendar([]AlpacaCalendarEntry{{Date: "2026-05-10"}})
	svc.httpClient = ts.Client()
	tickers := map[string]bool{"ABCD": true}

	svc.applyDilutionFiling("S-1", "takedown", ts.URL, tickers)

	svc.dilutionMu.RLock()
	entry, ok := svc.dilutionBlocks["ABCD"]
	svc.dilutionMu.RUnlock()
	if !ok {
		t.Fatal("expected ABCD to be blocked")
	}
	if entry.Bucket != "takedown" {
		t.Errorf("expected bucket=takedown, got %q", entry.Bucket)
	}
	if entry.FormType != "S-1" {
		t.Errorf("expected FormType=S-1, got %q", entry.FormType)
	}
}

func TestPollDilutionForms_S3IsShelf(t *testing.T) {
	body := loadFixture(t, "s3-shelf-fixture.atom")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer ts.Close()

	svc := newTestEdgarWithCalendar([]AlpacaCalendarEntry{{Date: "2026-05-10"}})
	svc.httpClient = ts.Client()
	svc.applyDilutionFiling("S-3", "shelf", ts.URL, map[string]bool{"ABCD": true})

	svc.dilutionMu.RLock()
	entry := svc.dilutionBlocks["ABCD"]
	svc.dilutionMu.RUnlock()
	if entry.Bucket != "shelf" {
		t.Errorf("expected bucket=shelf, got %q", entry.Bucket)
	}
}

func TestPollDilutionForms_S3AmendmentStillShelf(t *testing.T) {
	body := loadFixture(t, "s3-amendment-fixture.atom")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer ts.Close()

	svc := newTestEdgarWithCalendar([]AlpacaCalendarEntry{{Date: "2026-05-10"}})
	svc.httpClient = ts.Client()
	svc.applyDilutionFiling("S-3", "shelf", ts.URL, map[string]bool{"ABCD": true})

	entry := svc.dilutionBlocks["ABCD"]
	if entry.Bucket != "shelf" {
		t.Errorf("S-3/A must remain in shelf bucket (not promoted to takedown), got %q", entry.Bucket)
	}
}

func TestPollDilutionForms_424Takedown(t *testing.T) {
	body := loadFixture(t, "424b5-fixture.atom")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer ts.Close()

	svc := newTestEdgarWithCalendar([]AlpacaCalendarEntry{{Date: "2026-05-10"}})
	svc.httpClient = ts.Client()
	svc.applyDilutionFiling("424", "takedown", ts.URL, map[string]bool{"ABCD": true})

	entry := svc.dilutionBlocks["ABCD"]
	if entry.Bucket != "takedown" {
		t.Errorf("expected bucket=takedown for 424B5, got %q", entry.Bucket)
	}
}

func TestPollDilutionForms_NonUniverseIgnored(t *testing.T) {
	body := loadFixture(t, "non-universe-ticker-fixture.atom")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer ts.Close()

	svc := newTestEdgarWithCalendar([]AlpacaCalendarEntry{{Date: "2026-05-10"}})
	svc.httpClient = ts.Client()
	svc.applyDilutionFiling("S-1", "takedown", ts.URL, map[string]bool{"ABCD": true})

	if len(svc.dilutionBlocks) != 0 {
		t.Errorf("non-universe ticker should not produce a block, got %d blocks", len(svc.dilutionBlocks))
	}
}

func TestPollDilutionForms_ShelfDoesNotReplaceTakedown(t *testing.T) {
	svc := newTestEdgarWithCalendar([]AlpacaCalendarEntry{{Date: "2026-05-10"}})
	// Pre-seed a takedown.
	svc.dilutionBlocks["ABCD"] = dilutionEntry{
		Ticker:   "ABCD",
		FormType: "S-1",
		FiledAt:  time.Now(),
		Bucket:   "takedown",
	}
	// Now apply a shelf filing — must not downgrade.
	svc.upsertDilutionBlock("ABCD", "S-3", "shelf", time.Now(), "https://example.com")
	if svc.dilutionBlocks["ABCD"].Bucket != "takedown" {
		t.Error("shelf filing must not downgrade an active takedown block")
	}
}

func TestPollDilutionForms_TakedownReplacesShelf(t *testing.T) {
	svc := newTestEdgarWithCalendar([]AlpacaCalendarEntry{{Date: "2026-05-10"}})
	svc.dilutionBlocks["ABCD"] = dilutionEntry{
		Ticker:   "ABCD",
		FormType: "S-3",
		FiledAt:  time.Now(),
		Bucket:   "shelf",
	}
	svc.upsertDilutionBlock("ABCD", "S-1", "takedown", time.Now(), "https://example.com")
	if svc.dilutionBlocks["ABCD"].Bucket != "takedown" {
		t.Error("takedown filing must replace an active shelf block")
	}
}
```

Add the missing imports at the top of the file: `os` and `path/filepath`.

- [ ] **Step 2: Run tests to verify they fail**

```
go test ./services/... -run TestPollDilutionForms -v
```

Expected: FAIL — `applyDilutionFiling` and `upsertDilutionBlock` undefined.

- [ ] **Step 3: Implement `applyDilutionFiling` and `upsertDilutionBlock`**

Add at the bottom of `services/sec_edgar_service.go`:

```go
// dilutionFormSpec maps an EDGAR `type=` query parameter to the bucket label
// and a human-readable form-type tag for log lines. Order matters only for
// log clarity — multiple specs can match the same atom feed entry but each
// fetch covers exactly one type= value.
type dilutionFormSpec struct {
	queryType string // EDGAR getcurrent type= parameter
	bucket    string // "takedown" or "shelf"
}

// dilutionFormSpecs is the canonical fan-out list for pollDilutionForms.
var dilutionFormSpecs = []dilutionFormSpec{
	{queryType: "S-1", bucket: "takedown"},
	{queryType: "S-3", bucket: "shelf"},
	{queryType: "424", bucket: "takedown"},
	{queryType: "F-1", bucket: "takedown"},
	{queryType: "F-3", bucket: "shelf"},
}

// applyDilutionFiling fetches one EDGAR atom feed for the given type, walks
// each entry, and records a dilution block for any entry whose title contains
// a universe ticker. Used both by the production poll loop and by unit tests
// (which point url at an httptest server serving a fixture).
func (s *SECEdgarService) applyDilutionFiling(formType, bucket, url string, tickers map[string]bool) {
	entries, err := s.fetchAtom(url)
	if err != nil {
		s.logger.WithError(err).WithField("form", formType).
			Warn("SECEdgarService: dilution poll failed for form")
		return
	}
	for _, entry := range entries {
		ticker := extractTickerFromTitle(entry.Title, tickers)
		if ticker == "" {
			continue
		}
		filedAt, isFallback := parseAtomDate(entry.Updated)
		if isFallback {
			s.logger.Warnf("dilution block: skipping %s — unparseable timestamp %q", ticker, entry.Updated)
			continue
		}
		// FormType uses the title's actual form (e.g. "S-3/A") when extractable
		// for log fidelity; falls back to the queried form type otherwise.
		actualForm := extractFormFromTitle(entry.Title, formType)
		s.upsertDilutionBlock(ticker, actualForm, bucket, filedAt, "")
	}
}

// extractFormFromTitle pulls the actual form type from an EDGAR title like
// "S-3/A - ABCD CORP (0001234567) (Filer)". Falls back to the queried form if
// the title doesn't follow the expected leading-form-token pattern.
func extractFormFromTitle(title, fallback string) string {
	upper := strings.ToUpper(title)
	for _, candidate := range []string{"S-1/A", "S-1", "S-3/A", "S-3", "F-1/A", "F-1", "F-3/A", "F-3", "424B2", "424B3", "424B4", "424B5"} {
		if strings.HasPrefix(upper, candidate+" ") || strings.HasPrefix(upper, candidate+"-") {
			return candidate
		}
	}
	return fallback
}

// upsertDilutionBlock writes a dilution entry, applying the replacement rule:
// takedown beats shelf (never downgrade); same bucket replaces (refreshes
// window); shelf does not replace an existing takedown.
func (s *SECEdgarService) upsertDilutionBlock(ticker, formType, bucket string, filedAt time.Time, sourceURL string) {
	s.dilutionMu.Lock()
	defer s.dilutionMu.Unlock()
	existing, ok := s.dilutionBlocks[ticker]
	if ok && existing.Bucket == "takedown" && bucket == "shelf" {
		return // Don't downgrade.
	}
	s.dilutionBlocks[ticker] = dilutionEntry{
		Ticker:    ticker,
		FormType:  formType,
		FiledAt:   filedAt,
		Bucket:    bucket,
		SourceURL: sourceURL,
	}
	s.logger.WithFields(logrus.Fields{
		"ticker":    ticker,
		"form":      formType,
		"bucket":    bucket,
		"filed_at":  filedAt.Format(time.RFC3339),
		"source":    sourceURL,
	}).Warn("dilution block created")
}
```

- [ ] **Step 4: Run tests**

```
go test ./services/... -run TestPollDilutionForms -race -v
```

Expected: all 7 PASS.

- [ ] **Step 5: Commit**

```
git add services/sec_edgar_service.go services/sec_edgar_service_test.go
git commit -m "feat(edgar): per-form dilution polling for S-1/S-3/424/F-1/F-3

applyDilutionFiling fans into the existing fetchAtom helper for one form
type at a time; upsertDilutionBlock encodes the takedown-beats-shelf
replacement rule. Block creation logs at warn level for operator visibility."
```

---

### Task 6: Add 8-K dilution heuristic scanner

**Files:**
- Modify: `services/sec_edgar_service.go` (add pattern table + scanner)
- Modify: `services/sec_edgar_service_test.go` (fixture-driven tests)

- [ ] **Step 1: Write the failing tests**

Add to `services/sec_edgar_service_test.go`:

```go
func TestHeuristic8K_Item302Always(t *testing.T) {
	body := loadFixture(t, "8k-item-302-fixture.atom")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer ts.Close()

	svc := newTestEdgarWithCalendar([]AlpacaCalendarEntry{{Date: "2026-05-10"}})
	svc.httpClient = ts.Client()
	entries, err := svc.fetchAtom(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	svc.scanHeuristic8Ks(entries, map[string]bool{"ABCD": true})

	if _, ok := svc.dilutionBlocks["ABCD"]; !ok {
		t.Error("expected Item 3.02 to trigger heuristic block")
	}
	if svc.dilutionBlocks["ABCD"].Bucket != "takedown" {
		t.Errorf("expected bucket=takedown, got %q", svc.dilutionBlocks["ABCD"].Bucket)
	}
	if !strings.HasPrefix(svc.dilutionBlocks["ABCD"].FormType, "8-K-3.02") {
		t.Errorf("expected FormType prefix 8-K-3.02, got %q", svc.dilutionBlocks["ABCD"].FormType)
	}
}

func TestHeuristic8K_Item101WithSPA(t *testing.T) {
	body := loadFixture(t, "8k-item-101-spa-fixture.atom")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer ts.Close()

	svc := newTestEdgarWithCalendar([]AlpacaCalendarEntry{{Date: "2026-05-10"}})
	svc.httpClient = ts.Client()
	entries, _ := svc.fetchAtom(ts.URL)
	svc.scanHeuristic8Ks(entries, map[string]bool{"ABCD": true})

	if _, ok := svc.dilutionBlocks["ABCD"]; !ok {
		t.Error("expected Item 1.01 + SPA to trigger heuristic block")
	}
}

func TestHeuristic8K_Item101AssetPurchaseNotBlocked(t *testing.T) {
	body := loadFixture(t, "8k-item-101-asset-purchase-fixture.atom")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer ts.Close()

	svc := newTestEdgarWithCalendar([]AlpacaCalendarEntry{{Date: "2026-05-10"}})
	svc.httpClient = ts.Client()
	entries, _ := svc.fetchAtom(ts.URL)
	svc.scanHeuristic8Ks(entries, map[string]bool{"ABCD": true})

	if len(svc.dilutionBlocks) != 0 {
		t.Errorf("Asset Purchase Agreement under Item 1.01 must not block; got %d blocks", len(svc.dilutionBlocks))
	}
}

func TestHeuristic8K_Item101LicensingNotBlocked(t *testing.T) {
	body := loadFixture(t, "8k-item-101-licensing-fixture.atom")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer ts.Close()

	svc := newTestEdgarWithCalendar([]AlpacaCalendarEntry{{Date: "2026-05-10"}})
	svc.httpClient = ts.Client()
	entries, _ := svc.fetchAtom(ts.URL)
	svc.scanHeuristic8Ks(entries, map[string]bool{"ABCD": true})

	if len(svc.dilutionBlocks) != 0 {
		t.Errorf("software licensing under Item 1.01 must not block; got %d blocks", len(svc.dilutionBlocks))
	}
}

func TestHeuristic8K_VagueFinancingNotBlocked(t *testing.T) {
	body := loadFixture(t, "8k-vague-financing-fixture.atom")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer ts.Close()

	svc := newTestEdgarWithCalendar([]AlpacaCalendarEntry{{Date: "2026-05-10"}})
	svc.httpClient = ts.Client()
	entries, _ := svc.fetchAtom(ts.URL)
	svc.scanHeuristic8Ks(entries, map[string]bool{"ABCD": true})

	// Documents the acknowledged false negative — vague financing titles
	// without item numbers or trigger keywords are not caught by v1 heuristic.
	if len(svc.dilutionBlocks) != 0 {
		t.Errorf("vague 'Strategic financing update' must not block (heuristic limitation); got %d blocks", len(svc.dilutionBlocks))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```
go test ./services/... -run TestHeuristic8K -v
```

Expected: FAIL — `scanHeuristic8Ks` undefined.

- [ ] **Step 3: Implement the heuristic scanner**

Add to `services/sec_edgar_service.go`:

```go
// dilution8KPattern is one rule in the day-1 8-K item-number heuristic.
// itemMarker (e.g. "ITEM 3.02") is required; if any of keywordsAny is empty,
// the item marker alone fires the block.
type dilution8KPattern struct {
	itemMarker  string
	keywordsAny []string // empty means "marker alone is enough"
	formTag     string   // for log + FormType field, e.g. "8-K-3.02"
}

// dilution8KPatterns is the canonical match table for the 8-K dilution
// heuristic. Documented in docs/superpowers/specs/2026-05-10-pennyprophet-dilution-filter-design.md.
var dilution8KPatterns = []dilution8KPattern{
	{
		itemMarker:  "ITEM 1.01",
		keywordsAny: []string{
			"SECURITIES PURCHASE AGREEMENT",
			"EQUITY PURCHASE",
			"STANDBY EQUITY",
			"ATM OFFERING",
			"AT-THE-MARKET",
			"REGISTERED DIRECT",
			"SHELF TAKEDOWN",
		},
		formTag: "8-K-1.01",
	},
	{
		itemMarker:  "ITEM 3.02",
		keywordsAny: nil, // 3.02 is unambiguously dilutive
		formTag:     "8-K-3.02",
	},
	{
		itemMarker: "ITEM 8.01",
		keywordsAny: []string{
			"PUBLIC OFFERING",
			"PRIVATE PLACEMENT",
			"PIPE FINANCING",
			"WARRANT",
			"CONVERTIBLE NOTE",
		},
		formTag: "8-K-8.01",
	},
	{
		itemMarker:  "PRICING OF",
		keywordsAny: []string{"PUBLIC OFFERING"},
		formTag:     "8-K-pricing",
	},
	{
		itemMarker:  "COMMENCEMENT OF",
		keywordsAny: []string{"ATM"},
		formTag:     "8-K-commencement",
	},
}

// scanHeuristic8Ks applies the dilution8KPatterns table to a slice of already-
// fetched 8-K atom entries. Designed to piggyback on the existing 8-K poll —
// callers fetch once, then run both the positive-signal scan (existing
// pollEdgar) and this dilution scan on the same in-memory entries.
func (s *SECEdgarService) scanHeuristic8Ks(entries []atomEntry, tickers map[string]bool) {
	for _, e := range entries {
		ticker := extractTickerFromTitle(e.Title, tickers)
		if ticker == "" {
			continue
		}
		text := strings.ToUpper(e.Title + " " + e.Summary)
		matched := matchDilution8K(text)
		if matched == nil {
			continue
		}
		filedAt, isFallback := parseAtomDate(e.Updated)
		if isFallback {
			s.logger.Warnf("dilution heuristic: skipping %s — unparseable timestamp %q", ticker, e.Updated)
			continue
		}
		s.upsertDilutionBlock(ticker, matched.formTag, "takedown", filedAt, "")
		s.logger.WithFields(logrus.Fields{
			"ticker":          ticker,
			"matched_pattern": matched.itemMarker,
			"source":          "heuristic",
		}).Warn("dilution block created from 8-K heuristic")
	}
}

// matchDilution8K returns the first matching pattern, or nil. text must be
// uppercased by the caller.
func matchDilution8K(text string) *dilution8KPattern {
	for i := range dilution8KPatterns {
		p := &dilution8KPatterns[i]
		if !strings.Contains(text, p.itemMarker) {
			continue
		}
		if len(p.keywordsAny) == 0 {
			return p
		}
		for _, kw := range p.keywordsAny {
			if strings.Contains(text, kw) {
				return p
			}
		}
	}
	return nil
}
```

- [ ] **Step 4: Run tests**

```
go test ./services/... -run TestHeuristic8K -race -v
```

Expected: all 5 PASS.

- [ ] **Step 5: Commit**

```
git add services/sec_edgar_service.go services/sec_edgar_service_test.go
git commit -m "feat(edgar): add 8-K dilution item-number heuristic

Five-pattern matcher applied to title+summary of already-fetched 8-K atom
entries. Item 3.02 fires alone (unambiguous); 1.01 and 8.01 require an
equity-financing keyword to avoid false positives on M&A or licensing
deals. False-negative cases are documented in fixtures."
```

---

### Task 7: Wire dilution polling into `Start()`

**Files:**
- Modify: `services/sec_edgar_service.go` (add second goroutine + piggyback heuristic)

This task has no new tests because the wiring is integration-only; correctness is covered by Tasks 5 and 6's unit tests. After this task the production polling actually fires.

- [ ] **Step 1: Add the dilution refresh constant**

Near the top of `services/sec_edgar_service.go` (with the other constants around line 16):

```go
const dilutionRefreshInterval = 5 * time.Minute
```

- [ ] **Step 2: Replace `Start` and add `pollDilutionForms` and update `pollEdgar` to also run heuristic**

Replace the existing `Start` method (around line 51):

```go
// Start runs the polling loops until ctx is cancelled. Two goroutines:
// (1) the existing 30s positive-signal poll (8-K + GlobeNewswire),
// which now also piggybacks the 8-K dilution heuristic on the same fetch,
// (2) a slower 5min dilution-form poll (S-1, S-3, 424, F-1, F-3) that
// fans out concurrently across forms.
func (s *SECEdgarService) Start(ctx context.Context) {
	go s.runPositivePoll(ctx)
	go s.runDilutionPoll(ctx)
}

func (s *SECEdgarService) runPositivePoll(ctx context.Context) {
	s.poll()
	ticker := time.NewTicker(regulatoryRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.poll()
		}
	}
}

func (s *SECEdgarService) runDilutionPoll(ctx context.Context) {
	s.pollDilutionForms()
	ticker := time.NewTicker(dilutionRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.pollDilutionForms()
		}
	}
}

// pollDilutionForms fans out one fetch per dilutionFormSpec concurrently and
// applies each result. Failures of one form are isolated — others still apply.
func (s *SECEdgarService) pollDilutionForms() {
	tickers := tickerSet(s.universe.GetTickers())
	if len(tickers) == 0 {
		return
	}
	var wg sync.WaitGroup
	for _, spec := range dilutionFormSpecs {
		wg.Add(1)
		go func(sp dilutionFormSpec) {
			defer wg.Done()
			url := fmt.Sprintf(
				"https://www.sec.gov/cgi-bin/browse-edgar?action=getcurrent&type=%s&dateb=&owner=include&count=40&search_text=&output=atom",
				sp.queryType,
			)
			s.applyDilutionFiling(sp.queryType, sp.bucket, url, tickers)
		}(spec)
	}
	wg.Wait()
}
```

- [ ] **Step 3: Update `pollEdgar` to also run the 8-K heuristic on the same fetched entries**

Replace `pollEdgar` (around line 194):

```go
func (s *SECEdgarService) pollEdgar(tickers map[string]bool) (fallbacks, total int) {
	const edgarURL = "https://www.sec.gov/cgi-bin/browse-edgar?action=getcurrent&type=8-K&dateb=&owner=include&count=40&search_text=&output=atom"
	entries, err := s.fetchAtom(edgarURL)
	if err != nil {
		s.logger.WithError(err).Warn("SECEdgarService: EDGAR poll failed")
		return 0, 0
	}
	// Heuristic 8-K dilution scan piggybacks on the same fetched entries.
	s.scanHeuristic8Ks(entries, tickers)

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
			s.logger.Warnf("decay anchor: skipping %s — unparseable timestamp %q", ticker, entry.Updated)
			continue
		}
		desc := fmt.Sprintf("8-K filed %s", eventTime.Format("15:04 ET"))
		s.upsertEntry(ticker, 40.0, eventTime, desc)
	}
	return fallbacks, total
}
```

- [ ] **Step 4: Confirm full build**

```
go build ./...
go test ./services/... -race -count=1
```

Expected: clean build; all existing + new tests PASS.

- [ ] **Step 5: Commit**

```
git add services/sec_edgar_service.go
git commit -m "feat(edgar): wire dilution poll loop into Start

Splits Start into two goroutines: the existing 30s positive-signal poll
(unchanged behavior, now also runs the heuristic 8-K scan on the same
fetched entries) and a new 5min dilution-form poll that fans out across
S-1, S-3, 424, F-1, F-3 concurrently."
```

---

### Task 8: Already done in Task 2

The `cmd/bot/main.go` constructor call was updated as part of Task 2 Step 6. Skip ahead to Task 9.

---

### Task 9: Aggregator integration — `IsDilutionBlocked` check in `GetCandidates`

**Files:**
- Modify: `services/penny_signal_aggregator.go` (add check + lock-ordering comment)
- Modify: `services/penny_signal_aggregator_test.go` (suppression test)

- [ ] **Step 1: Write the failing test**

Add to `services/penny_signal_aggregator_test.go`:

```go
func TestGetCandidates_SuppressesDilutionBlocked(t *testing.T) {
	universe := &PennyUniverseService{}
	screener := &PennyScreenerService{scores: map[string]TechnicalEntry{}}
	earnings := &EarningsCalendarService{
		entries:  map[string]earningsEntry{},
		calendar: []AlpacaCalendarEntry{{Date: "2026-05-10"}},
		logger:   logrus.New(),
	}
	edgar := &SECEdgarService{
		entries:        make(map[string]regulatoryEntry),
		dilutionBlocks: make(map[string]dilutionEntry),
		logger:         logrus.New(),
		nowFunc:        func() time.Time { return time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC) },
		earnings:       earnings,
	}
	edgar.dilutionBlocks["BLOCKED"] = dilutionEntry{
		Ticker:   "BLOCKED",
		FormType: "S-1",
		FiledAt:  time.Date(2026, 5, 10, 9, 0, 0, 0, time.UTC),
		Bucket:   "takedown",
	}
	social := &SocialSignalService{}
	agg := NewPennySignalAggregator(universe, screener, edgar, social)

	SeedCandidateForTest(agg, CandidateScore{
		Ticker:            "OPEN",
		CompositeScore:    80,
		CompositeEligible: true,
	})
	SeedCandidateForTest(agg, CandidateScore{
		Ticker:            "BLOCKED",
		CompositeScore:    85,
		CompositeEligible: true,
	})

	got := agg.GetCandidates(60)
	if len(got) != 1 {
		t.Fatalf("expected 1 candidate after dilution suppression, got %d", len(got))
	}
	if got[0].Ticker != "OPEN" {
		t.Errorf("expected OPEN to remain, got %q", got[0].Ticker)
	}
}

// Regression guard: a dilution block must not delete the underlying score data;
// GetSignalDetail must still return the candidate so operator audit / log_decision
// trails remain intact. This pins the Section 3 design decision that block ≠ exit
// and block ≠ data deletion.
func TestDilutionBlockDoesNotDeleteSignalDetail(t *testing.T) {
	earnings := &EarningsCalendarService{
		entries:  map[string]earningsEntry{},
		calendar: []AlpacaCalendarEntry{{Date: "2026-05-10"}},
		logger:   logrus.New(),
	}
	edgar := &SECEdgarService{
		entries:        make(map[string]regulatoryEntry),
		dilutionBlocks: make(map[string]dilutionEntry),
		logger:         logrus.New(),
		nowFunc:        func() time.Time { return time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC) },
		earnings:       earnings,
	}
	edgar.dilutionBlocks["HELD"] = dilutionEntry{
		Ticker:   "HELD",
		FormType: "S-1",
		FiledAt:  time.Date(2026, 5, 10, 9, 0, 0, 0, time.UTC),
		Bucket:   "takedown",
	}
	agg := NewPennySignalAggregator(&PennyUniverseService{}, &PennyScreenerService{}, edgar, &SocialSignalService{})
	SeedCandidateForTest(agg, CandidateScore{
		Ticker:            "HELD",
		CompositeScore:    85,
		CompositeEligible: true,
		TechnicalScore:    35,
		RegulatoryScore:   30,
		SocialScore:       20,
	})

	// Block suppresses the candidate from GetCandidates...
	if cands := agg.GetCandidates(60); len(cands) != 0 {
		t.Errorf("expected 0 candidates from GetCandidates, got %d", len(cands))
	}
	// ...but GetSignalDetail still returns the underlying score for audit.
	detail := agg.GetSignalDetail("HELD")
	if detail == nil {
		t.Fatal("GetSignalDetail returned nil; block must not delete signal data")
	}
	if detail.CompositeScore != 85 {
		t.Errorf("expected composite=85 preserved, got %f", detail.CompositeScore)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```
go test ./services/... -run "TestGetCandidates_SuppressesDilutionBlocked|TestDilutionBlockDoesNotDeleteSignalDetail" -v
```

Expected: FAIL — `BLOCKED` candidate still returned, suppression not implemented.

- [ ] **Step 3: Add the dilution check to `GetCandidates`**

In `services/penny_signal_aggregator.go`, replace `GetCandidates` (around line 128):

```go
// GetCandidates returns all scored candidates above minScore that are composite-eligible
// and not blacklisted (bracket rejection or dilution).
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
		if blocked, reason := a.edgar.IsDilutionBlocked(c.Ticker); blocked {
			a.logger.WithFields(logrus.Fields{
				"ticker":    c.Ticker,
				"composite": c.CompositeScore,
				"reason":    reason,
			}).Info("dilution block: candidate suppressed")
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

- [ ] **Step 4: Update lock-ordering comment**

Update the comment block at lines 24-27 in `services/penny_signal_aggregator.go`:

```go
// Lock ordering: PennySignalAggregator.mu must always be acquired before
// BracketBlacklist.mu and before SECEdgarService.dilutionMu. GetCandidates
// holds a.mu.RLock while calling blacklist.IsBlacklisted (b.mu.RLock) and
// edgar.IsDilutionBlocked (which may take dilutionMu.Lock during eviction).
// No code path may acquire BracketBlacklist.mu or SECEdgarService.dilutionMu
// before PennySignalAggregator.mu.
```

- [ ] **Step 5: Run tests**

```
go test ./services/... -run "TestGetCandidates_SuppressesDilutionBlocked|TestDilutionBlockDoesNotDeleteSignalDetail" -race -v
```

Expected: both PASS.

- [ ] **Step 6: Run full aggregator suite**

```
go test ./services/... -run TestGetCandidates -race -v
go test ./services/... -run TestPennySignalAggregator -race -v
```

Expected: all PASS, no regressions.

- [ ] **Step 7: Commit**

```
git add services/penny_signal_aggregator.go services/penny_signal_aggregator_test.go
git commit -m "feat(penny): suppress dilution-blocked tickers from GetCandidates

Mirrors the existing BracketBlacklist gate. GetSignalDetail intentionally
still returns the score for blocked tickers (block != data deletion) so
operator audit trails stay intact. Lock-ordering comment updated to include
the new dilutionMu acquisition site."
```

---

### Task 10: `PENNY_DILUTION_FILTER_MODE` env var (shadow vs enforce)

**Files:**
- Modify: `services/penny_signal_aggregator.go` (add mode field + read env)
- Modify: `services/penny_signal_aggregator_test.go` (test shadow vs enforce behavior)

- [ ] **Step 1: Write the failing tests**

Add to `services/penny_signal_aggregator_test.go`:

```go
func TestDilutionFilterMode_ShadowDoesNotSuppress(t *testing.T) {
	earnings := &EarningsCalendarService{
		calendar: []AlpacaCalendarEntry{{Date: "2026-05-10"}},
		entries:  map[string]earningsEntry{},
		logger:   logrus.New(),
	}
	edgar := &SECEdgarService{
		entries:        make(map[string]regulatoryEntry),
		dilutionBlocks: make(map[string]dilutionEntry),
		logger:         logrus.New(),
		nowFunc:        func() time.Time { return time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC) },
		earnings:       earnings,
	}
	edgar.dilutionBlocks["BLOCKED"] = dilutionEntry{
		Ticker:   "BLOCKED",
		FormType: "S-1",
		FiledAt:  time.Date(2026, 5, 10, 9, 0, 0, 0, time.UTC),
		Bucket:   "takedown",
	}
	agg := NewPennySignalAggregator(&PennyUniverseService{}, &PennyScreenerService{}, edgar, &SocialSignalService{})
	agg.dilutionMode = "shadow"
	SeedCandidateForTest(agg, CandidateScore{
		Ticker: "BLOCKED", CompositeScore: 85, CompositeEligible: true,
	})

	got := agg.GetCandidates(60)
	if len(got) != 1 {
		t.Errorf("shadow mode must NOT suppress; expected 1 candidate, got %d", len(got))
	}
}

func TestDilutionFilterMode_EnforceSuppresses(t *testing.T) {
	earnings := &EarningsCalendarService{
		calendar: []AlpacaCalendarEntry{{Date: "2026-05-10"}},
		entries:  map[string]earningsEntry{},
		logger:   logrus.New(),
	}
	edgar := &SECEdgarService{
		entries:        make(map[string]regulatoryEntry),
		dilutionBlocks: make(map[string]dilutionEntry),
		logger:         logrus.New(),
		nowFunc:        func() time.Time { return time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC) },
		earnings:       earnings,
	}
	edgar.dilutionBlocks["BLOCKED"] = dilutionEntry{
		Ticker:   "BLOCKED",
		FormType: "S-1",
		FiledAt:  time.Date(2026, 5, 10, 9, 0, 0, 0, time.UTC),
		Bucket:   "takedown",
	}
	agg := NewPennySignalAggregator(&PennyUniverseService{}, &PennyScreenerService{}, edgar, &SocialSignalService{})
	agg.dilutionMode = "enforce"
	SeedCandidateForTest(agg, CandidateScore{
		Ticker: "BLOCKED", CompositeScore: 85, CompositeEligible: true,
	})

	got := agg.GetCandidates(60)
	if len(got) != 0 {
		t.Errorf("enforce mode must suppress; expected 0 candidates, got %d", len(got))
	}
}

func TestDilutionFilterMode_DefaultIsShadow(t *testing.T) {
	agg := NewPennySignalAggregator(&PennyUniverseService{}, &PennyScreenerService{}, nil, &SocialSignalService{})
	if agg.dilutionMode != "shadow" {
		t.Errorf("expected default dilutionMode=shadow, got %q", agg.dilutionMode)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```
go test ./services/... -run TestDilutionFilterMode -v
```

Expected: FAIL — `dilutionMode` field missing.

- [ ] **Step 3: Add the field, env-var read, and behavior**

In `services/penny_signal_aggregator.go`, update the struct (around line 45):

```go
type PennySignalAggregator struct {
	universe     *PennyUniverseService
	screener     *PennyScreenerService
	edgar        *SECEdgarService
	social       *SocialSignalService
	mu           sync.RWMutex
	candidates   map[string]CandidateScore
	blacklist    *BracketBlacklist
	logger       *logrus.Logger
	dilutionMode string // "shadow" (log only) or "enforce" (suppress); default "shadow"
}
```

Update `NewPennySignalAggregator` (around line 57). Add `os` to the imports if not present.

```go
func NewPennySignalAggregator(
	universe *PennyUniverseService,
	screener *PennyScreenerService,
	edgar *SECEdgarService,
	social *SocialSignalService,
) *PennySignalAggregator {
	logger := logrus.New()
	logger.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})
	mode := os.Getenv("PENNY_DILUTION_FILTER_MODE")
	if mode != "enforce" {
		mode = "shadow"
	}
	logger.WithField("mode", mode).Info("PennySignalAggregator: dilution filter mode")
	return &PennySignalAggregator{
		universe:     universe,
		screener:     screener,
		edgar:        edgar,
		social:       social,
		candidates:   make(map[string]CandidateScore),
		blacklist:    newBracketBlacklist(),
		logger:       logger,
		dilutionMode: mode,
	}
}
```

Update `GetCandidates` to gate the `continue` on enforce mode:

```go
if a.edgar != nil {
    if blocked, reason := a.edgar.IsDilutionBlocked(c.Ticker); blocked {
        a.logger.WithFields(logrus.Fields{
            "ticker":    c.Ticker,
            "composite": c.CompositeScore,
            "reason":    reason,
            "mode":      a.dilutionMode,
        }).Info("dilution block detected on candidate")
        if a.dilutionMode == "enforce" {
            continue
        }
    }
}
```

(The `a.edgar != nil` guard supports the test in step 1 that constructs the aggregator with a real edgar — but it also future-proofs against tests that pass nil.)

- [ ] **Step 4: Run tests**

```
go test ./services/... -run TestDilutionFilterMode -race -v
go test ./services/... -run "TestGetCandidates_SuppressesDilutionBlocked|TestDilutionBlockDoesNotDeleteSignalDetail" -race -v
```

Expected: PASS. Note: `TestGetCandidates_SuppressesDilutionBlocked` and `TestDilutionBlockDoesNotDeleteSignalDetail` from Task 9 will FAIL now because they default to shadow mode. Update them to explicitly set `agg.dilutionMode = "enforce"` immediately after construction.

- [ ] **Step 5: Update Task 9 tests to set enforce mode**

In `services/penny_signal_aggregator_test.go`, in both `TestGetCandidates_SuppressesDilutionBlocked` and `TestDilutionBlockDoesNotDeleteSignalDetail`, add `agg.dilutionMode = "enforce"` immediately after the `NewPennySignalAggregator` call.

- [ ] **Step 6: Re-run**

```
go test ./services/... -race -count=1
```

Expected: full test suite PASS.

- [ ] **Step 7: Commit**

```
git add services/penny_signal_aggregator.go services/penny_signal_aggregator_test.go
git commit -m "feat(penny): add PENNY_DILUTION_FILTER_MODE env (shadow|enforce)

Default ships as shadow — dilution blocks are logged but candidates are
not suppressed. Operator flips to enforce after one trading day of clean
shadow logs. Construction-time log line records the active mode for
operational visibility."
```

---

### Task 11: Document the rule in `TRADING_RULES_PENNY.md` and mirror to agent-config.json

**Files:**
- Modify: `TRADING_RULES_PENNY.md`
- Modify: `data/agent-config.json`

- [ ] **Step 1: Add a new section to `TRADING_RULES_PENNY.md`**

Insert immediately after the "Out of Scope (v1)" section at the bottom:

```markdown
---

## Dilution Filter

The signal pipeline suppresses any candidate whose issuer has filed a
dilution-related SEC document within a recent trading-day window. The
filter is **capital protection**, not alpha — it removes setups that look
attractive on technical/regulatory/social signals but where the issuer
has signaled active or imminent share dilution.

### Form-type coverage

| Form | Bucket | Window |
|---|---|---|
| S-1, S-1/A, F-1, F-1/A | takedown | 2 trading days |
| 424B* (any prospectus supplement) | takedown | 2 trading days |
| Bare S-3, S-3/A, F-3, F-3/A | shelf | 5 trading days |
| 8-K with item-3.02 in title or summary | takedown | 2 trading days |
| 8-K item-1.01 + equity-financing keyword | takedown | 2 trading days |
| 8-K item-8.01 + offering keyword | takedown | 2 trading days |

### Behavior

- A blocked ticker is **never returned** by `get_penny_candidates`,
  regardless of composite score.
- `get_penny_signal_detail` still returns the underlying score for a
  blocked ticker (block ≠ data deletion).
- A dilution block on a ticker the agent **already holds** does NOT
  trigger a forced exit. The dominant-signal stop rules remain the exit
  authority. The block is logged for operator review.

### Operator controls

- `PENNY_DILUTION_FILTER_MODE=shadow` (default): blocks are logged but
  candidates are not suppressed.
- `PENNY_DILUTION_FILTER_MODE=enforce`: blocks suppress candidates.
```

- [ ] **Step 2: Mirror the same content into `data/agent-config.json`**

Find the entry where `id == "penny-momentum"` and append the same Dilution Filter section to the `customRules` field. (Open the JSON, locate the strategy entry, edit the multi-line string value of `customRules`.)

- [ ] **Step 3: Validate the JSON parses**

```
node -e "JSON.parse(require('fs').readFileSync('data/agent-config.json', 'utf8')); console.log('ok')"
```

Expected output: `ok`. (If `node` is unavailable, use a Go one-liner: `go run -ldflags '-s' . < data/agent-config.json` is overkill — instead `python -c "import json; json.load(open('data/agent-config.json'))"` works.)

- [ ] **Step 4: Commit**

```
git add TRADING_RULES_PENNY.md data/agent-config.json
git commit -m "docs(penny): document dilution filter rules

Adds the Dilution Filter section to TRADING_RULES_PENNY.md and mirrors
into data/agent-config.json customRules (the agent-runtime authoritative
source). Documents form coverage, windows, and the block-not-exit rule."
```

---

### Task 12: End-to-end verification

- [ ] **Step 1: Full test sweep**

```
go test ./... -race -count=1
```

Expected: all PASS.

- [ ] **Step 2: Build the bot binary**

```
go build ./cmd/bot
```

Expected: clean build.

- [ ] **Step 3: Smoke-check that `Start()` doesn't deadlock**

This is hand-verified, since live EDGAR polling requires a real network. Bring the bot up briefly:

```
go run ./cmd/bot 2>&1 | findstr /I "dilution"
```

Look for one log line near startup: `PennySignalAggregator: dilution filter mode mode=shadow`.

If the bot starts cleanly and the line appears, the wiring is correct. Stop the bot.

- [ ] **Step 4: Final commit (if anything was tweaked)**

If steps 1-3 surfaced any issues that required fixes, commit them:

```
git add -A
git commit -m "fix(penny): address dilution-filter integration issues found in smoke test"
```

If everything passed clean, no extra commit is needed.

---

## Self-Review

**Spec coverage:**
- Form types (S-1, S-3, 424, F-1, F-3, 8-K heuristic): Tasks 5, 6 ✓
- Bucket windows (2 takedown, 5 shelf): Task 3 (constants), Tasks 5/6 (assignment) ✓
- Lazy eviction with trading-day distance: Task 3 ✓
- Replacement rule (takedown beats shelf): Task 5 (`upsertDilutionBlock`) ✓
- `IsDilutionBlocked` API: Task 3 ✓
- Calendar dependency (constructor signature change, `Calendar()` getter): Tasks 1, 2 ✓
- `nowFunc` clock injection: Task 2 ✓
- Concurrent fetch via WaitGroup: Task 7 ✓
- Two independent goroutines (positive 30s + dilution 5min): Task 7 ✓
- Heuristic 8-K piggybacks on existing poll: Task 7 (`pollEdgar` updated) ✓
- Aggregator integration in `GetCandidates`: Task 9 ✓
- Lock-ordering comment update: Task 9 ✓
- Block ≠ data deletion (regression test): Task 9 ✓
- Block ≠ exit (no `position_manager.go` coupling): structurally enforced — `SECEdgarService` is not threaded into `PositionManager`, and Task 9's `TestDilutionBlockDoesNotDeleteSignalDetail` pins the data-preservation invariant. The "no force exit" property is preserved by absence: no task in this plan adds `IsDilutionBlocked` calls outside the aggregator.
- Shadow/enforce mode env var: Task 10 ✓
- Three log event sites (creation warn, suppression info, mode info): Tasks 5 (creation warn), 9/10 (suppression info), 10 (mode info on startup) ✓
- Documentation in `TRADING_RULES_PENNY.md` and `agent-config.json`: Task 11 ✓
- All 11 fixtures: Task 4 ✓

**Placeholder scan:** No "TBD", "TODO", "implement later" found. All steps contain executable code or commands.

**Type consistency:** `dilutionEntry`, `dilutionFormSpec`, `dilution8KPattern`, `dilutionMode` referenced consistently. `IsDilutionBlocked`, `applyDilutionFiling`, `upsertDilutionBlock`, `scanHeuristic8Ks`, `matchDilution8K`, `Calendar()`, `nowFunc` — names stable across all tasks.
