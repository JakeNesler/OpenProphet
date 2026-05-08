package services

import (
	"context"
	"fmt"
	"time"

	"prophet-trader/interfaces"
)

// SegmentPnL is the live P&L view for a single strategy.
//
// v1 limitation: only unrealized P&L from currently-open broker positions
// is computed. Closed managed positions (where realized P&L lives in
// DBManagedPosition.UnrealizedPL frozen at exit, filterable by
// AgentStrategy) and closed iron condors (DBHarvestCondor.RealizedPnL) are
// not yet summed in. The EOD writer that materializes DBSegmentPnL is the
// natural place to fold those in — tracked as a follow-up in
// docs/shared-account-backend-spec.md.
//
// Managed-position attribution is now handled at the broker layer:
// PositionManager.placeEntryOrder forwards AgentStrategy onto the entry
// order, so GetSymbolStrategyAttribution picks up Penny/V2 managed
// positions automatically through DBOrder.
type SegmentPnL struct {
	Strategy         string  `json:"strategy"`
	AsOf             string  `json:"as_of"`
	OpenPositions    int     `json:"open_positions"`
	UnrealizedPnL    float64 `json:"unrealized_pnl"`
	DeployedDollars  float64 `json:"deployed_dollars"`
	DeployedPercent  float64 `json:"deployed_percent"`
	PortfolioValue   float64 `json:"portfolio_value"`
	UnrealizedPnLPct float64 `json:"unrealized_pnl_percent"`
	Limitation       string  `json:"limitation"`
}

// SegmentPnLService computes live segment-scoped P&L from broker positions
// and the strategy attribution map maintained by storage.
type SegmentPnLService struct {
	storage        interfaces.StorageService
	tradingService interfaces.TradingService
}

// NewSegmentPnLService constructs the service.
func NewSegmentPnLService(storage interfaces.StorageService, tradingService interfaces.TradingService) *SegmentPnLService {
	return &SegmentPnLService{storage: storage, tradingService: tradingService}
}

// GetSegmentPnL returns the current P&L summary for the requested strategy.
// Returns zero-valued OpenPositions / dollars / pct fields if the strategy
// has no attributable positions — this is a valid response, not an error.
func (s *SegmentPnLService) GetSegmentPnL(ctx context.Context, strategy string) (*SegmentPnL, error) {
	if strategy == "" {
		return nil, fmt.Errorf("strategy is required")
	}

	positions, err := s.tradingService.GetPositions(ctx)
	if err != nil {
		return nil, fmt.Errorf("get positions: %w", err)
	}

	account, err := s.tradingService.GetAccount(ctx)
	if err != nil {
		return nil, fmt.Errorf("get account: %w", err)
	}

	attribution, err := s.storage.GetSymbolStrategyAttribution()
	if err != nil {
		return nil, fmt.Errorf("get strategy attribution: %w", err)
	}

	var unrealized, deployed float64
	openCount := 0
	for _, pos := range positions {
		if attribution[pos.Symbol] != strategy {
			continue
		}
		unrealized += pos.UnrealizedPL
		// MarketValue can be negative for short positions; use absolute value
		// for "deployed" dollars so the cap math reflects gross exposure.
		mv := pos.MarketValue
		if mv < 0 {
			mv = -mv
		}
		deployed += mv
		openCount++
	}

	var deployedPct, unrealizedPct float64
	if account.PortfolioValue > 0 {
		deployedPct = (deployed / account.PortfolioValue) * 100.0
		unrealizedPct = (unrealized / account.PortfolioValue) * 100.0
	}

	return &SegmentPnL{
		Strategy:         strategy,
		AsOf:             time.Now().Format(time.RFC3339),
		OpenPositions:    openCount,
		UnrealizedPnL:    unrealized,
		DeployedDollars:  deployed,
		DeployedPercent:  deployedPct,
		PortfolioValue:   account.PortfolioValue,
		UnrealizedPnLPct: unrealizedPct,
		Limitation:       "v1: unrealized P&L from currently-open broker positions only. Closed managed positions and closed iron condors are not yet summed in.",
	}, nil
}
