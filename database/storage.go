package database

import (
	"fmt"
	"os"
	"path/filepath"
	"prophet-trader/interfaces"
	"prophet-trader/models"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// LocalStorage implements the StorageService interface using SQLite
type LocalStorage struct {
	db     *gorm.DB
	logger *logrus.Logger
}

// NewLocalStorage creates a new local storage service
func NewLocalStorage(dbPath string) (*LocalStorage, error) {
	// Ensure the directory exists
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	// Open SQLite database
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Auto-migrate schemas
	if err := db.AutoMigrate(
		&models.DBOrder{},
		&models.DBBar{},
		&models.DBPosition{},
		&models.DBTrade{},
		&models.DBAccountSnapshot{},
		&models.DBSignal{},
		&models.DBManagedPosition{},
		&models.DBHarvestCondor{},
		&models.DBHarvestIVSnapshot{},
	); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	logger := logrus.New()
	logger.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})

	return &LocalStorage{
		db:     db,
		logger: logger,
	}, nil
}

// SaveBars saves multiple bars to the database
func (s *LocalStorage) SaveBars(bars []*interfaces.Bar) error {
	if len(bars) == 0 {
		return nil
	}

	s.logger.WithField("count", len(bars)).Info("Saving bars to database")

	// Convert interface bars to DB bars
	dbBars := make([]*models.DBBar, len(bars))
	for i, bar := range bars {
		dbBars[i] = &models.DBBar{
			Symbol:    bar.Symbol,
			Timestamp: bar.Timestamp,
			Open:      bar.Open,
			High:      bar.High,
			Low:       bar.Low,
			Close:     bar.Close,
			Volume:    bar.Volume,
			VWAP:      bar.VWAP,
		}
	}

	// Batch insert with upsert on conflict
	result := s.db.Create(&dbBars)
	if result.Error != nil {
		return fmt.Errorf("failed to save bars: %w", result.Error)
	}

	s.logger.WithField("saved", result.RowsAffected).Info("Bars saved successfully")
	return nil
}

// GetBars retrieves bars for a symbol within a time range
func (s *LocalStorage) GetBars(symbol string, start, end time.Time) ([]*interfaces.Bar, error) {
	var dbBars []*models.DBBar

	result := s.db.Where("symbol = ? AND timestamp >= ? AND timestamp <= ?", symbol, start, end).
		Order("timestamp ASC").
		Find(&dbBars)

	if result.Error != nil {
		return nil, fmt.Errorf("failed to get bars: %w", result.Error)
	}

	// Convert DB bars to interface bars
	bars := make([]*interfaces.Bar, len(dbBars))
	for i, dbBar := range dbBars {
		bars[i] = &interfaces.Bar{
			Symbol:    dbBar.Symbol,
			Timestamp: dbBar.Timestamp,
			Open:      dbBar.Open,
			High:      dbBar.High,
			Low:       dbBar.Low,
			Close:     dbBar.Close,
			Volume:    dbBar.Volume,
			VWAP:      dbBar.VWAP,
		}
	}

	return bars, nil
}

// SaveOrder saves an order to the database
func (s *LocalStorage) SaveOrder(order *interfaces.Order) error {
	dbOrder := &models.DBOrder{
		OrderID:        order.ID,
		Symbol:         order.Symbol,
		Qty:            order.Qty,
		Side:           order.Side,
		Type:           order.Type,
		TimeInForce:    order.TimeInForce,
		LimitPrice:     order.LimitPrice,
		StopPrice:      order.StopPrice,
		Status:         order.Status,
		FilledQty:      order.FilledQty,
		FilledAvgPrice: order.FilledAvgPrice,
		SubmittedAt:    order.SubmittedAt,
		FilledAt:       order.FilledAt,
		CanceledAt:     order.CanceledAt,
	}

	result := s.db.Save(dbOrder)
	if result.Error != nil {
		return fmt.Errorf("failed to save order: %w", result.Error)
	}

	return nil
}

