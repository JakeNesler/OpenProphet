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
	logger.Debug("Initializing services...")

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

	// Create economic feeds service and controller.
	// EconCalendarService is optional: when FMP_API_KEY is unset, the
	// /api/v1/econ/blackout endpoint returns 503 and preflight fails open.
	economicFeedsService := services.NewEconomicFeedsService()
	var econCalendarService *services.EconCalendarService
	if cfg.FMPAPIKey != "" {
		econCalendarService = services.NewEconCalendarService(cfg.FMPAPIKey)
	} else {
		logger.Warn("FMP_API_KEY unset — econ blackout endpoint will return 503 and preflight will fail open")
	}
	economicFeedsController := controllers.NewEconomicFeedsController(economicFeedsService, econCalendarService)

	// Create AI news cleaner (provider selected via AI_PROVIDER env var or auto-detected)
	var aiService services.NewsCleanerService
	switch cfg.AIProvider {
	case "xai":
		aiService = services.NewXAIService(cfg.XAIAPIKey)
		logger.Info("AI news cleaning: using xAI (Grok)")
	default:
		aiService = services.NewClaudeService(cfg.ClaudeAPIKey)
		logger.Info("AI news cleaning: using Claude")
	}
	analysisService := services.NewTechnicalAnalysisService(dataService)
	stockAnalysisService := services.NewStockAnalysisService(dataService, newsService, aiService)
	intelligenceController := controllers.NewIntelligenceController(newsService, aiService, analysisService, stockAnalysisService, dataService)

	// Test account connection
	logger.Debug("Testing Alpaca connection...")
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

	// Cross-agent sector concentration caps. Values are fractions of portfolio value.
	// Buckets not listed here fall back to cfg.SectorDefaultMaxPct.
	sectorCaps := map[string]float64{
		"TECH":                   0.20,
		"INDEX_BETA":             0.25,
		"ENERGY":                 0.15,
		"FINANCIALS":             0.15,
		"HEALTHCARE":             0.15,
		"CONSUMER_DISCRETIONARY": 0.15,
		"STAPLES":                0.15,
		"INDUSTRIALS":            0.15,
		"UTILITIES":              0.15,
		"MATERIALS":              0.15,
		"REAL_ESTATE":            0.15,
		"COMMUNICATIONS":         0.15,
		"OTHER":                  0.15,
	}

	// Create trade guard and wire into both controllers
	tradeGuard := services.NewTradeGuard(
		positionManager,
		tradingService,
		services.TradeGuardConfig{
			PennyMaxCapitalPct:      cfg.PennyMaxCapitalPct,
			PennyMaxPositionDollars: cfg.PennyMaxPositionDollars,
			MaxDailyLossPct:         cfg.MaxDailyLossPct,
			EnableSectorAggregation: cfg.EnableSectorAggregation,
			SectorMaxExposurePct:    sectorCaps,
			DefaultSectorMaxPct:     cfg.SectorDefaultMaxPct,
		},
	)
	positionManager.SetGuard(tradeGuard)
	orderController.SetGuard(tradeGuard)
	guardController := controllers.NewGuardController(tradeGuard)

	logger.WithFields(logrus.Fields{
		"penny_max_capital_pct":      cfg.PennyMaxCapitalPct,
		"penny_max_position_dollars": cfg.PennyMaxPositionDollars,
		"max_daily_loss_pct":         cfg.MaxDailyLossPct,
		"sector_aggregation_enabled": cfg.EnableSectorAggregation,
		"sector_default_max_pct":     cfg.SectorDefaultMaxPct,
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
		logger.Debug("Activity logging session started")
	}

	// Initialize penny stock signal pipeline. Services are always constructed so
	// the HTTP controller stays non-nil; background goroutines + the FMP earnings
	// refresh are gated on ENABLE_PENNY_PIPELINE. Non-penny sandboxes (TrendProphet,
	// Harvest, Prophet) leave it unset, which skips the 60s scan loop and the
	// 40-day-bar warm-up that was generating the bulk of the IEX-fetch log noise.
	pennyPipelineEnabled := os.Getenv("ENABLE_PENNY_PIPELINE") == "true"

	earningsService := services.NewEarningsCalendarService(cfg.FMPAPIKey, cfg.AlpacaAPIKey, cfg.AlpacaSecretKey, cfg.AlpacaBaseURL, nil)
	if pennyPipelineEnabled {
		go earningsService.Start(ctx)
		if !earningsService.WaitForFirstRefresh(services.FirstRefreshWaitTimeout) {
			logger.Warn("earnings calendar first refresh did not complete within timeout — universe will start in fail-open mode")
		}
	}

	pennyUniverseService := services.NewPennyUniverseService(cfg.FMPAPIKey, cfg.AlpacaAPIKey, cfg.AlpacaSecretKey, cfg.AlpacaBaseURL, earningsService, nil)
	// Intraday context cache for the penny screener (ORB-15 capture + trailing
	// 20-day avg volume per ticker). Bounded HTTP cost: cache is per-ticker
	// per-session for ORB, per-ticker per-day for avg volume.
	pennyIntradayCache := services.NewPennyIntradayCache(dataService)
	pennyScreenerService := services.NewPennyScreenerService(cfg.AlpacaAPIKey, cfg.AlpacaSecretKey, pennyUniverseService, pennyIntradayCache)
	secEdgarService := services.NewSECEdgarService(pennyUniverseService, nil, cfg.OperatorEmail, earningsService)
	socialSignalService := services.NewSocialSignalService(pennyUniverseService, nil)
	pennyMaxFilter := services.NewPennyMaxFilterService(pennyUniverseService, dataService)
	pennyAggregator := services.NewPennySignalAggregator(pennyUniverseService, pennyScreenerService, secEdgarService, socialSignalService, pennyMaxFilter)
	pennyController := controllers.NewPennyController(pennyAggregator)

	// Wire dilution filter to operator-visible held-position logging.
	secEdgarService.SetHeldTickersFn(positionManager.HeldPennyTickers)

	if pennyPipelineEnabled {
		go pennyUniverseService.Start(ctx)
		go pennyScreenerService.Start(ctx)
		go secEdgarService.Start(ctx)
		go socialSignalService.Start(ctx)
		go pennyMaxFilter.Start(ctx)
		go pennyAggregator.Start(ctx)
		logger.Debug("Penny stock signal pipeline started")
	} else {
		logger.Info("Penny pipeline disabled (ENABLE_PENNY_PIPELINE != true) — endpoints return empty")
	}

	// Initialize Harvest services
	harvestIVRSvc := services.NewHarvestIVRService(storageService)
	harvestSvc := services.NewHarvestService(storageService)

	// Wire the IV provider into StockAnalysisService so analyze_stocks
	// responses include per-symbol IV rank / percentile / days_of_history.
	// Safe to set after construction; the field is read inside AnalyzeStock.
	stockAnalysisService.SetIVProvider(harvestIVRSvc)

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

	// Realized-vol service used to compute the IV–RV spread that gates
	// Harvest condor entries. Wired into both HarvestController (legacy
	// harvest/ivr route) and IVController (generic iv/:symbol route).
	realizedVolSvc := services.NewRealizedVolService(dataService)

	harvestController := controllers.NewHarvestController(
		harvestSvc,
		harvestIVRSvc,
		realizedVolSvc,
		storageService,
		placeMLegFn,
		getPortfolioValue,
	)

	// Start daily IV collection goroutine for Harvest
	go startHarvestIVCollection(ctx, harvestIVRSvc, tradingService, logger)

	logger.Debug("Harvest service initialized")

	// Initialize Trend signal service (used by TrendProphet for daily-bar signals)
	trendSignalSvc := services.NewTrendSignalService(dataService)
	trendController := controllers.NewTrendController(trendSignalSvc)
	logger.Debug("Trend signal service initialized")

	// Initialize Segment P&L service (used by segment-scoped circuit breakers)
	segmentPnLSvc := services.NewSegmentPnLService(storageService, tradingService)
	segmentPnLController := controllers.NewSegmentPnLController(segmentPnLSvc)
	logger.Debug("Segment P&L service initialized")

	// Generic IV-rank controller (shared by Harvest and Prophet via /api/v1/iv/:symbol).
	// rvSvc enriches the response with realized_vol_20d + iv_minus_rv.
	ivController := controllers.NewIVController(harvestIVRSvc, realizedVolSvc)
	logger.Debug("IV controller initialized")

	// Intraday signal service + controller (auto-pushed into Prophet beats,
	// also available on-demand via the get_intraday_signals MCP tool)
	intradaySignalSvc := services.NewIntradaySignalService(dataService)
	intradayController := controllers.NewIntradayController(intradaySignalSvc)
	logger.Debug("Intraday signal service initialized")

	// Setup HTTP server
	router := setupRouter(orderController, newsController, intelligenceController, positionController, activityController, economicFeedsController, pennyController, guardController, harvestController, trendController, segmentPnLController, ivController, intradayController)

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

func setupRouter(orderController *controllers.OrderController, newsController *controllers.NewsController, intelligenceController *controllers.IntelligenceController, positionController *controllers.PositionManagementController, activityController *controllers.ActivityController, economicFeedsController *controllers.EconomicFeedsController, pennyController *controllers.PennyController, guardController *controllers.GuardController, harvestController *controllers.HarvestController, trendController *controllers.TrendController, segmentPnLController *controllers.SegmentPnLController, ivController *controllers.IVController, intradayController *controllers.IntradayController) *gin.Engine {
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

		// US-economic-release blackout window (shared across all four agents)
		api.GET("/econ/blackout", economicFeedsController.HandleGetEconBlackout)

		// Generic IV rank/percentile lookup (latest stored snapshot per symbol)
		api.GET("/iv/:symbol", ivController.HandleGetIV)

		// Intraday signals (compact per-symbol blob; cached 60s)
		api.GET("/intraday/signals", intradayController.HandleGetSignals)

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

		trend := api.Group("/trend")
		{
			trend.GET("/signal/:symbol", trendController.HandleGetSignal)
		}

		api.GET("/segment-pnl/:strategy", segmentPnLController.HandleGetSegmentPnL)
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

// startHarvestIVCollection records ATM IV for Harvest + Prophet underlyings
// every 6h. Despite the name, the universe now spans both strategies: SPY/QQQ
// are shared, IWM/GLD/TLT are Harvest-specific, and NVDA/AMD/TSLA/MSTR are
// Prophet-specific. The function name is unchanged to keep this diff small;
// the IV data is consumed by both HarvestIVRService.GetIVRData (for Harvest's
// IVR ≥ 30 entry filter) and StockAnalysisService (for Prophet's IV-rank gate).
func startHarvestIVCollection(ctx context.Context, ivrSvc *services.HarvestIVRService, tradingService *services.AlpacaTradingService, logger *logrus.Logger) {
	ivUniverse := []string{
		"SPY", "QQQ", "IWM", "GLD", "TLT", // Harvest condor underlyings
		"NVDA", "AMD", "TSLA", "MSTR", // Prophet-specific watchlist
	}
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()

	collectAll := func() {
		for _, symbol := range ivUniverse {
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

// Background task to monitor and save account snapshots.
//
// Position snapshots were previously written here but the DBPosition schema's
// uniqueIndex on Symbol made every save after the first per symbol error with
// `UNIQUE constraint failed: positions.symbol`. Nothing in the codebase reads
// the positions table, so the save was removed rather than fixed. Position
// history is available via DBOrder + Alpaca's live positions endpoint.
func startPositionMonitor(ctx context.Context, orderController *controllers.OrderController, storage *database.LocalStorage, logger *logrus.Logger) {
	ticker := time.NewTicker(5 * time.Minute) // Check every 5 minutes
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if account, err := orderController.GetAccount(); err == nil {
				if err := storage.SaveAccountSnapshot(account); err != nil {
					logger.WithError(err).Error("Failed to save account snapshot")
				}
			}
		}
	}
}
