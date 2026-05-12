package services

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ── classifyEvent ──────────────────────────────────────────────────

func TestClassifyEvent_CPI(t *testing.T) {
	kind, ok := classifyEvent("US", "Consumer Price Index (CPI) m/m")
	if !ok || kind != EconCPI {
		t.Errorf("expected EconCPI, got kind=%v ok=%v", kind, ok)
	}
}

func TestClassifyEvent_CoreCPI(t *testing.T) {
	kind, ok := classifyEvent("US", "Core CPI m/m")
	if !ok || kind != EconCPI {
		t.Errorf("expected Core CPI to classify as EconCPI, got kind=%v ok=%v", kind, ok)
	}
}

func TestClassifyEvent_NFP(t *testing.T) {
	kind, ok := classifyEvent("US", "Non-Farm Payrolls")
	if !ok || kind != EconNFP {
		t.Errorf("expected EconNFP, got kind=%v ok=%v", kind, ok)
	}
}

func TestClassifyEvent_FOMC(t *testing.T) {
	kind, ok := classifyEvent("US", "FOMC Statement")
	if !ok || kind != EconFOMC {
		t.Errorf("expected EconFOMC, got kind=%v ok=%v", kind, ok)
	}
}

func TestClassifyEvent_PCE(t *testing.T) {
	kind, ok := classifyEvent("US", "Core PCE Price Index m/m")
	if !ok || kind != EconPCE {
		t.Errorf("expected EconPCE, got kind=%v ok=%v", kind, ok)
	}
}

func TestClassifyEvent_PPI(t *testing.T) {
	kind, ok := classifyEvent("US", "Producer Price Index (PPI) m/m")
	if !ok || kind != EconPPI {
		t.Errorf("expected EconPPI, got kind=%v ok=%v", kind, ok)
	}
}

func TestClassifyEvent_CoreRetail(t *testing.T) {
	kind, ok := classifyEvent("US", "Core Retail Sales m/m")
	if !ok || kind != EconCoreRetail {
		t.Errorf("expected EconCoreRetail, got kind=%v ok=%v", kind, ok)
	}
}

func TestClassifyEvent_PlainRetailNotCore(t *testing.T) {
	// Plain "Retail Sales" must NOT classify — the brief specifically lists
	// CORE retail sales, which is the higher-signal release.
	_, ok := classifyEvent("US", "Retail Sales m/m")
	if ok {
		t.Error("expected plain Retail Sales to not classify (only core retail is on the watchlist)")
	}
}

func TestClassifyEvent_NonUSIgnored(t *testing.T) {
	_, ok := classifyEvent("GB", "Consumer Price Index (CPI) m/m")
	if ok {
		t.Error("expected non-US CPI to not classify")
	}
}

func TestClassifyEvent_UnknownEventIgnored(t *testing.T) {
	_, ok := classifyEvent("US", "ADP Employment Change")
	if ok {
		t.Error("expected ADP to not classify (not on the six-event watchlist)")
	}
}

// ── computeBlackout boundaries ─────────────────────────────────────

func cpiAt(ts time.Time) EconEvent {
	return EconEvent{
		Time:    ts,
		Kind:    EconCPI,
		Name:    "Consumer Price Index (CPI) m/m",
		Country: "US",
	}
}

func TestComputeBlackout_BeforeWindow(t *testing.T) {
	event := cpiAt(time.Date(2026, 5, 13, 12, 30, 0, 0, time.UTC))
	now := event.Time.Add(-31 * time.Minute)
	status := computeBlackout(now, []EconEvent{event}, 30*time.Minute, 15*time.Minute)
	if status.IsBlackout {
		t.Error("expected no blackout 31 minutes before event")
	}
}

func TestComputeBlackout_AtWindowStart(t *testing.T) {
	event := cpiAt(time.Date(2026, 5, 13, 12, 30, 0, 0, time.UTC))
	now := event.Time.Add(-30 * time.Minute)
	status := computeBlackout(now, []EconEvent{event}, 30*time.Minute, 15*time.Minute)
	if !status.IsBlackout {
		t.Error("expected blackout exactly 30 minutes before event")
	}
}

func TestComputeBlackout_OneMinuteBefore(t *testing.T) {
	event := cpiAt(time.Date(2026, 5, 13, 12, 30, 0, 0, time.UTC))
	now := event.Time.Add(-1 * time.Minute)
	status := computeBlackout(now, []EconEvent{event}, 30*time.Minute, 15*time.Minute)
	if !status.IsBlackout {
		t.Error("expected blackout 1 minute before event")
	}
}

