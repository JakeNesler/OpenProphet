package services

import (
	"context"
	"fmt"
	"prophet-trader/interfaces"
	"sync"

	"github.com/sirupsen/logrus"
)

// AgentSource identifies which agent is placing a trade.
type AgentSource string

const (
	AgentMain    AgentSource = "main"
	AgentPenny   AgentSource = "penny"
	AgentHarvest AgentSource = "harvest"
)

const agentTagPrefix = "agent:"

// AgentTag returns the managed-position tag string for an agent.
func AgentTag(agent AgentSource) string {
	return agentTagPrefix + string(agent)
}

// TradeGuardConfig holds configurable limits for the guard.
type TradeGuardConfig struct {
	// PennyMaxCapitalPct is the maximum fraction of portfolio value the penny
	// agent may hold in aggregate (e.g. 0.20 = 20%).
	PennyMaxCapitalPct float64 `json:"penny_max_capital_pct"`

	// PennyMaxPositionDollars is the maximum dollar size of a single penny trade.
	PennyMaxPositionDollars float64 `json:"penny_max_position_dollars"`

	// MaxDailyLossPct is the daily loss circuit breaker as a positive percentage
	// of previous session equity (e.g. 5.0 = block new entries when intraday
	// loss reaches -5%). Zero or negative disables the check.
	MaxDailyLossPct float64 `json:"max_daily_loss_pct"`

	// EnableSectorAggregation toggles the cross-agent sector-bucket cap. Flag-gated
	// so the guard can be deployed in observation-mode (Status() reports buckets)
	// before enforcement begins.
	EnableSectorAggregation bool `json:"enable_sector_aggregation"`

	// SectorMaxExposurePct caps the total dollar exposure to each sector bucket
	// as a fraction of portfolio value (e.g. {"TECH": 0.20}). Buckets not listed
	// here fall back to DefaultSectorMaxPct.
	SectorMaxExposurePct map[string]float64 `json:"sector_max_exposure_pct"`

	// DefaultSectorMaxPct is the fallback cap for buckets without an explicit
	// entry in SectorMaxExposurePct. Zero disables the fallback (those buckets
	// are uncapped).
	DefaultSectorMaxPct float64 `json:"default_sector_max_pct"`
}

// SectorBucket categorizes a symbol's primary factor exposure for cross-agent
// concentration limits.
type SectorBucket string

const SectorBucketOther SectorBucket = "OTHER"

// etfToBucket consolidates sector / index ETFs into coarse exposure buckets.
// A symbol that maps to one of these ETFs via sectorETFMap inherits the bucket;
// a symbol that IS one of these ETFs uses its direct bucket.
var etfToBucket = map[string]SectorBucket{
	"XLK":  "TECH",
	"SMH":  "TECH",
	"SOXX": "TECH",
	"XLF":  "FINANCIALS",
	"XLE":  "ENERGY",
	"XLV":  "HEALTHCARE",
	"XLY":  "CONSUMER_DISCRETIONARY",
	"XLP":  "STAPLES",
	"XLI":  "INDUSTRIALS",
	"XLU":  "UTILITIES",
	"XLB":  "MATERIALS",
	"XLRE": "REAL_ESTATE",
	"XLC":  "COMMUNICATIONS",
	"SPY":  "INDEX_BETA",
	"QQQ":  "INDEX_BETA",
	"IWM":  "INDEX_BETA",
	"VTI":  "INDEX_BETA",
	"DIA":  "INDEX_BETA",
}

// positionLister is the subset of PositionManager needed by the guard.
type positionLister interface {
	ListManagedPositions(status string) []*ManagedPosition
}

// OptionsExposureProvider supplies delta-adjusted options exposure by sector
// bucket so that options-only services (e.g. Harvest) can contribute to the
// cross-agent sector concentration limit without their positions appearing
// in ManagedPositions. Implementations return notional × effective delta —
// a short SPY put at $50K notional with 0.30 delta proxy contributes
// {INDEX_BETA: 15000}. Returning nil is equivalent to zero exposure.
type OptionsExposureProvider interface {
	BucketExposureDollars() map[SectorBucket]float64
}

