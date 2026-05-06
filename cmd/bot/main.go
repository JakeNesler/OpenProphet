package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"prophet-trader/config"
	"prophet-trader/controllers"
	"prophet-trader/database"
	"prophet-trader/interfaces"
	"prophet-trader/services"
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

	// Create Claude service and intelligence controller
	claudeService := services.NewClaudeService(cfg.ClaudeAPIKey)
	analysisService := services.NewTechnicalAnalysisService(dataService)
	stockAnalysisService := services.NewStockAnalysisService(dataService, newsService, claudeService)
	intelligenceController := controllers.NewIntelligenceController(newsService, claudeService, analysisService, stockAnalysisService, dataService)

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

	// Start background tasks
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create position manager
	positionManager := services.NewPositionManager(tradingService, dataService, storageService)
	positionController := controllers.NewPositionManagementController(positionManager)

	// Create trade guard and wire into both controllers
	tradeGuard := services.NewTradeGuard(
		positionManager,
		tradingService,
		services.TradeGuardConfig{
			PennyMaxCapitalPct:      cfg.PennyMaxCapitalPct,
			PennyMaxPositionDollars: cfg.PennyMaxPositionDollars,
			MaxDailyLossPct:         cfg.MaxDailyLossPct,
		},
	)
	positionManager.SetGuard(tradeGuard)
	orderController.SetGuard(tradeGuard)
	guardController := controllers.NewGuardController(tradeGuard)

	logger.WithFields(logrus.Fields{
		"penny_max_capital_pct":      cfg.PennyMaxCapitalPct,
		"penny_max_position_dollars": cfg.PennyMaxPositionDollars,
		"max_daily_loss_pct":         cfg.MaxDailyLossPct,
	}).Info("Trade guard initialized")

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

	// Initialize penny stock signal pipeline
	pennyUniverseService := services.NewPennyUniverseService(cfg.FMPAPIKey, cfg.AlpacaAPIKey, cfg.AlpacaSecretKey, cfg.AlpacaBaseURL, nil)
	pennyScreenerService := services.NewPennyScreenerService(cfg.AlpacaAPIKey, cfg.AlpacaSecretKey, pennyUniverseService)
	secEdgarService := services.NewSECEdgarService(pennyUniverseService, nil, cfg.OperatorEmail)
	socialSignalService := services.NewSocialSignalService(pennyUniverseService, nil)
	pennyAggregator := services.NewPennySignalAggregator(pennyUniverseService, pennyScreenerService, secEdgarService, socialSignalService)
	pennyController := controllers.NewPennyController(pennyAggregator)

	// Start penny pipeline goroutines
	go pennyUniverseService.Start(ctx)
	go pennyScreenerService.Start(ctx)
	go secEdgarService.Start(ctx)
	go socialSignalService.Start(ctx)
	go pennyAggregator.Start(ctx)

	logger.Info("Penny stock signal pipeline started")

	// Initialize Harvest services
	harvestIVRSvc := services.NewHarvestIVRService(storageService)
	harvestSvc := services.NewHarvestService(storageService)

	getPortfolioValue := func() (float64, error) {
		acct, err := orderController.GetAccount()
		if err != nil {
			return 0, err
		}
		return acct.PortfolioValue, nil
	}

	placeMLegFn := services.PlaceMultiLegOrderFn(func(ctx context.Context, order services.MultiLegOrder) (string, error) {
		if tradingService == nil {
			return "", fmt.Errorf("trading service unavailable")
		}
		return tradingService.PlaceMultiLegOrder(ctx, order)
	})

	harvestController := controllers.NewHarvestController(
		harvestSvc,
		harvestIVRSvc,
		storageService,
		placeMLegFn,
		getPortfolioValue,
	)

	// Start daily IV collection goroutine for Harvest
	go startHarvestIVCollection(ctx, harvestIVRSvc, tradingService, logger)

	logger.Info("Harvest service initialized")

	// Setup HTTP server
	router := setupRouter(orderController, newsController, intelligenceController, positionController, activityController, economicFeedsController, pennyController, guardController, harvestController)

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
	logger.WithField("port", cfg.ServerPort).Info("Starting HTTP server...")
	if err := router.Run(":" + cfg.ServerPort); err != nil {
		logger.Fatal("Failed to start server:", err)
	}
}

