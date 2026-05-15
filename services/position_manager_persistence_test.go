package services

import (
	"context"
	"prophet-trader/database"
	"prophet-trader/interfaces"
	"testing"
)

// stubTradingPersist wraps stubTrading (declared in trade_guard_test.go) and
// overrides PlaceOrder to return a non-nil OrderResult so placeEntryOrder can
// exercise the "save the resulting DBOrder" path.
type stubTradingPersist struct {
	*stubTrading
}

func (s *stubTradingPersist) PlaceOrder(_ context.Context, order *interfaces.Order) (*interfaces.OrderResult, error) {
	return &interfaces.OrderResult{
		OrderID: "test-order-" + order.Symbol,
		Status:  "accepted",
	}, nil
}

// TestPlaceEntryOrder_PersistsDBOrderWithStrategy pins the contract that the
// managed-position entry path persists a DBOrder row tagged with the owning
// agent's strategy. Without persistence, GetSymbolStrategyAttribution cannot
// attribute the broker position back to the agent, and the per-strategy
// /positions filter (used by preflight) silently drops it. See III on Spark
// in sandbox 449fedf6 (2026-05-14): managed_positions row had
// agent_strategy="penny-momentum" but the orders table was empty, so the
// preflight saw zero penny positions and skipped beats.
func TestPlaceEntryOrder_PersistsDBOrderWithStrategy(t *testing.T) {
	storage, err := database.NewLocalStorage(":memory:")
	if err != nil {
		t.Fatalf("create storage: %v", err)
	}

	trading := &stubTradingPersist{stubTrading: &stubTrading{}}
	pm := NewPositionManager(trading, nil, storage)

	pos := &ManagedPosition{
		Symbol:         "III",
		Side:           "buy",
		Quantity:       100,
		EntryPrice:     4.27,
		EntryOrderType: "limit",
		AgentStrategy:  "penny-momentum",
	}

	if err := pm.placeEntryOrder(context.Background(), pos); err != nil {
		t.Fatalf("placeEntryOrder: %v", err)
	}

	if pos.EntryOrderID == "" {
		t.Fatalf("EntryOrderID not set on position after placeEntryOrder")
	}

	saved, err := storage.GetOrder(pos.EntryOrderID)
	if err != nil {
		t.Fatalf("GetOrder %q after placeEntryOrder: %v", pos.EntryOrderID, err)
	}
	if saved.Symbol != "III" {
		t.Errorf("saved.Symbol = %q, want III", saved.Symbol)
	}
	if saved.Side != "buy" {
		t.Errorf("saved.Side = %q, want buy", saved.Side)
	}
	if saved.Strategy != "penny-momentum" {
		t.Errorf("saved.Strategy = %q, want penny-momentum", saved.Strategy)
	}
}