// TradeGuard enforces cross-agent trade rules:
//   - Symbol non-overlap: a symbol held by one agent cannot be traded by the other.
//   - Daily-loss circuit: intraday equity drop ≥ MaxDailyLossPct blocks new buys.
//   - Penny per-position cap: each penny buy is capped at PennyMaxPositionDollars.
//   - Penny portfolio cap: total penny exposure ≤ PennyMaxCapitalPct × portfolio value.
//   - Sector concentration cap (flag-gated): aggregate dollar exposure to each
//     SectorBucket — summed across all agents' managed positions plus any
//     registered OptionsExposureProvider — is capped per bucket.
//
// Managed positions are the authoritative ownership source; raw buy/sell orders are
// tracked in-memory and cleared on sell (lost across restarts).
type TradeGuard struct {
	positions      positionLister
	tradingService interfaces.TradingService
	cfg            TradeGuardConfig

	// optionsProvider is optional. When non-nil, its per-bucket exposure is
	// added to the sum computed from ManagedPositions during sector-cap checks.
	optionsProvider OptionsExposureProvider

	// rawSymbols tracks symbols acquired via raw (non-managed) buy orders.
	rawSymbols map[AgentSource]map[string]struct{}
	mu         sync.RWMutex

	logger *logrus.Logger
}

// NewTradeGuard creates a guard. tradingService may be nil in tests (capital cap is skipped).
func NewTradeGuard(positions positionLister, ts interfaces.TradingService, cfg TradeGuardConfig) *TradeGuard {
	logger := logrus.New()
	logger.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})

	return &TradeGuard{
		positions:      positions,
		tradingService: ts,
		cfg:            cfg,
		rawSymbols: map[AgentSource]map[string]struct{}{
			AgentMain:    {},
			AgentPenny:   {},
			AgentHarvest: {},
		},
		logger: logger,
	}
}

// CheckBuy validates a buy order against all guard rules.
// allocationDollars is the intended spend; pass 0 when the dollar value is unknown
// (capital-cap check is skipped).
//
// At most one tradingService.GetAccount() call is made per invocation: the result
// is fetched lazily (only when daily-loss or capital-cap checks need it) and shared
// between the two checks for penny buys.
func (g *TradeGuard) CheckBuy(ctx context.Context, agent AgentSource, symbol string, allocationDollars float64) error {
	if agent == "" {
		agent = AgentMain
	}
	opponent := g.opponentOf(agent)

	// Lazily fetch account at most once per CheckBuy. Both the value and any
	// fetch error are cached so each downstream helper can apply its own policy:
	//   - checkDailyLoss treats fetch errors as "data missing, fail-open"
	//   - checkPennyCapCap treats fetch errors as fail-closed (preserves prior behavior)
	var acct *interfaces.Account
	var acctErr error
	var acctFetched bool
	getAcct := func() (*interfaces.Account, error) {
		if acctFetched {
			return acct, acctErr
		}
		acctFetched = true
		if g.tradingService == nil {
			return nil, nil
		}
		acct, acctErr = g.tradingService.GetAccount(ctx)
		return acct, acctErr
	}

	// Daily-loss circuit breaker applies to BOTH agents.
	dailyAcct, _ := getAcct()
	if err := g.checkDailyLoss(dailyAcct); err != nil {
		return err
	}

	if g.agentOwnsSymbol(opponent, symbol) {
		return fmt.Errorf("guard: %s agent holds %s — %s agent cannot open a position in the same symbol", opponent, symbol, agent)
	}

	if g.cfg.EnableSectorAggregation && allocationDollars > 0 {
		sectorAcct, sectorErr := getAcct()
		if err := g.checkSectorCap(sectorAcct, sectorErr, symbol, allocationDollars); err != nil {
			return err
		}
	}

	if agent == AgentPenny {
		if allocationDollars > 0 && allocationDollars > g.cfg.PennyMaxPositionDollars {
			return fmt.Errorf("guard: penny position $%.2f exceeds per-position cap of $%.2f", allocationDollars, g.cfg.PennyMaxPositionDollars)
		}
		if allocationDollars > 0 {
			capAcct, capErr := getAcct()
			if err := g.checkPennyCapCap(capAcct, capErr, allocationDollars); err != nil {
				return err
			}
		}
	}

	return nil
}

