package config

import (
	"os"
	"testing"
)

func TestLoad_MissingOperatorEmail_ReturnsError(t *testing.T) {
	os.Unsetenv("OPERATOR_EMAIL")
	err := Load()
	if err == nil {
		t.Fatal("expected error when OPERATOR_EMAIL is unset, got nil")
	}
}

func TestLoad_WithOperatorEmail_Succeeds(t *testing.T) {
	os.Setenv("OPERATOR_EMAIL", "test@example.com")
	defer os.Unsetenv("OPERATOR_EMAIL")
	err := Load()
	if err != nil {
		t.Fatalf("expected no error with OPERATOR_EMAIL set, got: %v", err)
	}
	if AppConfig.OperatorEmail != "test@example.com" {
		t.Errorf("expected OperatorEmail=test@example.com, got %q", AppConfig.OperatorEmail)
	}
}
