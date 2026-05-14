package services

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRegimeGate_ParsesPythonWriterOutput is an integration check that the
// Go side parses the exact schema scripts/compute_daily_regime_score.py
// writes. If this test fails after a script change, either the script broke
// or the Go regimeGateFile struct needs a corresponding update.
//
// Keep this fixture byte-exact with the script's output format (json.dumps
// with indent=2). Use a real timestamp the Go parser can consume.
func TestRegimeGate_ParsesPythonWriterOutput(t *testing.T) {
	const fixture = `{
  "score": 52,
  "as_of": "2026-05-14T22:05:17Z",
  "stale_after": "2026-05-16T03:05:17Z",
  "components": {
    "breadth":  {"value": 70, "source": "breadth.json",  "present": true},
    "macro":    {"value": 80, "source": "macro.json",    "present": true},
    "top_risk": {"value": 30, "source": "top.json",      "present": true},
    "bubble":   {"value": 50, "source": "bubble.json",   "present": true}
  },
  "formula": "0.35*breadth + 0.30*(100-macro) + 0.20*(100-top_risk) + 0.15*(100-bubble)"
}`
	dir := t.TempDir()
	path := filepath.Join(dir, "regime_gate.json")
	if err := os.WriteFile(path, []byte(fixture), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	svc := NewRegimeGateService(RegimeGateConfig{
		EnableRegimeGate: true,
		ReportPath:       path,
	})
	status := svc.GetStatus()

	if status.Score != 52 {
		t.Errorf("Score: want 52, got %d", status.Score)
	}
	if status.Tier != "NORMAL" {
		t.Errorf("Tier: want NORMAL (40<=52<70), got %q", status.Tier)
	}
	if status.SizingMultiplier != 0.8 {
		t.Errorf("SizingMultiplier: want 0.8, got %f", status.SizingMultiplier)
	}
	if status.AsOf.IsZero() {
		t.Error("AsOf: want parsed RFC3339, got zero time")
	}
	if status.StaleAfter.IsZero() {
		t.Error("StaleAfter: want parsed RFC3339, got zero time")
	}
}
