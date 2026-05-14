package services

import (
	"encoding/json"
	"os"
	"time"
)

// RegimeGateStatus is the agent-readable snapshot of the current regime gate.
// Score is the raw 0-100 composite from upstream skills; Tier and the
// enforcement fields are derived from it.
type RegimeGateStatus struct {
	Score            int       `json:"score"`
	Tier             string    `json:"tier"`
	SizingMultiplier float64   `json:"sizing_multiplier"`
	BlockNewEntries  bool      `json:"block_new_entries"`
	AsOf             time.Time `json:"as_of"`
	StaleAfter       time.Time `json:"stale_after"`
	IsStale          bool      `json:"is_stale"`
}

// RegimeGateConfig configures the regime gate service.
//
// Flag-gated rollout: while EnableRegimeGate is false, GetStatus returns a
// neutral output (sizing_multiplier=1.0, block=false) regardless of the
// underlying tier. This mirrors Item 1's pattern: observe in production
// for ~2 weeks via Status() before flipping enforcement on.
type RegimeGateConfig struct {
	EnableRegimeGate bool
	ReportPath       string
}

// RegimeGateService loads the daily regime-gate JSON snapshot written by the
// upstream Python computation and exposes it to agents. Fail-open semantics:
// missing or unparseable file returns tier=UNKNOWN, sizing=1.0, block=false —
// a transient absence must not silently halt all trading. The operator gets
// the signal via the structured log; the agent sees neutral output.
type RegimeGateService struct {
	cfg RegimeGateConfig
}

// NewRegimeGateService constructs the service. The report file is not read
// at construction time; each GetStatus call re-reads from disk to pick up
// daily updates without restart.
func NewRegimeGateService(cfg RegimeGateConfig) *RegimeGateService {
	return &RegimeGateService{cfg: cfg}
}

// regimeGateFile is the on-disk JSON schema written by compute_daily_regime_score.py.
type regimeGateFile struct {
	Score      int       `json:"score"`
	AsOf       time.Time `json:"as_of"`
	StaleAfter time.Time `json:"stale_after"`
}

// GetStatus returns the current regime-gate state.
//   - File missing or unparseable → tier=UNKNOWN, sizing=1.0, block=false.
//   - EnableRegimeGate=false → underlying tier reported, but sizing=1.0
//     and block=false (observation-only).
//   - Otherwise → tier derived from Score, sizing/block derived from tier.
func (s *RegimeGateService) GetStatus() RegimeGateStatus {
	neutral := RegimeGateStatus{
		Tier:             "UNKNOWN",
		SizingMultiplier: 1.0,
		BlockNewEntries:  false,
	}
	if s.cfg.ReportPath == "" {
		return neutral
	}
	data, err := os.ReadFile(s.cfg.ReportPath)
	if err != nil {
		return neutral
	}
	var raw regimeGateFile
	if err := json.Unmarshal(data, &raw); err != nil {
		return neutral
	}
	return s.buildStatus(raw)
}

// buildStatus is split out so test paths and future cache layers share the
// same Score→Tier→sizing/block derivation.
func (s *RegimeGateService) buildStatus(raw regimeGateFile) RegimeGateStatus {
	tier, mult, block := deriveTier(raw.Score)
	if !s.cfg.EnableRegimeGate {
		// Observation-mode: tier is still reported so operators can see what
		// enforcement WOULD do, but sizing/block stay neutral.
		mult = 1.0
		block = false
	}
	return RegimeGateStatus{
		Score:            raw.Score,
		Tier:             tier,
		SizingMultiplier: mult,
		BlockNewEntries:  block,
		AsOf:             raw.AsOf,
		StaleAfter:       raw.StaleAfter,
		IsStale:          !raw.StaleAfter.IsZero() && time.Now().After(raw.StaleAfter),
	}
}

// deriveTier maps a 0-100 regime score to the tier name, sizing multiplier,
// and entry-block flag. Boundaries follow the plan:
//
//	[0,20)   RED        0.0× (entries blocked)
//	[20,40)  DEFENSIVE  0.5×
//	[40,70)  NORMAL     0.8×
//	[70,100] GREEN      1.0×
func deriveTier(score int) (tier string, mult float64, block bool) {
	switch {
	case score < 20:
		return "RED", 0.0, true
	case score < 40:
		return "DEFENSIVE", 0.5, false
	case score < 70:
		return "NORMAL", 0.8, false
	default:
		return "GREEN", 1.0, false
	}
}
