package services

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPennyUniverseService_Filter(t *testing.T) {
	items := []fmpScreenerItem{
		{Symbol: "GOOD", CompanyName: "Good Co", MarketCap: 100_000_000, Price: 5.0, Volume: 100_000, ExchangeShortName: "NASDAQ"},
		{Symbol: "CHEAP", CompanyName: "Too Cheap", MarketCap: 100_000_000, Price: 1.0, Volume: 100_000, ExchangeShortName: "NASDAQ"},
		{Symbol: "PRICEY", CompanyName: "Too Pricey", MarketCap: 100_000_000, Price: 15.0, Volume: 100_000, ExchangeShortName: "NASDAQ"},
		{Symbol: "TINYCAP", CompanyName: "Tiny Cap", MarketCap: 10_000_000, Price: 5.0, Volume: 100_000, ExchangeShortName: "NASDAQ"},
		{Symbol: "LOWVOL", CompanyName: "Low Vol", MarketCap: 100_000_000, Price: 5.0, Volume: 1_000, ExchangeShortName: "NASDAQ"},
		{Symbol: "OTC", CompanyName: "OTC Co", MarketCap: 100_000_000, Price: 5.0, Volume: 100_000, ExchangeShortName: "OTC"},
	}
	svc := NewPennyUniverseService("dummy", "", "", "", nil, nil)
	result, _ := svc.filter(items)
	if len(result) != 1 {
		t.Fatalf("expected 1 symbol, got %d", len(result))
	}
	if result[0].Ticker != "GOOD" {
		t.Errorf("expected GOOD, got %s", result[0].Ticker)
	}
}

func TestPennyUniverseService_HTTPRefresh(t *testing.T) {
	items := []fmpScreenerItem{
		{Symbol: "TEST", CompanyName: "Test Inc", MarketCap: 200_000_000, Price: 4.0, Volume: 200_000, ExchangeShortName: "NYSE"},
	}
	body, _ := json.Marshal(items)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	defer ts.Close()

	svc := NewPennyUniverseService("testkey", "", "", "", nil, ts.Client())
	svc.fmpBaseURL = ts.URL
	svc.refresh()
	tickers := svc.GetTickers()
	if len(tickers) != 1 || tickers[0] != "TEST" {
		t.Errorf("expected [TEST], got %v", tickers)
	}
}

func TestUniverseFilter_ADV_BoundaryExcluded(t *testing.T) {
	svc := NewPennyUniverseService("key", "", "", "", nil, nil)
	items := []fmpScreenerItem{
		{Symbol: "LOW", CompanyName: "Low Vol", MarketCap: 100_000_000, Price: 5.0,
			Volume: 99_999, ExchangeShortName: "NASDAQ"}, // dollarVol = 499,995 < 500,000
	}
	result, _ := svc.filter(items)
	if len(result) != 0 {
		t.Errorf("expected ADV $499,995 to be excluded, got %d results", len(result))
	}
}

func TestUniverseFilter_ADV_BoundaryIncluded(t *testing.T) {
	svc := NewPennyUniverseService("key", "", "", "", nil, nil)
	items := []fmpScreenerItem{
		{Symbol: "OK", CompanyName: "OK Vol", MarketCap: 100_000_000, Price: 5.0,
			Volume: 100_000, ExchangeShortName: "NASDAQ"}, // dollarVol = 500,000 >= 500,000
	}
	result, _ := svc.filter(items)
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

func TestIsMarketHours_ExactOpen(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	cal := AlpacaCalendarEntry{Date: "2026-05-02", Open: "09:30", Close: "16:00", SessionOpen: "0400", SessionClose: "2000"}
	now := time.Date(2026, 5, 2, 9, 30, 0, 0, loc)
	got := isMarketHours(now, cal)
	if got != "open" {
		t.Errorf("expected 'open' at exactly 09:30:00, got %q", got)
	}
}

func TestIsMarketHours_ExactClose(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	cal := AlpacaCalendarEntry{Date: "2026-05-02", Open: "09:30", Close: "16:00", SessionOpen: "0400", SessionClose: "2000"}
	now := time.Date(2026, 5, 2, 16, 0, 0, 0, loc)
	got := isMarketHours(now, cal)
	if got != "after" {
		t.Errorf("expected 'after' at exactly 16:00:00, got %q", got)
	}
}

func TestIsMarketHours_ExactSessionClose(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	cal := AlpacaCalendarEntry{Date: "2026-05-02", Open: "09:30", Close: "16:00", SessionOpen: "0400", SessionClose: "2000"}
	now := time.Date(2026, 5, 2, 20, 0, 0, 0, loc)
	got := isMarketHours(now, cal)
	if got != "closed" {
		t.Errorf("expected 'closed' at exactly 20:00:00 (session end), got %q", got)
	}
}

func TestIsMarketHours_BeforeSessionOpen(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	cal := AlpacaCalendarEntry{Date: "2026-05-02", Open: "09:30", Close: "16:00", SessionOpen: "0400", SessionClose: "2000"}
	now := time.Date(2026, 5, 2, 3, 59, 0, 0, loc)
	got := isMarketHours(now, cal)
	if got != "closed" {
		t.Errorf("expected 'closed' before session open, got %q", got)
	}
}

type stubEarningsChecker struct {
	excluded map[string]bool
}

func (s *stubEarningsChecker) IsExcluded(ticker string, _ time.Time) bool {
	return s.excluded[ticker]
}

func TestUniverseFilter_NilChecker_DoesNotExclude(t *testing.T) {
	svc := NewPennyUniverseService("k", "", "", "", nil, nil)
	items := []fmpScreenerItem{
		{Symbol: "GOOD", CompanyName: "Good", MarketCap: 100_000_000, Price: 5.0, Volume: 100_000, ExchangeShortName: "NASDAQ"},
	}
	out, excluded := svc.filter(items)
	if len(out) != 1 || excluded != 0 {
		t.Errorf("nil checker: expected 1 included / 0 excluded, got %d / %d", len(out), excluded)
	}
}

func TestUniverseFilter_CheckerExcludesTicker(t *testing.T) {
	checker := &stubEarningsChecker{excluded: map[string]bool{"BAD": true}}
	svc := NewPennyUniverseService("k", "", "", "", checker, nil)
	items := []fmpScreenerItem{
		{Symbol: "GOOD", CompanyName: "Good", MarketCap: 100_000_000, Price: 5.0, Volume: 100_000, ExchangeShortName: "NASDAQ"},
		{Symbol: "BAD", CompanyName: "Bad", MarketCap: 100_000_000, Price: 5.0, Volume: 100_000, ExchangeShortName: "NASDAQ"},
	}
	out, excluded := svc.filter(items)
	if len(out) != 1 || out[0].Ticker != "GOOD" {
		t.Errorf("expected only GOOD to remain, got %+v", out)
	}
	if excluded != 1 {
		t.Errorf("expected excluded count 1, got %d", excluded)
	}
}

func TestUniverseFilter_CheckerExcludesNothing(t *testing.T) {
	checker := &stubEarningsChecker{excluded: map[string]bool{}}
	svc := NewPennyUniverseService("k", "", "", "", checker, nil)
	items := []fmpScreenerItem{
		{Symbol: "GOOD", CompanyName: "Good", MarketCap: 100_000_000, Price: 5.0, Volume: 100_000, ExchangeShortName: "NASDAQ"},
	}
	out, excluded := svc.filter(items)
	if len(out) != 1 || excluded != 0 {
		t.Errorf("expected 1 included / 0 excluded, got %d / %d", len(out), excluded)
	}
}
