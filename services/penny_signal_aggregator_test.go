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
	// composite = 30+20+10 = 60
	if c.CompositeScore < 59 || c.CompositeScore > 61 {
		t.Errorf("expected composite ~60, got %f", c.CompositeScore)
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
	agg.candidates["MED"] = CandidateScore{Ticker: "MED", CompositeScore: 65}
	agg.candidates["HIGH"] = CandidateScore{Ticker: "HIGH", CompositeScore: 82}

	above80 := agg.GetCandidates(80)
	if len(above80) != 1 || above80[0].Ticker != "HIGH" {
		t.Errorf("expected only HIGH above 80, got %v", above80)
	}
}

func TestAggregator_GetCandidateSummaries_StripsContext(t *testing.T) {
	agg := aggregatorForTest(0, 0, 0, []string{"ABC"})
	agg.candidates["ABC"] = CandidateScore{
		Ticker:           "ABC",
		CompositeScore:   75,
		TechnicalScore:   30,
		RegulatoryScore:  25,
		SocialScore:      20,
		DominantSignal:   "technical",
		TechnicalContext: "RSI 72, volume 4.2x",
		RegulatoryEvent:  "8-K filed 09:32 ET",
		SocialContext:    "3.2x mention velocity, 71% bullish",
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
