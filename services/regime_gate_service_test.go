package services

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeRegimeFixture writes a fixture regime-gate JSON into a temp dir and
// returns its path. Callers pass score/as_of explicitly; stale_after defaults
// to as_of + 29 hours (matches the 24h+buffer pattern the Python writer uses).
func writeRegimeFixture(t *testing.T, score int, asOf time.Time) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "regime_gate.json")
	content := fmt.Sprintf(
		`{"score": %d, "as_of": %q, "stale_after": %q}`,
		score,
		asOf.Format(time.RFC3339),
		asOf.Add(29*time.Hour).Format(time.RFC3339),
	)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestRegimeGate_LoadsValidFile(t *testing.T) {
	// Given a well-formed regime_gate.json on disk, GetStatus must populate
	// Score and AsOf from the file. Tier derivation is exercised separately
	// in TestRegimeGate_TierBoundaries.
	asOf := time.Now().UTC().Add(-1 * time.Hour).Truncate(time.Second) // fresh: 1h old (RFC3339 = second precision)
	path := writeRegimeFixture(t, 62, asOf)
	svc := NewRegimeGateService(RegimeGateConfig{
		EnableRegimeGate: true,
		ReportPath:       path,
	})
	status := svc.GetStatus()

	if status.Score != 62 {
		t.Errorf("Score: want 62, got %d", status.Score)
	}
	if !status.AsOf.Equal(asOf) {
		t.Errorf("AsOf: want %v, got %v", asOf, status.AsOf)
	}
	if status.IsStale {
		t.Error("IsStale: want false on a 1h-old file")
	}
}

func TestRegimeGate_TierBoundaries(t *testing.T) {
	// Score→Tier→SizingMultiplier mapping:
	//   0–19  RED        0.0×
	//   20–39 DEFENSIVE  0.5×
	//   40–69 NORMAL     0.8×
	//   70–100 GREEN     1.0×
	cases := []struct {
		score    int
		wantTier string
		wantMult float64
	}{
		{0, "RED", 0.0},
		{19, "RED", 0.0},
		{20, "DEFENSIVE", 0.5},
		{39, "DEFENSIVE", 0.5},
		{40, "NORMAL", 0.8},
		{69, "NORMAL", 0.8},
		{70, "GREEN", 1.0},
		{100, "GREEN", 1.0},
	}
	asOf := time.Now().UTC().Add(-1 * time.Hour).Truncate(time.Second)
	for _, tc := range cases {
		path := writeRegimeFixture(t, tc.score, asOf)
		svc := NewRegimeGateService(RegimeGateConfig{
			EnableRegimeGate: true,
			ReportPath:       path,
		})
		status := svc.GetStatus()
		if status.Tier != tc.wantTier {
			t.Errorf("score %d: Tier want %q, got %q", tc.score, tc.wantTier, status.Tier)
		}
		if status.SizingMultiplier != tc.wantMult {
			t.Errorf("score %d: SizingMultiplier want %.2f, got %.2f", tc.score, tc.wantMult, status.SizingMultiplier)
		}
	}
}

func TestRegimeGate_BlockNewEntriesOnlyInRedTier(t *testing.T) {
	// BlockNewEntries must be true ONLY when tier=RED. DEFENSIVE/NORMAL/GREEN
	// throttle sizing but allow entries.
	cases := []struct {
		score     int
		wantBlock bool
	}{
		{0, true}, {19, true},
		{20, false}, {39, false}, {40, false}, {69, false}, {70, false}, {100, false},
	}
	asOf := time.Now().UTC().Add(-1 * time.Hour).Truncate(time.Second)
	for _, tc := range cases {
		path := writeRegimeFixture(t, tc.score, asOf)
		svc := NewRegimeGateService(RegimeGateConfig{
			EnableRegimeGate: true,
			ReportPath:       path,
		})
		if got := svc.GetStatus().BlockNewEntries; got != tc.wantBlock {
			t.Errorf("score %d: BlockNewEntries want %v, got %v", tc.score, tc.wantBlock, got)
		}
	}
}

