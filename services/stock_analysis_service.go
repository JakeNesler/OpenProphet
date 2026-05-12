package services

import (
	"context"
	"fmt"
	"math"
	"prophet-trader/interfaces"
	"time"

	"github.com/sirupsen/logrus"
)

// IVProvider is the narrow interface StockAnalysisService uses to enrich
// analyses with IV-rank context. Set via SetIVProvider; when nil, IV fields
// are omitted from the response. In production this is wired to
// HarvestIVRService (the daily collection now covers both Harvest and Prophet
// underlyings).
type IVProvider interface {
	GetIVRDataLatest(underlying string) (*IVRData, error)
}

// StockAnalysisService provides comprehensive stock analysis
type StockAnalysisService struct {
	dataService   interfaces.DataService
	newsService   *NewsService
	claudeService NewsCleanerService
	ivProvider    IVProvider
	logger        *logrus.Logger
}

// NewStockAnalysisService creates a new stock analysis service
func NewStockAnalysisService(dataService interfaces.DataService, newsService *NewsService, claudeService NewsCleanerService) *StockAnalysisService {
	logger := logrus.New()
	logger.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})

	return &StockAnalysisService{
		dataService:   dataService,
		newsService:   newsService,
		claudeService: claudeService,
		logger:        logger,
	}
}

// SetIVProvider wires the optional IV-rank provider. Idempotent; pass nil to
// disable enrichment. Adds ~30 tokens per AnalyzeStock response when wired.
func (sas *StockAnalysisService) SetIVProvider(p IVProvider) {
	sas.ivProvider = p
}

// StockAnalysis represents comprehensive analysis of a stock
type StockAnalysis struct {
	Symbol          string                 `json:"symbol"`
	CurrentPrice    float64                `json:"current_price"`
	MarketCap       string                 `json:"market_cap_estimate"`
	Technical       TechnicalAnalysis      `json:"technical"`
	NewsSummary     string                 `json:"news_summary"` // Just summary, not full articles
	TradeSetup      TradeSetup             `json:"trade_setup"`
	IV              *IVSummary             `json:"iv,omitempty"` // optional IV context (Prophet's IV-rank gate)
	Timestamp       time.Time              `json:"timestamp"`
}

// IVSummary is the per-symbol IV context exposed to the LLM. Rank and
// Percentile are -1 when the symbol has no recorded history yet (newly
// added to the collection universe); DaysOfHistory makes that explicit.
// Rules layer (TRADING_RULES_V2.md) treats DaysOfHistory < 20 as
// low-confidence regardless of Rank.
type IVSummary struct {
	Rank          float64 `json:"rank"`            // 0..100 or -1 if no history
	Percentile    float64 `json:"percentile"`      // 0..100 or -1 if no history
	DaysOfHistory int     `json:"days_of_history"` // 0 means symbol just added
}

// TechnicalAnalysis contains technical indicators
type TechnicalAnalysis struct {
	Price         float64  `json:"price"`
	DayChange     float64  `json:"day_change_percent"`
	Volume        int64    `json:"volume"`
	AvgVolume     int64    `json:"avg_volume_30d"`
	VolumeRatio   float64  `json:"volume_ratio"` // Current vs avg
	Trend         string   `json:"trend"` // "BULLISH", "BEARISH", "NEUTRAL"
	Support       float64  `json:"support_level"`
	Resistance    float64  `json:"resistance_level"`
	Volatility    float64  `json:"volatility_30d"`
	RSI           float64  `json:"rsi_14"` // 0-100
	PriceStrength string   `json:"price_strength"` // "OVERSOLD", "NEUTRAL", "OVERBOUGHT"
}

