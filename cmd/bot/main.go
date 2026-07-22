package main

import (
	"context"
	"log"
	"net/url"
	"os"
	"os/signal"
	"prophet-trader/config"
	"prophet-trader/controllers"
	"prophet-trader/database"
	"prophet-trader/interfaces"
	"prophet-trader/services"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

func main() {
	// Load configuration
	if err := config.Load(); err != nil {
		log.Fatal("Failed to load configuration:", err)
	}

	cfg := config.AppConfig

	// Initialize logger
	logger := logrus.New()
	logger.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})

	if cfg.EnableLogging {
		level, _ := logrus.ParseLevel(cfg.LogLevel)
		logger.SetLevel(level)
	}

	logger.Info("Starting Prophet Trader Bot...")

	// Validate required configuration
	if cfg.AlpacaAPIKey == "" || cfg.AlpacaSecretKey == "" {
		logger.Fatal("Alpaca API credentials not configured. Please set ALPACA_API_KEY and ALPACA_SECRET_KEY")
	}

	// Initialize services
	logger.Info("Initializing services...")

	// Create trading service
	tradingService, err := services.NewAlpacaTradingService(
		cfg.AlpacaAPIKey,
		cfg.AlpacaSecretKey,
		cfg.AlpacaBaseURL,
		cfg.AlpacaPaper,
	)
	if err != nil {
		logger.Warn("Failed to create trading service (will retry on requests):", err)
	}

	// Create data service
	dataService := services.NewAlpacaDataService(
		cfg.AlpacaAPIKey,
		cfg.AlpacaSecretKey,
	)

	// Create storage service
	storageService, err := database.NewLocalStorage(cfg.DatabasePath)
	if err != nil {
		logger.Fatal("Failed to create storage service:", err)
	}

	// Create order controller
	orderController := controllers.NewOrderController(
		tradingService,
		dataService,
		storageService,
	)

	// Create news service and controller
	newsService := services.NewNewsService()
	newsController := controllers.NewNewsController(newsService)

	// Create economic feeds service and controller
	economicFeedsService := services.NewEconomicFeedsService()
	economicFeedsController := controllers.NewEconomicFeedsController(economicFeedsService)

	// Create Gemini service and intelligence controller
	geminiService := services.NewGeminiService(cfg.GeminiAPIKey)
	analysisService := services.NewTechnicalAnalysisService(dataService)
	stockAnalysisService := services.NewStockAnalysisService(dataService, newsService, geminiService)
	intelligenceController := controllers.NewIntelligenceController(newsService, geminiService, analysisService, stockAnalysisService, dataService)

	// Test account connection
	logger.Info("Testing Alpaca connection...")
	if tradingService != nil {
		if account, err := orderController.GetAccount(); err != nil {
			logger.Warn("Failed to connect to Alpaca (trading will be unavailable):", err)
		} else {
			logger.WithFields(logrus.Fields{
				"cash":            account.Cash,
				"buying_power":    account.BuyingPower,
				"portfolio_value": account.PortfolioValue,
			}).Info("Successfully connected to Alpaca")
		}
	} else {
		logger.Warn("Trading service unavailable - API credentials may be invalid")
	}

	if tradingService != nil {
		// Run reconciliation in the background: the Alpaca SDK lookup ignores the context,
		// so a slow/hung broker call must never block startup or serving. It only repairs
		// local records; trading resumes on the agent heartbeat regardless.
		go func() {
			reconcileCtx, reconcileCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer reconcileCancel()
			reconciled, skipped := orderController.ReconcileOpenOrders(reconcileCtx)
			logger.WithFields(logrus.Fields{
				"reconciled": reconciled,
				"skipped":    skipped,
			}).Info("Startup order reconciliation complete")
		}()
	}

	// Start background tasks
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create position manager
	positionManager := services.NewPositionManager(tradingService, dataService, storageService)
	positionController := controllers.NewPositionManagementController(positionManager)

	// Create activity logger
	activityLogDir := os.Getenv("ACTIVITY_LOG_DIR")
	if activityLogDir == "" {
		activityLogDir = "./activity_logs"
	}
	activityLogger := services.NewActivityLogger(activityLogDir)
	activityController := controllers.NewActivityController(activityLogger)

	// Start trading session automatically
	if account, err := orderController.GetAccount(); err == nil {
		activityLogger.StartSession(ctx, account.PortfolioValue)
		logger.Info("Activity logging session started")
	}

	// Setup HTTP server
	router := setupRouter(orderController, newsController, intelligenceController, positionController, activityController, economicFeedsController)

	// Start data cleanup routine
	go startDataCleanup(ctx, storageService, cfg.DataRetentionDays, logger)

	// Start position monitor
	go startPositionMonitor(ctx, orderController, storageService, logger)

	// Start managed position monitoring
	go positionManager.MonitorPositions(ctx)

	// Setup graceful shutdown
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-shutdown
		logger.Info("Shutting down gracefully...")
		cancel()
		time.Sleep(2 * time.Second)
		os.Exit(0)
	}()

	// Start HTTP server
	logger.WithFields(logrus.Fields{"host": cfg.ServerHost, "port": cfg.ServerPort}).Info("Starting HTTP server...")
	if err := router.Run(cfg.ServerHost + ":" + cfg.ServerPort); err != nil {
		logger.Fatal("Failed to start server:", err)
	}
}

