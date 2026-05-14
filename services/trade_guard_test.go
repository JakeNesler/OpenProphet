package services

import (
	"context"
	"errors"
	"prophet-trader/interfaces"
	"testing"
	"time"
)

// --- stubs ---

type stubLister struct {
	positions []*ManagedPosition
}

type stubOptionsProvider struct {
	exposure map[SectorBucket]float64
}

func (s *stubOptionsProvider) BucketExposureDollars() map[SectorBucket]float64 {
	return s.exposure
}

func (s *stubLister) ListManagedPositions(_ string) []*ManagedPosition {
	return s.positions
}

type stubTrading struct {
	portfolio    float64
	lastEquity   float64
	getAcctCalls int
	getAcctErr   error
}

func (s *stubTrading) GetAccount(_ context.Context) (*interfaces.Account, error) {
	s.getAcctCalls++
	if s.getAcctErr != nil {
		return nil, s.getAcctErr
	}
	return &interfaces.Account{PortfolioValue: s.portfolio, LastEquity: s.lastEquity}, nil
}
func (s *stubTrading) PlaceOrder(_ context.Context, _ *interfaces.Order) (*interfaces.OrderResult, error) {
	return nil, nil
}
func (s *stubTrading) CancelOrder(_ context.Context, _ string) error { return nil }
func (s *stubTrading) GetOrder(_ context.Context, _ string) (*interfaces.Order, error) {
	return nil, nil
}
func (s *stubTrading) ListOrders(_ context.Context, _ string) ([]*interfaces.Order, error) {
	return nil, nil
}
func (s *stubTrading) GetPositions(_ context.Context) ([]*interfaces.Position, error) {
	return nil, nil
}
func (s *stubTrading) PlaceOptionsOrder(_ context.Context, _ *interfaces.OptionsOrder) (*interfaces.OrderResult, error) {
	return nil, nil
}
func (s *stubTrading) GetOptionsChain(_ context.Context, _ string, _ time.Time) ([]*interfaces.OptionContract, error) {
	return nil, nil
}
func (s *stubTrading) GetOptionsQuote(_ context.Context, _ string) (*interfaces.OptionsQuote, error) {
	return nil, nil
}
func (s *stubTrading) GetOptionsPosition(_ context.Context, _ string) (*interfaces.OptionsPosition, error) {
	return nil, nil
}
func (s *stubTrading) ListOptionsPositions(_ context.Context) ([]*interfaces.OptionsPosition, error) {
	return nil, nil
}

// --- helpers ---

func managedPos(symbol string, agent AgentSource, status string, allocation float64) *ManagedPosition {
	return &ManagedPosition{
		Symbol:            symbol,
		Status:            status,
		AllocationDollars: allocation,
		Tags:              []string{AgentTag(agent)},
	}
}

func defaultConfig() TradeGuardConfig {
	return TradeGuardConfig{
		PennyMaxCapitalPct:      0.20,
		PennyMaxPositionDollars: 500,
	}
}

func sectorConfig() TradeGuardConfig {
	cfg := defaultConfig()
	cfg.EnableSectorAggregation = true
	cfg.SectorMaxExposurePct = map[string]float64{
		"TECH":       0.20,
		"INDEX_BETA": 0.25,
		"ENERGY":     0.15,
		"FINANCIALS": 0.15,
		"HEALTHCARE": 0.15,
		"OTHER":      0.15,
	}
	cfg.DefaultSectorMaxPct = 0.15
	return cfg
}

// --- tests ---

func TestGuard_PennyCannotBuyMainSymbol(t *testing.T) {
	lister := &stubLister{
		positions: []*ManagedPosition{
			managedPos("AAPL", AgentMain, "ACTIVE", 1000),
		},
	}
	g := NewTradeGuard(lister, &stubTrading{portfolio: 10000}, defaultConfig())
	err := g.CheckBuy(context.Background(), AgentPenny, "AAPL", 100)
	if err == nil {
		t.Fatal("expected error: penny buying main symbol")
	}
}

func TestGuard_MainCannotBuyPennySymbol(t *testing.T) {
	lister := &stubLister{
		positions: []*ManagedPosition{
			managedPos("MEME", AgentPenny, "ACTIVE", 200),
		},
	}
	g := NewTradeGuard(lister, &stubTrading{portfolio: 10000}, defaultConfig())
	err := g.CheckBuy(context.Background(), AgentMain, "MEME", 500)
	if err == nil {
		t.Fatal("expected error: main buying penny symbol")
	}
}

