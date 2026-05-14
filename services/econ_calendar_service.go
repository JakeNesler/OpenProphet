package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// EconCalendarService provides a US-economic-release blackout window across
// all four trading agents. It fetches scheduled releases from the FMP economic
// calendar API, classifies them against the six-event watchlist (CPI, NFP,
// FOMC, PCE, PPI, Core Retail Sales), caches the result, and exposes a
// computed blackout status for a 30-min-before / 15-min-after window.
//
// Two consumers:
//   - preflight skips no-position beats during blackout (token saver)
//   - LLM reads blackout status via MCP and avoids opening new entries
//
// Fail-open on fetch errors: returns IsBlackout=false with Error populated.
// Preflight treats this as run-the-beat; rules treat presence of Error as
// "no new entries", so neither layer trades blind during an outage.

type EconEventKind string

const (
	EconCPI        EconEventKind = "CPI"
	EconNFP        EconEventKind = "NFP"
	EconFOMC       EconEventKind = "FOMC"
	EconPCE        EconEventKind = "PCE"
	EconPPI        EconEventKind = "PPI"
	EconCoreRetail EconEventKind = "CoreRetail"
)

// Window constants for the shared blackout. The 24h pre-FOMC ban for Harvest
// condor entries lives separately in TRADING_RULES_HARVEST.md / harvest_service.go
// and is unaffected.
const (
	econWindowBefore = 30 * time.Minute
	econWindowAfter  = 15 * time.Minute
	econFreshness    = 6 * time.Hour
	econHorizon      = 14 * 24 * time.Hour
	econLookback     = 24 * time.Hour
)

// EconEvent is one classified US release the agents care about.
type EconEvent struct {
	Time    time.Time     `json:"time"`
	Kind    EconEventKind `json:"kind"`
	Name    string        `json:"name"`
	Country string        `json:"country"`
}

// EconBlackoutStatus is the per-call response.
type EconBlackoutStatus struct {
	IsBlackout      bool       `json:"is_blackout"`
	Reason          string     `json:"reason,omitempty"`
	BlackoutUntil   *time.Time `json:"blackout_until,omitempty"`
	NextEvent       *EconEvent `json:"next_event,omitempty"`
	WindowBeforeMin int        `json:"window_before_min"`
	WindowAfterMin  int        `json:"window_after_min"`
	Error           string     `json:"error,omitempty"`
}

// EconCalendarService caches FMP econ calendar data in memory.
type EconCalendarService struct {
	apiKey string
	client *http.Client

	mu            sync.RWMutex
	cache         []EconEvent // classified, sorted by Time ascending
	fetchedAt     time.Time
	cachedHorizon time.Time
}

// NewEconCalendarService constructs a service. apiKey may be empty for tests
// that inject a custom http.Client transport.
func NewEconCalendarService(apiKey string) *EconCalendarService {
	return &EconCalendarService{
		apiKey: apiKey,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// GetBlackoutStatus returns the blackout status at `now`. Refreshes the cache
// if stale; surfaces fetch errors in the Error field rather than panicking
// (fail-open in preflight, fail-closed in rules).
func (s *EconCalendarService) GetBlackoutStatus(ctx context.Context, now time.Time) *EconBlackoutStatus {
	if err := s.ensureFresh(ctx, now); err != nil {
		s.mu.RLock()
		events := s.cache
		s.mu.RUnlock()
		// Use whatever we have in the cache so a transient error doesn't
		// silently turn off blackout protection. If the cache is empty (first
		// call failed), computeBlackout will return IsBlackout=false naturally.
		status := computeBlackout(now, events, econWindowBefore, econWindowAfter)
		status.Error = err.Error()
		return status
	}
	s.mu.RLock()
	events := s.cache
	s.mu.RUnlock()
	return computeBlackout(now, events, econWindowBefore, econWindowAfter)
}

// UpcomingEvents returns the cached classified events within the next
// lookaheadDays from now. Refreshes if stale.
func (s *EconCalendarService) UpcomingEvents(ctx context.Context, now time.Time, lookaheadDays int) ([]EconEvent, error) {
	if err := s.ensureFresh(ctx, now); err != nil {
		return nil, err
	}
	horizon := now.Add(time.Duration(lookaheadDays) * 24 * time.Hour)
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]EconEvent, 0, len(s.cache))
	for _, e := range s.cache {
		if !e.Time.Before(now) && !e.Time.After(horizon) {
			out = append(out, e)
		}
	}
	return out, nil
}

// ensureFresh refreshes when the cache is stale by either time or horizon.
func (s *EconCalendarService) ensureFresh(ctx context.Context, now time.Time) error {
	s.mu.RLock()
	stale := s.fetchedAt.IsZero() ||
		now.Sub(s.fetchedAt) > econFreshness ||
		now.After(s.cachedHorizon.Add(-24*time.Hour))
	s.mu.RUnlock()
	if !stale {
		return nil
	}
	return s.refresh(ctx, now)
}

