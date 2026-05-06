package controllers

import (
	"context"
	"math"
	"prophet-trader/interfaces"
	"prophet-trader/services"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// OrderController handles trading operations
type OrderController struct {
	tradingService interfaces.TradingService
	dataService    interfaces.DataService
	storageService interfaces.StorageService
	guard          *services.TradeGuard
	logger         *logrus.Logger
}

// NewOrderController creates a new order controller
func NewOrderController(
	trading interfaces.TradingService,
	data interfaces.DataService,
	storage interfaces.StorageService,
) *OrderController {
	logger := logrus.New()
	logger.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})

	return &OrderController{
		tradingService: trading,
		dataService:    data,
		storageService: storage,
		logger:         logger,
	}
}

// SetGuard attaches a trade guard to the controller.
func (oc *OrderController) SetGuard(guard *services.TradeGuard) {
	oc.guard = guard
}

// BuyRequest represents a buy order request
type BuyRequest struct {
	Symbol      string              `json:"symbol" binding:"required"`
	Qty         float64             `json:"qty" binding:"required,gt=0"`
	Type        string              `json:"type"`         // "market", "limit", "stop", "stop_limit"
	TimeInForce string              `json:"time_in_force"` // "day", "gtc", "ioc", "fok"
	LimitPrice  *float64            `json:"limit_price,omitempty"`
	StopPrice   *float64            `json:"stop_price,omitempty"`
	AgentSource services.AgentSource `json:"agent_source,omitempty"` // "main" or "penny"; defaults to "main"
}

// SellRequest represents a sell order request
type SellRequest struct {
	Symbol      string              `json:"symbol" binding:"required"`
	Qty         float64             `json:"qty" binding:"required,gt=0"`
	Type        string              `json:"type"`         // "market", "limit", "stop", "stop_limit"
	TimeInForce string              `json:"time_in_force"` // "day", "gtc", "ioc", "fok"
	LimitPrice  *float64            `json:"limit_price,omitempty"`
	StopPrice   *float64            `json:"stop_price,omitempty"`
	AgentSource services.AgentSource `json:"agent_source,omitempty"` // "main" or "penny"; defaults to "main"
}

// Buy executes a buy order
func (oc *OrderController) Buy(ctx context.Context, req BuyRequest) (*interfaces.OrderResult, error) {
	// Set defaults
	if req.Type == "" {
		req.Type = "market"
	}
	if req.TimeInForce == "" {
		req.TimeInForce = "day"
	}
	agent := req.AgentSource
	if agent == "" {
		agent = services.AgentMain
	}

	// Trade guard check
	if oc.guard != nil {
		allocationDollars := 0.0
		if agent == services.AgentPenny {
			if quote, err := oc.dataService.GetLatestQuote(ctx, req.Symbol); err == nil {
				price := quote.AskPrice
				if price <= 0 {
					price = quote.BidPrice
				}
				allocationDollars = price * req.Qty
			}
		}
		if err := oc.guard.CheckBuy(ctx, agent, req.Symbol, allocationDollars); err != nil {
			oc.logger.WithError(err).Warn("Buy order blocked by trade guard")
			return nil, err
		}
	}

	oc.logger.WithFields(logrus.Fields{
		"symbol": req.Symbol,
		"qty":    req.Qty,
		"type":   req.Type,
	}).Info("Processing buy order")

	order := &interfaces.Order{
		Symbol:      req.Symbol,
		Qty:         req.Qty,
		Side:        "buy",
		Type:        req.Type,
		TimeInForce: req.TimeInForce,
		LimitPrice:  req.LimitPrice,
		StopPrice:   req.StopPrice,
		Status:      "pending",
		SubmittedAt: time.Now(),
	}

	// Place the order
	result, err := oc.tradingService.PlaceOrder(ctx, order)
	if err != nil {
		oc.logger.WithError(err).Error("Failed to place buy order")
		return nil, err
	}

	// Save order to database
	order.ID = result.OrderID
	order.Status = result.Status
	if err := oc.storageService.SaveOrder(order); err != nil {
		oc.logger.WithError(err).Warn("Failed to save order to database")
	}

	if oc.guard != nil {
		oc.guard.RecordRawBuy(agent, req.Symbol)
	}

	oc.logger.WithField("orderID", result.OrderID).Info("Buy order placed successfully")
	return result, nil
}