func TestRegimeGate_FailOpenOnStaleFile(t *testing.T) {
	// File present but stale_after has passed. IsStale must report true, but
	// the tier/sizing/block keep firing as if fresh — switching behavior on
	// stale data would whipsaw between "use last known regime" and "neutral"
	// based on a clock crossing. Better to flag and trust the most recent
	// computation; the operator's loud-log handles real outages.
	now := time.Now().UTC()
	asOf := now.Add(-48 * time.Hour).Truncate(time.Second)        // 2 days ago
	staleAfter := now.Add(-12 * time.Hour).Truncate(time.Second)  // crossed 12h ago
	dir := t.TempDir()
	path := filepath.Join(dir, "regime_gate.json")
	content := fmt.Sprintf(
		`{"score": 62, "as_of": %q, "stale_after": %q}`,
		asOf.Format(time.RFC3339), staleAfter.Format(time.RFC3339),
	)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	svc := NewRegimeGateService(RegimeGateConfig{
		EnableRegimeGate: true,
		ReportPath:       path,
	})
	status := svc.GetStatus()

	if !status.IsStale {
		t.Error("IsStale: want true on past stale_after")
	}
	if status.Tier != "NORMAL" {
		t.Errorf("Tier: want NORMAL (preserve last-known), got %q", status.Tier)
	}
	if status.SizingMultiplier != 0.8 {
		t.Errorf("SizingMultiplier: want 0.8 (preserve last-known), got %.2f", status.SizingMultiplier)
	}
}

func TestRegimeGate_FlagOffNeutralizesOutput(t *testing.T) {
	// Observation-mode rollout: when EnableRegimeGate=false the service still
	// loads and reports the underlying Score/Tier (so operators can see what
	// the gate WOULD do in production logs), but SizingMultiplier resets to
	// 1.0 and BlockNewEntries to false. Mirrors Item 1's flag-gated pattern.
	asOf := time.Now().UTC().Add(-1 * time.Hour).Truncate(time.Second)
	path := writeRegimeFixture(t, 10, asOf) // score 10 → RED tier with enforcement
	svc := NewRegimeGateService(RegimeGateConfig{
		EnableRegimeGate: false,
		ReportPath:       path,
	})
	status := svc.GetStatus()

	if status.Tier != "RED" {
		t.Errorf("Tier: want RED (still reported for observation), got %q", status.Tier)
	}
	if status.Score != 10 {
		t.Errorf("Score: want 10 (still reported), got %d", status.Score)
	}
	if status.SizingMultiplier != 1.0 {
		t.Errorf("SizingMultiplier: want 1.0 with flag off, got %.2f", status.SizingMultiplier)
	}
	if status.BlockNewEntries {
		t.Error("BlockNewEntries: want false with flag off")
	}
}

func TestRegimeGate_FailOpenOnMissingFile(t *testing.T) {
	// Regime data going missing must not brick the trading system. When the
	// JSON file is absent, GetStatus must return a neutral fail-open status:
	// tier=UNKNOWN, sizing_multiplier=1.0, block=false. A loud log is the
	// operator's signal, not a sizing change.
	svc := NewRegimeGateService(RegimeGateConfig{
		EnableRegimeGate: true,
		ReportPath:       `/nonexistent/path/regime_gate.json`,
	})
	status := svc.GetStatus()

	if status.Tier != "UNKNOWN" {
		t.Errorf("Tier: want UNKNOWN, got %q", status.Tier)
	}
	if status.SizingMultiplier != 1.0 {
		t.Errorf("SizingMultiplier: want 1.0, got %f", status.SizingMultiplier)
	}
	if status.BlockNewEntries {
		t.Error("BlockNewEntries: want false on missing file")
	}
}
