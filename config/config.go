package config

import (
	"os"

	"github.com/joho/godotenv"
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

	AppConfig = &Config{
		AlpacaAPIKey:      os.Getenv("ALPACA_API_KEY"),
		AlpacaSecretKey:   os.Getenv("ALPACA_SECRET_KEY"),
		AlpacaBaseURL:     getEnvOrDefault("ALPACA_BASE_URL", "https://paper-api.alpaca.markets"),
		AlpacaPaper:       getEnvOrDefault("ALPACA_PAPER", "true") == "true",
		GeminiAPIKey:      os.Getenv("GEMINI_API_KEY"),
		DatabasePath:      getEnvOrDefault("DATABASE_PATH", "./data/prophet_trader.db"),
		ServerPort:        getEnvOrDefault("PORT", getEnvOrDefault("SERVER_PORT", "4534")),
		ServerHost:        getEnvOrDefault("SERVER_HOST", "127.0.0.1"),
		AuthToken:         getEnvOrDefault("TRADING_BOT_TOKEN", ""),
		AllowedOrigins:    getEnvOrDefault("CORS_ALLOWED_ORIGINS", ""),
		EnableLogging:     getEnvOrDefault("ENABLE_LOGGING", "true") == "true",
		LogLevel:          getEnvOrDefault("LOG_LEVEL", "info"),
		DataRetentionDays: 90,
	}

	return nil
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
