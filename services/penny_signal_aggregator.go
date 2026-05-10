package services

import (
	"context"
	"math"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

const aggregatorRefreshInterval = 10 * time.Second
const evictionThreshold = 10.0

// BracketBlacklistEntry records a session-scoped bracket-rejection for one ticker.
type BracketBlacklistEntry struct {
	Ticker       string
	RejectedAt   time.Time
	RejectReason string
	AttemptCount int
}

// Lock ordering: PennySignalAggregator.mu must always be acquired before
// BracketBlacklist.mu and before SECEdgarService.dilutionMu. GetCandidates
// holds a.mu.RLock while calling blacklist.IsBlacklisted (b.mu.RLock) and
// edgar.IsDilutionBlocked (which may take dilutionMu.Lock during eviction).
// No code path may acquire BracketBlacklist.mu or SECEdgarService.dilutionMu
// before PennySignalAggregator.mu.
type BracketBlacklist struct {
	mu      sync.RWMutex
	entries map[string]BracketBlacklistEntry
}

func newBracketBlacklist() *BracketBlacklist {
	return &BracketBlacklist{entries: make(map[string]BracketBlacklistEntry)}
}

func (b *BracketBlacklist) IsBlacklisted(ticker string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	_, ok := b.entries[ticker]
	return ok
}

// PennySignalAggregator combines three sub-scores into composite CandidateScore entries.
type PennySignalAggregator struct {
	universe     *PennyUniverseService
	screener     *PennyScreenerService
	edgar        *SECEdgarService
	social       *SocialSignalService
	mu           sync.RWMutex
	candidates   map[string]CandidateScore
	blacklist    *BracketBlacklist
	logger       *logrus.Logger
	dilutionMode string // "shadow" (log only) or "enforce" (suppress); default "shadow"
}

// NewPennySignalAggregator creates the aggregator.
func NewPennySignalAggregator(
	universe *PennyUniverseService,
	screener *PennyScreenerService,
	edgar *SECEdgarService,
	social *SocialSignalService,
) *PennySignalAggregator {
	logger := logrus.New()
	logger.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})
	mode := os.Getenv("PENNY_DILUTION_FILTER_MODE")
	if mode != "enforce" {
		mode = "shadow"
	}
	logger.WithField("mode", mode).Info("PennySignalAggregator: dilution filter mode")
	return &PennySignalAggregator{
		universe:     universe,
		screener:     screener,
		edgar:        edgar,
		social:       social,
		candidates:   make(map[string]CandidateScore),
		blacklist:    newBracketBlacklist(),
		logger:       logger,
		dilutionMode: mode,
	}
}

func (a *PennySignalAggregator) AddToBlacklist(ticker, reason string) {
	a.blacklist.mu.Lock()
	defer a.blacklist.mu.Unlock()
	if entry, ok := a.blacklist.entries[ticker]; ok {
		entry.AttemptCount++
		entry.RejectReason = reason
		a.blacklist.entries[ticker] = entry
	} else {
		a.blacklist.entries[ticker] = BracketBlacklistEntry{
			Ticker:       ticker,
			RejectedAt:   time.Now(),
			RejectReason: reason,
			AttemptCount: 1,
		}
	}
	a.logger.WithField("ticker", ticker).WithField("reason", reason).
		Info("PennySignalAggregator: added to bracket blacklist")
}

func (a *PennySignalAggregator) RemoveFromBlacklist(ticker string) {
	a.blacklist.mu.Lock()
	defer a.blacklist.mu.Unlock()
	delete(a.blacklist.entries, ticker)
}

func (a *PennySignalAggregator) ClearBlacklist() {
	a.blacklist.mu.Lock()
	defer a.blacklist.mu.Unlock()
	a.blacklist.entries = make(map[string]BracketBlacklistEntry)
}

func (a *PennySignalAggregator) IsBlacklisted(ticker string) bool {
	return a.blacklist.IsBlacklisted(ticker)
}

// Start runs the aggregation loop until ctx is cancelled.
func (a *PennySignalAggregator) Start(ctx context.Context) {
	a.aggregate()
	ticker := time.NewTicker(aggregatorRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.aggregate()
		}
	}
}

