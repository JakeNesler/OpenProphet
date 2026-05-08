package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	earningsExclusionDays    = 3
	staleThreshold           = 36 * time.Hour
	staleWarnInterval        = 4 * time.Hour
	earningsFetchHorizonDays = 10
	calendarFetchHorizonDays = 14
	refreshCheckInterval     = 1 * time.Hour

	// FirstRefreshWaitTimeout is the maximum time cmd/bot/main.go waits for
	// the first earnings refresh before proceeding in fail-open mode.
	FirstRefreshWaitTimeout = 5 * time.Second
)

type earningsEntry struct {
	Ticker string
	Date   time.Time
	Time   string // "bmo" | "amc" | "" (other values normalized to "")
}

type EarningsCalendarService struct {
	httpClient        *http.Client
	fmpAPIKey         string
	fmpBaseURL        string
	alpacaAPIKey      string
	alpacaSecretKey   string
	alpacaBaseURL     string
	mu                sync.RWMutex
	entries           map[string]earningsEntry
	calendar          []AlpacaCalendarEntry
	lastRefreshETDate string
	lastRefresh       time.Time
	lastWarnTime      time.Time
	firstRefreshDone  chan struct{}
	firstRefreshOnce  sync.Once
	logger            *logrus.Logger
}

func NewEarningsCalendarService(
	fmpAPIKey, alpacaAPIKey, alpacaSecretKey, alpacaBaseURL string,
	httpClient *http.Client,
) *EarningsCalendarService {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	if alpacaBaseURL == "" {
		alpacaBaseURL = "https://paper-api.alpaca.markets"
	}
	logger := logrus.New()
	logger.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})
	return &EarningsCalendarService{
		httpClient:       httpClient,
		fmpAPIKey:        fmpAPIKey,
		fmpBaseURL:       "https://financialmodelingprep.com",
		alpacaAPIKey:     alpacaAPIKey,
		alpacaSecretKey:  alpacaSecretKey,
		alpacaBaseURL:    alpacaBaseURL,
		entries:          make(map[string]earningsEntry),
		firstRefreshDone: make(chan struct{}),
		logger:           logger,
	}
}

// Start, IsExcluded, WaitForFirstRefresh, refresh implemented in subsequent tasks.
func (s *EarningsCalendarService) Start(ctx context.Context)                          {}

// IsExcluded returns true if the ticker has an effective earnings date within the
// next earningsExclusionDays trading days. Fail-open semantics: returns false if
// the cache has never been populated or if required calendar data is missing.
func (s *EarningsCalendarService) IsExcluded(ticker string, now time.Time) bool {
	s.mu.RLock()
	entry, hasEntry := s.entries[ticker]
	calendar := s.calendar
	lastRefresh := s.lastRefresh
	s.mu.RUnlock()

	if lastRefresh.IsZero() {
		return false
	}

	if time.Since(lastRefresh) > staleThreshold {
		if s.maybeWarn(now) {
			s.logger.Warnf("earnings calendar is stale (last refresh > %s ago) — still applying cached exclusions", staleThreshold)
		}
	}

	if !hasEntry {
		return false
	}

	if len(calendar) == 0 {
		if s.maybeWarn(now) {
			s.logger.Warn("earnings calendar trading-day cache empty — exclusion temporarily disabled")
		}
		return false
	}

	loc := nyLoc
	if loc == nil {
		loc = time.UTC
	}
	nowET := now.In(loc)
	nowDate := time.Date(nowET.Year(), nowET.Month(), nowET.Day(), 0, 0, 0, 0, loc)
	effective := s.effectiveDate(entry, calendar)
	distance := tradingDayDistance(nowDate, effective, calendar)
	if distance < 0 {
		return false
	}
	return distance <= earningsExclusionDays
}
// WaitForFirstRefresh blocks until the first successful refresh has signaled
// firstRefreshDone, or the timeout elapses. Returns true if the signal arrived first.
func (s *EarningsCalendarService) WaitForFirstRefresh(timeout time.Duration) bool {
	select {
	case <-s.firstRefreshDone:
		return true
	case <-time.After(timeout):
		return false
	}
}

