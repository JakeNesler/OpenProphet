package controllers

import (
	"context"
	"errors"
	"path/filepath"
	"prophet-trader/database"
	"prophet-trader/interfaces"
	"testing"
	"time"
)

type reconciliationTradingService struct {
	orders map[string]*interfaces.Order
	err    error
}

func (s *reconciliationTradingService) PlaceOrder(context.Context, *interfaces.Order) (*interfaces.OrderResult, error) {
	return nil, nil
}

func (s *reconciliationTradingService) CancelOrder(context.Context, string) error {
	return nil
}

func (s *reconciliationTradingService) GetOrder(context.Context, string) (*interfaces.Order, error) {
	return nil, nil
}

func (s *reconciliationTradingService) GetOrderByClientOrderID(_ context.Context, clientOrderID string) (*interfaces.Order, error) {
	if clientOrderID == "op-error" {
		return nil, s.err
	}
	return s.orders[clientOrderID], nil
}

func (s *reconciliationTradingService) ListOrders(context.Context, string) ([]*interfaces.Order, error) {
	return nil, nil
}

func (s *reconciliationTradingService) GetPositions(context.Context) ([]*interfaces.Position, error) {
	return nil, nil
}

func (*reconciliationTradingService) GetAccount(context.Context) (*interfaces.Account, error) {
	return nil, nil
}

func (s *reconciliationTradingService) PlaceOptionsOrder(context.Context, *interfaces.OptionsOrder) (*interfaces.OrderResult, error) {
	return nil, nil
}

func (s *reconciliationTradingService) GetOptionsChain(context.Context, string, time.Time) ([]*interfaces.OptionContract, error) {
	return nil, nil
}

func (s *reconciliationTradingService) GetOptionsQuote(context.Context, string) (*interfaces.OptionsQuote, error) {
	return nil, nil
}

func (s *reconciliationTradingService) GetOptionsPosition(context.Context, string) (*interfaces.OptionsPosition, error) {
	return nil, nil
}

func (s *reconciliationTradingService) ListOptionsPositions(context.Context) ([]*interfaces.OptionsPosition, error) {
	return nil, nil
}

func TestReconcileOpenOrdersOnlyUpdatesConfirmedOrders(t *testing.T) {
	storage, err := database.NewLocalStorage(filepath.Join(t.TempDir(), "orders.db"))
	if err != nil {
		t.Fatalf("NewLocalStorage() error = %v", err)
	}
	defer storage.Close()

	for _, order := range []*interfaces.Order{
		{ClientOrderID: "op-confirmed", Symbol: "AAPL", Status: "pending"},
		{ClientOrderID: "op-error", Symbol: "MSFT", Status: "submit_failed"},
	} {
		if err := storage.SaveOrder(order); err != nil {
			t.Fatalf("SaveOrder(%q) error = %v", order.ClientOrderID, err)
		}
	}

	trading := &reconciliationTradingService{
		orders: map[string]*interfaces.Order{
			"op-confirmed": {
				ID:             "broker-confirmed",
				Status:         "accepted",
				FilledQty:      1,
				FilledAvgPrice: floatPtr(123.45),
			},
		},
		err: errors.New("broker lookup failed"),
	}
	controller := NewOrderController(trading, nil, storage)

	reconciled, skipped := controller.ReconcileOpenOrders(context.Background())
	if reconciled != 1 || skipped != 1 {
		t.Fatalf("ReconcileOpenOrders() = (%d, %d), want (1, 1)", reconciled, skipped)
	}

	confirmed, err := storage.GetOrder("broker-confirmed")
	if err != nil {
		t.Fatalf("GetOrder(confirmed) error = %v", err)
	}
	if confirmed.Status != "accepted" || confirmed.FilledQty != 1 || confirmed.FilledAvgPrice == nil || *confirmed.FilledAvgPrice != 123.45 {
		t.Fatalf("confirmed order = %#v, want broker state", confirmed)
	}

	pending, err := storage.GetOrdersNeedingReconciliation()
	if err != nil {
		t.Fatalf("GetOrdersNeedingReconciliation() error = %v", err)
	}
	if len(pending) != 1 || pending[0].ClientOrderID != "op-error" || pending[0].Status != "submit_failed" {
		t.Fatalf("orders left untouched after lookup error = %#v, want op-error submit_failed", pending)
	}
}

