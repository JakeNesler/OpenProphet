package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"prophet-trader/interfaces"
	"time"

	"github.com/alpacahq/alpaca-trade-api-go/v3/alpaca"
	"github.com/alpacahq/alpaca-trade-api-go/v3/marketdata"
	"github.com/shopspring/decimal"
	"github.com/sirupsen/logrus"
)

// AlpacaTradingService implements TradingService using Alpaca API
type AlpacaTradingService struct {
	client     *alpaca.Client
	dataClient *marketdata.Client
	apiKey     string
	apiSecret  string
	logger     *logrus.Logger
}

// NewAlpacaTradingService creates a new Alpaca trading service
func NewAlpacaTradingService(apiKey, secretKey, baseURL string, isPaper bool) (*AlpacaTradingService, error) {
	client := alpaca.NewClient(alpaca.ClientOpts{
		APIKey:    apiKey,
		APISecret: secretKey,
		BaseURL:   baseURL,
	})

	// Create data client
	dataClient := marketdata.NewClient(marketdata.ClientOpts{
		APIKey:    apiKey,
		APISecret: secretKey,
	})

	logger := logrus.New()
	logger.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})

	return &AlpacaTradingService{
		client:     client,
		dataClient: dataClient,
		apiKey:     apiKey,
		apiSecret:  secretKey,
		logger:     logger,
	}, nil
}

// PlaceOrder places a new order
func (s *AlpacaTradingService) PlaceOrder(ctx context.Context, order *interfaces.Order) (*interfaces.OrderResult, error) {
	qty := decimal.NewFromFloat(order.Qty)
	req := alpaca.PlaceOrderRequest{
		Symbol:      order.Symbol,
		Qty:         &qty,
		Side:        alpaca.Side(order.Side),
		Type:        alpaca.OrderType(order.Type),
		TimeInForce: alpaca.TimeInForce(order.TimeInForce),
	}

	if order.LimitPrice != nil {
		limitPrice := decimal.NewFromFloat(*order.LimitPrice)
		req.LimitPrice = &limitPrice
	}

	if order.StopPrice != nil {
		stopPrice := decimal.NewFromFloat(*order.StopPrice)
		req.StopPrice = &stopPrice
	}

	s.logger.WithFields(logrus.Fields{
		"symbol": order.Symbol,
		"side":   order.Side,
		"qty":    order.Qty,
		"type":   order.Type,
	}).Info("Placing order")

	alpacaOrder, err := s.client.PlaceOrder(req)
	if err != nil {
		s.logger.WithError(err).Error("Failed to place order")
		return nil, fmt.Errorf("failed to place order: %w", err)
	}

	return &interfaces.OrderResult{
		OrderID: alpacaOrder.ID,
		Status:  string(alpacaOrder.Status),
		Message: fmt.Sprintf("Order placed successfully: %s %v shares of %s", order.Side, order.Qty, order.Symbol),
	}, nil
}

// CancelOrder cancels an existing order
func (s *AlpacaTradingService) CancelOrder(ctx context.Context, orderID string) error {
	s.logger.WithField("orderID", orderID).Info("Canceling order")

	err := s.client.CancelOrder(orderID)
	if err != nil {
		s.logger.WithError(err).Error("Failed to cancel order")
		return fmt.Errorf("failed to cancel order: %w", err)
	}

	return nil
}

// GetOrder retrieves a specific order
func (s *AlpacaTradingService) GetOrder(ctx context.Context, orderID string) (*interfaces.Order, error) {
	alpacaOrder, err := s.client.GetOrder(orderID)
	if err != nil {
		return nil, fmt.Errorf("failed to get order: %w", err)
	}

	return s.convertAlpacaOrder(alpacaOrder), nil
}

// ListOrders retrieves orders with optional status filter
func (s *AlpacaTradingService) ListOrders(ctx context.Context, status string) ([]*interfaces.Order, error) {
	req := alpaca.GetOrdersRequest{
		Limit: 500,
	}

	if status != "" {
		req.Status = status
	}

	alpacaOrders, err := s.client.GetOrders(req)
	if err != nil {
		return nil, fmt.Errorf("failed to list orders: %w", err)
	}

	orders := make([]*interfaces.Order, len(alpacaOrders))
	for i, ao := range alpacaOrders {
		orders[i] = s.convertAlpacaOrder(&ao)
	}

	return orders, nil
}

// GetPositions retrieves all current positions
func (s *AlpacaTradingService) GetPositions(ctx context.Context) ([]*interfaces.Position, error) {
	alpacaPositions, err := s.client.GetPositions()
	if err != nil {
		return nil, fmt.Errorf("failed to get positions: %w", err)
	}

	positions := make([]*interfaces.Position, len(alpacaPositions))
	for i, ap := range alpacaPositions {
		positions[i] = &interfaces.Position{
			Symbol:           ap.Symbol,
			Qty:              ap.Qty.InexactFloat64(),
			AvgEntryPrice:    ap.AvgEntryPrice.InexactFloat64(),
			MarketValue:      ap.MarketValue.InexactFloat64(),
			CostBasis:        ap.CostBasis.InexactFloat64(),
			UnrealizedPL:     ap.UnrealizedPL.InexactFloat64(),
			UnrealizedPLPC:   ap.UnrealizedIntradayPLPC.InexactFloat64(),
			CurrentPrice:     ap.CurrentPrice.InexactFloat64(),
			Side:             string(ap.Side),
		}
	}

	return positions, nil
}

