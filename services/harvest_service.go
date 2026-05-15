package services

import (
	"context"
	"fmt"
	"math"
	"time"

	"prophet-trader/models"
)

// harvestStateStore is the storage subset used by HarvestService.
type harvestStateStore interface {
	ListOpenHarvestCondors() ([]*models.DBHarvestCondor, error)
	GetHarvestClosedPnL(start, end time.Time) (float64, error)
	SaveHarvestCondor(c *models.DBHarvestCondor) error
	UpdateHarvestCondor(condorID string, updates map[string]interface{}) error
	GetHarvestCondorByID(condorID string) (*models.DBHarvestCondor, error)
}

// fomc2026Dates holds scheduled FOMC announcement times (UTC).
// Source: https://www.federalreserve.gov/monetarypolicy/fomccalendars.htm
// Verify all dates quarterly. Retrieved: 2026-05-01.
var fomc2026Dates = []time.Time{
	time.Date(2026, 1, 28, 19, 0, 0, 0, time.UTC),  // Jan 28, 2026 2pm ET
	time.Date(2026, 3, 18, 18, 0, 0, 0, time.UTC),  // Mar 18, 2026
	time.Date(2026, 5, 6, 18, 0, 0, 0, time.UTC),   // May 6, 2026
	time.Date(2026, 6, 17, 18, 0, 0, 0, time.UTC),  // Jun 17, 2026
	time.Date(2026, 7, 29, 18, 0, 0, 0, time.UTC),  // Jul 29, 2026
	time.Date(2026, 9, 16, 18, 0, 0, 0, time.UTC),  // Sep 16, 2026
	time.Date(2026, 11, 4, 19, 0, 0, 0, time.UTC),  // Nov 4, 2026
	time.Date(2026, 12, 16, 19, 0, 0, 0, time.UTC), // Dec 16, 2026
}

// HarvestStateResponse is what the API returns for GET /harvest/state.
type HarvestStateResponse struct {
	OpenCondors            int                       `json:"open_condors"`
	OpenCondorsDetail      []*models.DBHarvestCondor `json:"open_condors_detail"`
	DeployedBuyingPowerPct float64                   `json:"deployed_buying_power_pct"`
	Trailing30dPnL         float64                   `json:"trailing_30d_pnl"`
	Trailing30dPnLPct      float64                   `json:"trailing_30d_pnl_pct"`
	CircuitBreakerActive   bool                      `json:"circuit_breaker_active"`
	PortfolioValue         float64                   `json:"portfolio_value"`
}

// FOMCStatusResponse is what the API returns for GET /harvest/fomc.
type FOMCStatusResponse struct {
	IsBlackout     bool       `json:"is_blackout"`
	NextFOMCDate   time.Time  `json:"next_fomc_date"`
	HoursUntilFOMC float64    `json:"hours_until_fomc"`
	BlackoutUntil  *time.Time `json:"blackout_until,omitempty"`
}

// MonthlyExpiration is what the API returns for GET /harvest/expirations/:symbol.
type MonthlyExpiration struct {
	Symbol         string    `json:"symbol"`
	ExpirationDate time.Time `json:"expiration_date"`
	DTE            int       `json:"dte"`
}

// DefaultShortPutDeltaProxy is the multiplier applied to short-put notional
// when contributing to the cross-agent INDEX_BETA bucket via
// OptionsExposureProvider. Intentionally heavier than the ~0.16 raw delta of
// typical 16-delta short put strikes — the elevated weighting is a
// conservative beta proxy that accounts for gamma + vol expansion under
// stress, when a quiet short put can rapidly grow into meaningful index
// exposure. Configurable via SetShortPutDeltaProxy.
const DefaultShortPutDeltaProxy = 0.30

// HarvestService provides core harvest logic: state, FOMC, expirations.
type HarvestService struct {
	store              harvestStateStore
	fomcDates          []time.Time
	shortPutDeltaProxy float64
}

