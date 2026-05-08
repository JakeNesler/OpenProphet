package database

import (
	"testing"
	"time"

	"prophet-trader/interfaces"
)

func TestGetSymbolStrategyAttribution(t *testing.T) {
	s := setupHarvestTestDB(t)

	// Two orders for TLT — most recent (newer SubmittedAt) wins.
	older := &interfaces.Order{
		ID:           "order-tlt-old",
		Symbol:       "TLT",
		Qty:          100,
		Side:         "buy",
		Type:         "market",
		Status:       "filled",
		Strategy: "trend",
		SubmittedAt:  time.Now().Add(-24 * time.Hour),
	}
	newer := &interfaces.Order{
		ID:          "order-tlt-new",
		Symbol:      "TLT",
		Qty:         50,
		Side:        "buy",
		Type:        "market",
		Status:      "filled",
		Strategy:    "v2-options",
		SubmittedAt: time.Now().Add(-1 * time.Hour),
	}
	if err := s.SaveOrder(older); err != nil {
		t.Fatalf("save older: %v", err)
	}
	if err := s.SaveOrder(newer); err != nil {
		t.Fatalf("save newer: %v", err)
	}

	// One unrelated symbol with only ClientOrderID (no StrategyName) — exercises
	// the ParseStrategyFromClientOrderID fallback path.
	gld := &interfaces.Order{
		ID:            "order-gld",
		Symbol:        "GLD",
		Qty:           10,
		Side:          "buy",
		Type:          "market",
		Status:        "filled",
		ClientOrderID: "trend:abcd-1234",
		// StrategyName intentionally omitted to test the fallback.
		SubmittedAt: time.Now().Add(-2 * time.Hour),
	}
	if err := s.SaveOrder(gld); err != nil {
		t.Fatalf("save gld: %v", err)
	}

	// Untagged order — should be excluded from the map.
	untagged := &interfaces.Order{
		ID:          "order-aapl",
		Symbol:      "AAPL",
		Qty:         5,
		Side:        "buy",
		Type:        "market",
		Status:      "filled",
		SubmittedAt: time.Now().Add(-3 * time.Hour),
	}
	if err := s.SaveOrder(untagged); err != nil {
		t.Fatalf("save aapl: %v", err)
	}

	// Sell-side orders should NOT contribute to attribution (we attribute
	// based on the most recent BUY).
	sell := &interfaces.Order{
		ID:          "order-msft-sell",
		Symbol:      "MSFT",
		Qty:         1,
		Side:        "sell",
		Type:        "market",
		Status:      "filled",
		Strategy:    "should-not-appear",
		SubmittedAt: time.Now(),
	}
	if err := s.SaveOrder(sell); err != nil {
		t.Fatalf("save sell: %v", err)
	}

	got, err := s.GetSymbolStrategyAttribution()
	if err != nil {
		t.Fatalf("GetSymbolStrategyAttribution: %v", err)
	}

	if got["TLT"] != "v2-options" {
		t.Errorf("TLT attribution = %q, want v2-options (most recent buy wins)", got["TLT"])
	}
	if got["GLD"] != "trend" {
		t.Errorf("GLD attribution = %q, want trend (parsed from client_order_id)", got["GLD"])
	}
	if _, present := got["AAPL"]; present {
		t.Errorf("AAPL should NOT be in attribution map (untagged order), got %q", got["AAPL"])
	}
	if _, present := got["MSFT"]; present {
		t.Errorf("MSFT should NOT be attributed from a sell-side order, got %q", got["MSFT"])
	}
}
