package config

import "testing"

func lookup(env map[string]string) func(string) string {
	return func(key string) string { return env[key] }
}

func TestDefaultsToPaper(t *testing.T) {
	cfg := fromEnv(lookup(nil))
	if cfg.AlpacaBaseURL != PaperTradingURL || !cfg.AlpacaPaper {
		t.Fatalf("empty env: got url=%q paper=%v, want paper endpoint", cfg.AlpacaBaseURL, cfg.AlpacaPaper)
	}
	if cfg.ServerPort != "4534" {
		t.Fatalf("empty env: got port %q, want 4534", cfg.ServerPort)
	}
}

func TestDocumentedAliases(t *testing.T) {
	cfg := fromEnv(lookup(map[string]string{
		"ALPACA_PUBLIC_KEY": "alias-key",
		"ALPACA_SECRET_KEY": "alias-secret",
		"ALPACA_ENDPOINT":   PaperTradingURL,
		"TRADING_BOT_PORT":  "4600",
	}))
	if cfg.AlpacaAPIKey != "alias-key" || cfg.AlpacaSecretKey != "alias-secret" {
		t.Fatalf("got key=%q secret=%q, want alias values", cfg.AlpacaAPIKey, cfg.AlpacaSecretKey)
	}
	if cfg.AlpacaBaseURL != PaperTradingURL || !cfg.AlpacaPaper {
		t.Fatalf("got url=%q paper=%v, want ALPACA_ENDPOINT paper", cfg.AlpacaBaseURL, cfg.AlpacaPaper)
	}
	if cfg.ServerPort != "4600" {
		t.Fatalf("got port %q, want TRADING_BOT_PORT 4600", cfg.ServerPort)
	}
}

func TestCanonicalBeatsAliases(t *testing.T) {
	cfg := fromEnv(lookup(map[string]string{
		"ALPACA_API_KEY":    "canonical-key",
		"ALPACA_PUBLIC_KEY": "alias-key",
		"ALPACA_BASE_URL":   "http://127.0.0.1:9/paper",
		"ALPACA_ENDPOINT":   LiveTradingURL,
		"PORT":              "4535",
		"SERVER_PORT":       "4536",
		"TRADING_BOT_PORT":  "4537",
	}))
	if cfg.AlpacaAPIKey != "canonical-key" {
		t.Fatalf("got key %q, want ALPACA_API_KEY to win", cfg.AlpacaAPIKey)
	}
	if cfg.AlpacaBaseURL != "http://127.0.0.1:9/paper" || !cfg.AlpacaPaper {
		t.Fatalf("got url=%q paper=%v, want ALPACA_BASE_URL to win", cfg.AlpacaBaseURL, cfg.AlpacaPaper)
	}
	if cfg.ServerPort != "4535" {
		t.Fatalf("got port %q, want PORT to win", cfg.ServerPort)
	}

	cfg = fromEnv(lookup(map[string]string{"SERVER_PORT": "4536", "TRADING_BOT_PORT": "4537"}))
	if cfg.ServerPort != "4536" {
		t.Fatalf("got port %q, want SERVER_PORT over TRADING_BOT_PORT", cfg.ServerPort)
	}
}

func TestEmptyCanonicalFallsBackToAlias(t *testing.T) {
	cfg := fromEnv(lookup(map[string]string{
		"ALPACA_API_KEY":    "",
		"ALPACA_PUBLIC_KEY": "alias-key",
		"ALPACA_BASE_URL":   "",
		"ALPACA_ENDPOINT":   PaperTradingURL,
		"PORT":              "",
		"TRADING_BOT_PORT":  "4600",
	}))
	if cfg.AlpacaAPIKey != "alias-key" || cfg.AlpacaBaseURL != PaperTradingURL || cfg.ServerPort != "4600" {
		t.Fatalf("got key=%q url=%q port=%q, want alias values", cfg.AlpacaAPIKey, cfg.AlpacaBaseURL, cfg.ServerPort)
	}
}

func TestPaperFlagAndURLAgree(t *testing.T) {
	cases := []struct {
		name      string
		env       map[string]string
		wantURL   string
		wantPaper bool
	}{
		{"paper flag true", map[string]string{"ALPACA_PAPER": "true"}, PaperTradingURL, true},
		{"unrecognised flag stays paper", map[string]string{"ALPACA_PAPER": "no"}, PaperTradingURL, true},
		{"explicit live opt-in", map[string]string{"ALPACA_PAPER": "false"}, LiveTradingURL, false},
		{"live endpoint decides", map[string]string{"ALPACA_ENDPOINT": LiveTradingURL, "ALPACA_PAPER": "true"}, LiveTradingURL, false},
		{"paper endpoint decides", map[string]string{"ALPACA_BASE_URL": PaperTradingURL, "ALPACA_PAPER": "false"}, PaperTradingURL, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := fromEnv(lookup(tc.env))
			if cfg.AlpacaBaseURL != tc.wantURL || cfg.AlpacaPaper != tc.wantPaper {
				t.Fatalf("got url=%q paper=%v, want url=%q paper=%v", cfg.AlpacaBaseURL, cfg.AlpacaPaper, tc.wantURL, tc.wantPaper)
			}
		})
	}
}

func TestLoadReadsProcessEnv(t *testing.T) {
	for _, key := range []string{
		"ALPACA_API_KEY", "ALPACA_PUBLIC_KEY", "ALPACA_SECRET_KEY", "ALPACA_BASE_URL",
		"ALPACA_ENDPOINT", "ALPACA_PAPER", "PORT", "SERVER_PORT", "TRADING_BOT_PORT",
	} {
		t.Setenv(key, "")
	}
	t.Setenv("ALPACA_PUBLIC_KEY", "alias-key")
	t.Setenv("TRADING_BOT_PORT", "4600")

	if err := Load(); err != nil {
		t.Fatal(err)
	}
	if AppConfig.AlpacaAPIKey != "alias-key" || AppConfig.ServerPort != "4600" || AppConfig.AlpacaBaseURL != PaperTradingURL {
		t.Fatalf("got key=%q port=%q url=%q", AppConfig.AlpacaAPIKey, AppConfig.ServerPort, AppConfig.AlpacaBaseURL)
	}
}