// TradeSetup provides neutral trading data for AI interpretation
type TradeSetup struct {
	// Price Levels (NEUTRAL - just data)
	Entry          float64  `json:"entry"`          // Current price
	StopLoss       float64  `json:"stop_loss"`      // Suggested -15% stop
	TakeProfit     float64  `json:"take_profit"`    // Suggested +30% target
	RiskReward     float64  `json:"risk_reward"`    // Ratio

	// Catalysts (NEUTRAL - just facts)
	RecentNews     []string `json:"recent_news"`    // Headlines only
	KeyCatalysts   []string `json:"key_catalysts"`  // Factual catalysts

	// Scoring (NEUTRAL - numerical only)
	TechnicalScore int      `json:"technical_score"` // 0-10 based on indicators
	CatalystScore  int      `json:"catalyst_score"`  // 0-10 based on news recency/quality
	VolumeScore    int      `json:"volume_score"`    // 0-10 based on volume ratio

	// Overall (NEUTRAL - composite)
	CompositeScore int      `json:"composite_score"` // 0-10 (avg of above)

	// Notes (FACTUAL - no recommendation)
	Notes          string   `json:"notes"`           // Factual observations only
}

// AnalyzeStocks analyzes multiple stocks and returns comprehensive analysis
func (sas *StockAnalysisService) AnalyzeStocks(ctx context.Context, symbols []string) (map[string]*StockAnalysis, error) {
	sas.logger.WithField("symbols", symbols).Info("Starting comprehensive stock analysis")

	results := make(map[string]*StockAnalysis)

	for _, symbol := range symbols {
		analysis, err := sas.AnalyzeStock(ctx, symbol)
		if err != nil {
			sas.logger.WithError(err).WithField("symbol", symbol).Warn("Failed to analyze stock")
			continue
		}
		results[symbol] = analysis
	}

	return results, nil
}

// AnalyzeStock provides comprehensive analysis for a single stock
func (sas *StockAnalysisService) AnalyzeStock(ctx context.Context, symbol string) (*StockAnalysis, error) {
	analysis := &StockAnalysis{
		Symbol:    symbol,
		Timestamp: time.Now(),
	}

	// Get current quote
	quote, err := sas.dataService.GetLatestQuote(ctx, symbol)
	if err != nil {
		return nil, fmt.Errorf("failed to get quote: %w", err)
	}
	analysis.CurrentPrice = quote.BidPrice

	// Get latest bar for volume
	bar, err := sas.dataService.GetLatestBar(ctx, symbol)
	if err == nil {
		analysis.Technical.Price = bar.Close
		analysis.Technical.Volume = bar.Volume
	} else {
		analysis.Technical.Price = quote.BidPrice
	}

	// Get historical data for technical analysis (30 days)
	endTime := time.Now()
	startTime := endTime.AddDate(0, 0, -30)

	bars, err := sas.dataService.GetHistoricalBars(ctx, symbol, startTime, endTime, "1Day")
	if err == nil && len(bars) > 0 {
		analysis.Technical = sas.calculateTechnicalIndicators(bars)
	} else {
		// Minimal analysis without historical data
		analysis.Technical.Price = quote.BidPrice
		analysis.Technical.Trend = "UNKNOWN"
		analysis.Technical.PriceStrength = "UNKNOWN"
	}

	// Estimate market cap range
	analysis.MarketCap = sas.estimateMarketCap(analysis.Technical.Price, symbol)

	// Get recent news (summarize to save tokens)
	newsSummary := ""
	catalysts := []string{}
	news, err := sas.newsService.GetGoogleNewsSearch(symbol)
	if err == nil && len(news) > 0 {
		// Get top 3 most recent headlines only
		limit := 3
		if len(news) < limit {
			limit = len(news)
		}
		for i := 0; i < limit; i++ {
			catalysts = append(catalysts, news[i].Title)
		}
		if len(news) > 0 {
			newsSummary = fmt.Sprintf("%d recent articles (past 48h)", len(news))
		}
	}
	analysis.NewsSummary = newsSummary

	// Generate NEUTRAL trade setup (no recommendations, just data)
	analysis.TradeSetup = sas.generateTradeSetup(analysis.Technical, catalysts, analysis.CurrentPrice)

	// Optional IV-rank enrichment for the Prophet IV gate. No-op when the
	// provider is unset or the symbol is outside the IV-collection universe.
	enrichAnalysisWithIV(analysis, sas.ivProvider, symbol)

	return analysis, nil
}

