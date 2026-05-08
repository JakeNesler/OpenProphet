package database

import (
	"testing"

	"prophet-trader/models"
)

// TestManagedPosition_AgentStrategyRoundTrip verifies that the trade-style
// Strategy field and the agent-attribution AgentStrategy field are stored
// independently. This pins the namespace separation introduced when Penny's
// "DAY_TRADE"/"SWING_TRADE"/"LONG_TERM" semantics collided with the agent
// strategyId used for shared-account attribution.
func TestManagedPosition_AgentStrategyRoundTrip(t *testing.T) {
	s := setupHarvestTestDB(t)

	pos := &models.DBManagedPosition{
		PositionID:    "mp-test-1",
		Symbol:        "NVDA",
		Side:          "buy",
		Strategy:      "DAY_TRADE",
		AgentStrategy: "penny-momentum",
		Quantity:      10,
		EntryPrice:    100.0,
		Status:        "ACTIVE",
	}
	if err := s.SaveManagedPosition(pos); err != nil {
		t.Fatalf("SaveManagedPosition: %v", err)
	}

	got, err := s.GetManagedPosition("mp-test-1")
	if err != nil {
		t.Fatalf("GetManagedPosition: %v", err)
	}
	if got.Strategy != "DAY_TRADE" {
		t.Errorf("Strategy = %q, want DAY_TRADE", got.Strategy)
	}
	if got.AgentStrategy != "penny-momentum" {
		t.Errorf("AgentStrategy = %q, want penny-momentum", got.AgentStrategy)
	}
}

// TestManagedPosition_AgentStrategyEmpty verifies that legacy rows without an
// AgentStrategy still round-trip cleanly. Any reader must tolerate the empty
// case and fall back to DBOrder attribution (or treat the row as unattributable).
func TestManagedPosition_AgentStrategyEmpty(t *testing.T) {
	s := setupHarvestTestDB(t)

	pos := &models.DBManagedPosition{
		PositionID: "mp-test-legacy",
		Symbol:     "AAPL",
		Side:       "buy",
		Strategy:   "SWING_TRADE",
		// AgentStrategy intentionally empty (pre-migration row).
		Quantity:   5,
		EntryPrice: 200.0,
		Status:     "ACTIVE",
	}
	if err := s.SaveManagedPosition(pos); err != nil {
		t.Fatalf("SaveManagedPosition: %v", err)
	}

	got, err := s.GetManagedPosition("mp-test-legacy")
	if err != nil {
		t.Fatalf("GetManagedPosition: %v", err)
	}
	if got.Strategy != "SWING_TRADE" {
		t.Errorf("Strategy = %q, want SWING_TRADE", got.Strategy)
	}
	if got.AgentStrategy != "" {
		t.Errorf("AgentStrategy = %q, want empty", got.AgentStrategy)
	}
}