func TestComputeBlackout_AtEventTime(t *testing.T) {
	event := cpiAt(time.Date(2026, 5, 13, 12, 30, 0, 0, time.UTC))
	now := event.Time
	status := computeBlackout(now, []EconEvent{event}, 30*time.Minute, 15*time.Minute)
	if !status.IsBlackout {
		t.Error("expected blackout at event time")
	}
}

func TestComputeBlackout_FourteenMinutesAfter(t *testing.T) {
	event := cpiAt(time.Date(2026, 5, 13, 12, 30, 0, 0, time.UTC))
	now := event.Time.Add(14 * time.Minute)
	status := computeBlackout(now, []EconEvent{event}, 30*time.Minute, 15*time.Minute)
	if !status.IsBlackout {
		t.Error("expected blackout 14 minutes after event")
	}
}

func TestComputeBlackout_AtWindowEnd(t *testing.T) {
	event := cpiAt(time.Date(2026, 5, 13, 12, 30, 0, 0, time.UTC))
	now := event.Time.Add(15 * time.Minute)
	status := computeBlackout(now, []EconEvent{event}, 30*time.Minute, 15*time.Minute)
	if !status.IsBlackout {
		t.Error("expected blackout exactly 15 minutes after event")
	}
}

func TestComputeBlackout_AfterWindow(t *testing.T) {
	event := cpiAt(time.Date(2026, 5, 13, 12, 30, 0, 0, time.UTC))
	now := event.Time.Add(16 * time.Minute)
	status := computeBlackout(now, []EconEvent{event}, 30*time.Minute, 15*time.Minute)
	if status.IsBlackout {
		t.Error("expected no blackout 16 minutes after event")
	}
}

// Defensive: even if a non-US event somehow makes it into the cache,
// computeBlackout should not trigger blackout on it.
func TestComputeBlackout_NonUSEventIgnored(t *testing.T) {
	event := EconEvent{
		Time:    time.Date(2026, 5, 13, 12, 30, 0, 0, time.UTC),
		Kind:    EconCPI,
		Name:    "UK CPI",
		Country: "GB",
	}
	now := event.Time
	status := computeBlackout(now, []EconEvent{event}, 30*time.Minute, 15*time.Minute)
	if status.IsBlackout {
		t.Error("expected non-US event to not trigger blackout")
	}
}

// Defensive: events with empty Kind (unclassified) should not trigger blackout.
func TestComputeBlackout_UnclassifiedEventIgnored(t *testing.T) {
	event := EconEvent{
		Time:    time.Date(2026, 5, 13, 12, 30, 0, 0, time.UTC),
		Kind:    "",
		Name:    "Something Else",
		Country: "US",
	}
	now := event.Time
	status := computeBlackout(now, []EconEvent{event}, 30*time.Minute, 15*time.Minute)
	if status.IsBlackout {
		t.Error("expected unclassified event to not trigger blackout")
	}
}

func TestComputeBlackout_OverlappingEventsPicksActive(t *testing.T) {
	// CPI at 12:30, PPI 5 minutes later. At 12:25 we are inside CPI's
	// blackout (-30..+15) and also 10 minutes before PPI (still pre-window
	// for PPI since PPI's pre-window starts at 12:05). At 12:31 we are
	// inside both windows (CPI's after-window and PPI's before-window).
	cpi := cpiAt(time.Date(2026, 5, 13, 12, 30, 0, 0, time.UTC))
	ppi := EconEvent{
		Time:    time.Date(2026, 5, 13, 12, 35, 0, 0, time.UTC),
		Kind:    EconPPI,
		Name:    "PPI m/m",
		Country: "US",
	}
	events := []EconEvent{cpi, ppi}

	// At 12:31 — inside CPI window.
	now := time.Date(2026, 5, 13, 12, 31, 0, 0, time.UTC)
	status := computeBlackout(now, events, 30*time.Minute, 15*time.Minute)
	if !status.IsBlackout {
		t.Error("expected blackout when inside an overlapping window")
	}
	if status.Reason == "" {
		t.Error("expected non-empty reason when in blackout")
	}
}

func TestComputeBlackout_BlackoutUntilSet(t *testing.T) {
	event := cpiAt(time.Date(2026, 5, 13, 12, 30, 0, 0, time.UTC))
	now := event.Time.Add(-10 * time.Minute)
	status := computeBlackout(now, []EconEvent{event}, 30*time.Minute, 15*time.Minute)
	if !status.IsBlackout {
		t.Fatal("expected blackout")
	}
	if status.BlackoutUntil == nil {
		t.Fatal("expected BlackoutUntil to be set during blackout")
	}
	want := event.Time.Add(15 * time.Minute)
	if !status.BlackoutUntil.Equal(want) {
		t.Errorf("expected BlackoutUntil=%s, got %s", want, status.BlackoutUntil)
	}
}