// Sell executes a sell order
func (oc *OrderController) Sell(ctx context.Context, req SellRequest) (*interfaces.OrderResult, error) {
	// Set defaults
	if req.Type == "" {
		req.Type = "market"
	}
	if req.TimeInForce == "" {
		req.TimeInForce = "day"
	}
	agent := req.AgentSource
	if agent == "" {
		agent = services.AgentMain
	}

	// Trade guard check
	if oc.guard != nil {
		if err := oc.guard.CheckSell(ctx, agent, req.Symbol); err != nil {
			oc.logger.WithError(err).Warn("Sell order blocked by trade guard")
			return nil, err
		}
	}

	oc.logger.WithFields(logrus.Fields{
		"symbol": req.Symbol,
		"qty":    req.Qty,
		"type":   req.Type,
	}).Info("Processing sell order")

	order := &interfaces.Order{
		Symbol:      req.Symbol,
		Qty:         req.Qty,
		Side:        "sell",
		Type:        req.Type,
		TimeInForce: req.TimeInForce,
		LimitPrice:  req.LimitPrice,
		StopPrice:   req.StopPrice,
		Status:      "pending",
		SubmittedAt: time.Now(),
	}

	// Place the order
	result, err := oc.tradingService.PlaceOrder(ctx, order)
	if err != nil {
		oc.logger.WithError(err).Error("Failed to place sell order")
		return nil, err
	}

	// Save order to database
	order.ID = result.OrderID
	order.Status = result.Status
	if err := oc.storageService.SaveOrder(order); err != nil {
		oc.logger.WithError(err).Warn("Failed to save order to database")
	}

	if oc.guard != nil {
		oc.guard.RecordRawSell(agent, req.Symbol)
	}

	oc.logger.WithField("orderID", result.OrderID).Info("Sell order placed successfully")
	return result, nil
}

// QuickBuy executes a simple market buy order
func (oc *OrderController) QuickBuy(symbol string, qty float64) (*interfaces.OrderResult, error) {
	return oc.Buy(context.Background(), BuyRequest{
		Symbol: symbol,
		Qty:    qty,
		Type:   "market",
	})
}

// QuickSell executes a simple market sell order
func (oc *OrderController) QuickSell(symbol string, qty float64) (*interfaces.OrderResult, error) {
	return oc.Sell(context.Background(), SellRequest{
		Symbol: symbol,
		Qty:    qty,
		Type:   "market",
	})
}

// CancelOrder cancels an existing order
func (oc *OrderController) CancelOrder(orderID string) error {
	ctx := context.Background()
	err := oc.tradingService.CancelOrder(ctx, orderID)
	if err != nil {
		oc.logger.WithError(err).Error("Failed to cancel order")
		return err
	}

	// Update order status in database
	if order, err := oc.storageService.GetOrder(orderID); err == nil {
		order.Status = "canceled"
		now := time.Now()
		order.CanceledAt = &now
		oc.storageService.SaveOrder(order)
	}

	oc.logger.WithField("orderID", orderID).Info("Order canceled successfully")
	return nil
}

// GetPositions retrieves current positions
func (oc *OrderController) GetPositions() ([]*interfaces.Position, error) {
	ctx := context.Background()
	return oc.tradingService.GetPositions(ctx)
}

// GetAccount retrieves account information
func (oc *OrderController) GetAccount() (*interfaces.Account, error) {
	ctx := context.Background()
	return oc.tradingService.GetAccount(ctx)
}

// HTTP Handlers for Gin framework

// HandleBuy handles HTTP buy requests
func (oc *OrderController) HandleBuy(c *gin.Context) {
	var req BuyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	result, err := oc.Buy(c.Request.Context(), req)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, result)
}

// HandleSell handles HTTP sell requests
func (oc *OrderController) HandleSell(c *gin.Context) {
	var req SellRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	result, err := oc.Sell(c.Request.Context(), req)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, result)
}

// HandleCancelOrder handles HTTP cancel order requests
func (oc *OrderController) HandleCancelOrder(c *gin.Context) {
	orderID := c.Param("id")
	if orderID == "" {
		c.JSON(400, gin.H{"error": "order ID required"})
		return
	}

	if err := oc.CancelOrder(orderID); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, gin.H{"message": "Order canceled successfully"})
}

