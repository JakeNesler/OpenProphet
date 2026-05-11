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

// Start runs an initial refresh, then wakes every refreshCheckInterval and runs
// another refresh when the ET calendar day has rolled over. Exits on ctx cancellation.
func (s *EarningsCalendarService) Start(ctx context.Context) {
	loc := nyLoc
	if loc == nil {
		loc = time.UTC
	}
	s.refresh(time.Now())
	ticker := time.NewTicker(refreshCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			todayET := now.In(loc).Format("2006-01-02")
			s.mu.RLock()
			last := s.lastRefreshETDate
			s.mu.RUnlock()
			if shouldRefreshNow(todayET, last) {
				s.refresh(now)
			}
		}
	}
}

// shouldRefreshNow returns true if a refresh should fire because the ET calendar
// day has rolled over since the last successful refresh (or one has never run).
func shouldRefreshNow(todayETDate, lastRefreshETDate string) bool {
	return lastRefreshETDate != todayETDate
}

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
	url := fmt.Sprintf("%s/stable/earnings-calendar?from=%s&to=%s&apikey=%s",
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

// refreshCalendar fetches the multi-day Alpaca trading-day calendar. Returns the
// first and last dates in the fetched calendar (for logging) on success.
func (s *EarningsCalendarService) refreshCalendar(now time.Time) (firstDate, lastDate string, err error) {
	loc := nyLoc
	if loc == nil {
		loc = time.UTC
	}
	nowET := now.In(loc)
	start := nowET.Format("2006-01-02")
	end := nowET.AddDate(0, 0, calendarFetchHorizonDays).Format("2006-01-02")
	url := fmt.Sprintf("%s/v2/calendar?start=%s&end=%s", s.alpacaBaseURL, start, end)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		s.logger.WithError(err).Warn("EarningsCalendarService: Alpaca calendar request build failed")
		return "", "", err
	}
	req.Header.Set("APCA-API-KEY-ID", s.alpacaAPIKey)
	req.Header.Set("APCA-API-SECRET-KEY", s.alpacaSecretKey)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		s.logger.WithError(err).Warn("EarningsCalendarService: Alpaca calendar fetch failed")
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		s.logger.WithField("status", resp.StatusCode).Warn("EarningsCalendarService: Alpaca calendar non-200")
		return "", "", fmt.Errorf("alpaca calendar returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}
	var entries []AlpacaCalendarEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		s.logger.WithError(err).Warn("EarningsCalendarService: failed to parse Alpaca calendar JSON")
		return "", "", err
	}
	if len(entries) == 0 {
		s.logger.Warn("EarningsCalendarService: Alpaca calendar returned 0 entries")
		return "", "", fmt.Errorf("alpaca calendar empty")
	}

	s.mu.Lock()
	s.calendar = entries
	s.mu.Unlock()
	return entries[0].Date, entries[len(entries)-1].Date, nil
}

// refresh runs both fetches and updates lastRefresh / lastRefreshETDate.
// Signals firstRefreshDone only when both fetches succeeded. FMP failure aborts;
// Alpaca failure leaves prior calendar in place but skips the firstRefreshDone signal.
// On success, emits a single combined info log.
func (s *EarningsCalendarService) refresh(now time.Time) {
	parsed, skipped, err := s.refreshEarnings(now)
	if err != nil {
		return
	}
	calFrom, calTo, calErr := s.refreshCalendar(now)

	loc := nyLoc
	if loc == nil {
		loc = time.UTC
	}
	todayET := now.In(loc).Format("2006-01-02")

	s.mu.Lock()
	s.lastRefresh = now
	s.lastRefreshETDate = todayET
	s.mu.Unlock()

	fields := logrus.Fields{
		"parsed":  parsed,
		"skipped": skipped,
	}
	if calErr == nil {
		fields["calendar_from"] = calFrom
		fields["calendar_to"] = calTo
		s.firstRefreshOnce.Do(func() { close(s.firstRefreshDone) })
	}
	s.logger.WithFields(fields).Info("EarningsCalendarService: refresh complete")
}