// CheckSell validates a sell order. An agent may not sell a symbol owned by the other agent.
func (g *TradeGuard) CheckSell(_ context.Context, agent AgentSource, symbol string) error {
	if agent == "" {
		agent = AgentMain
	}
	opponent := g.opponentOf(agent)

	if g.agentOwnsSymbol(opponent, symbol) {
		return fmt.Errorf("guard: %s agent holds %s — %s agent cannot sell it", opponent, symbol, agent)
	}

	return nil
}

// SetOptionsExposureProvider registers a provider for options-based exposure
// (e.g. Harvest's short-put book). Pass nil to clear. Safe to call at any time;
// the provider is read only during CheckBuy and Status().
func (g *TradeGuard) SetOptionsExposureProvider(p OptionsExposureProvider) {
	g.mu.Lock()
	g.optionsProvider = p
	g.mu.Unlock()
}

// RecordRawBuy records that an agent acquired a symbol via a raw (non-managed) order.
func (g *TradeGuard) RecordRawBuy(agent AgentSource, symbol string) {
	if agent == "" {
		agent = AgentMain
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rawSymbols[agent][symbol] = struct{}{}
}

// RecordRawSell removes the in-memory ownership record when an agent exits a raw position.
func (g *TradeGuard) RecordRawSell(agent AgentSource, symbol string) {
	if agent == "" {
		agent = AgentMain
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.rawSymbols[agent], symbol)
}

// GuardStatus is the payload returned by the status endpoint.
type GuardStatus struct {
	Config          TradeGuardConfig `json:"config"`
	MainSymbols     []string         `json:"main_symbols"`
	PennySymbols    []string         `json:"penny_symbols"`
	PennyExposure   float64          `json:"penny_exposure_dollars"`
	PennyCapitalMax float64          `json:"penny_capital_max_dollars"`

	// SectorExposure reports per-bucket dollar exposure (managed positions +
	// any registered OptionsExposureProvider). Useful for observation-mode
	// before EnableSectorAggregation is turned on.
	SectorExposure map[string]float64 `json:"sector_exposure_dollars,omitempty"`

	// SectorMaxByBucket reports the configured dollar cap for each bucket
	// (portfolio value × per-bucket pct, or × DefaultSectorMaxPct). Only
	// populated for buckets with an active cap.
	SectorMaxByBucket map[string]float64 `json:"sector_max_by_bucket_dollars,omitempty"`
}

// Status returns a snapshot of current guard state.
func (g *TradeGuard) Status(ctx context.Context) GuardStatus {
	mainSet := g.symbolsFor(AgentMain)
	pennySet := g.symbolsFor(AgentPenny)

	mainList := setToSlice(mainSet)
	pennyList := setToSlice(pennySet)

	exposure := g.currentPennyExposure()
	portfolioValue := 0.0
	if g.tradingService != nil {
		if acct, err := g.tradingService.GetAccount(ctx); err == nil {
			portfolioValue = acct.PortfolioValue
		}
	}
	maxDollars := portfolioValue * g.cfg.PennyMaxCapitalPct

	sectorExposure := make(map[string]float64)
	for bucket, dollars := range g.currentSectorExposure() {
		sectorExposure[string(bucket)] = dollars
	}
	sectorMax := g.sectorMaxByBucket(portfolioValue, sectorExposure)

	return GuardStatus{
		Config:            g.cfg,
		MainSymbols:       mainList,
		PennySymbols:      pennyList,
		PennyExposure:     exposure,
		PennyCapitalMax:   maxDollars,
		SectorExposure:    sectorExposure,
		SectorMaxByBucket: sectorMax,
	}
}

// sectorMaxByBucket returns the dollar cap for each bucket that either has
// observed exposure or an explicit per-bucket override. Buckets with neither
// are omitted to keep the payload compact.
func (g *TradeGuard) sectorMaxByBucket(portfolioValue float64, exposure map[string]float64) map[string]float64 {
	out := make(map[string]float64)
	if portfolioValue <= 0 {
		return out
	}
	for bucket := range exposure {
		if cap, ok := g.sectorCapFor(SectorBucket(bucket), portfolioValue); ok {
			out[bucket] = cap
		}
	}
	for bucket, pct := range g.cfg.SectorMaxExposurePct {
		if _, alreadySet := out[bucket]; alreadySet || pct <= 0 {
			continue
		}
		out[bucket] = portfolioValue * pct
	}
	return out
}

// --- internal helpers ---

func (g *TradeGuard) agentOwnsSymbol(agent AgentSource, symbol string) bool {
	owned := g.symbolsFor(agent)
	_, found := owned[symbol]
	return found
}

func (g *TradeGuard) symbolsFor(agent AgentSource) map[string]struct{} {
	result := make(map[string]struct{})

	if g.positions != nil {
		for _, p := range g.positions.ListManagedPositions("") {
			if isActivePosition(p) && positionBelongsTo(p, agent) {
				result[p.Symbol] = struct{}{}
			}
		}
	}

	g.mu.RLock()
	for sym := range g.rawSymbols[agent] {
		result[sym] = struct{}{}
	}
	g.mu.RUnlock()

	return result
}

// checkDailyLoss returns an error if intraday equity is down beyond MaxDailyLossPct.
// Disabled when MaxDailyLossPct <= 0, when acct is nil (no data available),
// or when LastEquity/PortfolioValue is zero (fail-open to avoid bricking new accounts).
// The caller is responsible for fetching the account; passing nil makes this a no-op.
func (g *TradeGuard) checkDailyLoss(acct *interfaces.Account) error {
	if g.cfg.MaxDailyLossPct <= 0 || acct == nil {
		return nil
	}
	if acct.LastEquity <= 0 || acct.PortfolioValue <= 0 {
		return nil
	}
	lossPct := (acct.LastEquity - acct.PortfolioValue) / acct.LastEquity * 100
	if lossPct >= g.cfg.MaxDailyLossPct {
		return fmt.Errorf(
			"guard: daily loss circuit breaker — down %.2f%% from previous close ($%.2f → $%.2f), exceeds %.2f%% limit",
			lossPct, acct.LastEquity, acct.PortfolioValue, g.cfg.MaxDailyLossPct,
		)
	}
	return nil
}

// checkPennyCapCap enforces the aggregate penny exposure cap.
//   - acct == nil && acctErr == nil: trading service unavailable (e.g. tests) — skip check (no-op).
//   - acct == nil && acctErr != nil: fetch failed — fail-closed, return wrapped error.
//   - acct != nil: run the cap check normally.
//
// The fail-closed behavior on fetch errors is intentional and matches the original
// pre-refactor semantics: a flaky API call should NOT silently let a penny buy
// bypass the capital cap.
func (g *TradeGuard) checkPennyCapCap(acct *interfaces.Account, acctErr error, additionalDollars float64) error {
	if acctErr != nil {
		return fmt.Errorf("guard: failed to fetch account for capital check: %w", acctErr)
	}
	if acct == nil {
		return nil
	}

	exposure := g.currentPennyExposure()
	maxDollars := acct.PortfolioValue * g.cfg.PennyMaxCapitalPct

	if exposure+additionalDollars > maxDollars {
		return fmt.Errorf(
			"guard: penny capital cap — current $%.2f + new $%.2f exceeds %.0f%% cap ($%.2f of $%.2f portfolio)",
			exposure, additionalDollars,
			g.cfg.PennyMaxCapitalPct*100, maxDollars, acct.PortfolioValue,
		)
	}
	return nil
}

// bucketFor returns the sector bucket for a symbol. Lookup order:
//  1. etfToBucket directly (the symbol IS a sector/index ETF).
//  2. sectorETFMap → etfToBucket (the symbol is an equity mapped to an ETF).
//  3. SectorBucketOther otherwise.
func (g *TradeGuard) bucketFor(symbol string) SectorBucket {
	if b, ok := etfToBucket[symbol]; ok {
		return b
	}
	if etf, ok := sectorETFMap[symbol]; ok {
		if b, ok := etfToBucket[etf]; ok {
			return b
		}
	}
	return SectorBucketOther
}

// currentSectorExposure sums managed-position AllocationDollars per sector
// bucket across all agents, plus any contribution from a registered
// OptionsExposureProvider. Raw (non-managed) orders are intentionally not
// counted — they have no allocation-dollar value to aggregate.
func (g *TradeGuard) currentSectorExposure() map[SectorBucket]float64 {
	out := make(map[SectorBucket]float64)
	if g.positions != nil {
		for _, p := range g.positions.ListManagedPositions("") {
			if !isActivePosition(p) {
				continue
			}
			out[g.bucketFor(p.Symbol)] += p.AllocationDollars
		}
	}
	g.mu.RLock()
	provider := g.optionsProvider
	g.mu.RUnlock()
	if provider != nil {
		for bucket, dollars := range provider.BucketExposureDollars() {
			out[bucket] += dollars
		}
	}
	return out
}

// sectorCapFor returns the dollar cap for a bucket given a portfolio value.
// Bucket-specific override wins; otherwise DefaultSectorMaxPct is used.
// Returns ok=false when neither is configured (bucket is uncapped).
func (g *TradeGuard) sectorCapFor(bucket SectorBucket, portfolioValue float64) (float64, bool) {
	if pct, found := g.cfg.SectorMaxExposurePct[string(bucket)]; found && pct > 0 {
		return portfolioValue * pct, true
	}
	if g.cfg.DefaultSectorMaxPct > 0 {
		return portfolioValue * g.cfg.DefaultSectorMaxPct, true
	}
	return 0, false
}

// checkSectorCap enforces the cross-agent sector concentration cap.
//   - acctErr != nil  → fail-closed (matches penny-cap policy: a flaky API
//     call must not silently bypass a concentration limit).
//   - acct == nil     → no-op (trading service unavailable, e.g. tests).
//   - PortfolioValue ≤ 0 → no-op (uninitialized/new account, fail-open).
func (g *TradeGuard) checkSectorCap(acct *interfaces.Account, acctErr error, symbol string, additionalDollars float64) error {
	if acctErr != nil {
		return fmt.Errorf("guard: failed to fetch account for sector cap check: %w", acctErr)
	}
	if acct == nil || acct.PortfolioValue <= 0 {
		return nil
	}
	bucket := g.bucketFor(symbol)
	cap, ok := g.sectorCapFor(bucket, acct.PortfolioValue)
	if !ok {
		return nil
	}
	projected := g.currentSectorExposure()[bucket] + additionalDollars
	if projected > cap {
		return fmt.Errorf(
			"guard: sector cap — %s bucket would reach $%.2f including new $%.2f, exceeds $%.2f cap on $%.2f portfolio",
			bucket, projected, additionalDollars, cap, acct.PortfolioValue,
		)
	}
	return nil
}

func (g *TradeGuard) currentPennyExposure() float64 {
	if g.positions == nil {
		return 0
	}
	total := 0.0
	for _, p := range g.positions.ListManagedPositions("") {
		if isActivePosition(p) && positionBelongsTo(p, AgentPenny) {
			total += p.AllocationDollars
		}
	}
	return total
}

func (g *TradeGuard) opponentOf(agent AgentSource) AgentSource {
	if agent == AgentMain {
		return AgentPenny
	}
	return AgentMain
}

func isActivePosition(p *ManagedPosition) bool {
	return p.Status == "ACTIVE" || p.Status == "PARTIAL" || p.Status == "PENDING"
}

// positionBelongsTo returns true if the position is tagged for the given agent.
// Untagged positions default to main.
func positionBelongsTo(p *ManagedPosition, agent AgentSource) bool {
	tag := AgentTag(agent)
	pennyTag := AgentTag(AgentPenny)

	for _, t := range p.Tags {
		if t == tag {
			return true
		}
	}

	// Untagged = main
	if agent == AgentMain {
		for _, t := range p.Tags {
			if t == pennyTag {
				return false
			}
		}
		return true
	}

	return false
}

func setToSlice(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