// HandleGetPositions handles HTTP get positions requests
func (oc *OrderController) HandleGetPositions(c *gin.Context) {
	positions, err := oc.GetPositions()
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, positions)
}

// HandleGetAccount handles HTTP get account requests
func (oc *OrderController) HandleGetAccount(c *gin.Context) {
	account, err := oc.GetAccount()
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, account)
}

// HandleGetOrders handles HTTP get orders requests
func (oc *OrderController) HandleGetOrders(c *gin.Context) {
	status := c.Query("status")

	ctx := context.Background()
	orders, err := oc.tradingService.ListOrders(ctx, status)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, orders)
}

// HandleGetQuote handles HTTP get quote requests
// GET /api/v1/market/quote/:symbol
func (oc *OrderController) HandleGetQuote(c *gin.Context) {
	symbol := c.Param("symbol")
	if symbol == "" {
		c.JSON(400, gin.H{"error": "symbol required"})
		return
	}

	ctx := context.Background()
	quote, err := oc.dataService.GetLatestQuote(ctx, symbol)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, quote)
}

// HandleGetBar handles HTTP get latest bar requests
// GET /api/v1/market/bar/:symbol
func (oc *OrderController) HandleGetBar(c *gin.Context) {
	symbol := c.Param("symbol")
	if symbol == "" {
		c.JSON(400, gin.H{"error": "symbol required"})
		return
	}

	ctx := context.Background()
	bar, err := oc.dataService.GetLatestBar(ctx, symbol)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, bar)
}

// HandleGetBars handles HTTP get historical bars requests
// GET /api/v1/market/bars/:symbol?start=2025-01-01&end=2025-01-10&timeframe=1D
func (oc *OrderController) HandleGetBars(c *gin.Context) {
	symbol := c.Param("symbol")
	if symbol == "" {
		c.JSON(400, gin.H{"error": "symbol required"})
		return
	}

	// Parse query parameters
	startStr := c.Query("start")
	endStr := c.Query("end")
	timeframe := c.DefaultQuery("timeframe", "1D")

	// Default to last 30 days if not specified
	end := time.Now()
	start := end.AddDate(0, 0, -30)

	if startStr != "" {
		if t, err := time.Parse("2006-01-02", startStr); err == nil {
			start = t
		}
	}

	if endStr != "" {
		if t, err := time.Parse("2006-01-02", endStr); err == nil {
			end = t
		}
	}

	ctx := context.Background()
	bars, err := oc.dataService.GetHistoricalBars(ctx, symbol, start, end, timeframe)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, gin.H{
		"symbol":    symbol,
		"start":     start,
		"end":       end,
		"timeframe": timeframe,
		"count":     len(bars),
		"bars":      bars,
	})
}

// OptionsOrderRequest represents an options order request
type OptionsOrderRequest struct {
	Symbol        string   `json:"symbol" binding:"required"`
	Underlying    string   `json:"underlying"`
	Qty           float64  `json:"qty" binding:"required,gt=0"`
	Side          string   `json:"side" binding:"required,oneof=buy sell"`
	PositionIntent string  `json:"position_intent"` // "buy_to_open", "buy_to_close", "sell_to_open", "sell_to_close"
	Type          string   `json:"type"` // "market", "limit"
	TimeInForce   string   `json:"time_in_force"` // "day", "gtc"
	LimitPrice    *float64 `json:"limit_price,omitempty"`
}

