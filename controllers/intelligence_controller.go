package controllers

import (
	"context"
	"net/http"
	"prophet-trader/interfaces"
	"prophet-trader/services"
	"time"

	"github.com/gin-gonic/gin"
)

// IntelligenceController handles AI-powered intelligence operations
type IntelligenceController struct {
	newsService          *services.NewsService
	claudeService        services.NewsCleanerService
	analysisService      *services.TechnicalAnalysisService
	stockAnalysisService *services.StockAnalysisService
	dataService          interfaces.DataService
}

// NewIntelligenceController creates a new intelligence controller
func NewIntelligenceController(newsService *services.NewsService, aiService services.NewsCleanerService, analysisService *services.TechnicalAnalysisService, stockAnalysisService *services.StockAnalysisService, dataService interfaces.DataService) *IntelligenceController {
	return &IntelligenceController{
		newsService:          newsService,
		claudeService:        aiService,
		analysisService:      analysisService,
		stockAnalysisService: stockAnalysisService,
		dataService:          dataService,
	}
}

// AggregateNewsRequest represents a request to aggregate news from multiple sources
type AggregateNewsRequest struct {
	IncludeGoogle        bool     `json:"include_google"`
	IncludeMarketWatch   bool     `json:"include_marketwatch"`
	GoogleTopics         []string `json:"google_topics"`           // BUSINESS, TECHNOLOGY, etc.
	Symbols              []string `json:"symbols"`                 // Stock symbols to search for
	MaxArticlesPerSource int      `json:"max_articles_per_source"` // Default 10
}

// HandleGetCleanedNews aggregates news from multiple sources and returns a cleaned summary
// POST /api/v1/intelligence/cleaned-news
func (ic *IntelligenceController) HandleGetCleanedNews(c *gin.Context) {
	var req AggregateNewsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid request",
			"details": err.Error(),
		})
		return
	}

	// Set defaults
	if !req.IncludeGoogle && !req.IncludeMarketWatch {
		req.IncludeGoogle = true
		req.IncludeMarketWatch = true
	}
	if req.MaxArticlesPerSource == 0 {
		req.MaxArticlesPerSource = 25
	}

	// Aggregate news from all requested sources
	allNews := make([]services.NewsItem, 0)

	// Fetch from Google News
	if req.IncludeGoogle {
		// Fetch by topics
		for _, topic := range req.GoogleTopics {
			if news, err := ic.newsService.GetGoogleNewsByTopic(topic); err == nil {
				limit := min(len(news), req.MaxArticlesPerSource)
				allNews = append(allNews, news[:limit]...)
			}
		}

		// Fetch by symbols
		for _, symbol := range req.Symbols {
			if news, err := ic.newsService.GetGoogleNewsSearch(symbol); err == nil {
				limit := min(len(news), req.MaxArticlesPerSource)
				allNews = append(allNews, news[:limit]...)
			}
		}

		// If no specific topics or symbols, get general business news
		if len(req.GoogleTopics) == 0 && len(req.Symbols) == 0 {
			if news, err := ic.newsService.GetGoogleNewsByTopic("BUSINESS"); err == nil {
				limit := min(len(news), req.MaxArticlesPerSource)
				allNews = append(allNews, news[:limit]...)
			}
		}
	}

	// Fetch from MarketWatch
	if req.IncludeMarketWatch {
		if news, err := ic.newsService.GetAllMarketWatchNews(); err == nil {
			limit := min(len(news), req.MaxArticlesPerSource*4) // Get from all 4 feeds
			allNews = append(allNews, news[:limit]...)
		}
	}

	if len(allNews) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"message":      "No news found",
			"cleaned_news": nil,
		})
		return
	}

	// Clean the news using Claude
	cleanedNews, err := ic.claudeService.CleanNewsForTrading(allNews)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to clean news",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"cleaned_news":      cleanedNews,
		"raw_article_count": len(allNews),
	})
}

// HandleGetQuickMarketIntelligence provides a quick market overview
// GET /api/v1/intelligence/quick-market
func (ic *IntelligenceController) HandleGetQuickMarketIntelligence(c *gin.Context) {
	// Get latest from MarketWatch (fastest, most relevant)
	allNews := make([]services.NewsItem, 0)

	// Get top stories
	if news, err := ic.newsService.GetMarketWatchTopStories(); err == nil {
		allNews = append(allNews, news[:min(5, len(news))]...)
	}

	// Get bulletins
	if news, err := ic.newsService.GetMarketWatchBulletins(); err == nil {
		allNews = append(allNews, news[:min(5, len(news))]...)
	}

	// Get market pulse
	if news, err := ic.newsService.GetMarketWatchMarketPulse(); err == nil {
		allNews = append(allNews, news[:min(5, len(news))]...)
	}

	if len(allNews) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"message": "No news found",
		})
		return
	}

	// Clean the news
	cleanedNews, err := ic.claudeService.CleanNewsForTrading(allNews)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to generate intelligence",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, cleanedNews)
}

// HandleAnalyzeStock provides comprehensive analysis for a single stock
// GET /api/v1/intelligence/analyze/:symbol
func (ic *IntelligenceController) HandleAnalyzeStock(c *gin.Context) {
	symbol := c.Param("symbol")
	if symbol == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "symbol required",
		})
		return
	}

	// Add timeout to prevent indefinite hangs
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	analysis, err := ic.stockAnalysisService.AnalyzeStock(ctx, symbol)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to analyze stock",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, analysis)
}

// AnalyzeStocksRequest represents a request to analyze multiple stocks
type AnalyzeStocksRequest struct {
	Symbols []string `json:"symbols" binding:"required"`
}

// HandleAnalyzeMultipleStocks provides comprehensive analysis for multiple stocks
// POST /api/v1/intelligence/analyze-multiple
func (ic *IntelligenceController) HandleAnalyzeMultipleStocks(c *gin.Context) {
	var req AnalyzeStocksRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid request",
			"details": err.Error(),
		})
		return
	}

	if len(req.Symbols) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "At least one symbol required",
		})
		return
	}

	// Add timeout to prevent indefinite hangs
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	analyses, err := ic.stockAnalysisService.AnalyzeStocks(ctx, req.Symbols)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to analyze stocks",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"analyses": analyses,
		"count":    len(analyses),
	})
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