// GetAccount retrieves account information
func (s *AlpacaTradingService) GetAccount(ctx context.Context) (*interfaces.Account, error) {
	alpacaAccount, err := s.client.GetAccount()
	if err != nil {
		return nil, fmt.Errorf("failed to get account: %w", err)
	}

	return &interfaces.Account{
		ID:               alpacaAccount.ID,
		Cash:             alpacaAccount.Cash.InexactFloat64(),
		PortfolioValue:   alpacaAccount.PortfolioValue.InexactFloat64(),
		BuyingPower:      alpacaAccount.BuyingPower.InexactFloat64(),
		DayTradeCount:    int(alpacaAccount.DaytradeCount),
		PatternDayTrader: alpacaAccount.PatternDayTrader,
		LastEquity:       alpacaAccount.LastEquity.InexactFloat64(),
	}, nil
}

// Helper function to convert Alpaca order to our interface
func (s *AlpacaTradingService) convertAlpacaOrder(ao *alpaca.Order) *interfaces.Order {
	order := &interfaces.Order{
		ID:          ao.ID,
		Symbol:      ao.Symbol,
		Qty:         ao.Qty.InexactFloat64(),
		Side:        string(ao.Side),
		Type:        string(ao.Type),
		TimeInForce: string(ao.TimeInForce),
		Status:      string(ao.Status),
		SubmittedAt: ao.SubmittedAt,
	}

	if ao.LimitPrice != nil {
		val := ao.LimitPrice.InexactFloat64()
		order.LimitPrice = &val
	}

	if ao.StopPrice != nil {
		val := ao.StopPrice.InexactFloat64()
		order.StopPrice = &val
	}

	// FilledQty is not a pointer, it's a decimal.Decimal
	if !ao.FilledQty.IsZero() {
		order.FilledQty = ao.FilledQty.InexactFloat64()
	}

	if ao.FilledAvgPrice != nil {
		val := ao.FilledAvgPrice.InexactFloat64()
		order.FilledAvgPrice = &val
	}

	if ao.FilledAt != nil {
		order.FilledAt = ao.FilledAt
	}

	if ao.CanceledAt != nil {
		order.CanceledAt = ao.CanceledAt
	}

	return order
}

// PlaceOptionsOrder places a new options order
func (s *AlpacaTradingService) PlaceOptionsOrder(ctx context.Context, order *interfaces.OptionsOrder) (*interfaces.OrderResult, error) {
	qty := decimal.NewFromFloat(order.Qty)
	req := alpaca.PlaceOrderRequest{
		Symbol:      order.Symbol,
		Qty:         &qty,
		Side:        alpaca.Side(order.Side),
		Type:        alpaca.OrderType(order.Type),
		TimeInForce: alpaca.TimeInForce(order.TimeInForce),
	}

	if order.LimitPrice != nil {
		limitPrice := decimal.NewFromFloat(*order.LimitPrice)
		req.LimitPrice = &limitPrice
	}

	s.logger.WithFields(logrus.Fields{
		"symbol": order.Symbol,
		"side":   order.Side,
		"qty":    order.Qty,
		"type":   order.Type,
	}).Info("Placing options order")

	alpacaOrder, err := s.client.PlaceOrder(req)
	if err != nil {
		s.logger.WithError(err).Error("Failed to place options order")
		return nil, fmt.Errorf("failed to place options order: %w", err)
	}

	return &interfaces.OrderResult{
		OrderID: alpacaOrder.ID,
		Status:  string(alpacaOrder.Status),
		Message: fmt.Sprintf("Options order placed successfully: %s %v contracts of %s", order.Side, order.Qty, order.Symbol),
	}, nil
}

// alpacaOptionsSnapshot represents the response from Alpaca options snapshots API
type alpacaOptionsSnapshot struct {
	Snapshots map[string]struct {
		LatestQuote struct {
			Ask      float64   `json:"ap"`
			AskSize  int       `json:"as"`
			Bid      float64   `json:"bp"`
			BidSize  int       `json:"bs"`
			T        time.Time `json:"t"`
		} `json:"latestQuote"`
		LatestTrade struct {
			Price float64   `json:"p"`
			Size  int       `json:"s"`
			T     time.Time `json:"t"`
		} `json:"latestTrade"`
		Greeks struct {
			Delta float64 `json:"delta"`
			Gamma float64 `json:"gamma"`
			Theta float64 `json:"theta"`
			Vega  float64 `json:"vega"`
			Rho   float64 `json:"rho"`
		} `json:"greeks"`
		ImpliedVolatility float64 `json:"impliedVolatility"`
	} `json:"snapshots"`
	NextPageToken string `json:"next_page_token"`
}