// GetCandidates returns all scored candidates above minScore that are composite-eligible
// and not blacklisted (bracket rejection or dilution).
func (a *PennySignalAggregator) GetCandidates(minScore float64) []CandidateScore {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var out []CandidateScore
	for _, c := range a.candidates {
		if !c.CompositeEligible || c.CompositeScore < minScore {
			continue
		}
		if a.blacklist.IsBlacklisted(c.Ticker) {
			continue
		}
		if a.edgar != nil {
			if blocked, reason := a.edgar.IsDilutionBlocked(c.Ticker); blocked {
				a.logger.WithFields(logrus.Fields{
					"ticker":    c.Ticker,
					"composite": c.CompositeScore,
					"reason":    reason,
					"mode":      a.dilutionMode,
				}).Info("dilution block detected on candidate")
				if a.dilutionMode == "enforce" {
					continue
				}
			}
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CompositeScore > out[j].CompositeScore
	})
	return out
}

// GetCandidateSummaries returns the same data as GetCandidates but with the
// human-readable context strings (technical_context, regulatory_event,
// social_context) cleared. Scores and dominant_signal are preserved so callers
// can still rank, filter, and route by signal type. Use GetSignalDetail to
// fetch the context strings for a specific ticker.
//
// Empty context strings are omitted from JSON output via the `omitempty` tags,
// so this version of the payload is significantly smaller than GetCandidates.
func (a *PennySignalAggregator) GetCandidateSummaries(minScore float64) []CandidateScore {
	full := a.GetCandidates(minScore)
	for i := range full {
		full[i].TechnicalContext = ""
		full[i].RegulatoryEvent = ""
		full[i].SocialContext = ""
	}
	return full
}

// SeedCandidateForTest inserts a candidate directly into the aggregator's cache.
// Intended for tests in other packages (e.g. controllers) that need to populate
// the aggregator without running the full aggregate() pipeline.
func SeedCandidateForTest(a *PennySignalAggregator, c CandidateScore) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.candidates == nil {
		a.candidates = make(map[string]CandidateScore)
	}
	a.candidates[c.Ticker] = c
}

// GetSignalDetail returns the full CandidateScore for one ticker, or nil if not tracked.
func (a *PennySignalAggregator) GetSignalDetail(ticker string) *CandidateScore {
	a.mu.RLock()
	defer a.mu.RUnlock()
	c, ok := a.candidates[ticker]
	if !ok {
		return nil
	}
	cp := c
	return &cp
}

// GetUniverse returns the current universe from the universe service.
func (a *PennySignalAggregator) GetUniverse() []UniverseSymbol {
	return a.universe.GetUniverse()
}

// RefreshUniverse logs an informational message. The universe self-refreshes on its 15-min cycle.
// This method exists for MCP tool compatibility (scan_penny_universe_now).
func (a *PennySignalAggregator) RefreshUniverse() {
	a.logger.Info("PennySignalAggregator: RefreshUniverse called (next auto-refresh in ≤15min)")
}

func (a *PennySignalAggregator) aggregate() {
	universe := a.universe.GetUniverse()
	now := time.Now()

	a.mu.Lock()
	defer a.mu.Unlock()

	for _, u := range universe {
		techScore, techCtx := a.screener.GetTechnicalScore(u.Ticker)
		regScore, regEvent := a.edgar.GetRegulatoryScore(u.Ticker)
		socScore, socCtx := a.social.GetSocialScore(u.Ticker)

		techEff := techScore
		if techScore < 15 {
			techEff = 0
		}
		regEff := regScore
		if regScore < 25 {
			regEff = 0
		}
		socEff := socScore
		if socScore < 10 {
			socEff = 0
		}

		signalCount := 0
		if techEff > 0 {
			signalCount++
		}
		if regEff > 0 {
			signalCount++
		}
		if socEff > 0 {
			signalCount++
		}

		composite := math.Min(techEff+regEff+socEff, 100.0)
		eligible := signalCount >= 2

		if composite < evictionThreshold {
			delete(a.candidates, u.Ticker)
			continue
		}

		if !eligible {
			a.logger.WithFields(logrus.Fields{
				"ticker":    u.Ticker,
				"composite": composite,
			}).Debug("single-signal candidate, below confluence requirement")
		}

		a.candidates[u.Ticker] = CandidateScore{
			Ticker:              u.Ticker,
			Price:               u.Price,
			CompositeScore:      composite,
			SignalCount:         signalCount,
			CompositeEligible:   eligible,
			TechnicalScore:      techScore,
			TechnicalEffective:  techEff,
			RegulatoryScore:     regScore,
			RegulatoryEffective: regEff,
			SocialScore:         socScore,
			SocialEffective:     socEff,
			DominantSignal:      dominantSignal(techEff, regEff, socEff),
			TechnicalContext:    techCtx,
			RegulatoryEvent:     regEvent,
			SocialContext:       socCtx,
			LastUpdated:         now,
		}
	}
}
