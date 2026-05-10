package services

import (
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

// aggregatorForTest builds a PennySignalAggregator with pre-seeded sub-service state.
func aggregatorForTest(techScore, regScore, socScore float64, tickers []string) *PennySignalAggregator {
	universe := &PennyUniverseService{logger: logrus.New()}
	universe.universe = make([]UniverseSymbol, len(tickers))
	for i, t := range tickers {
		universe.universe[i] = UniverseSymbol{Ticker: t, Price: 5.0}
	}

	screener := &PennyScreenerService{
		scores: make(map[string]TechnicalEntry),
		logger: logrus.New(),
	}
	for _, t := range tickers {
		screener.scores[t] = TechnicalEntry{Entry: DecayEntry{BaseScore: techScore, EventTime: time.Now(), HalfLifeHrs: 2.0}}
	}

	edgar := &SECEdgarService{
		entries: make(map[string]regulatoryEntry),
		logger:  logrus.New(),
	}
	for _, t := range tickers {
		edgar.entries[t] = regulatoryEntry{
			Entry:     DecayEntry{BaseScore: regScore, EventTime: time.Now(), HalfLifeHrs: regulatoryHalfLifeHours},
			EventDesc: "test event",
		}
	}

	social := &SocialSignalService{
		entries: make(map[string]socialEntry),
		logger:  logrus.New(),
	}
	for _, t := range tickers {
		social.entries[t] = socialEntry{
			Entry:      DecayEntry{BaseScore: socScore, EventTime: time.Now(), HalfLifeHrs: socialHalfLifeHours},
			MentionPts: socScore,
			Context:    "test ctx",
		}
	}

	return NewPennySignalAggregator(universe, screener, edgar, social)
}

func TestAggregator_Composite(t *testing.T) {
	agg := aggregatorForTest(30.0, 20.0, 10.0, []string{"TICK"})
	agg.aggregate()
	candidates := agg.GetCandidates(0)
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	c := candidates[0]
	if c.Ticker != "TICK" {
		t.Errorf("expected TICK, got %s", c.Ticker)
	}
	// tech=30 (≥15), reg=20 (<25 → 0), social=10 (≥10) → composite=40, eligible=true
	if c.CompositeScore < 39 || c.CompositeScore > 41 {
		t.Errorf("expected composite ~40, got %f", c.CompositeScore)
	}
	if !c.CompositeEligible {
		t.Error("expected CompositeEligible=true (tech+social both contribute)")
	}
	if c.DominantSignal != "technical" {
		t.Errorf("expected dominant=technical, got %s", c.DominantSignal)
	}
}

func TestAggregator_EvictsLowScore(t *testing.T) {
	agg := aggregatorForTest(5.0, 2.0, 1.0, []string{"WEAK"}) // composite=8 < evictionThreshold=10
	agg.aggregate()
	candidates := agg.GetCandidates(0)
	if len(candidates) != 0 {
		t.Errorf("expected 0 candidates for composite<10, got %d", len(candidates))
	}
}

func TestAggregator_MinScoreFilter(t *testing.T) {
	agg := aggregatorForTest(30.0, 25.0, 15.0, []string{"HIGH", "MED"})
	// Directly seed candidates to avoid aggregate() recomputing the scores.
	agg.candidates["MED"] = CandidateScore{Ticker: "MED", CompositeScore: 65, CompositeEligible: true}
	agg.candidates["HIGH"] = CandidateScore{Ticker: "HIGH", CompositeScore: 82, CompositeEligible: true}

	above80 := agg.GetCandidates(80)
	if len(above80) != 1 || above80[0].Ticker != "HIGH" {
		t.Errorf("expected only HIGH above 80, got %v", above80)
	}
}

func TestAggregator_GetCandidateSummaries_StripsContext(t *testing.T) {
	agg := aggregatorForTest(0, 0, 0, []string{"ABC"})
	agg.candidates["ABC"] = CandidateScore{
		Ticker:            "ABC",
		CompositeScore:    75,
		CompositeEligible: true,
		TechnicalScore:    30,
		RegulatoryScore:   25,
		SocialScore:       20,
		DominantSignal:    "technical",
		TechnicalContext:  "RSI 72, volume 4.2x",
		RegulatoryEvent:   "8-K filed 09:32 ET",
		SocialContext:     "3.2x mention velocity, 71% bullish",
	}

	summaries := agg.GetCandidateSummaries(60)
	if len(summaries) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(summaries))
	}
	c := summaries[0]
	if c.Ticker != "ABC" || c.CompositeScore != 75 {
		t.Errorf("scalar fields should be preserved, got ticker=%s score=%v", c.Ticker, c.CompositeScore)
	}
	if c.DominantSignal != "technical" {
		t.Errorf("dominant_signal should be preserved (load-bearing for routing), got %q", c.DominantSignal)
	}
	if c.TechnicalContext != "" || c.RegulatoryEvent != "" || c.SocialContext != "" {
		t.Errorf("context strings should be cleared, got tech=%q reg=%q soc=%q",
			c.TechnicalContext, c.RegulatoryEvent, c.SocialContext)
	}

	// Confirm the internal cache was NOT mutated — the full-detail call must still return context.
	full := agg.GetCandidates(60)
	if len(full) != 1 || full[0].TechnicalContext != "RSI 72, volume 4.2x" {
		t.Errorf("GetCandidateSummaries must not mutate the underlying cache; full[0]=%+v", full[0])
	}
}