// PlaceOptionsOrder handles POST /api/options/order
func (oc *OrderController) PlaceOptionsOrder(c *gin.Context) {
	var req OptionsOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	// Set defaults
	if req.Type == "" {
		req.Type = "market"
	}
	if req.TimeInForce == "" {
		req.TimeInForce = "day"
	}
	if req.PositionIntent == "" {
		if req.Side == "buy" {
			req.PositionIntent = "buy_to_open"
		} else {
			req.PositionIntent = "sell_to_close"
		}
	}

	order := &interfaces.OptionsOrder{
		Symbol:        req.Symbol,
		Underlying:    req.Underlying,
		Qty:           req.Qty,
		Side:          req.Side,
		PositionIntent: req.PositionIntent,
		Type:          req.Type,
		TimeInForce:   req.TimeInForce,
		LimitPrice:    req.LimitPrice,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := oc.tradingService.PlaceOptionsOrder(ctx, order)
	if err != nil {
		oc.logger.WithError(err).Error("Failed to place options order")
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, result)
}

// GetOptionsPosition handles GET /api/options/position/:symbol
func (oc *OrderController) GetOptionsPosition(c *gin.Context) {
	symbol := c.Param("symbol")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	position, err := oc.tradingService.GetOptionsPosition(ctx, symbol)
	if err != nil {
		c.JSON(404, gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, position)
}

// ListOptionsPositions handles GET /api/options/positions
func (oc *OrderController) ListOptionsPositions(c *gin.Context) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	positions, err := oc.tradingService.ListOptionsPositions(ctx)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, positions)
}

// GetOptionsChain handles GET /api/options/chain/:symbol?expiration=2025-11-22&delta_min=0.4&delta_max=0.6&min_bid=0.1
func (oc *OrderController) GetOptionsChain(c *gin.Context) {
	symbol := c.Param("symbol")
	if symbol == "" {
		c.JSON(400, gin.H{"error": "symbol required"})
		return
	}

	// Get expiration date from query parameter
	expirationStr := c.Query("expiration")
	var expiration time.Time
	var err error

	if expirationStr != "" {
		expiration, err = time.Parse("2006-01-02", expirationStr)
		if err != nil {
			c.JSON(400, gin.H{"error": "invalid expiration date format, use YYYY-MM-DD"})
			return
		}
	} else {
		// Default to next Friday (typical weekly options expiration)
		expiration = getNextFriday()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	chain, err := oc.tradingService.GetOptionsChain(ctx, symbol, expiration)
	if err != nil {
		oc.logger.WithError(err).Error("Failed to get options chain")
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	// Apply filters for token efficiency
	filtered := make([]*interfaces.OptionContract, 0)

	// Filter by delta range (absolute value for puts)
	deltaMinStr := c.Query("delta_min")
	deltaMaxStr := c.Query("delta_max")
	minBidStr := c.Query("min_bid")
	optionType := c.Query("type")

	// Parse filter values
	var deltaMin, deltaMax, minBid float64
	var hasMin, hasMax, hasMinBid bool

	if deltaMinStr != "" {
		if val, err := strconv.ParseFloat(deltaMinStr, 64); err == nil {
			deltaMin = val
			hasMin = true
		}
	}
	if deltaMaxStr != "" {
		if val, err := strconv.ParseFloat(deltaMaxStr, 64); err == nil {
			deltaMax = val
			hasMax = true
		}
	}
	if minBidStr != "" {
		if val, err := strconv.ParseFloat(minBidStr, 64); err == nil {
			minBid = val
			hasMinBid = true
		}
	}

	// Apply all filters in one pass
	for _, contract := range chain {
		// Skip contracts with zero Greeks (invalid/stale data)
		if contract.Delta == 0 && contract.Gamma == 0 && contract.Theta == 0 {
			continue
		}

		absDelta := math.Abs(contract.Delta)

		// Apply delta filters
		if hasMin && absDelta < deltaMin {
			continue
		}
		if hasMax && absDelta > deltaMax {
			continue
		}

		// Apply bid filter
		if hasMinBid && contract.Bid <= minBid {
			continue
		}

		// Apply option type filter
		if optionType == "call" && contract.Delta <= 0 {
			continue
		}
		if optionType == "put" && contract.Delta >= 0 {
			continue
		}

		filtered = append(filtered, contract)
	}

	c.JSON(200, gin.H{
		"symbol":     symbol,
		"expiration": expiration.Format("2006-01-02"),
		"total":      len(chain),
		"filtered":   len(filtered),
		"contracts":  filtered,
	})
}

// getNextFriday returns the date of the next Friday
func getNextFriday() time.Time {
	now := time.Now()
	daysUntilFriday := (int(time.Friday) - int(now.Weekday()) + 7) % 7
	if daysUntilFriday == 0 {
		daysUntilFriday = 7 // If today is Friday, get next Friday
	}
	return now.AddDate(0, 0, daysUntilFriday)
}