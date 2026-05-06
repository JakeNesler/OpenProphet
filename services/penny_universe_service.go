package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

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
	loc := nyLoc
	if loc == nil {
		loc = time.UTC
	}
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

	if open.Equal(calDate) || close_.Equal(calDate) || sessOpen.Equal(calDate) || sessClose.Equal(calDate) {
		return staticMarketPhase(now, loc)
	}

	if nowET.Before(sessOpen) || !nowET.Before(sessClose) {
		return "closed"
	}
	if nowET.Before(open) {
		return "pre"
	}
	if !nowET.Before(close_) {
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
	case total < 4*60 || total >= 20*60:
		return "closed"
	case total < 9*60+30:
		return "pre"
	case total >= 16*60:
		return "after"
	default:
		return "open"
	}
}

type fmpScreenerItem struct {
	Symbol            string  `json:"symbol"`
	CompanyName       string  `json:"companyName"`
	MarketCap         float64 `json:"marketCap"`
	Price             float64 `json:"price"`
	Volume            float64 `json:"volume"` // 30-day avg share volume from FMP
	ExchangeShortName string  `json:"exchangeShortName"`
}

// PennyUniverseService maintains a filtered universe of penny stocks.
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

// NewPennyUniverseService creates the service. Pass a custom httpClient for testing.
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

// Start runs the refresh loop until ctx is cancelled.
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

// GetUniverse returns a copy of the current universe.
func (s *PennyUniverseService) GetUniverse() []UniverseSymbol {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]UniverseSymbol, len(s.universe))
	copy(out, s.universe)
	return out
}

// GetTickers returns just the ticker symbols.
func (s *PennyUniverseService) GetTickers() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tickers := make([]string, len(s.universe))
	for i, u := range s.universe {
		tickers[i] = u.Ticker
	}
	return tickers
}

func (s *PennyUniverseService) refresh() {
	url := fmt.Sprintf(
		"%s/api/v3/stock-screener?marketCapMoreThan=50000000&marketCapLowerThan=500000000&priceMoreThan=2&priceLowerThan=10&exchange=NASDAQ,NYSE,AMEX&country=US&limit=500&apikey=%s",
		s.fmpBaseURL,
		s.fmpAPIKey,
	)
	resp, err := s.httpClient.Get(url)
	if err != nil {
		s.logger.WithError(err).Warn("PennyUniverseService: FMP request failed")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		s.logger.WithField("status", resp.StatusCode).Warn("PennyUniverseService: FMP returned non-200")
		return
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.logger.WithError(err).Warn("PennyUniverseService: failed to read FMP response")
		return
	}
	var items []fmpScreenerItem
	if err := json.Unmarshal(body, &items); err != nil {
		s.logger.WithError(err).Warn("PennyUniverseService: failed to parse FMP response")
		return
	}
	universe := s.filter(items)
	s.mu.Lock()
	s.universe = universe
	s.mu.Unlock()
	s.logger.WithField("count", len(universe)).Info("PennyUniverseService: universe refreshed")
}

var allowedExchanges = map[string]bool{
	"NASDAQ": true,
	"NYSE":   true,
	"AMEX":   true,
}

var nyLoc, _ = time.LoadLocation("America/New_York")

func (s *PennyUniverseService) filter(items []fmpScreenerItem) []UniverseSymbol {
	out := make([]UniverseSymbol, 0)
	for _, item := range items {
		if !allowedExchanges[item.ExchangeShortName] {
			continue
		}
		if item.Price < 2.0 || item.Price > 10.0 {
			continue
		}
		if item.MarketCap < 50_000_000 || item.MarketCap > 500_000_000 {
			continue
		}
		dollarVol := item.Volume * item.Price // approx: avg share volume × current price
		if dollarVol < 500_000 {
			continue
		}
		out = append(out, UniverseSymbol{
			Ticker:       item.Symbol,
			Name:         item.CompanyName,
			Exchange:     item.ExchangeShortName,
			Price:        item.Price,
			MarketCapM:   item.MarketCap / 1_000_000,
			AvgDollarVol: dollarVol,
		})
	}
	return out
}

func (s *PennyUniverseService) maybeRefreshCalendar(now time.Time) {
	s.mu.RLock()
	loc := nyLoc
	if loc == nil {
		loc = time.UTC
	}
	nowET := now.In(loc)
	calET := s.calDate.In(loc)
	sameDay := !s.calDate.IsZero() && calET.Year() == nowET.Year() &&
		calET.Month() == nowET.Month() && calET.Day() == nowET.Day()
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