// refresh unconditionally fetches the FMP calendar over [now-1d, now+14d]
// and replaces the cache. Caller controls when this is invoked.
//
// Endpoint order: stable first (post-Aug-2025 accounts), legacy v3 fallback
// for pre-Aug-2025 subscriptions. FMP retired the v3 path with a 403
// "Legacy Endpoint" body on Aug 31 2025, so the stable URL is now the
// primary on every new account.
func (s *EconCalendarService) refresh(ctx context.Context, now time.Time) error {
	from := now.Add(-econLookback).UTC().Format("2006-01-02")
	to := now.Add(econHorizon).UTC().Format("2006-01-02")

	params := url.Values{}
	params.Set("from", from)
	params.Set("to", to)
	if s.apiKey != "" {
		params.Set("apikey", s.apiKey)
	}
	endpoints := []string{
		"https://financialmodelingprep.com/stable/economic-calendar",
		"https://financialmodelingprep.com/api/v3/economic_calendar",
	}

	type rawEvent struct {
		Date    string `json:"date"`
		Country string `json:"country"`
		Event   string `json:"event"`
		Impact  string `json:"impact"`
	}
	var raw []rawEvent
	var lastErr error
	for _, base := range endpoints {
		raw = nil
		lastErr = s.fetchFMPCalendar(ctx, base+"?"+params.Encode(), &raw)
		if lastErr == nil {
			break
		}
	}
	if lastErr != nil {
		return lastErr
	}

	classified := make([]EconEvent, 0, len(raw))
	for _, r := range raw {
		kind, ok := classifyEvent(r.Country, r.Event)
		if !ok {
			continue
		}
		ts, err := time.Parse("2006-01-02 15:04:05", r.Date)
		if err != nil {
			continue
		}
		// FMP documents date as UTC; parse with no location yields UTC already.
		classified = append(classified, EconEvent{
			Time:    ts.UTC(),
			Kind:    kind,
			Name:    r.Event,
			Country: r.Country,
		})
	}
	sort.Slice(classified, func(i, j int) bool { return classified[i].Time.Before(classified[j].Time) })

	s.mu.Lock()
	s.cache = classified
	s.fetchedAt = now
	s.cachedHorizon = now.Add(econHorizon)
	s.mu.Unlock()
	return nil
}

// fetchFMPCalendar issues one GET against the given URL and decodes the JSON
// array into out. Non-2xx or transport errors are returned; the body is
// included in the error so callers (and the LLM via the blackout endpoint)
// can distinguish "legacy endpoint retired" from a real outage.
func (s *EconCalendarService) fetchFMPCalendar(ctx context.Context, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("building fmp request: %w", err)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("fmp econ_calendar fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("fmp econ_calendar status %d: %s", resp.StatusCode, string(body))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decoding fmp econ_calendar: %w", err)
	}
	return nil
}

// classifyEvent maps a (country, event-name) pair from FMP to one of the six
// watchlist kinds. Returns "", false if the event is not on the watchlist or
// is not a US release. Matching is case-insensitive substring against the
// raw FMP event name.
func classifyEvent(country, name string) (EconEventKind, bool) {
	if country != "US" {
		return "", false
	}
	upper := strings.ToUpper(name)

	// Core Retail Sales must be checked before any generic "retail" check
	// to ensure only the core release classifies (per the brief).
	if strings.Contains(upper, "CORE RETAIL SALES") {
		return EconCoreRetail, true
	}
	if strings.Contains(upper, "CPI") || strings.Contains(upper, "CONSUMER PRICE INDEX") {
		return EconCPI, true
	}
	if strings.Contains(upper, "NON-FARM PAYROLLS") || strings.Contains(upper, "NONFARM PAYROLLS") || strings.Contains(upper, "NFP") {
		return EconNFP, true
	}
	if strings.Contains(upper, "FOMC") || strings.Contains(upper, "FEDERAL FUNDS RATE") || strings.Contains(upper, "FED INTEREST RATE") {
		return EconFOMC, true
	}
	if strings.Contains(upper, "PCE") || strings.Contains(upper, "PERSONAL CONSUMPTION") {
		return EconPCE, true
	}
	if strings.Contains(upper, "PPI") || strings.Contains(upper, "PRODUCER PRICE INDEX") {
		return EconPPI, true
	}
	return "", false
}

// computeBlackout iterates events and returns the active blackout, if any.
// Events with empty Kind or non-US Country are skipped defensively even
// though the cache pre-filters them.
func computeBlackout(now time.Time, events []EconEvent, before, after time.Duration) *EconBlackoutStatus {
	status := &EconBlackoutStatus{
		WindowBeforeMin: int(before / time.Minute),
		WindowAfterMin:  int(after / time.Minute),
	}

	// Track the earliest active blackout and the next upcoming event.
	for i := range events {
		e := events[i]
		if e.Kind == "" || e.Country != "US" {
			continue
		}
		windowStart := e.Time.Add(-before)
		windowEnd := e.Time.Add(after)
		if !now.Before(windowStart) && !now.After(windowEnd) {
			// In blackout. First match wins (events are sorted by time).
			until := windowEnd
			if status.IsBlackout {
				// Already blackout from earlier event — keep first.
				continue
			}
			status.IsBlackout = true
			status.Reason = fmt.Sprintf("%s release at %s UTC", e.Kind, e.Time.Format("2006-01-02 15:04"))
			status.BlackoutUntil = &until
			ne := e
			status.NextEvent = &ne
			continue
		}
		if status.NextEvent == nil && e.Time.After(now) {
			ne := e
			status.NextEvent = &ne
		}
	}
	return status
}