func setupRouter(orderController *controllers.OrderController, newsController *controllers.NewsController, intelligenceController *controllers.IntelligenceController, positionController *controllers.PositionManagementController, activityController *controllers.ActivityController, economicFeedsController *controllers.EconomicFeedsController) *gin.Engine {
	router := gin.Default()

	// Enable CORS
	router.Use(func(c *gin.Context) {
		if isAllowedOrigin(c.GetHeader("Origin"), config.AppConfig.AllowedOrigins) {
			c.Writer.Header().Set("Access-Control-Allow-Origin", c.GetHeader("Origin"))
			c.Writer.Header().Set("Vary", "Origin")
		}
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})

	// Health check
	router.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "healthy"})
	})

	// Trading endpoints
	api := router.Group("/api/v1")
	api.Use(func(c *gin.Context) {
		if config.AppConfig.AuthToken == "" || c.GetHeader("Authorization") == "Bearer "+config.AppConfig.AuthToken {
			c.Next()
			return
		}
		c.AbortWithStatus(401)
	})
	{
		// Order endpoints
		api.POST("/orders/buy", orderController.HandleBuy)
		api.POST("/orders/sell", orderController.HandleSell)
		api.DELETE("/orders/:id", orderController.HandleCancelOrder)
		api.GET("/orders", orderController.HandleGetOrders)

		// Position and account endpoints
		api.GET("/positions", orderController.HandleGetPositions)
		api.GET("/account", orderController.HandleGetAccount)

		// Market data endpoints
		api.GET("/market/quote/:symbol", orderController.HandleGetQuote)
		api.GET("/market/bar/:symbol", orderController.HandleGetBar)
		api.GET("/market/bars/:symbol", orderController.HandleGetBars)

		// Options trading endpoints
		api.POST("/options/order", orderController.PlaceOptionsOrder)
		api.GET("/options/positions", orderController.ListOptionsPositions)
		api.GET("/options/position/:symbol", orderController.GetOptionsPosition)
		api.GET("/options/chain/:symbol", orderController.GetOptionsChain)

		// News endpoints
		api.GET("/news", newsController.HandleGetNews)
		api.GET("/news/topic/:topic", newsController.HandleGetNewsByTopic)
		api.GET("/news/search", newsController.HandleSearchNews)
		api.GET("/news/market", newsController.HandleGetMarketNews)

		// MarketWatch endpoints
		api.GET("/news/marketwatch/topstories", newsController.HandleGetMarketWatchTopStories)
		api.GET("/news/marketwatch/realtime", newsController.HandleGetMarketWatchRealtimeHeadlines)
		api.GET("/news/marketwatch/bulletins", newsController.HandleGetMarketWatchBulletins)
		api.GET("/news/marketwatch/marketpulse", newsController.HandleGetMarketWatchMarketPulse)
		api.GET("/news/marketwatch/all", newsController.HandleGetAllMarketWatchNews)

		// Intelligence endpoints (AI-powered)
		api.POST("/intelligence/cleaned-news", intelligenceController.HandleGetCleanedNews)
		api.GET("/intelligence/quick-market", intelligenceController.HandleGetQuickMarketIntelligence)
		api.GET("/intelligence/analyze/:symbol", intelligenceController.HandleAnalyzeStock)
		api.POST("/intelligence/analyze-multiple", intelligenceController.HandleAnalyzeMultipleStocks)

		// Position management endpoints
		api.POST("/positions/managed", positionController.HandlePlaceManagedPosition)
		api.GET("/positions/managed", positionController.HandleListManagedPositions)
		api.GET("/positions/managed/:id", positionController.HandleGetManagedPosition)
		api.DELETE("/positions/managed/:id", positionController.HandleCloseManagedPosition)

		// Activity logging endpoints
		// Economic intelligence feeds (free, no API key required)
		api.GET("/feeds/treasury", economicFeedsController.HandleGetTreasury)
		api.GET("/feeds/gdelt", economicFeedsController.HandleGetGDELT)
		api.GET("/feeds/bls", economicFeedsController.HandleGetBLS)
		api.GET("/feeds/yfinance", economicFeedsController.HandleGetYFinance)
		api.GET("/feeds/usaspending", economicFeedsController.HandleGetUSASpending)
		api.GET("/feeds/comtrade", economicFeedsController.HandleGetComtrade)

		api.GET("/activity/current", activityController.HandleGetCurrentActivity)
		api.GET("/activity/:date", activityController.HandleGetActivityByDate)
		api.GET("/activity", activityController.HandleListActivityLogs)
		api.POST("/activity/session/start", activityController.HandleStartSession)
		api.POST("/activity/session/end", activityController.HandleEndSession)
		api.POST("/activity/log", activityController.HandleLogActivity)
	}

	// Serve dashboard
	router.Static("/dashboard", "./web")

	return router
}