func TestGuard_PennyExceedsPerPositionCap(t *testing.T) {
	g := NewTradeGuard(&stubLister{}, &stubTrading{portfolio: 100000}, defaultConfig())
	err := g.CheckBuy(context.Background(), AgentPenny, "XYZ", 600) // cap is 500
	if err == nil {
		t.Fatal("expected error: penny position exceeds per-position cap")
	}
}

func TestGuard_PennyExceedsCapitalCap(t *testing.T) {
	lister := &stubLister{
		positions: []*ManagedPosition{
			managedPos("AAA", AgentPenny, "ACTIVE", 1800), // already $1800 of $2000 cap
		},
	}
	g := NewTradeGuard(lister, &stubTrading{portfolio: 10000}, defaultConfig()) // cap = 20% * 10000 = 2000
	err := g.CheckBuy(context.Background(), AgentPenny, "BBB", 300)             // 1800+300 > 2000
	if err == nil {
		t.Fatal("expected error: penny capital cap exceeded")
	}
}

func TestGuard_PennyBuyAllowed(t *testing.T) {
	lister := &stubLister{
		positions: []*ManagedPosition{
			managedPos("AAA", AgentPenny, "ACTIVE", 500),
		},
	}
	g := NewTradeGuard(lister, &stubTrading{portfolio: 10000}, defaultConfig()) // cap = 2000
	err := g.CheckBuy(context.Background(), AgentPenny, "BBB", 400)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGuard_ClosedPositionNotConflict(t *testing.T) {
	lister := &stubLister{
		positions: []*ManagedPosition{
			managedPos("AAPL", AgentMain, "CLOSED", 1000),
		},
	}
	g := NewTradeGuard(lister, &stubTrading{portfolio: 10000}, defaultConfig())
	err := g.CheckBuy(context.Background(), AgentPenny, "AAPL", 100)
	if err != nil {
		t.Fatalf("closed position should not block: %v", err)
	}
}

func TestGuard_SellBlockedByOpponent(t *testing.T) {
	lister := &stubLister{
		positions: []*ManagedPosition{
			managedPos("TSLA", AgentMain, "ACTIVE", 1000),
		},
	}
	g := NewTradeGuard(lister, nil, defaultConfig())
	err := g.CheckSell(context.Background(), AgentPenny, "TSLA")
	if err == nil {
		t.Fatal("expected error: penny selling main-owned symbol")
	}
}

func TestGuard_RawOrderTracking(t *testing.T) {
	g := NewTradeGuard(&stubLister{}, nil, defaultConfig())
	g.RecordRawBuy(AgentMain, "NVDA")

	// Penny should not be able to buy NVDA now
	err := g.CheckBuy(context.Background(), AgentPenny, "NVDA", 100)
	if err == nil {
		t.Fatal("expected error: penny buying raw-main symbol")
	}

	// After main sells, penny should be able to buy
	g.RecordRawSell(AgentMain, "NVDA")
	err = g.CheckBuy(context.Background(), AgentPenny, "NVDA", 100)
	if err != nil {
		t.Fatalf("expected no error after raw sell: %v", err)
	}
}

func TestGuard_UntaggedPositionTreatedAsMain(t *testing.T) {
	// Legacy position with no agent tag should be owned by main
	lister := &stubLister{
		positions: []*ManagedPosition{
			{Symbol: "IBM", Status: "ACTIVE", AllocationDollars: 500, Tags: []string{}},
		},
	}
	g := NewTradeGuard(lister, nil, defaultConfig())
	err := g.CheckBuy(context.Background(), AgentPenny, "IBM", 100)
	if err == nil {
		t.Fatal("expected error: untagged position should block penny")
	}
}

func TestGuard_EmptyAgentSourceDefaultsToMain(t *testing.T) {
	lister := &stubLister{
		positions: []*ManagedPosition{
			managedPos("GOOG", AgentPenny, "ACTIVE", 200),
		},
	}
	g := NewTradeGuard(lister, nil, defaultConfig())
	// Empty agent source = main; penny holds GOOG → should block
	err := g.CheckBuy(context.Background(), "", "GOOG", 500)
	if err == nil {
		t.Fatal("expected error: empty agent treated as main, cannot buy penny symbol")
	}
}

func TestGuard_DailyLossCircuitBreakerTriggers(t *testing.T) {
	cfg := defaultConfig()
	cfg.MaxDailyLossPct = 5.0
	// Down 6% from previous close: 100000 → 94000
	g := NewTradeGuard(&stubLister{}, &stubTrading{portfolio: 94000, lastEquity: 100000}, cfg)
	if err := g.CheckBuy(context.Background(), AgentMain, "AAPL", 500); err == nil {
		t.Fatal("expected error: daily loss circuit breaker should block at -6% with 5% limit")
	}
	if err := g.CheckBuy(context.Background(), AgentPenny, "ABCD", 100); err == nil {
		t.Fatal("expected error: circuit breaker also applies to penny agent")
	}
}

func TestGuard_DailyLossWithinLimitAllowed(t *testing.T) {
	cfg := defaultConfig()
	cfg.MaxDailyLossPct = 5.0
	// Down 3% — within limit
	g := NewTradeGuard(&stubLister{}, &stubTrading{portfolio: 97000, lastEquity: 100000}, cfg)
	if err := g.CheckBuy(context.Background(), AgentMain, "AAPL", 500); err != nil {
		t.Fatalf("unexpected error within daily-loss limit: %v", err)
	}
}

func TestGuard_DailyLossDisabledWhenZero(t *testing.T) {
	cfg := defaultConfig() // MaxDailyLossPct defaults to 0
	g := NewTradeGuard(&stubLister{}, &stubTrading{portfolio: 50000, lastEquity: 100000}, cfg)
	// Down 50%, but check is disabled — should pass
	if err := g.CheckBuy(context.Background(), AgentMain, "AAPL", 500); err != nil {
		t.Fatalf("daily loss check should be disabled when MaxDailyLossPct is 0: %v", err)
	}
}

func TestGuard_DailyLossSkippedWhenLastEquityMissing(t *testing.T) {
	cfg := defaultConfig()
	cfg.MaxDailyLossPct = 5.0
	// LastEquity = 0 (e.g. brand-new account); fail-open
	g := NewTradeGuard(&stubLister{}, &stubTrading{portfolio: 50000, lastEquity: 0}, cfg)
	if err := g.CheckBuy(context.Background(), AgentMain, "AAPL", 500); err != nil {
		t.Fatalf("daily loss check should fail-open when LastEquity is unknown: %v", err)
	}
}

func TestGuard_DailyLossSkippedWhenTradingServiceNil(t *testing.T) {
	cfg := defaultConfig()
	cfg.MaxDailyLossPct = 5.0
	// No trading service available — must not block trades
	g := NewTradeGuard(&stubLister{}, nil, cfg)
	if err := g.CheckBuy(context.Background(), AgentMain, "AAPL", 0); err != nil {
		t.Fatalf("daily loss check should be skipped when tradingService is nil: %v", err)
	}
}

func TestGuard_PennyCheckBuyFetchesAccountAtMostOnce(t *testing.T) {
	cfg := defaultConfig()
	cfg.MaxDailyLossPct = 5.0
	stub := &stubTrading{portfolio: 100000, lastEquity: 100000}
	g := NewTradeGuard(&stubLister{}, stub, cfg)
	// Penny buy goes through both checkDailyLoss AND checkPennyCapCap — without
	// the single-fetch refactor, this would call GetAccount twice.
	if err := g.CheckBuy(context.Background(), AgentPenny, "ABCD", 100); err != nil {
		t.Fatalf("unexpected error on healthy penny buy: %v", err)
	}
	if stub.getAcctCalls != 1 {
		t.Errorf("expected exactly 1 GetAccount call per CheckBuy, got %d", stub.getAcctCalls)
	}
}

func TestGuard_MainCheckBuyFetchesAccountAtMostOnce(t *testing.T) {
	cfg := defaultConfig()
	cfg.MaxDailyLossPct = 5.0
	stub := &stubTrading{portfolio: 100000, lastEquity: 100000}
	g := NewTradeGuard(&stubLister{}, stub, cfg)
	// Main buy only triggers checkDailyLoss — no penny-cap check, so 1 fetch.
	if err := g.CheckBuy(context.Background(), AgentMain, "AAPL", 1000); err != nil {
		t.Fatalf("unexpected error on healthy main buy: %v", err)
	}
	if stub.getAcctCalls != 1 {
		t.Errorf("expected exactly 1 GetAccount call per main CheckBuy, got %d", stub.getAcctCalls)
	}
}

func TestGuard_PennyCapCapFailsClosedOnFetchError(t *testing.T) {
	// Preserve the pre-refactor semantics: when the trading service errors during
	// the capital-cap check, the buy must be BLOCKED. Allowing it through would
	// silently let a penny position bypass the cap on a flaky network.
	cfg := defaultConfig()
	cfg.MaxDailyLossPct = 0 // disable daily-loss check so we exercise capital-cap path alone
	stub := &stubTrading{getAcctErr: errors.New("alpaca timeout")}
	g := NewTradeGuard(&stubLister{}, stub, cfg)
	err := g.CheckBuy(context.Background(), AgentPenny, "ABCD", 100)
	if err == nil {
		t.Fatal("expected error: penny capital-cap check must fail-closed on fetch error")
	}
}

// ── Sector aggregation (Item 1: cross-agent sector & beta-bucket cap) ──

func TestGuard_SectorCap_BlocksOverConcentration(t *testing.T) {
	// Existing TECH exposure (NVDA + AMD both map to SMH → TECH) at $18K.
	// Portfolio $100K, TECH cap 20% = $20K. A new $3K NVDA buy pushes TECH to
	// $21K → must block.
	lister := &stubLister{
		positions: []*ManagedPosition{
			managedPos("NVDA", AgentMain, "ACTIVE", 10000),
			managedPos("AMD", AgentMain, "ACTIVE", 8000),
		},
	}
	g := NewTradeGuard(lister, &stubTrading{portfolio: 100000, lastEquity: 100000}, sectorConfig())
	err := g.CheckBuy(context.Background(), AgentMain, "NVDA", 3000)
	if err == nil {
		t.Fatal("expected error: TECH sector cap exceeded")
	}
}

func TestGuard_SectorCap_AllowsUnderCap(t *testing.T) {
	// Same TECH positions as the over-concentration test ($18K of $20K cap), but
	// the new buy is small enough to stay under the $20K cap.
	lister := &stubLister{
		positions: []*ManagedPosition{
			managedPos("NVDA", AgentMain, "ACTIVE", 10000),
			managedPos("AMD", AgentMain, "ACTIVE", 8000),
		},
	}
	g := NewTradeGuard(lister, &stubTrading{portfolio: 100000, lastEquity: 100000}, sectorConfig())
	if err := g.CheckBuy(context.Background(), AgentMain, "NVDA", 1500); err != nil {
		t.Fatalf("unexpected error under sector cap: %v", err)
	}
}

func TestGuard_SectorCap_DisabledWhenFlagOff(t *testing.T) {
	// Same over-cap setup as TestGuard_SectorCap_BlocksOverConcentration, but
	// EnableSectorAggregation defaults to false → check must be bypassed.
	cfg := sectorConfig()
	cfg.EnableSectorAggregation = false
	lister := &stubLister{
		positions: []*ManagedPosition{
			managedPos("NVDA", AgentMain, "ACTIVE", 10000),
			managedPos("AMD", AgentMain, "ACTIVE", 8000),
		},
	}
	g := NewTradeGuard(lister, &stubTrading{portfolio: 100000, lastEquity: 100000}, cfg)
	if err := g.CheckBuy(context.Background(), AgentMain, "NVDA", 3000); err != nil {
		t.Fatalf("flag-off should bypass sector cap, got: %v", err)
	}
}

func TestGuard_SectorCap_DefaultBucketForUnmappedSymbol(t *testing.T) {
	// A symbol absent from etfToBucket and sectorETFMap (e.g. "FOOBAR") must
	// fall to the OTHER bucket and inherit DefaultSectorMaxPct when OTHER is
	// not explicitly set in SectorMaxExposurePct.
	cfg := sectorConfig()
	delete(cfg.SectorMaxExposurePct, "OTHER") // force fallback path
	cfg.DefaultSectorMaxPct = 0.15            // $15K cap on $100K portfolio
	lister := &stubLister{
		positions: []*ManagedPosition{
			managedPos("FOOBAR", AgentMain, "ACTIVE", 14000),
		},
	}
	g := NewTradeGuard(lister, &stubTrading{portfolio: 100000, lastEquity: 100000}, cfg)
	if err := g.CheckBuy(context.Background(), AgentMain, "FOOBAR", 2000); err == nil {
		t.Fatal("expected error: OTHER bucket exceeds DefaultSectorMaxPct")
	}
}

func TestGuard_SectorCap_FailsClosedOnFetchError(t *testing.T) {
	// Mirrors the penny-cap fail-closed policy: a transient API failure must
	// NOT silently let a buy bypass the concentration limit.
	cfg := sectorConfig()
	cfg.MaxDailyLossPct = 0 // disable daily-loss path so it doesn't short-circuit
	stub := &stubTrading{getAcctErr: errors.New("alpaca timeout")}
	g := NewTradeGuard(&stubLister{}, stub, cfg)
	err := g.CheckBuy(context.Background(), AgentMain, "NVDA", 1000)
	if err == nil {
		t.Fatal("expected error: sector-cap check must fail-closed on fetch error")
	}
}

func TestGuard_SectorCap_IncludesOptionsProviderContribution(t *testing.T) {
	// A registered OptionsExposureProvider (Harvest, in production) contributes
	// $15K to INDEX_BETA. Cap = 25% × $100K = $25K. A new $11K SPY buy pushes
	// total to $26K and must be blocked.
	g := NewTradeGuard(&stubLister{}, &stubTrading{portfolio: 100000, lastEquity: 100000}, sectorConfig())
	g.SetOptionsExposureProvider(&stubOptionsProvider{
		exposure: map[SectorBucket]float64{"INDEX_BETA": 15000},
	})
	if err := g.CheckBuy(context.Background(), AgentMain, "SPY", 11000); err == nil {
		t.Fatal("expected error: options-provider exposure must count toward INDEX_BETA cap")
	}
}

func TestGuard_Status_ReportsSectorExposure(t *testing.T) {
	// Two managed positions (NVDA → TECH via SMH, XLF → FINANCIALS direct) and
	// an options-provider INDEX_BETA contribution must all appear in Status().
	// SectorMaxByBucket reflects per-bucket caps × portfolio value.
	lister := &stubLister{
		positions: []*ManagedPosition{
			managedPos("NVDA", AgentMain, "ACTIVE", 10000),
			managedPos("XLF", AgentMain, "ACTIVE", 5000),
		},
	}
	g := NewTradeGuard(lister, &stubTrading{portfolio: 100000, lastEquity: 100000}, sectorConfig())
	g.SetOptionsExposureProvider(&stubOptionsProvider{
		exposure: map[SectorBucket]float64{"INDEX_BETA": 7500},
	})
	status := g.Status(context.Background())

	if got := status.SectorExposure["TECH"]; got != 10000 {
		t.Errorf("TECH exposure: want $10000, got $%.2f", got)
	}
	if got := status.SectorExposure["FINANCIALS"]; got != 5000 {
		t.Errorf("FINANCIALS exposure: want $5000, got $%.2f", got)
	}
	if got := status.SectorExposure["INDEX_BETA"]; got != 7500 {
		t.Errorf("INDEX_BETA exposure: want $7500, got $%.2f", got)
	}
	if got := status.SectorMaxByBucket["TECH"]; got != 20000 {
		t.Errorf("TECH cap: want $20000, got $%.2f", got)
	}
	if got := status.SectorMaxByBucket["INDEX_BETA"]; got != 25000 {
		t.Errorf("INDEX_BETA cap: want $25000, got $%.2f", got)
	}
}

func TestGuard_DailyLossFailsOpenOnFetchError(t *testing.T) {
	// Daily-loss check is fail-open on fetch errors (transient API hiccup
	// shouldn't block all trading). Verify a fetch error doesn't block a main buy.
	cfg := defaultConfig()
	cfg.MaxDailyLossPct = 5.0
	stub := &stubTrading{getAcctErr: errors.New("alpaca timeout")}
	g := NewTradeGuard(&stubLister{}, stub, cfg)
	if err := g.CheckBuy(context.Background(), AgentMain, "AAPL", 0); err != nil {
		t.Fatalf("daily-loss check should fail-open on fetch error, got: %v", err)
	}
}