// NewHarvestService creates a new HarvestService.
func NewHarvestService(store harvestStateStore) *HarvestService {
	return &HarvestService{
		store:              store,
		fomcDates:          fomc2026Dates,
		shortPutDeltaProxy: DefaultShortPutDeltaProxy,
	}
}

// SetShortPutDeltaProxy overrides the default beta proxy used by
// BucketExposureDollars. Intended for calibration after observation.
func (s *HarvestService) SetShortPutDeltaProxy(p float64) {
	s.shortPutDeltaProxy = p
}

// BucketExposureDollars implements OptionsExposureProvider. Each open condor
// on an INDEX_BETA underlying (SPY/QQQ/IWM/DIA/VTI) contributes
//
//	ShortPutStrike × Contracts × 100 × shortPutDeltaProxy
//
// dollars to the INDEX_BETA bucket. Non-index underlyings are skipped — a
// configuration error worth surfacing later rather than silently
// misattributing equity exposure to the index bucket.
//
// Returns nil when there are no open condors, no index condors, or the store
// call fails. Soft-failing on store error is deliberate: TradeGuard already
// fails closed on its own account fetch errors during sector-cap checks, so
// a transient DB hiccup here would otherwise compound into double-blocking.
func (s *HarvestService) BucketExposureDollars() map[SectorBucket]float64 {
	condors, err := s.store.ListOpenHarvestCondors()
	if err != nil || len(condors) == 0 {
		return nil
	}
	var indexBeta float64
	for _, c := range condors {
		if !isIndexBetaUnderlying(c.Underlying) {
			continue
		}
		notional := c.ShortPutStrike * float64(c.Contracts) * 100.0
		indexBeta += notional * s.shortPutDeltaProxy
	}
	if indexBeta == 0 {
		return nil
	}
	return map[SectorBucket]float64{"INDEX_BETA": indexBeta}
}

// isIndexBetaUnderlying reports whether the given symbol is one of Harvest's
// supported index-ETF underlyings. Kept local to HarvestService rather than
// pulling from TradeGuard's bucketFor so the two services don't develop a
// circular dependency on bucket-membership semantics.
func isIndexBetaUnderlying(symbol string) bool {
	switch symbol {
	case "SPY", "QQQ", "IWM", "DIA", "VTI":
		return true
	}
	return false
}

// GetState returns the current Harvest portfolio state.
func (s *HarvestService) GetState(portfolioValue float64) (*HarvestStateResponse, error) {
	condors, err := s.store.ListOpenHarvestCondors()
	if err != nil {
		return nil, fmt.Errorf("fetching open condors: %w", err)
	}
	deployedPct := calcDeployedBuyingPowerPct(condors, portfolioValue)

	now := time.Now().UTC()
	tradingDayStart := now.AddDate(0, 0, -44) // ~30 trading days back (44 calendar days)
	pnl30d, err := s.store.GetHarvestClosedPnL(tradingDayStart, now)
	if err != nil {
		return nil, fmt.Errorf("fetching 30d P&L: %w", err)
	}
	pnl30dPct := 0.0
	if portfolioValue > 0 {
		pnl30dPct = (pnl30d / portfolioValue) * 100.0
	}
	circuitBreaker := portfolioValue > 0 && pnl30dPct < -5.0

	return &HarvestStateResponse{
		OpenCondors:            len(condors),
		OpenCondorsDetail:      condors,
		DeployedBuyingPowerPct: deployedPct,
		Trailing30dPnL:         pnl30d,
		Trailing30dPnLPct:      pnl30dPct,
		CircuitBreakerActive:   circuitBreaker,
		PortfolioValue:         portfolioValue,
	}, nil
}

