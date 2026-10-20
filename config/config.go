package config

import (
	"os"
	"strings"

	"github.com/joho/godotenv"
)

const (
	PaperTradingURL = "https://paper-api.alpaca.markets"
	LiveTradingURL  = "https://api.alpaca.markets"
)

type Config struct {
	AlpacaAPIKey      string
	AlpacaSecretKey   string
	AlpacaBaseURL     string
	AlpacaPaper       bool
	GeminiAPIKey      string
	DatabasePath      string
	ServerPort        string
	ServerHost        string
	AuthToken         string
	AllowedOrigins    string
	EnableLogging     bool
	LogLevel          string
	DataRetentionDays int
}

var AppConfig *Config

func Load() error {
	// Load .env file if it exists (don't override existing env vars)
	_ = godotenv.Load()

	AppConfig = fromEnv(os.Getenv)

	return nil
}

// fromEnv builds the config from an env lookup. Canonical names (what agent/server.js hands the
// spawned backend) always win over the documented .env aliases ALPACA_PUBLIC_KEY, ALPACA_ENDPOINT
// and TRADING_BOT_PORT, so an inherited .env never overrides the account the dashboard selected.
func fromEnv(getenv func(string) string) *Config {
	get := func(key, defaultValue string) string {
		if value := getenv(key); value != "" {
			return value
		}
		return defaultValue
	}

	// Paper unless told otherwise, matching agent/config-store.js: an explicit endpoint decides
	// the mode, and without one only ALPACA_PAPER=false opts into live. URL and flag always agree.
	baseURL := get("ALPACA_BASE_URL", getenv("ALPACA_ENDPOINT"))
	paper := getenv("ALPACA_PAPER") != "false"
	if baseURL != "" {
		paper = strings.Contains(baseURL, "paper")
	} else if paper {
		baseURL = PaperTradingURL
	} else {
		baseURL = LiveTradingURL
	}

	return &Config{
		AlpacaAPIKey:      get("ALPACA_API_KEY", getenv("ALPACA_PUBLIC_KEY")),
		AlpacaSecretKey:   getenv("ALPACA_SECRET_KEY"),
		AlpacaBaseURL:     baseURL,
		AlpacaPaper:       paper,
		GeminiAPIKey:      getenv("GEMINI_API_KEY"),
		DatabasePath:      get("DATABASE_PATH", "./data/prophet_trader.db"),
		ServerPort:        get("PORT", get("SERVER_PORT", get("TRADING_BOT_PORT", "4534"))),
		ServerHost:        get("SERVER_HOST", "127.0.0.1"),
		AuthToken:         get("TRADING_BOT_TOKEN", ""),
		AllowedOrigins:    get("CORS_ALLOWED_ORIGINS", ""),
		EnableLogging:     get("ENABLE_LOGGING", "true") == "true",
		LogLevel:          get("LOG_LEVEL", "info"),
		DataRetentionDays: 90,
	}
}
