package models

import (
	"time"

	"gorm.io/gorm"
)

// DBHarvestCondor tracks an iron condor position opened by the Harvest agent.
type DBHarvestCondor struct {
	gorm.Model
	CondorID   string    `gorm:"uniqueIndex"`
	Underlying string    `gorm:"index"`
	Expiration time.Time

	// Four legs
	ShortPutSymbol  string
	ShortPutStrike  float64
	LongPutSymbol   string
	LongPutStrike   float64
	ShortCallSymbol string
	ShortCallStrike float64
	LongCallSymbol  string
	LongCallStrike  float64

	// Position details
	Contracts             int
	WingWidth             float64
	CreditPerContract     float64
	TotalCredit           float64
	MaxLoss               float64
	PortfolioValueAtEntry float64

	// Order tracking
	EntryOrderID string
	CloseOrderID string `gorm:"column:close_order_id"`

	// Status: OPEN | CLOSING | CLOSED
	Status               string     `gorm:"index"`
	CloseReason          string
	CloseCostPerContract float64    `gorm:"column:close_cost_per_contract"`
	RealizedPnL          float64    `gorm:"column:realized_pnl"`
	OpenedAt             time.Time
	ClosedAt             *time.Time

	// Analysis metadata
	IVRAtEntry float64
	OverlapLog string // JSON: [{agent, underlying, direction, contracts, dte}]
}

// DBHarvestIVSnapshot stores one ATM-IV reading per underlying per trading day.
type DBHarvestIVSnapshot struct {
	gorm.Model
	Underlying string    `gorm:"uniqueIndex:idx_harvest_iv_under_date"`
	Date       time.Time `gorm:"uniqueIndex:idx_harvest_iv_under_date"`
	ATMIV      float64   // at-the-money implied volatility (average of nearest put+call)
}

func (DBHarvestCondor) TableName() string     { return "harvest_condors" }
func (DBHarvestIVSnapshot) TableName() string { return "harvest_iv_snapshots" }
