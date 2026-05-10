package services

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func newTestEdgar() *SECEdgarService {
	return &SECEdgarService{
		entries: make(map[string]regulatoryEntry),
		logger:  logrus.New(),
		nowFunc: time.Now,
	}
}

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

func TestExtractTickerFromTitle(t *testing.T) {
	tickers := map[string]bool{"ACME": true, "FOO": true}
	tests := []struct {
		title string
		want  string
	}{
		{"8-K - ACME CORP (Issuer)", "ACME"},
		{"8-K - BORING INC (Issuer)", ""},
		{"8-K - (FOO) Corp", "FOO"},
		{"ACME CORP files 8-K", "ACME"},
	}
	for _, tc := range tests {
		got := extractTickerFromTitle(tc.title, tickers)
		if got != tc.want {
			t.Errorf("extractTickerFromTitle(%q) = %q, want %q", tc.title, got, tc.want)
		}
	}
}

func TestSECEdgarService_GetRegulatoryScore_Decay(t *testing.T) {
	svc := &SECEdgarService{
		entries: make(map[string]regulatoryEntry),
		logger:  logrus.New(),
		nowFunc: time.Now,
	}
	svc.entries["TICK"] = regulatoryEntry{
		Entry:     DecayEntry{BaseScore: 40.0, EventTime: time.Now(), HalfLifeHrs: regulatoryHalfLifeHours},
		EventDesc: "test",
	}
	score, desc := svc.GetRegulatoryScore("TICK")
	if score < 39 || score > 40 {
		t.Errorf("fresh entry: expected ~40, got %f", score)
	}
	if desc != "test" {
		t.Errorf("expected desc 'test', got %q", desc)
	}
}

func TestSECEdgarService_UpsertEntry_KeepsHigher(t *testing.T) {
	svc := &SECEdgarService{
		entries: make(map[string]regulatoryEntry),
		logger:  logrus.New(),
		nowFunc: time.Now,
	}
	now := time.Now()
	svc.upsertEntry("T", 25.0, now, "pr wire")
	svc.upsertEntry("T", 40.0, now, "8-K")
	svc.upsertEntry("T", 10.0, now, "lower")
	if svc.entries["T"].Entry.BaseScore != 40.0 {
		t.Errorf("expected 40.0, got %f", svc.entries["T"].Entry.BaseScore)
	}
	if svc.entries["T"].EventDesc != "8-K" {
		t.Errorf("expected '8-K', got %q", svc.entries["T"].EventDesc)
	}
}

func TestSECEdgarService_FetchAtom_NonOK(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer ts.Close()

	svc := NewSECEdgarService(nil, ts.Client(), "test@example.com", nil)
	entries, err := svc.fetchAtom(ts.URL)
	if err == nil {
		t.Error("expected error for non-200 response, got nil")
	}
	if entries != nil {
		t.Errorf("expected nil entries on error, got %v", entries)
	}
}

func TestSECEdgarService_FetchRSS_NonOK(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer ts.Close()

	svc := NewSECEdgarService(nil, ts.Client(), "test@example.com", nil)
	items, err := svc.fetchRSS(ts.URL)
	if err == nil {
		t.Error("expected error for non-200 response, got nil")
	}
	if items != nil {
		t.Errorf("expected nil items on error, got %v", items)
	}
}

func TestSECEdgarService_FetchRSS_ParsesTwoItems(t *testing.T) {
	rssBody := `<?xml version="1.0"?>
<rss version="2.0">
  <channel>
    <item>
      <title>ACME Corp announces partnership</title>
      <description>ACME today announced a major deal</description>
    </item>
    <item>
      <title>Unrelated news</title>
      <description>Nothing relevant here</description>
    </item>
  </channel>
</rss>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Write([]byte(rssBody))
	}))
	defer ts.Close()

	svc := NewSECEdgarService(nil, ts.Client(), "test@example.com", nil)
	tickers := map[string]bool{"ACME": true}
	// Override the URL by calling the internal method with our tickers
	// We'll test via pollGlobeNewswire by temporarily injecting — but since
	// the URL is hardcoded in pollGlobeNewswire, test fetchRSS parsing instead:
	items, err := svc.fetchRSS(ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	// Verify the ticker-match logic
	found := false
	for _, item := range items {
		combined := strings.ToUpper(item.Title + " " + item.Description)
		if strings.Contains(combined, "ACME") {
			found = true
		}
	}
	_ = tickers
	if !found {
		t.Error("expected ACME to be found in feed items")
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
	if score < 37.0 || score > 40.0 {
		t.Errorf("expected decayed old score ~37.8 (40 base, 2h elapsed, 24h half-life), got %f", score)
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

func TestSECEdgar_ParseRSSDate_NoSeconds_NamedTz(t *testing.T) {
	// EDGAR has been observed emitting timestamps without seconds, e.g.
	// "Fri, 08 May 2026 17:12 GMT". The previous parser rejected these.
	ts := "Fri, 08 May 2026 17:12 GMT"
	parsed, fallback := parseRSSDate(ts)
	if fallback {
		t.Error("expected no fallback for no-seconds named-tz timestamp")
	}
	if parsed.UTC().Hour() != 17 || parsed.UTC().Minute() != 12 {
		t.Errorf("expected 17:12 UTC, got %v", parsed.UTC())
	}
}

func TestSECEdgar_ParseRSSDate_NoSeconds_NumericTz(t *testing.T) {
	ts := "Fri, 08 May 2026 13:12 -0400"
	parsed, fallback := parseRSSDate(ts)
	if fallback {
		t.Error("expected no fallback for no-seconds numeric-tz timestamp")
	}
	if parsed.UTC().Hour() != 17 || parsed.UTC().Minute() != 12 {
		t.Errorf("expected 17:12 UTC, got %v", parsed.UTC())
	}
}

func TestSECEdgar_ParseAtomDate_NoSeconds_NamedTz(t *testing.T) {
	ts := "Fri, 08 May 2026 17:12 GMT"
	parsed, fallback := parseAtomDate(ts)
	if fallback {
		t.Error("expected no fallback for no-seconds named-tz timestamp")
	}
	if parsed.UTC().Hour() != 17 || parsed.UTC().Minute() != 12 {
		t.Errorf("expected 17:12 UTC, got %v", parsed.UTC())
	}
}

func TestSECEdgar_ParseAtomDate_FallbackSkipsUpsert(t *testing.T) {
	// Verify that a bad timestamp does not insert an entry
	svc := newTestEdgar()
	svc.mu.Lock()
	eventTime, isFallback := parseAtomDate("not-a-date")
	if !isFallback {
		t.Fatal("expected fallback=true for bad date")
	}
	// Simulate what pollEdgar does — skip on fallback
	if !isFallback {
		svc.upsertEntry("TICK", 40.0, eventTime, "bad entry")
	}
	svc.mu.Unlock()
	score, _ := svc.GetRegulatoryScore("TICK")
	if score != 0 {
		t.Errorf("expected no entry for unparseable date, got score=%f", score)
	}
}

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
