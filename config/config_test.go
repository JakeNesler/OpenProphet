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

// Accept the .env.example / README convention (ALPACA_PUBLIC_KEY,
// ALPACA_ENDPOINT) as fallbacks for the Go-canonical names (ALPACA_API_KEY,
// ALPACA_BASE_URL). Mirrors the dual-name behavior in agent/config-store.js so
// `go run ./cmd/bot/main.go` works against the same .env that the Node side
// already accepts.
func TestLoad_AlpacaKeys_FallbackToPublicKeyAndEndpoint(t *testing.T) {
	t.Setenv("OPERATOR_EMAIL", "test@example.com")
	t.Setenv("ALPACA_API_KEY", "")
	t.Setenv("ALPACA_BASE_URL", "")
	t.Setenv("ALPACA_PUBLIC_KEY", "pk-fallback")
	t.Setenv("ALPACA_ENDPOINT", "https://example.alpaca.test")
	if err := Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if AppConfig.AlpacaAPIKey != "pk-fallback" {
		t.Errorf("AlpacaAPIKey fallback: want %q, got %q", "pk-fallback", AppConfig.AlpacaAPIKey)
	}
	if AppConfig.AlpacaBaseURL != "https://example.alpaca.test" {
		t.Errorf("AlpacaBaseURL fallback: want %q, got %q", "https://example.alpaca.test", AppConfig.AlpacaBaseURL)
	}
}

// When both the canonical and legacy names are set, the canonical Go name wins.
func TestLoad_AlpacaKeys_CanonicalNamePreferredOverFallback(t *testing.T) {
	t.Setenv("OPERATOR_EMAIL", "test@example.com")
	t.Setenv("ALPACA_API_KEY", "pk-canonical")
	t.Setenv("ALPACA_PUBLIC_KEY", "pk-legacy")
	t.Setenv("ALPACA_BASE_URL", "https://canonical.alpaca.test")
	t.Setenv("ALPACA_ENDPOINT", "https://legacy.alpaca.test")
	if err := Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if AppConfig.AlpacaAPIKey != "pk-canonical" {
		t.Errorf("AlpacaAPIKey: canonical should win, got %q", AppConfig.AlpacaAPIKey)
	}
	if AppConfig.AlpacaBaseURL != "https://canonical.alpaca.test" {
		t.Errorf("AlpacaBaseURL: canonical should win, got %q", AppConfig.AlpacaBaseURL)
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
