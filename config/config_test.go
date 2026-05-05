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