func setupRouter(orderController *controllers.OrderController, newsController *controllers.NewsController, intelligenceController *controllers.IntelligenceController, positionController *controllers.PositionManagementController, activityController *controllers.ActivityController, economicFeedsController *controllers.EconomicFeedsController, pennyController *controllers.PennyController, guardController *controllers.GuardController, harvestController *controllers.HarvestController) *gin.Engine {
	router := gin.Default()
	router.SetTrustedProxies([]string{"127.0.0.1"})

	// Enable CORS
	router.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
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

		// Penny stock signal endpoints
		api.GET("/penny/candidates", pennyController.HandleGetCandidates)
		api.GET("/penny/signal/:ticker", pennyController.HandleGetSignalDetail)
		api.GET("/penny/universe", pennyController.HandleGetUniverse)
		api.POST("/penny/scan", pennyController.HandleScanNow)
		api.DELETE("/penny/blacklist", pennyController.HandleClearBlacklist)
		api.DELETE("/penny/blacklist/:ticker", pennyController.HandleRemoveFromBlacklist)

		// Trade guard endpoint
		api.GET("/guard/status", guardController.HandleGetStatus)

		// Harvest premium seller endpoints
		harvest := api.Group("/harvest")
		{
			harvest.GET("/state", harvestController.HandleGetState)
			harvest.GET("/fomc", harvestController.HandleGetFOMC)
			harvest.GET("/expirations/:symbol", harvestController.HandleGetExpirations)
			harvest.GET("/ivr/:symbol", harvestController.HandleGetIVR)
			harvest.GET("/condors", harvestController.HandleListCondors)
			harvest.POST("/condors", harvestController.HandleOpenCondor)
			harvest.POST("/condors/:id/close", harvestController.HandleCloseCondor)
			harvest.POST("/iv", harvestController.HandleRecordIV)
		}
	}

	// Serve dashboard
	router.Static("/dashboard", "./web")

	return router
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

// startHarvestIVCollection records ATM IV for all Harvest underlyings once per trading day.
func startHarvestIVCollection(ctx context.Context, ivrSvc *services.HarvestIVRService, tradingService *services.AlpacaTradingService, logger *logrus.Logger) {
	harvestUniverse := []string{"SPY", "QQQ", "IWM", "GLD", "TLT"}
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()

	collectAll := func() {
		for _, symbol := range harvestUniverse {
			if tradingService == nil {
				continue
			}
			chain, err := tradingService.GetOptionsChain(ctx, symbol, time.Now().AddDate(0, 0, 30))
			if err != nil {
				logger.WithError(err).Warnf("harvest IV collection: failed to get chain for %s", symbol)
				continue
			}
			atmIV := calcATMIV(chain)
			if atmIV <= 0 {
				continue
			}
			if err := ivrSvc.RecordDailyIV(symbol, atmIV); err != nil {
				logger.WithError(err).Warnf("harvest IV collection: failed to record IV for %s", symbol)
			}
		}
	}

	collectAll() // run immediately on startup

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			collectAll()
		}
	}
}

// calcATMIV averages the IV of contracts with |delta| in [0.45, 0.55].
func calcATMIV(chain []*interfaces.OptionContract) float64 {
	var sum float64
	var count int
	for _, c := range chain {
		absDelta := c.Delta
		if absDelta < 0 {
			absDelta = -absDelta
		}
		if absDelta >= 0.45 && absDelta <= 0.55 && c.ImpliedVolatility > 0 {
			sum += c.ImpliedVolatility
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return sum / float64(count)
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