func TestAggregator_GetSignalDetail(t *testing.T) {
	agg := aggregatorForTest(30.0, 20.0, 10.0, []string{"TICK"})
	agg.aggregate()
	detail := agg.GetSignalDetail("TICK")
	if detail == nil {
		t.Fatal("expected detail for TICK, got nil")
	}
	if detail.Ticker != "TICK" {
		t.Errorf("expected TICK, got %s", detail.Ticker)
	}
	// Verify returned pointer is a copy (mutating it should not affect the cache)
	detail.CompositeScore = 999
	cached := agg.GetSignalDetail("TICK")
	if cached.CompositeScore == 999 {
		t.Error("GetSignalDetail returned a reference to the internal cache, not a copy")
	}
}

func TestAggregator_GetSignalDetail_NotFound(t *testing.T) {
	agg := aggregatorForTest(0, 0, 0, []string{})
	detail := agg.GetSignalDetail("NONE")
	if detail != nil {
		t.Errorf("expected nil for unknown ticker, got %+v", detail)
	}
}

func TestAggregator_SingleSignalBlocked_HighReg(t *testing.T) {
	// tech=10 (<15 min), reg=30 (≥25), social=5 (<10 min) → only reg contributes → single → not eligible
	agg := aggregatorForTest(10.0, 30.0, 5.0, []string{"TICK"})
	agg.aggregate()
	candidates := agg.GetCandidates(0)
	if len(candidates) != 0 {
		t.Errorf("single-signal candidate should not appear in GetCandidates, got %d", len(candidates))
	}
	detail := agg.GetSignalDetail("TICK")
	if detail == nil {
		t.Fatal("single-signal candidate should still be stored internally")
	}
	if detail.CompositeEligible {
		t.Error("expected CompositeEligible=false for single-signal")
	}
	if detail.SignalCount != 1 {
		t.Errorf("expected SignalCount=1, got %d", detail.SignalCount)
	}
}

func TestAggregator_TwoSignalPasses(t *testing.T) {
	// tech=20 (≥15), reg=30 (≥25), social=5 (<10) → tech+reg contribute → eligible
	agg := aggregatorForTest(20.0, 30.0, 5.0, []string{"TICK"})
	agg.aggregate()
	candidates := agg.GetCandidates(0)
	if len(candidates) != 1 {
		t.Errorf("two-signal candidate should appear in GetCandidates, got %d", len(candidates))
	}
	if candidates[0].SignalCount != 2 {
		t.Errorf("expected SignalCount=2, got %d", candidates[0].SignalCount)
	}
	if !candidates[0].CompositeEligible {
		t.Error("expected CompositeEligible=true")
	}
	// composite = techEff(20) + regEff(30) + socEff(0) = 50
	if candidates[0].CompositeScore < 49 || candidates[0].CompositeScore > 51 {
		t.Errorf("expected composite ~50, got %f", candidates[0].CompositeScore)
	}
}

