package services

import (
	"testing"
)

func TestAgentHarvestConstant(t *testing.T) {
	if AgentHarvest == AgentMain {
		t.Error("AgentHarvest must be distinct from AgentMain")
	}
	if AgentHarvest == AgentPenny {
		t.Error("AgentHarvest must be distinct from AgentPenny")
	}
	if string(AgentHarvest) != "harvest" {
		t.Errorf("expected AgentHarvest to be 'harvest', got %q", AgentHarvest)
	}
}