func floatPtr(value float64) *float64 {
	return &value
}

// placeOrderRecorder captures the order handed to PlaceOrder and lets the test drive the
// broker result/error. Embeds reconciliationTradingService to satisfy the rest of the interface.
type placeOrderRecorder struct {
	*reconciliationTradingService
	placed   *interfaces.Order
	result   *interfaces.OrderResult
	placeErr error
}

func (s *placeOrderRecorder) PlaceOrder(_ context.Context, o *interfaces.Order) (*interfaces.OrderResult, error) {
	s.placed = o
	return s.result, s.placeErr
}

func TestBuyPersistsIntentBeforeSubmit(t *testing.T) {
	// Case 1: broker submit FAILS. The intent must already be durably recorded (persist
	// before submit), marked submit_failed, and carry the ClientOrderID that reached the broker.
	storage, err := database.NewLocalStorage(filepath.Join(t.TempDir(), "buy-fail.db"))
	if err != nil {
		t.Fatalf("NewLocalStorage() error = %v", err)
	}
	defer storage.Close()

	rec := &placeOrderRecorder{reconciliationTradingService: &reconciliationTradingService{}, placeErr: errors.New("broker timeout")}
	oc := NewOrderController(rec, nil, storage)

	if _, err := oc.Buy(context.Background(), BuyRequest{Symbol: "AAPL", Qty: 1, Type: "market"}); err == nil {
		t.Fatal("Buy() expected an error when the broker submit fails")
	}
	if rec.placed == nil || rec.placed.ClientOrderID == "" {
		t.Fatalf("PlaceOrder should receive an order carrying a ClientOrderID, got %#v", rec.placed)
	}
	failed, err := storage.GetOrders("submit_failed")
	if err != nil {
		t.Fatalf("GetOrders(submit_failed) error = %v", err)
	}
	if len(failed) != 1 || failed[0].Symbol != "AAPL" || failed[0].ClientOrderID != rec.placed.ClientOrderID {
		t.Fatalf("expected 1 submit_failed intent persisted with the broker's client id, got %#v", failed)
	}

	// Case 2: broker submit SUCCEEDS. The pre-submit pending row upserts to the broker's
	// id/status on the same ClientOrderID — one row, not two.
	storage2, err := database.NewLocalStorage(filepath.Join(t.TempDir(), "buy-ok.db"))
	if err != nil {
		t.Fatalf("NewLocalStorage() error = %v", err)
	}
	defer storage2.Close()

	rec2 := &placeOrderRecorder{reconciliationTradingService: &reconciliationTradingService{}, result: &interfaces.OrderResult{OrderID: "broker-1", Status: "accepted"}}
	oc2 := NewOrderController(rec2, nil, storage2)

	res, err := oc2.Buy(context.Background(), BuyRequest{Symbol: "MSFT", Qty: 2, Type: "market"})
	if err != nil {
		t.Fatalf("Buy() unexpected error = %v", err)
	}
	if res == nil || res.OrderID != "broker-1" {
		t.Fatalf("Buy() result = %#v, want broker-1", res)
	}
	saved, err := storage2.GetOrder("broker-1")
	if err != nil {
		t.Fatalf("GetOrder(broker-1) error = %v", err)
	}
	if saved.Status != "accepted" || saved.ClientOrderID == "" {
		t.Fatalf("saved order = %#v, want accepted status + a ClientOrderID", saved)
	}
	all, err := storage2.GetOrders("")
	if err != nil {
		t.Fatalf("GetOrders() error = %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected exactly 1 row after pre-submit + post-submit upsert, got %d", len(all))
	}
}
