package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	AlpacaAPIKey      string
	AlpacaSecretKey   string
	AlpacaBaseURL     string
	AlpacaPaper       bool
	ClaudeAPIKey      string
	FMPAPIKey         string
	DatabasePath      string
	ServerPort        string
	EnableLogging     bool
	LogLevel          string
	DataRetentionDays int
	OperatorEmail     string // SEC EDGAR User-Agent contact; set via OPERATOR_EMAIL env var

	// Trade guard limits
	PennyMaxCapitalPct      float64 // fraction of portfolio, e.g. 0.20
	PennyMaxPositionDollars float64 // max dollars per single penny trade, e.g. 500
	MaxDailyLossPct         float64 // daily loss circuit breaker as positive percent, e.g. 5.0; 0 disables
}

var AppConfig *Config

func Load() error {
	// Load .env file if it exists (don't override existing env vars)
	_ = godotenv.Load()

	AppConfig = &Config{
		AlpacaAPIKey:      os.Getenv("ALPACA_API_KEY"),
		AlpacaSecretKey:   os.Getenv("ALPACA_SECRET_KEY"),
		AlpacaBaseURL:     getEnvOrDefault("ALPACA_BASE_URL", "https://paper-api.alpaca.markets"),
		AlpacaPaper:       getEnvOrDefault("ALPACA_PAPER", "true") == "true",
		ClaudeAPIKey:      os.Getenv("CLAUDE_API_KEY"),
		FMPAPIKey:         os.Getenv("FMP_API_KEY"),
		DatabasePath:      getEnvOrDefault("DATABASE_PATH", "./data/prophet_trader.db"),
		ServerPort:        getEnvOrDefault("PORT", getEnvOrDefault("SERVER_PORT", "4534")),
		EnableLogging:     getEnvOrDefault("ENABLE_LOGGING", "true") == "true",
		LogLevel:          getEnvOrDefault("LOG_LEVEL", "info"),
		DataRetentionDays: 90,

		PennyMaxCapitalPct:      parseFloat(getEnvOrDefault("PENNY_MAX_CAPITAL_PCT", "0.20")),
		PennyMaxPositionDollars: parseFloat(getEnvOrDefault("PENNY_MAX_POSITION_DOLLARS", "500")),
		MaxDailyLossPct:         parseFloat(getEnvOrDefault("MAX_DAILY_LOSS_PCT", "5")),

		OperatorEmail: os.Getenv("OPERATOR_EMAIL"),
	}

	if AppConfig.OperatorEmail == "" {
		return fmt.Errorf("OPERATOR_EMAIL must be set — SEC EDGAR policy requires a real contact address in the User-Agent header. Set OPERATOR_EMAIL=your@email.com in .env")
	}
	return nil
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func parseFloat(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}