// GetFOMCStatus returns current FOMC blackout status.
func (s *HarvestService) GetFOMCStatus() *FOMCStatusResponse {
	now := time.Now().UTC()
	resp := &FOMCStatusResponse{}

	for _, fomc := range s.fomcDates {
		if !fomc.Before(now) {
			resp.NextFOMCDate = fomc
			resp.HoursUntilFOMC = fomc.Sub(now).Hours()
			break
		}
	}

	resp.IsBlackout = isFOMCBlackout(now, s.fomcDates)
	if resp.IsBlackout {
		until := resp.NextFOMCDate.Add(1 * time.Millisecond)
		resp.BlackoutUntil = &until
	}
	return resp
}

// GetNextMonthlyExpiration finds the next monthly expiration in [minDTE, maxDTE].
func (s *HarvestService) GetNextMonthlyExpiration(symbol string, minDTE, maxDTE int) (*MonthlyExpiration, error) {
	now := time.Now().UTC()
	exp, dte, ok := nextMonthlyExpiration(now, minDTE, maxDTE)
	if !ok {
		return nil, fmt.Errorf("no monthly expiration found in [%d,%d] DTE band for %s", minDTE, maxDTE, symbol)
	}
	return &MonthlyExpiration{Symbol: symbol, ExpirationDate: exp, DTE: dte}, nil
}

// isFOMCBlackout returns true if now is within 24h before any FOMC date.
func isFOMCBlackout(now time.Time, dates []time.Time) bool {
	for _, fomc := range dates {
		hoursUntil := fomc.Sub(now).Hours()
		if hoursUntil >= 0 && hoursUntil <= 24 {
			return true
		}
	}
	return false
}

// thirdFriday returns the third Friday of the given month and year (UTC midnight).
func thirdFriday(year int, month time.Month) time.Time {
	first := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	daysUntilFriday := (int(time.Friday) - int(first.Weekday()) + 7) % 7
	firstFriday := first.AddDate(0, 0, daysUntilFriday)
	return firstFriday.AddDate(0, 0, 14) // + 2 weeks = 3rd Friday
}

// nextMonthlyExpiration finds the nearest third-Friday expiration with DTE in [minDTE, maxDTE].
// Checks up to 6 upcoming monthly expirations.
func nextMonthlyExpiration(now time.Time, minDTE, maxDTE int) (time.Time, int, bool) {
	for i := 0; i < 6; i++ {
		year, month, _ := now.AddDate(0, i, 0).Date()
		exp := thirdFriday(year, month)
		dte := int(math.Round(exp.Sub(now).Hours() / 24))
		if dte >= minDTE && dte <= maxDTE {
			return exp, dte, true
		}
	}
	return time.Time{}, 0, false
}

// calcDeployedBuyingPowerPct sums max losses across open condors as pct of portfolio.
func calcDeployedBuyingPowerPct(condors []*models.DBHarvestCondor, portfolioValue float64) float64 {
	if portfolioValue <= 0 {
		return 0
	}
	var total float64
	for _, c := range condors {
		total += c.MaxLoss
	}
	return (total / portfolioValue) * 100.0
}

// MultiLegOrder represents a 4-leg iron condor order for Alpaca's mleg API.
type MultiLegOrder struct {
	Underlying  string
	Legs        []MultiLegOrderLeg
	Contracts   int
	LimitPrice  float64 // net credit limit (positive = we receive credit)
	TimeInForce string  // "day"
	// Strategy identifies the agent that owns this combo. Encoded into
	// Alpaca's client_order_id as "{strategy}:{uuid}" so the tag survives
	// fills and reconciliation. Empty string is a no-op (legacy behavior).
	Strategy    string
}

// MultiLegOrderLeg is one leg of the combo.
type MultiLegOrderLeg struct {
	Symbol         string
	Side           string // "buy" | "sell"
	PositionIntent string // "buy_to_open" | "sell_to_open" | "buy_to_close" | "sell_to_close"
}

// PlaceMultiLegOrderFn is a function that places a multi-leg order.
// Injectable for testing without a real broker connection.
type PlaceMultiLegOrderFn func(ctx context.Context, order MultiLegOrder) (string, error)