func TestComputeBlackout_WindowsExposedInResponse(t *testing.T) {
	event := cpiAt(time.Date(2026, 5, 13, 12, 30, 0, 0, time.UTC))
	now := event.Time.Add(-2 * time.Hour) // far outside
	status := computeBlackout(now, []EconEvent{event}, 30*time.Minute, 15*time.Minute)
	if status.WindowBeforeMin != 30 {
		t.Errorf("expected WindowBeforeMin=30, got %d", status.WindowBeforeMin)
	}
	if status.WindowAfterMin != 15 {
		t.Errorf("expected WindowAfterMin=15, got %d", status.WindowAfterMin)
	}
}

// ── EconCalendarService HTTP + cache ────────────────────────────────

type countingTransport struct {
	count int32
	resp  string
}

func (t *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	atomic.AddInt32(&t.count, 1)
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(t.resp)),
		Header:     make(http.Header),
	}, nil
}

type errTransport struct{}

func (e *errTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("network down")
}

func TestEconCalendarService_ParsesAndFiltersFMPResponse(t *testing.T) {
	now := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)
	fmpResp := `[
		{"date":"2026-05-13 12:30:00","country":"US","event":"Consumer Price Index (CPI) m/m","impact":"High"},
		{"date":"2026-05-14 14:00:00","country":"GB","event":"Bank of England Rate Decision","impact":"High"},
		{"date":"2026-05-15 08:30:00","country":"US","event":"Initial Jobless Claims","impact":"Medium"}
	]`
	transport := &countingTransport{resp: fmpResp}
	svc := NewEconCalendarService("test-key")
	svc.client = &http.Client{Transport: transport}

	status := svc.GetBlackoutStatus(context.Background(), now)
	if status == nil {
		t.Fatal("status should never be nil")
	}
	if status.Error != "" {
		t.Errorf("unexpected error: %s", status.Error)
	}

	// Cache should hold exactly one event: US CPI. UK rate decision is non-US;
	// Initial Jobless Claims is US but not on the watchlist.
	if got, want := len(svc.cache), 1; got != want {
		t.Fatalf("expected %d classified event in cache, got %d: %+v", want, got, svc.cache)
	}
	if svc.cache[0].Kind != EconCPI {
		t.Errorf("expected cached event Kind=EconCPI, got %v", svc.cache[0].Kind)
	}
}

func TestEconCalendarService_CacheFreshness(t *testing.T) {
	now := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)
	fmpResp := `[{"date":"2026-05-13 12:30:00","country":"US","event":"Consumer Price Index (CPI) m/m","impact":"High"}]`
	transport := &countingTransport{resp: fmpResp}
	svc := NewEconCalendarService("test-key")
	svc.client = &http.Client{Transport: transport}

	// First call hits HTTP.
	_ = svc.GetBlackoutStatus(context.Background(), now)
	if got := atomic.LoadInt32(&transport.count); got != 1 {
		t.Fatalf("first call: expected 1 HTTP call, got %d", got)
	}

	// Second call 1h later is within the 6h freshness window — no HTTP.
	_ = svc.GetBlackoutStatus(context.Background(), now.Add(1*time.Hour))
	if got := atomic.LoadInt32(&transport.count); got != 1 {
		t.Errorf("second call within freshness: expected total=1 HTTP call, got %d", got)
	}

	// Third call 7h later is stale — must refetch.
	_ = svc.GetBlackoutStatus(context.Background(), now.Add(7*time.Hour))
	if got := atomic.LoadInt32(&transport.count); got != 2 {
		t.Errorf("third call after staleness: expected total=2 HTTP calls, got %d", got)
	}
}

func TestEconCalendarService_FetchErrorReportedFailOpen(t *testing.T) {
	now := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)
	svc := NewEconCalendarService("test-key")
	svc.client = &http.Client{Transport: &errTransport{}}

	status := svc.GetBlackoutStatus(context.Background(), now)
	if status == nil {
		t.Fatal("status must not be nil on error")
	}
	if status.Error == "" {
		t.Error("expected Error to be populated after fetch failure")
	}
	if status.IsBlackout {
		t.Error("IsBlackout should be false when we have no data — do not fabricate blackout")
	}
}
