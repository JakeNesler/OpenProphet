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