// GetOrder retrieves an order by ID
func (s *LocalStorage) GetOrder(orderID string) (*interfaces.Order, error) {
	var dbOrder models.DBOrder

	result := s.db.Where("order_id = ?", orderID).First(&dbOrder)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get order: %w", result.Error)
	}

	return &interfaces.Order{
		ID:             dbOrder.OrderID,
		Symbol:         dbOrder.Symbol,
		Qty:            dbOrder.Qty,
		Side:           dbOrder.Side,
		Type:           dbOrder.Type,
		TimeInForce:    dbOrder.TimeInForce,
		LimitPrice:     dbOrder.LimitPrice,
		StopPrice:      dbOrder.StopPrice,
		Status:         dbOrder.Status,
		FilledQty:      dbOrder.FilledQty,
		FilledAvgPrice: dbOrder.FilledAvgPrice,
		SubmittedAt:    dbOrder.SubmittedAt,
		FilledAt:       dbOrder.FilledAt,
		CanceledAt:     dbOrder.CanceledAt,
	}, nil
}

// GetOrders retrieves orders by status
func (s *LocalStorage) GetOrders(status string) ([]*interfaces.Order, error) {
	var dbOrders []*models.DBOrder

	query := s.db.Model(&models.DBOrder{})
	if status != "" {
		query = query.Where("status = ?", status)
	}

	result := query.Order("submitted_at DESC").Find(&dbOrders)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get orders: %w", result.Error)
	}

	orders := make([]*interfaces.Order, len(dbOrders))
	for i, dbOrder := range dbOrders {
		orders[i] = &interfaces.Order{
			ID:             dbOrder.OrderID,
			Symbol:         dbOrder.Symbol,
			Qty:            dbOrder.Qty,
			Side:           dbOrder.Side,
			Type:           dbOrder.Type,
			TimeInForce:    dbOrder.TimeInForce,
			LimitPrice:     dbOrder.LimitPrice,
			StopPrice:      dbOrder.StopPrice,
			Status:         dbOrder.Status,
			FilledQty:      dbOrder.FilledQty,
			FilledAvgPrice: dbOrder.FilledAvgPrice,
			SubmittedAt:    dbOrder.SubmittedAt,
			FilledAt:       dbOrder.FilledAt,
			CanceledAt:     dbOrder.CanceledAt,
		}
	}

	return orders, nil
}

// CleanupOldData removes data older than the specified time
func (s *LocalStorage) CleanupOldData(before time.Time) error {
	s.logger.WithField("before", before).Info("Cleaning up old data")

	// Delete old bars
	if err := s.db.Where("timestamp < ?", before).Delete(&models.DBBar{}).Error; err != nil {
		return fmt.Errorf("failed to delete old bars: %w", err)
	}

	// Delete old account snapshots
	if err := s.db.Where("snapshot_time < ?", before).Delete(&models.DBAccountSnapshot{}).Error; err != nil {
		return fmt.Errorf("failed to delete old snapshots: %w", err)
	}

	// Delete old signals
	if err := s.db.Where("created_at < ?", before).Delete(&models.DBSignal{}).Error; err != nil {
		return fmt.Errorf("failed to delete old signals: %w", err)
	}

	s.logger.Info("Old data cleaned up successfully")
	return nil
}

// Additional helper methods

// SavePosition saves a position snapshot
func (s *LocalStorage) SavePosition(position *interfaces.Position) error {
	dbPosition := &models.DBPosition{
		Symbol:         position.Symbol,
		Qty:            position.Qty,
		AvgEntryPrice:  position.AvgEntryPrice,
		MarketValue:    position.MarketValue,
		CostBasis:      position.CostBasis,
		UnrealizedPL:   position.UnrealizedPL,
		UnrealizedPLPC: position.UnrealizedPLPC,
		CurrentPrice:   position.CurrentPrice,
		Side:           position.Side,
		SnapshotTime:   time.Now(),
	}

	result := s.db.Save(dbPosition)
	if result.Error != nil {
		return fmt.Errorf("failed to save position: %w", result.Error)
	}

	return nil
}

// SaveAccountSnapshot saves an account snapshot
func (s *LocalStorage) SaveAccountSnapshot(account *interfaces.Account) error {
	dbSnapshot := &models.DBAccountSnapshot{
		Cash:             account.Cash,
		PortfolioValue:   account.PortfolioValue,
		BuyingPower:      account.BuyingPower,
		DayTradeCount:    account.DayTradeCount,
		PatternDayTrader: account.PatternDayTrader,
		SnapshotTime:     time.Now(),
	}

	result := s.db.Save(dbSnapshot)
	if result.Error != nil {
		return fmt.Errorf("failed to save account snapshot: %w", result.Error)
	}

	return nil
}