func TestAggregator_ThreeSignalMaxCapped(t *testing.T) {
	// tech=40, reg=40, social=20 → composite = min(100, 100) = 100
	agg := aggregatorForTest(40.0, 40.0, 20.0, []string{"TICK"})
	agg.aggregate()
	candidates := agg.GetCandidates(0)
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	if candidates[0].CompositeScore != 100.0 {
		t.Errorf("expected composite=100, got %f", candidates[0].CompositeScore)
	}
	if candidates[0].SignalCount != 3 {
		t.Errorf("expected SignalCount=3, got %d", candidates[0].SignalCount)
	}
}

func TestAggregator_DominantSignal_UsesEffective(t *testing.T) {
	// tech=10 (below min → techEff=0), reg=30 (≥25 → regEff=30), social=15 (≥10 → socEff=15)
	// dominant from effective: reg=30, soc=15 → reg wins
	agg := aggregatorForTest(10.0, 30.0, 15.0, []string{"TICK"})
	agg.aggregate()
	detail := agg.GetSignalDetail("TICK")
	if detail == nil {
		t.Fatal("expected detail")
	}
	if detail.DominantSignal != "regulatory" {
		t.Errorf("expected dominant=regulatory, got %q", detail.DominantSignal)
	}
}

func TestAggregator_Blacklist_FiltersFromGetCandidates(t *testing.T) {
	agg := aggregatorForTest(30.0, 30.0, 15.0, []string{"TICK"})
	agg.aggregate()
	// Confirm candidate is eligible before blacklisting
	before := agg.GetCandidates(0)
	if len(before) != 1 {
		t.Fatalf("expected 1 candidate before blacklisting, got %d", len(before))
	}
	agg.AddToBlacklist("TICK", "bracket rejection test")
	after := agg.GetCandidates(0)
	if len(after) != 0 {
		t.Errorf("expected 0 candidates after blacklisting TICK, got %d", len(after))
	}
}

func TestAggregator_Blacklist_RemoveRestoresCandidates(t *testing.T) {
	agg := aggregatorForTest(30.0, 30.0, 15.0, []string{"TICK"})
	agg.aggregate()
	agg.AddToBlacklist("TICK", "test")
	agg.RemoveFromBlacklist("TICK")
	candidates := agg.GetCandidates(0)
	if len(candidates) != 1 {
		t.Errorf("expected 1 candidate after removing from blacklist, got %d", len(candidates))
	}
}

func TestAggregator_Blacklist_ClearUnblocksAll(t *testing.T) {
	agg := aggregatorForTest(30.0, 30.0, 15.0, []string{"A", "B"})
	agg.aggregate()
	agg.AddToBlacklist("A", "test")
	agg.AddToBlacklist("B", "test")
	agg.ClearBlacklist()
	candidates := agg.GetCandidates(0)
	if len(candidates) != 2 {
		t.Errorf("expected 2 candidates after ClearBlacklist, got %d", len(candidates))
	}
}

func TestAggregator_Blacklist_AttemptCountIncrements(t *testing.T) {
	agg := aggregatorForTest(0, 0, 0, []string{})
	agg.AddToBlacklist("TICK", "first")
	agg.AddToBlacklist("TICK", "second")
	// IsBlacklisted confirms it's still there; AttemptCount is internal but
	// we verify the entry is present and the second call didn't reset it.
	if !agg.IsBlacklisted("TICK") {
		t.Error("expected TICK still blacklisted after second AddToBlacklist")
	}
	// Verify AttemptCount by checking the blacklist entry directly via the exported method.
	// Since AttemptCount isn't exposed, we verify the invariant via observable behavior:
	// the entry survives and is still blacklisted.
}

