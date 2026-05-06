package interfaces

import (
	"context"
	"time"
)

// TradingService defines the interface for executing trades
type TradingService interface {
	PlaceOrder(ctx context.Context, order *Order) (*OrderResult, error)
	CancelOrder(ctx context.Context, orderID string) error
	GetOrder(ctx context.Context, orderID string) (*Order, error)
	ListOrders(ctx context.Context, status string) ([]*Order, error)
	GetPositions(ctx context.Context) ([]*Position, error)
	GetAccount(ctx context.Context) (*Account, error)

	// Options trading methods
	PlaceOptionsOrder(ctx context.Context, order *OptionsOrder) (*OrderResult, error)
	GetOptionsChain(ctx context.Context, underlying string, expiration time.Time) ([]*OptionContract, error)
	GetOptionsQuote(ctx context.Context, symbol string) (*OptionsQuote, error)
	GetOptionsPosition(ctx context.Context, symbol string) (*OptionsPosition, error)
	ListOptionsPositions(ctx context.Context) ([]*OptionsPosition, error)
}

// DataService defines the interface for market data operations
type DataService interface {
	GetHistoricalBars(ctx context.Context, symbol string, start, end time.Time, timeframe string) ([]*Bar, error)
	GetLatestBar(ctx context.Context, symbol string) (*Bar, error)
	GetLatestQuote(ctx context.Context, symbol string) (*Quote, error)
	GetLatestTrade(ctx context.Context, symbol string) (*Trade, error)
	StreamBars(ctx context.Context, symbols []string) (<-chan *Bar, error)
}

// StorageService defines the interface for local data persistence
type StorageService interface {
	SaveBars(bars []*Bar) error
	GetBars(symbol string, start, end time.Time) ([]*Bar, error)
	SaveOrder(order *Order) error
	GetOrder(orderID string) (*Order, error)
	GetOrders(status string) ([]*Order, error)
	CleanupOldData(before time.Time) error
}

// StrategyExecutor defines the interface for strategy execution
// This will be useful for AI personas and quant strategies later
type StrategyExecutor interface {
	Initialize(config map[string]interface{}) error
	ShouldBuy(ctx context.Context, symbol string, data *MarketData) (bool, *OrderRequest)
	ShouldSell(ctx context.Context, symbol string, data *MarketData) (bool, *OrderRequest)
	OnOrderFilled(order *Order)
	OnMarketData(data *MarketData)
	GetName() string
}

// Common data structures used across interfaces
type Order struct {
	ID            string
	Symbol        string
	Qty           float64
	Side          string // "buy" or "sell"
	Type          string // "market", "limit", etc.
	TimeInForce   string // "day", "gtc", etc.
	LimitPrice    *float64
	StopPrice     *float64
	Status        string
	FilledQty     float64
	FilledAvgPrice *float64
	SubmittedAt   time.Time
	FilledAt      *time.Time
	CanceledAt    *time.Time
}

type OrderRequest struct {
	Symbol      string
	Qty         float64
	Side        string
	Type        string
	TimeInForce string
	LimitPrice  *float64
	StopPrice   *float64
}

type OrderResult struct {
	OrderID string
	Status  string
	Message string
}

type Position struct {
	Symbol           string
	Qty              float64
	AvgEntryPrice    float64
	MarketValue      float64
	CostBasis        float64
	UnrealizedPL     float64
	UnrealizedPLPC   float64
	CurrentPrice     float64
	Side             string
}

type Account struct {
	ID               string
	Cash             float64
	PortfolioValue   float64
	BuyingPower      float64
	DayTradeCount    int
	PatternDayTrader bool
	// LastEquity is the previous trading session's closing equity, used to compute
	// intraday P&L. Zero indicates the data is unavailable (e.g. test stubs).
	LastEquity float64
}

type Bar struct {
	Symbol    string
	Timestamp time.Time
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    int64
	VWAP      float64
}

type Quote struct {
	Symbol    string
	BidPrice  float64
	BidSize   int64
	AskPrice  float64
	AskSize   int64
	Timestamp time.Time
}

type Trade struct {
	Symbol    string
	Price     float64
	Size      int64
	Timestamp time.Time
}

type MarketData struct {
	Symbol       string
	CurrentBar   *Bar
	RecentBars   []*Bar
	LatestQuote  *Quote
	LatestTrade  *Trade
	Indicators   map[string]float64 // For calculated indicators
}

// Options trading structures
type OptionsOrder struct {
	Symbol        string  // Options symbol in OCC format (e.g., TSLA251219C00400000)
	Underlying    string  // Underlying stock symbol
	Qty           float64
	Side          string // "buy" or "sell"
	PositionIntent string // "buy_to_open", "buy_to_close", "sell_to_open", "sell_to_close"
	Type          string // "market", "limit"
	TimeInForce   string // "day", "gtc"
	LimitPrice    *float64
}

type OptionsQuote struct {
	Symbol    string
	BidPrice  float64
	BidSize   int64
	AskPrice  float64
	AskSize   int64
	LastPrice float64
	Volume    int64
	Timestamp time.Time
}

type OptionsPosition struct {
	Symbol        string
	Underlying    string
	Qty           float64
	AvgEntryPrice float64
	MarketValue   float64
	CostBasis     float64
	UnrealizedPL  float64
	UnrealizedPLPC float64
	CurrentPrice  float64
	Side          string // "long" or "short"
	Expiration    time.Time
	Strike        float64
	OptionType    string // "call" or "put"
}