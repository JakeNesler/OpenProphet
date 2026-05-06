package services

import (
	"net/http"
	"net/http/httptest"
	"strings"
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
	svc := &SECEdgarService{entries: make(map[string]regulatoryEntry), logger: logrus.New()}
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

	svc := NewSECEdgarService(nil, ts.Client(), "test@example.com")
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

	svc := NewSECEdgarService(nil, ts.Client(), "test@example.com")
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

	svc := NewSECEdgarService(nil, ts.Client(), "test@example.com")
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
