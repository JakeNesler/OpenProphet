package services

import (
	"context"
	"errors"
	"path/filepath"
	"prophet-trader/database"
	"prophet-trader/interfaces"
	"testing"
	"time"
)

type exitOrderRecorder struct {
	placed   *interfaces.Order
	result   *interfaces.OrderResult
	placeErr error
}

func (s *exitOrderRecorder) PlaceOrder(_ context.Context, o *interfaces.Order) (*interfaces.OrderResult, error) {
	s.placed = o
	return s.result, s.placeErr
}
func (s *exitOrderRecorder) CancelOrder(context.Context, string) error { return nil }
func (s *exitOrderRecorder) GetOrder(context.Context, string) (*interfaces.Order, error) {
	return nil, nil
}
func (s *exitOrderRecorder) GetOrderByClientOrderID(context.Context, string) (*interfaces.Order, error) {
	return nil, nil
}
func (s *exitOrderRecorder) ListOrders(context.Context, string) ([]*interfaces.Order, error) {
	return nil, nil
}
func (s *exitOrderRecorder) GetPositions(context.Context) ([]*interfaces.Position, error) {
	return nil, nil
}
func (s *exitOrderRecorder) GetAccount(context.Context) (*interfaces.Account, error) {
	return nil, nil
}
func (s *exitOrderRecorder) PlaceOptionsOrder(context.Context, *interfaces.OptionsOrder) (*interfaces.OrderResult, error) {
	return nil, nil
}
func (s *exitOrderRecorder) GetOptionsChain(context.Context, string, time.Time) ([]*interfaces.OptionContract, error) {
	return nil, nil
}
func (s *exitOrderRecorder) GetOptionsQuote(context.Context, string) (*interfaces.OptionsQuote, error) {
	return nil, nil
}
func (s *exitOrderRecorder) GetOptionsPosition(context.Context, string) (*interfaces.OptionsPosition, error) {
	return nil, nil
}
func (s *exitOrderRecorder) ListOptionsPositions(context.Context) ([]*interfaces.OptionsPosition, error) {
	return nil, nil
}

func newTestPositionManager(t *testing.T, rec *exitOrderRecorder) (*PositionManager, *database.LocalStorage) {
	t.Helper()
	storage, err := database.NewLocalStorage(filepath.Join(t.TempDir(), "pm.db"))
	if err != nil {
		t.Fatalf("NewLocalStorage() error = %v", err)
	}
	return NewPositionManager(rec, nil, storage), storage
}

func TestManagedProtectiveLegsPersistIntentBeforeSubmit(t *testing.T) {
	cases := []struct {
		name     string
		position func() *ManagedPosition
		place    func(*PositionManager, *ManagedPosition) error
	}{
		{
			name:     "stop_loss",
			position: func() *ManagedPosition { return &ManagedPosition{ID: "p1", Symbol: "AAPL", Side: "buy", RemainingQty: 10, StopLossPrice: 90} },
			place:    func(pm *PositionManager, pos *ManagedPosition) error { return pm.placeStopLossOrder(context.Background(), pos) },
		},
		{
			name:     "take_profit",
			position: func() *ManagedPosition { return &ManagedPosition{ID: "p2", Symbol: "AAPL", Side: "buy", RemainingQty: 10, TakeProfitPrice: 110} },
			place:    func(pm *PositionManager, pos *ManagedPosition) error { return pm.placeTakeProfitOrder(context.Background(), pos) },
		},
		{
			name: "partial_exit",
			position: func() *ManagedPosition {
				return &ManagedPosition{ID: "p3", Symbol: "AAPL", Side: "buy", Quantity: 10, RemainingQty: 10, PartialExit: &PartialExitConfig{Enabled: true, Percent: 50, TargetPrice: 105}}
			},
			place: func(pm *PositionManager, pos *ManagedPosition) error { return pm.placePartialExitOrder(context.Background(), pos) },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name+"/submit_fails", func(t *testing.T) {
			rec := &exitOrderRecorder{placeErr: errors.New("broker timeout")}
			pm, storage := newTestPositionManager(t, rec)
			defer storage.Close()

			if err := tc.place(pm, tc.position()); err == nil {
				t.Fatal("expected an error when the broker submit fails")
			}
			if rec.placed == nil || rec.placed.ClientOrderID == "" {
				t.Fatalf("PlaceOrder should receive an order carrying a ClientOrderID, got %#v", rec.placed)
			}
			failed, err := storage.GetOrders("submit_failed")
			if err != nil {
				t.Fatalf("GetOrders(submit_failed) error = %v", err)
			}
			if len(failed) != 1 || failed[0].ClientOrderID != rec.placed.ClientOrderID {
				t.Fatalf("expected 1 submit_failed intent persisted with the broker client id, got %#v", failed)
			}
		})

		t.Run(tc.name+"/submit_ok", func(t *testing.T) {
			rec := &exitOrderRecorder{result: &interfaces.OrderResult{OrderID: "broker-" + tc.name, Status: "accepted"}}
			pm, storage := newTestPositionManager(t, rec)
			defer storage.Close()

			if err := tc.place(pm, tc.position()); err != nil {
				t.Fatalf("place() unexpected error = %v", err)
			}
			saved, err := storage.GetOrder("broker-" + tc.name)
			if err != nil {
				t.Fatalf("GetOrder() error = %v", err)
			}
			if saved.Status != "accepted" || saved.ClientOrderID == "" {
				t.Fatalf("saved order = %#v, want accepted status + a ClientOrderID", saved)
			}
			all, err := storage.GetOrders("")
			if err != nil {
				t.Fatalf("GetOrders() error = %v", err)
			}
			if len(all) != 1 {
				t.Fatalf("expected exactly 1 row after pre-submit + post-submit upsert, got %d", len(all))
			}
		})
	}
}

func TestCloseManagedPositionPersistsExitIntentOnAmbiguousSubmit(t *testing.T) {
	rec := &exitOrderRecorder{placeErr: errors.New("market closed")}
	pm, storage := newTestPositionManager(t, rec)
	defer storage.Close()

	pos := &ManagedPosition{ID: "c1", Symbol: "AAPL", Side: "buy", Status: "ACTIVE", RemainingQty: 10}
	pm.positions[pos.ID] = pos

	if err := pm.CloseManagedPosition(context.Background(), pos.ID); err != nil {
		t.Fatalf("CloseManagedPosition() error = %v", err)
	}
	if pos.Status != "CLOSED" {
		t.Fatalf("position status = %q, want CLOSED even when the exit submit fails", pos.Status)
	}
	if rec.placed == nil || rec.placed.ClientOrderID == "" {
		t.Fatalf("exit PlaceOrder should carry a ClientOrderID, got %#v", rec.placed)
	}
	failed, err := storage.GetOrders("submit_failed")
	if err != nil {
		t.Fatalf("GetOrders(submit_failed) error = %v", err)
	}
	if len(failed) != 1 || failed[0].ClientOrderID != rec.placed.ClientOrderID {
		t.Fatalf("expected the ambiguous exit persisted as submit_failed for reconciliation, got %#v", failed)
	}
}
