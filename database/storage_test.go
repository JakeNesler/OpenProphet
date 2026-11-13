package database

import (
	"path/filepath"
	"prophet-trader/interfaces"
	"prophet-trader/models"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSaveOrderUpsertsByClientOrderID(t *testing.T) {
	storage, err := NewLocalStorage(filepath.Join(t.TempDir(), "orders.db"))
	if err != nil {
		t.Fatalf("NewLocalStorage() error = %v", err)
	}
	defer storage.Close()

	order := &interfaces.Order{
		ClientOrderID: "op-test-order",
		Symbol:        "AAPL",
		Qty:           1,
		Side:          "buy",
		Type:          "market",
		TimeInForce:   "day",
		Status:        "pending",
		SubmittedAt:   time.Now(),
	}
	if err := storage.SaveOrder(order); err != nil {
		t.Fatalf("SaveOrder(intent) error = %v", err)
	}

	order.ID = "broker-order-id"
	order.Status = "accepted"
	if err := storage.SaveOrder(order); err != nil {
		t.Fatalf("SaveOrder(result) error = %v", err)
	}

	var count int64
	if err := storage.db.Model(&models.DBOrder{}).
		Where("client_order_id = ?", order.ClientOrderID).
		Count(&count).Error; err != nil {
		t.Fatalf("count saved orders: %v", err)
	}
	if count != 1 {
		t.Fatalf("saved orders = %d, want 1", count)
	}

	saved, err := storage.GetOrder(order.ID)
	if err != nil {
		t.Fatalf("GetOrder() error = %v", err)
	}
	if saved.ClientOrderID != order.ClientOrderID || saved.Status != order.Status {
		t.Fatalf("saved order = %#v, want client_order_id %q and status %q", saved, order.ClientOrderID, order.Status)
	}
}

func TestSaveOrderUpdatesLegacyBrokerOrder(t *testing.T) {
	storage, err := NewLocalStorage(filepath.Join(t.TempDir(), "orders.db"))
	if err != nil {
		t.Fatalf("NewLocalStorage() error = %v", err)
	}
	defer storage.Close()

	order := &interfaces.Order{ID: "legacy-broker-order", Symbol: "AAPL", Status: "pending"}
	if err := storage.SaveOrder(order); err != nil {
		t.Fatalf("SaveOrder(initial) error = %v", err)
	}
	order.Status = "canceled"
	if err := storage.SaveOrder(order); err != nil {
		t.Fatalf("SaveOrder(update) error = %v", err)
	}

	var count int64
	if err := storage.db.Model(&models.DBOrder{}).Where("order_id = ?", order.ID).Count(&count).Error; err != nil {
		t.Fatalf("count saved orders: %v", err)
	}
	if count != 1 {
		t.Fatalf("saved orders = %d, want 1", count)
	}
}

func TestGetOrdersNeedingReconciliation(t *testing.T) {
	storage, err := NewLocalStorage(filepath.Join(t.TempDir(), "orders.db"))
	if err != nil {
		t.Fatalf("NewLocalStorage() error = %v", err)
	}
	defer storage.Close()

	orders := []*interfaces.Order{
		{ClientOrderID: "op-pending", Symbol: "AAPL", Status: "pending"},
		{ClientOrderID: "op-submit-failed", Symbol: "MSFT", Status: "submit_failed"},
		{ClientOrderID: "op-filled", Symbol: "GOOG", Status: "filled"},
		{Symbol: "TSLA", Status: "pending"},
	}
	for _, order := range orders {
		if err := storage.SaveOrder(order); err != nil {
			t.Fatalf("SaveOrder(%q) error = %v", order.Symbol, err)
		}
	}

	got, err := storage.GetOrdersNeedingReconciliation()
	if err != nil {
		t.Fatalf("GetOrdersNeedingReconciliation() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("GetOrdersNeedingReconciliation() returned %d orders, want 2", len(got))
	}

	clientOrderIDs := map[string]bool{}
	for _, order := range got {
		clientOrderIDs[order.ClientOrderID] = true
	}
	if !clientOrderIDs["op-pending"] || !clientOrderIDs["op-submit-failed"] {
		t.Fatalf("GetOrdersNeedingReconciliation() client order IDs = %v, want pending and submit_failed orders", clientOrderIDs)
	}
}

func TestSavePositionKeepsSnapshotHistory(t *testing.T) {
	storage, err := NewLocalStorage(filepath.Join(t.TempDir(), "positions.db"))
	if err != nil {
		t.Fatalf("NewLocalStorage() error = %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := storage.SavePosition(&interfaces.Position{Symbol: "NVDA", Qty: 10, CurrentPrice: float64(100 + i)}); err != nil {
			t.Fatalf("SavePosition() snapshot %d error = %v", i, err)
		}
	}

	var count int64
	storage.db.Model(&models.DBPosition{}).Where("symbol = ?", "NVDA").Count(&count)
	if count != 3 {
		t.Fatalf("snapshot count = %d, want 3", count)
	}
}

func TestNewLocalStorageMigratesLegacyUniquePositionIndex(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	legacy, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	if err := legacy.AutoMigrate(&models.DBPosition{}); err != nil {
		t.Fatalf("migrate legacy db: %v", err)
	}
	if err := legacy.Exec("DROP INDEX idx_positions_symbol").Error; err != nil {
		t.Fatalf("drop index: %v", err)
	}
	if err := legacy.Exec("CREATE UNIQUE INDEX idx_positions_symbol ON positions(symbol)").Error; err != nil {
		t.Fatalf("create legacy unique index: %v", err)
	}
	if err := legacy.Create(&models.DBPosition{Symbol: "NVDA"}).Error; err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	sqlDB, _ := legacy.DB()
	sqlDB.Close()

	storage, err := NewLocalStorage(dbPath)
	if err != nil {
		t.Fatalf("NewLocalStorage() error = %v", err)
	}
	if err := storage.SavePosition(&interfaces.Position{Symbol: "NVDA", Qty: 1}); err != nil {
		t.Fatalf("SavePosition() after migration error = %v", err)
	}
	if !storage.db.Migrator().HasIndex(&models.DBPosition{}, "idx_positions_symbol") {
		t.Fatal("expected non-unique idx_positions_symbol to exist after migration")
	}
}