// enrichAnalysisWithIV attaches the IVSummary to an analysis when the
// provider returns usable data. Provider error or nil response leaves
// analysis.IV unchanged. A zero-history response attaches an explicit
// sentinel so the LLM can see "symbol on file, not yet warmed up".
func enrichAnalysisWithIV(analysis *StockAnalysis, provider IVProvider, symbol string) {
	if analysis == nil || provider == nil {
		return
	}
	d, err := provider.GetIVRDataLatest(symbol)
	if err != nil || d == nil {
		return
	}
	analysis.IV = &IVSummary{
		Rank:          d.IVR,
		Percentile:    d.IVPercentile,
		DaysOfHistory: d.DaysOfHistory,
	}
}

// calculateTechnicalIndicators calculates technical indicators from historical bars
func (sas *StockAnalysisService) calculateTechnicalIndicators(bars []*interfaces.Bar) TechnicalAnalysis {
	if len(bars) == 0 {
		return TechnicalAnalysis{}
	}

	latest := bars[len(bars)-1]
	tech := TechnicalAnalysis{
		Price:  latest.Close,
		Volume: latest.Volume,
	}

	// Calculate day change
	if len(bars) > 1 {
		prevClose := bars[len(bars)-2].Close
		tech.DayChange = ((latest.Close - prevClose) / prevClose) * 100
	}

	// Calculate average volume
	totalVolume := int64(0)
	for _, bar := range bars {
		totalVolume += bar.Volume
	}
	tech.AvgVolume = totalVolume / int64(len(bars))

	if tech.AvgVolume > 0 {
		tech.VolumeRatio = float64(latest.Volume) / float64(tech.AvgVolume)
	}

	// Calculate support and resistance (30-day high/low)
	high := bars[0].High
	low := bars[0].Low
	for _, bar := range bars {
		if bar.High > high {
			high = bar.High
		}
		if bar.Low < low {
			low = bar.Low
		}
	}
	tech.Resistance = high
	tech.Support = low

	// Calculate volatility (standard deviation of daily returns)
	if len(bars) > 1 {
		returns := make([]float64, len(bars)-1)
		for i := 1; i < len(bars); i++ {
			returns[i-1] = (bars[i].Close - bars[i-1].Close) / bars[i-1].Close
		}
		tech.Volatility = sas.standardDeviation(returns) * 100 // Convert to percentage
	}

	// Calculate RSI (14-period)
	if len(bars) >= 14 {
		tech.RSI = sas.calculateRSI(bars, 14)

		// Determine price strength
		if tech.RSI < 30 {
			tech.PriceStrength = "OVERSOLD"
		} else if tech.RSI > 70 {
			tech.PriceStrength = "OVERBOUGHT"
		} else {
			tech.PriceStrength = "NEUTRAL"
		}
	}

	// Determine trend
	if len(bars) >= 10 {
		// Simple trend: compare current price to 10-day average
		sum := 0.0
		for i := len(bars) - 10; i < len(bars); i++ {
			sum += bars[i].Close
		}
		avg10 := sum / 10.0

		if latest.Close > avg10*1.05 {
			tech.Trend = "BULLISH"
		} else if latest.Close < avg10*0.95 {
			tech.Trend = "BEARISH"
		} else {
			tech.Trend = "NEUTRAL"
		}
	}

	return tech
}

// calculateRSI calculates the Relative Strength Index
func (sas *StockAnalysisService) calculateRSI(bars []*interfaces.Bar, period int) float64 {
	if len(bars) < period+1 {
		return 50.0 // Default neutral
	}

	gains := 0.0
	losses := 0.0

	// Calculate initial average gain/loss
	for i := len(bars) - period; i < len(bars); i++ {
		change := bars[i].Close - bars[i-1].Close
		if change > 0 {
			gains += change
		} else {
			losses += -change
		}
	}

	avgGain := gains / float64(period)
	avgLoss := losses / float64(period)

	if avgLoss == 0 {
		return 100.0
	}

	rs := avgGain / avgLoss
	rsi := 100 - (100 / (1 + rs))

	return rsi
}

