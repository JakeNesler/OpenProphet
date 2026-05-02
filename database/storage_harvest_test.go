package database

import (
	"testing"
	"time"

	"prophet-trader/models"
)

func setupHarvestTestDB(t *testing.T) *LocalStorage {
	t.Helper()
	s, err := NewLocalStorage(":memory:")
	if err != nil {
		t.Fatalf("failed to create test DB: %v", err)
	}
	return s
}

func TestSaveAndGetHarvestCondor(t *testing.T) {
	s := setupHarvestTestDB(t)
	condor := &models.DBHarvestCondor{
		CondorID:              "test-condor-1",
		Underlying:            "SPY",
		Expiration:            time.Now().AddDate(0, 0, 45),
		ShortPutSymbol:        "SPY260619P00520000",
		ShortPutStrike:        520.0,
		LongPutSymbol:         "SPY260619P00515000",
		LongPutStrike:         515.0,
		ShortCallSymbol:       "SPY260619C00560000",
		ShortCallStrike:       560.0,
		LongCallSymbol:        "SPY260619C00565000",
		LongCallStrike:        565.0,
		Contracts:             2,
		WingWidth:             5.0,
		CreditPerContract:     1.50,
		TotalCredit:           300.0,
		MaxLoss:               1000.0,
		PortfolioValueAtEntry: 100000.0,
		EntryOrderID:          "ord-001",
		Status:                "OPEN",
		IVRAtEntry:            45.0,
		OpenedAt:              time.Now(),
	}
	if err := s.SaveHarvestCondor(condor); err != nil {
		t.Fatalf("SaveHarvestCondor failed: %v", err)
	}
	got, err := s.GetHarvestCondorByID("test-condor-1")
	if err != nil {
		t.Fatalf("GetHarvestCondorByID failed: %v", err)
	}
	if got.Underlying != "SPY" {
		t.Errorf("expected Underlying=SPY, got %s", got.Underlying)
	}
}

func TestListOpenHarvestCondors(t *testing.T) {
	s := setupHarvestTestDB(t)
	condors := []*models.DBHarvestCondor{
		{CondorID: "c1", Underlying: "SPY", Status: "OPEN",
			Expiration: time.Now().AddDate(0, 0, 40), OpenedAt: time.Now(),
			WingWidth: 5, CreditPerContract: 1.0, MaxLoss: 500},
		{CondorID: "c2", Underlying: "QQQ", Status: "CLOSED",
			Expiration: time.Now().AddDate(0, 0, 40), OpenedAt: time.Now(),
			WingWidth: 5, CreditPerContract: 1.0, MaxLoss: 500},
	}
	for _, c := range condors {
		_ = s.SaveHarvestCondor(c)
	}
	open := s.ListOpenHarvestCondors()
	if len(open) != 1 || open[0].CondorID != "c1" {
		t.Errorf("expected 1 OPEN condor (c1), got %d", len(open))
	}
}

func TestGetHarvestClosedPnL(t *testing.T) {
	s := setupHarvestTestDB(t)
	now := time.Now()
	condors := []*models.DBHarvestCondor{
		{CondorID: "p1", Status: "CLOSED", RealizedPnL: 150.0,
			ClosedAt: &now, Underlying: "SPY", OpenedAt: now,
			WingWidth: 5, CreditPerContract: 1.0, MaxLoss: 500,
			Expiration: now.AddDate(0, 0, 10)},
		{CondorID: "p2", Status: "CLOSED", RealizedPnL: -80.0,
			ClosedAt: &now, Underlying: "QQQ", OpenedAt: now,
			WingWidth: 5, CreditPerContract: 1.0, MaxLoss: 500,
			Expiration: now.AddDate(0, 0, 10)},
	}
	for _, c := range condors {
		_ = s.SaveHarvestCondor(c)
	}
	pnl, err := s.GetHarvestClosedPnL(now.AddDate(0, 0, -30), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("GetHarvestClosedPnL failed: %v", err)
	}
	if pnl != 70.0 {
		t.Errorf("expected PnL=70.0, got %.2f", pnl)
	}
}

func TestSaveAndGetHarvestIVSnapshot(t *testing.T) {
	s := setupHarvestTestDB(t)
	today := time.Now().Truncate(24 * time.Hour)
	snap := &models.DBHarvestIVSnapshot{
		Underlying: "SPY",
		Date:       today,
		ATMIV:      0.185,
	}
	if err := s.SaveHarvestIVSnapshot(snap); err != nil {
		t.Fatalf("SaveHarvestIVSnapshot failed: %v", err)
	}
	snaps, err := s.GetHarvestIVSnapshots("SPY", today.AddDate(0, 0, -1), today.Add(time.Hour))
	if err != nil {
		t.Fatalf("GetHarvestIVSnapshots failed: %v", err)
	}
	if len(snaps) != 1 || snaps[0].ATMIV != 0.185 {
		t.Errorf("unexpected snapshots: %+v", snaps)
	}
}
