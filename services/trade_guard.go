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
	AgentMain  AgentSource = "main"
	AgentPenny AgentSource = "penny"
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
}

// positionLister is the subset of PositionManager needed by the guard.
type positionLister interface {
	ListManagedPositions(status string) []*ManagedPosition
}

// TradeGuard enforces cross-agent trade rules:
//   - Symbol non-overlap: a symbol held by one agent cannot be traded by the other.
//   - Penny per-position cap: each penny buy is capped at PennyMaxPositionDollars.
//   - Penny portfolio cap: total penny exposure ≤ PennyMaxCapitalPct × portfolio value.
//
// Managed positions are the authoritative ownership source; raw buy/sell orders are
// tracked in-memory and cleared on sell (lost across restarts).
type TradeGuard struct {
	positions      positionLister
	tradingService interfaces.TradingService
	cfg            TradeGuardConfig

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
			AgentMain:  {},
			AgentPenny: {},
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
}

// Status returns a snapshot of current guard state.
func (g *TradeGuard) Status(ctx context.Context) GuardStatus {
	mainSet := g.symbolsFor(AgentMain)
	pennySet := g.symbolsFor(AgentPenny)

	mainList := setToSlice(mainSet)
	pennyList := setToSlice(pennySet)

	exposure := g.currentPennyExposure()
	maxDollars := 0.0
	if g.tradingService != nil {
		if acct, err := g.tradingService.GetAccount(ctx); err == nil {
			maxDollars = acct.PortfolioValue * g.cfg.PennyMaxCapitalPct
		}
	}

	return GuardStatus{
		Config:          g.cfg,
		MainSymbols:     mainList,
		PennySymbols:    pennyList,
		PennyExposure:   exposure,
		PennyCapitalMax: maxDollars,
	}
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