// standardDeviation calculates standard deviation of a slice of floats
func (sas *StockAnalysisService) standardDeviation(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}

	// Calculate mean
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	mean := sum / float64(len(values))

	// Calculate variance
	variance := 0.0
	for _, v := range values {
		diff := v - mean
		variance += diff * diff
	}
	variance /= float64(len(values))

	return math.Sqrt(variance)
}

// estimateMarketCap provides rough market cap estimate based on symbol and price
func (sas *StockAnalysisService) estimateMarketCap(price float64, symbol string) string {
	// This is a rough estimate - in production you'd want to fetch actual market cap
	// For now, provide general ranges to help classify stocks

	// Very rough heuristic based on common patterns
	if price < 5 {
		return "Small-cap (likely $300M-$3B)"
	} else if price < 50 {
		return "Small to Mid-cap (likely $500M-$10B)"
	} else if price < 200 {
		return "Mid to Large-cap (likely $3B-$50B)"
	} else {
		return "Large-cap (likely $50B+)"
	}
}

// generateTradeSetup creates neutral trade setup data for AI interpretation
func (sas *StockAnalysisService) generateTradeSetup(tech TechnicalAnalysis, catalysts []string, currentPrice float64) TradeSetup {
	setup := TradeSetup{
		Entry:        currentPrice,
		StopLoss:     currentPrice * 0.85,  // Default 15% stop
		TakeProfit:   currentPrice * 1.30,  // Default 30% target
		RiskReward:   2.0,
		RecentNews:   catalysts,
		KeyCatalysts: catalysts,
	}

	// Calculate NEUTRAL scores (0-10) based on data only

	// Technical Score (0-10)
	technicalScore := 5 // Start neutral
	if tech.Trend == "BULLISH" {
		technicalScore += 2
	} else if tech.Trend == "BEARISH" {
		technicalScore -= 2
	}
	if tech.RSI > 30 && tech.RSI < 70 {
		technicalScore += 1 // Healthy RSI range
	}
	if tech.RSI < 30 {
		technicalScore += 2 // Oversold opportunity
	}
	if tech.RSI > 75 {
		technicalScore -= 2 // Overbought risk
	}
	if tech.Volatility > 15 {
		technicalScore += 1 // High volatility = opportunity for small-caps
	}
	setup.TechnicalScore = maxInt(0, minInt(10, technicalScore))

	// Volume Score (0-10)
	volumeScore := 5 // Start neutral
	if tech.VolumeRatio > 2.0 {
		volumeScore = 9 // Very high volume
	} else if tech.VolumeRatio > 1.5 {
		volumeScore = 7 // Good volume
	} else if tech.VolumeRatio > 1.0 {
		volumeScore = 6 // Above average
	} else if tech.VolumeRatio > 0.5 {
		volumeScore = 4 // Below average
	} else {
		volumeScore = 2 // Very low volume
	}
	setup.VolumeScore = volumeScore

	// Catalyst Score (0-10) based on news recency
	catalystScore := 5 // Start neutral
	if len(catalysts) > 5 {
		catalystScore = 8 // Lots of recent news
	} else if len(catalysts) > 2 {
		catalystScore = 7 // Moderate news
	} else if len(catalysts) > 0 {
		catalystScore = 6 // Some news
	} else {
		catalystScore = 3 // No news
	}
	setup.CatalystScore = catalystScore

	// Composite Score (simple average)
	setup.CompositeScore = (setup.TechnicalScore + setup.VolumeScore + setup.CatalystScore) / 3

	// Factual notes only
	notes := fmt.Sprintf("Trend: %s | RSI: %.0f (%s) | Vol: %.1fx avg | Volatility: %.1f%%",
		tech.Trend, tech.RSI, tech.PriceStrength, tech.VolumeRatio, tech.Volatility)
	setup.Notes = notes

	return setup
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