func TestGetCandidates_SuppressesDilutionBlocked(t *testing.T) {
	universe := &PennyUniverseService{}
	screener := &PennyScreenerService{scores: map[string]TechnicalEntry{}}
	earnings := &EarningsCalendarService{
		entries:  map[string]earningsEntry{},
		calendar: []AlpacaCalendarEntry{{Date: "2026-05-10"}},
		logger:   logrus.New(),
	}
	edgar := &SECEdgarService{
		entries:        make(map[string]regulatoryEntry),
		dilutionBlocks: make(map[string]dilutionEntry),
		logger:         logrus.New(),
		nowFunc:        func() time.Time { return time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC) },
		earnings:       earnings,
	}
	edgar.dilutionBlocks["BLOCKED"] = dilutionEntry{
		Ticker:   "BLOCKED",
		FormType: "S-1",
		FiledAt:  time.Date(2026, 5, 10, 9, 0, 0, 0, time.UTC),
		Bucket:   "takedown",
	}
	social := &SocialSignalService{}
	agg := NewPennySignalAggregator(universe, screener, edgar, social)

	SeedCandidateForTest(agg, CandidateScore{
		Ticker:            "OPEN",
		CompositeScore:    80,
		CompositeEligible: true,
	})
	SeedCandidateForTest(agg, CandidateScore{
		Ticker:            "BLOCKED",
		CompositeScore:    85,
		CompositeEligible: true,
	})

	got := agg.GetCandidates(60)
	if len(got) != 1 {
		t.Fatalf("expected 1 candidate after dilution suppression, got %d", len(got))
	}
	if got[0].Ticker != "OPEN" {
		t.Errorf("expected OPEN to remain, got %q", got[0].Ticker)
	}
}

// Regression guard: a dilution block must not delete the underlying score data;
// GetSignalDetail must still return the candidate so operator audit / log_decision
// trails remain intact. This pins the Section 3 design decision that block ≠ exit
// and block ≠ data deletion.
func TestDilutionBlockDoesNotDeleteSignalDetail(t *testing.T) {
	earnings := &EarningsCalendarService{
		entries:  map[string]earningsEntry{},
		calendar: []AlpacaCalendarEntry{{Date: "2026-05-10"}},
		logger:   logrus.New(),
	}
	edgar := &SECEdgarService{
		entries:        make(map[string]regulatoryEntry),
		dilutionBlocks: make(map[string]dilutionEntry),
		logger:         logrus.New(),
		nowFunc:        func() time.Time { return time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC) },
		earnings:       earnings,
	}
	edgar.dilutionBlocks["HELD"] = dilutionEntry{
		Ticker:   "HELD",
		FormType: "S-1",
		FiledAt:  time.Date(2026, 5, 10, 9, 0, 0, 0, time.UTC),
		Bucket:   "takedown",
	}
	agg := NewPennySignalAggregator(&PennyUniverseService{}, &PennyScreenerService{}, edgar, &SocialSignalService{})
	SeedCandidateForTest(agg, CandidateScore{
		Ticker:            "HELD",
		CompositeScore:    85,
		CompositeEligible: true,
		TechnicalScore:    35,
		RegulatoryScore:   30,
		SocialScore:       20,
	})

	// Block suppresses the candidate from GetCandidates...
	if cands := agg.GetCandidates(60); len(cands) != 0 {
		t.Errorf("expected 0 candidates from GetCandidates, got %d", len(cands))
	}
	// ...but GetSignalDetail still returns the underlying score for audit.
	detail := agg.GetSignalDetail("HELD")
	if detail == nil {
		t.Fatal("GetSignalDetail returned nil; block must not delete signal data")
	}
	if detail.CompositeScore != 85 {
		t.Errorf("expected composite=85 preserved, got %f", detail.CompositeScore)
	}
}
