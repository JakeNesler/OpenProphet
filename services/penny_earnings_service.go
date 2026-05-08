package services

import (
	"context"
	"net/http"
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
func (s *EarningsCalendarService) IsExcluded(ticker string, now time.Time) bool       { return false }
func (s *EarningsCalendarService) WaitForFirstRefresh(timeout time.Duration) bool     { return false }

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