// GetOptionsChain retrieves the options chain for an underlying symbol
func (s *AlpacaTradingService) GetOptionsChain(ctx context.Context, underlying string, expiration time.Time) ([]*interfaces.OptionContract, error) {
	s.logger.WithFields(logrus.Fields{
		"underlying": underlying,
		"expiration": expiration,
	}).Info("Getting options chain")

	// Build the URL with query parameters
	url := fmt.Sprintf("https://data.alpaca.markets/v1beta1/options/snapshots/%s", underlying)

	// Add query parameters
	expirationStr := expiration.Format("2006-01-02")
	url += fmt.Sprintf("?expiration_date=%s&limit=1000", expirationStr)

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add Alpaca API headers
	req.Header.Set("APCA-API-KEY-ID", s.apiKey)
	req.Header.Set("APCA-API-SECRET-KEY", s.apiSecret)
	req.Header.Set("Accept", "application/json")

	// Execute request
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch options chain: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("options chain API error (HTTP %d): %s", resp.StatusCode, string(body))
	}

	// Parse response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var snapshot alpacaOptionsSnapshot
	if err := json.Unmarshal(body, &snapshot); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Convert to our OptionContract format
	contracts := make([]*interfaces.OptionContract, 0, len(snapshot.Snapshots))
	for symbol, data := range snapshot.Snapshots {
		// Parse the OCC symbol to extract strike, expiration, and type
		// OCC format: TSLA251219C00400000
		// This is a simplified parser - you may want to use a proper OCC parser library
		contract := &interfaces.OptionContract{
			Symbol:           symbol,
			UnderlyingSymbol: underlying,
			Bid:              data.LatestQuote.Bid,
			Ask:              data.LatestQuote.Ask,
			Premium:          data.LatestTrade.Price,
			ImpliedVolatility: data.ImpliedVolatility,
			Delta:            data.Greeks.Delta,
			Gamma:            data.Greeks.Gamma,
			Theta:            data.Greeks.Theta,
			Vega:             data.Greeks.Vega,
			ExpirationDate:   expiration,
			// TODO: Parse strike price and option type from OCC symbol
		}
		contracts = append(contracts, contract)
	}

	s.logger.WithField("count", len(contracts)).Info("Fetched options chain")
	return contracts, nil
}

// GetOptionsQuote retrieves a quote for a specific options contract
func (s *AlpacaTradingService) GetOptionsQuote(ctx context.Context, symbol string) (*interfaces.OptionsQuote, error) {
	// Note: This would use Alpaca's options quotes API
	// For now, return nil as placeholder
	s.logger.WithField("symbol", symbol).Info("Getting options quote")

	return nil, fmt.Errorf("options quote not implemented yet")
}

// GetOptionsPosition retrieves a specific options position
func (s *AlpacaTradingService) GetOptionsPosition(ctx context.Context, symbol string) (*interfaces.OptionsPosition, error) {
	positions, err := s.client.GetPositions()
	if err != nil {
		return nil, fmt.Errorf("failed to get positions: %w", err)
	}

	for _, pos := range positions {
		if pos.Symbol == symbol && pos.AssetClass == "us_option" {
			return &interfaces.OptionsPosition{
				Symbol:        pos.Symbol,
				Qty:           pos.Qty.InexactFloat64(),
				AvgEntryPrice: pos.AvgEntryPrice.InexactFloat64(),
				MarketValue:   pos.MarketValue.InexactFloat64(),
				CostBasis:     pos.CostBasis.InexactFloat64(),
				UnrealizedPL:  pos.UnrealizedPL.InexactFloat64(),
				UnrealizedPLPC: pos.UnrealizedIntradayPLPC.InexactFloat64(),
				CurrentPrice:  pos.CurrentPrice.InexactFloat64(),
				Side:          string(pos.Side),
			}, nil
		}
	}

	return nil, fmt.Errorf("options position not found: %s", symbol)
}

// ListOptionsPositions retrieves all options positions
func (s *AlpacaTradingService) ListOptionsPositions(ctx context.Context) ([]*interfaces.OptionsPosition, error) {
	positions, err := s.client.GetPositions()
	if err != nil {
		return nil, fmt.Errorf("failed to get positions: %w", err)
	}

	optionsPositions := []*interfaces.OptionsPosition{}
	for _, pos := range positions {
		if pos.AssetClass == "us_option" {
			optionsPositions = append(optionsPositions, &interfaces.OptionsPosition{
				Symbol:        pos.Symbol,
				Qty:           pos.Qty.InexactFloat64(),
				AvgEntryPrice: pos.AvgEntryPrice.InexactFloat64(),
				MarketValue:   pos.MarketValue.InexactFloat64(),
				CostBasis:     pos.CostBasis.InexactFloat64(),
				UnrealizedPL:  pos.UnrealizedPL.InexactFloat64(),
				UnrealizedPLPC: pos.UnrealizedIntradayPLPC.InexactFloat64(),
				CurrentPrice:  pos.CurrentPrice.InexactFloat64(),
				Side:          string(pos.Side),
			})
		}
	}

	return optionsPositions, nil
}