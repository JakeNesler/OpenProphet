package models

import (
	"time"

	"gorm.io/gorm"
)

// DBOrder represents an order in the database
type DBOrder struct {
	gorm.Model
	OrderID        string  `gorm:"index"`
	ClientOrderID  *string `gorm:"uniqueIndex"` // pointer so empty stays NULL — many NULLs allowed, only non-empty ids dedupe
	Symbol         string `gorm:"index"`
	Qty            float64
	Side           string
	Type           string
	TimeInForce    string
	LimitPrice     *float64
	StopPrice      *float64
	Status         string `gorm:"index"`
	FilledQty      float64
	FilledAvgPrice *float64
	SubmittedAt    time.Time
	FilledAt       *time.Time
	CanceledAt     *time.Time
	// Metadata for strategy tracking
	StrategyName string
	Metadata     string // JSON string for flexible data
}

// DBBar represents historical price data in the database
type DBBar struct {
	gorm.Model
	Symbol    string `gorm:"index:idx_symbol_timestamp"`
	Timestamp time.Time `gorm:"index:idx_symbol_timestamp"`
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    int64
	VWAP      float64
	Timeframe string
}

// DBPosition represents a position snapshot in the database
type DBPosition struct {
	gorm.Model
	Symbol         string `gorm:"uniqueIndex"`
	Qty            float64
	AvgEntryPrice  float64
	MarketValue    float64
	CostBasis      float64
	UnrealizedPL   float64
	UnrealizedPLPC float64
	CurrentPrice   float64
	Side           string
	SnapshotTime   time.Time `gorm:"index"`
}

// DBTrade represents executed trades for analysis
type DBTrade struct {
	gorm.Model
	Symbol       string `gorm:"index"`
	EntryPrice   float64
	ExitPrice    float64
	Qty          float64
	Side         string
	PnL          float64
	PnLPercent   float64
	EntryTime    time.Time
	ExitTime     time.Time
	Duration     int64 // seconds
	StrategyName string
	Metadata     string
}

// DBAccountSnapshot represents account state at a point in time
type DBAccountSnapshot struct {
	gorm.Model
	Cash             float64
	PortfolioValue   float64
	BuyingPower      float64
	DayTradeCount    int
	PatternDayTrader bool
	SnapshotTime     time.Time `gorm:"index"`
}

// DBSignal represents trading signals for audit/analysis
type DBSignal struct {
	gorm.Model
	Symbol       string `gorm:"index"`
	SignalType   string // "BUY", "SELL", "HOLD"
	Strength     float64
	StrategyName string `gorm:"index"`
	Reason       string
	Metadata     string
	Executed     bool
	ExecutedAt   *time.Time
	OrderID      string
}

// DBManagedPosition represents a managed position with automated risk management
type DBManagedPosition struct {
	gorm.Model
	PositionID        string `gorm:"uniqueIndex"`
	Symbol            string `gorm:"index"`
	Side              string
	Strategy          string

	// Entry details
	Quantity          float64
	EntryPrice        float64
	EntryOrderID      string
	EntryOrderType    string
	AllocationDollars float64

	// Risk management
	StopLossPrice     float64
	StopLossPercent   float64
	StopLossOrderID   string
	TrailingStop      bool
	TrailingPercent   float64

	// Profit targets
	TakeProfitPrice   float64
	TakeProfitPercent float64
	TakeProfitOrderID string

	// Partial exit
	PartialExitEnabled      bool
	PartialExitPercent      float64
	PartialExitTargetPercent float64
	PartialExitTargetPrice   float64
	PartialExitOrders       string // JSON array of order IDs

	// Status
	Status           string `gorm:"index"` // PENDING, ACTIVE, PARTIAL, CLOSED, STOPPED_OUT
	CurrentPrice     float64
	UnrealizedPL     float64
	UnrealizedPLPC   float64
	RemainingQty     float64

	// Metadata
	Notes     string
	Tags      string // JSON array
	ClosedAt  *time.Time
}

// TableName overrides for cleaner table names
func (DBOrder) TableName() string {
	return "orders"
}

func (DBBar) TableName() string {
	return "bars"
}

func (DBPosition) TableName() string {
	return "positions"
}

func (DBTrade) TableName() string {
	return "trades"
}

func (DBAccountSnapshot) TableName() string {
	return "account_snapshots"
}

func (DBSignal) TableName() string {
	return "signals"
}

func (DBManagedPosition) TableName() string {
	return "managed_positions"
}
