package config

import (
	"testing"
)

func TestLoad_MissingOperatorEmail_ReturnsError(t *testing.T) {
	t.Setenv("OPERATOR_EMAIL", "")
	err := Load()
	if err == nil {
		t.Fatal("expected error when OPERATOR_EMAIL is unset, got nil")
	}
}

func TestLoad_WithOperatorEmail_Succeeds(t *testing.T) {
	t.Setenv("OPERATOR_EMAIL", "test@example.com")
	err := Load()
	if err != nil {
		t.Fatalf("expected no error with OPERATOR_EMAIL set, got: %v", err)
	}
	if AppConfig.OperatorEmail != "test@example.com" {
		t.Errorf("expected OperatorEmail=test@example.com, got %q", AppConfig.OperatorEmail)
	}
}

// Regime gate defaults follow the Item 1 flag-gated rollout pattern:
// enforcement off until observed in production. ReportPath defaults to the
// canonical scheduler-written location so the operator doesn't have to set it.
func TestLoad_RegimeGate_DefaultsWhenEnvUnset(t *testing.T) {
	t.Setenv("OPERATOR_EMAIL", "test@example.com")
	t.Setenv("ENABLE_REGIME_GATE", "")
	t.Setenv("REGIME_REPORT_PATH", "")
	if err := Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if AppConfig.EnableRegimeGate {
		t.Error("EnableRegimeGate: want false default (flag-gated rollout)")
	}
	if AppConfig.RegimeReportPath != "./data/reports/regime_gate.json" {
		t.Errorf("RegimeReportPath default: want %q, got %q",
			"./data/reports/regime_gate.json", AppConfig.RegimeReportPath)
	}
}

func TestLoad_RegimeGate_HonorsEnvOverrides(t *testing.T) {
	t.Setenv("OPERATOR_EMAIL", "test@example.com")
	t.Setenv("ENABLE_REGIME_GATE", "true")
	t.Setenv("REGIME_REPORT_PATH", "/tmp/custom_regime.json")
	if err := Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !AppConfig.EnableRegimeGate {
		t.Error("EnableRegimeGate: want true when ENABLE_REGIME_GATE=true")
	}
	if AppConfig.RegimeReportPath != "/tmp/custom_regime.json" {
		t.Errorf("RegimeReportPath override: want %q, got %q",
			"/tmp/custom_regime.json", AppConfig.RegimeReportPath)
	}
}
