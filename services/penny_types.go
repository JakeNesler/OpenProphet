package services

import (
	"math"
	"time"
)

// DecayEntry is the canonical in-memory representation of a decaying signal.
// All three signal services store one DecayEntry per ticker.
type DecayEntry struct {
	BaseScore   float64
	EventTime   time.Time
	HalfLifeHrs float64
}

// EffectiveScore returns the decayed score, floored to zero below 5% of base.
func (d DecayEntry) EffectiveScore() float64 {
	if d.BaseScore <= 0 || d.HalfLifeHrs <= 0 {
		return 0
	}
	elapsed := time.Since(d.EventTime).Hours()
	lambda := math.Log(2) / d.HalfLifeHrs
	decayed := d.BaseScore * math.Exp(-lambda*elapsed)
	if decayed < 0.05*d.BaseScore {
		return 0
	}
	return decayed
}

// UniverseSymbol is a single entry in the penny stock watchable universe.
type UniverseSymbol struct {
	Ticker       string  `json:"ticker"`
	Name         string  `json:"name"`
	Exchange     string  `json:"exchange"`
	Price        float64 `json:"price"`
	MarketCapM   float64 `json:"market_cap_m"` // millions
	AvgDollarVol float64 `json:"avg_dollar_vol"`
}

// CandidateScore is the aggregated signal score for one symbol.
type CandidateScore struct {
	Ticker              string    `json:"ticker"`
	Price               float64   `json:"price"`
	CompositeScore      float64   `json:"composite_score"`
	SignalCount         int       `json:"signal_count"`
	CompositeEligible   bool      `json:"composite_eligible"`

	TechnicalScore      float64   `json:"technical_score"`
	TechnicalEffective  float64   `json:"technical_effective"`
	RegulatoryScore     float64   `json:"regulatory_score"`
	RegulatoryEffective float64   `json:"regulatory_effective"`
	SocialScore         float64   `json:"social_score"`
	SocialEffective     float64   `json:"social_effective"`

	DominantSignal   string    `json:"dominant_signal"`
	TechnicalContext string    `json:"technical_context,omitempty"`
	RegulatoryEvent  string    `json:"regulatory_event,omitempty"`
	SocialContext    string    `json:"social_context,omitempty"`
	LastUpdated      time.Time `json:"last_updated"`
}

// scoreWithDecay applies exponential decay to a base score.
// halfLifeHours: time in hours for the score to decay to 50%.
func scoreWithDecay(baseScore float64, detectedAt time.Time, halfLifeHours float64) float64 {
	elapsed := time.Since(detectedAt).Hours()
	lambda := math.Log(2) / halfLifeHours
	return baseScore * math.Exp(-lambda*elapsed)
}

// dominantSignal returns which of the three sub-scores dominates,
// normalized by its maximum possible value.
func dominantSignal(technical, regulatory, social float64) string {
	techNorm := technical / 40.0
	regNorm := regulatory / 40.0
	socNorm := social / 20.0
	if techNorm >= regNorm && techNorm >= socNorm {
		return "technical"
	}
	if regNorm >= socNorm {
		return "regulatory"
	}
	return "social"
}