// SaveSignal saves a trading signal
func (s *LocalStorage) SaveSignal(symbol, signalType, strategyName, reason string, strength float64) error {
	dbSignal := &models.DBSignal{
		Symbol:       symbol,
		SignalType:   signalType,
		Strength:     strength,
		StrategyName: strategyName,
		Reason:       reason,
		Executed:     false,
	}

	result := s.db.Save(dbSignal)
	if result.Error != nil {
		return fmt.Errorf("failed to save signal: %w", result.Error)
	}

	return nil
}

// SaveManagedPosition saves a managed position to the database
func (s *LocalStorage) SaveManagedPosition(position *models.DBManagedPosition) error {
	result := s.db.Save(position)
	if result.Error != nil {
		return fmt.Errorf("failed to save managed position: %w", result.Error)
	}
	return nil
}

// GetManagedPosition retrieves a managed position by ID
func (s *LocalStorage) GetManagedPosition(positionID string) (*models.DBManagedPosition, error) {
	var dbPosition models.DBManagedPosition

	result := s.db.Where("position_id = ?", positionID).First(&dbPosition)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get managed position: %w", result.Error)
	}

	return &dbPosition, nil
}

// GetAllManagedPositions retrieves all managed positions with optional status filter
func (s *LocalStorage) GetAllManagedPositions(status string) ([]*models.DBManagedPosition, error) {
	var dbPositions []*models.DBManagedPosition

	query := s.db.Model(&models.DBManagedPosition{})
	if status != "" {
		query = query.Where("status = ?", status)
	}

	result := query.Order("created_at DESC").Find(&dbPositions)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get managed positions: %w", result.Error)
	}

	return dbPositions, nil
}

// DeleteManagedPosition deletes a managed position by ID
func (s *LocalStorage) DeleteManagedPosition(positionID string) error {
	result := s.db.Where("position_id = ?", positionID).Delete(&models.DBManagedPosition{})
	if result.Error != nil {
		return fmt.Errorf("failed to delete managed position: %w", result.Error)
	}
	return nil
}

// ── Harvest condor storage ─────────────────────────────────────────

func (s *LocalStorage) SaveHarvestCondor(c *models.DBHarvestCondor) error {
	return s.db.Save(c).Error
}

// UpdateHarvestCondor applies partial updates to a condor by condorID.
// Map keys must be GORM column names (e.g. "realized_pnl", "status"), not Go field names.
func (s *LocalStorage) UpdateHarvestCondor(condorID string, updates map[string]interface{}) error {
	return s.db.Model(&models.DBHarvestCondor{}).
		Where("condor_id = ?", condorID).
		Updates(updates).Error
}

func (s *LocalStorage) GetHarvestCondorByID(condorID string) (*models.DBHarvestCondor, error) {
	var c models.DBHarvestCondor
	if err := s.db.Where("condor_id = ?", condorID).First(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *LocalStorage) ListOpenHarvestCondors() ([]*models.DBHarvestCondor, error) {
	var condors []*models.DBHarvestCondor
	err := s.db.Where("status = ?", "OPEN").Find(&condors).Error
	return condors, err
}

// GetHarvestClosedPnL sums realized P&L for condors closed within [start, end].
func (s *LocalStorage) GetHarvestClosedPnL(start, end time.Time) (float64, error) {
	var total float64
	err := s.db.Model(&models.DBHarvestCondor{}).
		Where("status = ? AND closed_at >= ? AND closed_at <= ?", "CLOSED", start, end).
		Select("COALESCE(SUM(realized_pnl), 0)").
		Scan(&total).Error
	return total, err
}

// ── Harvest IV snapshot storage ────────────────────────────────────

func (s *LocalStorage) SaveHarvestIVSnapshot(snap *models.DBHarvestIVSnapshot) error {
	// OnConflict{DoNothing: true} makes duplicate (underlying, date) inserts a no-op,
	// relying on the DB-level unique index as the idempotency mechanism.
	return s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(snap).Error
}

func (s *LocalStorage) GetHarvestIVSnapshots(underlying string, start, end time.Time) ([]*models.DBHarvestIVSnapshot, error) {
	var snaps []*models.DBHarvestIVSnapshot
	// end is an exclusive upper bound (date < end) so callers can use midnight boundaries cleanly.
	err := s.db.Where("underlying = ? AND date >= ? AND date < ?", underlying, start, end).
		Order("date ASC").
		Find(&snaps).Error
	return snaps, err
}

// Close closes the database connection
func (s *LocalStorage) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}