func isAllowedOrigin(origin, allowedOrigins string) bool {
	if origin == "" {
		return false
	}

	if allowedOrigins != "" {
		for _, allowedOrigin := range strings.Split(allowedOrigins, ",") {
			if origin == strings.TrimSpace(allowedOrigin) {
				return true
			}
		}
		return false
	}

	parsedOrigin, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return parsedOrigin.Scheme == "http" && (parsedOrigin.Hostname() == "localhost" || parsedOrigin.Hostname() == "127.0.0.1")
}

// Background task to clean up old data
func startDataCleanup(ctx context.Context, storage interfaces.StorageService, retentionDays int, logger *logrus.Logger) {
	ticker := time.NewTicker(24 * time.Hour) // Run daily
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cutoff := time.Now().AddDate(0, 0, -retentionDays)
			logger.WithField("cutoff", cutoff).Info("Running data cleanup")

			if err := storage.CleanupOldData(cutoff); err != nil {
				logger.WithError(err).Error("Failed to cleanup old data")
			}
		}
	}
}

// Background task to monitor and save positions
func startPositionMonitor(ctx context.Context, orderController *controllers.OrderController, storage *database.LocalStorage, logger *logrus.Logger) {
	ticker := time.NewTicker(5 * time.Minute) // Check every 5 minutes
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Get current positions
			positions, err := orderController.GetPositions()
			if err != nil {
				logger.WithError(err).Error("Failed to get positions")
				continue
			}

			// Save position snapshots
			for _, position := range positions {
				if err := storage.SavePosition(position); err != nil {
					logger.WithError(err).Error("Failed to save position snapshot")
				}
			}

			// Get and save account snapshot
			if account, err := orderController.GetAccount(); err == nil {
				if err := storage.SaveAccountSnapshot(account); err != nil {
					logger.WithError(err).Error("Failed to save account snapshot")
				}
			}

			logger.WithField("positions", len(positions)).Debug("Position monitor update complete")
		}
	}
}