// tradingDayDistance returns the number of trading days from nowDate (exclusive)
// to effective (inclusive). Both arguments are compared by date in their stored
// location. Returns -1 if effective is strictly before nowDate.
func tradingDayDistance(nowDate, effective time.Time, calendar []AlpacaCalendarEntry) int {
	nowYMD := nowDate.Format("2006-01-02")
	effYMD := effective.Format("2006-01-02")
	if effYMD < nowYMD {
		return -1
	}
	if effYMD == nowYMD {
		return 0
	}
	count := 0
	for _, e := range calendar {
		if e.Date > nowYMD && e.Date <= effYMD {
			count++
		}
	}
	return count
}

// effectiveDate computes the trading day on which the post-earnings gap will manifest.
// For BMO/empty time: returns the first trading day on or after entry.Date.
// For AMC: returns the first trading day strictly after entry.Date.
// Returns entry.Date unchanged if calendar is empty or no qualifying day exists.
func (s *EarningsCalendarService) effectiveDate(entry earningsEntry, calendar []AlpacaCalendarEntry) time.Time {
	if len(calendar) == 0 {
		return entry.Date
	}
	entryYMD := entry.Date.Format("2006-01-02")
	loc := nyLoc
	if loc == nil {
		loc = time.UTC
	}
	for _, c := range calendar {
		if entry.Time == "amc" {
			if c.Date > entryYMD {
				if d, err := time.ParseInLocation("2006-01-02", c.Date, loc); err == nil {
					return d
				}
			}
		} else {
			if c.Date >= entryYMD {
				if d, err := time.ParseInLocation("2006-01-02", c.Date, loc); err == nil {
					return d
				}
			}
		}
	}
	return entry.Date
}

// maybeWarn returns true if a warning should be emitted right now (caller emits the log message)
// and updates lastWarnTime under write-lock. Returns false if the previous warn was within
// staleWarnInterval. The shared throttle covers all warn types (stale, empty calendar, etc.)
// to keep logs from flooding when multiple conditions co-occur.
func (s *EarningsCalendarService) maybeWarn(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.lastWarnTime.IsZero() && now.Sub(s.lastWarnTime) < staleWarnInterval {
		return false
	}
	s.lastWarnTime = now
	return true
}

type fmpEarningsItem struct {
	Symbol string `json:"symbol"`
	Date   string `json:"date"`
	Time   string `json:"time"`
}

// refreshEarnings fetches the FMP earnings calendar and replaces the entries map
// with the parsed result. The HTTP call and parse run outside the mutex; the lock
// is held only for the final swap. Returns the count of parsed and skipped entries
// for the caller to log; on failure returns an error and preserves the prior cache.
func (s *EarningsCalendarService) refreshEarnings(now time.Time) (parsed, skipped int, err error) {
	loc := nyLoc
	if loc == nil {
		loc = time.UTC
	}
	nowET := now.In(loc)
	from := nowET.Format("2006-01-02")
	to := nowET.AddDate(0, 0, earningsFetchHorizonDays).Format("2006-01-02")
	url := fmt.Sprintf("%s/api/v3/earning_calendar?from=%s&to=%s&apikey=%s",
		s.fmpBaseURL, from, to, s.fmpAPIKey)

	resp, err := s.httpClient.Get(url)
	if err != nil {
		s.logger.WithError(err).Warn("EarningsCalendarService: FMP earnings fetch failed")
		return 0, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		s.logger.WithField("status", resp.StatusCode).Warn("EarningsCalendarService: FMP earnings non-200")
		return 0, 0, fmt.Errorf("fmp earnings returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.logger.WithError(err).Warn("EarningsCalendarService: failed to read FMP earnings body")
		return 0, 0, err
	}
	var items []fmpEarningsItem
	if err := json.Unmarshal(body, &items); err != nil {
		s.logger.WithError(err).Warn("EarningsCalendarService: failed to parse FMP earnings JSON")
		return 0, 0, err
	}

	todayYMD := from
	parsedMap := make(map[string]earningsEntry)
	for _, it := range items {
		d, perr := time.ParseInLocation("2006-01-02", it.Date, loc)
		if perr != nil {
			skipped++
			continue
		}
		if it.Date < todayYMD {
			continue
		}
		t := strings.ToLower(strings.TrimSpace(it.Time))
		if t != "bmo" && t != "amc" {
			t = ""
		}
		entry := earningsEntry{Ticker: it.Symbol, Date: d, Time: t}
		if existing, ok := parsedMap[it.Symbol]; !ok || d.Before(existing.Date) {
			parsedMap[it.Symbol] = entry
		}
	}

	s.mu.Lock()
	s.entries = parsedMap
	s.mu.Unlock()

	return len(parsedMap), skipped, nil
}
