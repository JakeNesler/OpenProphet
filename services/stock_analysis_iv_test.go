package services

import (
	"fmt"
	"testing"
)

// stubIVProvider implements the IVProvider interface for enrichment tests.
type stubIVProvider struct {
	data *IVRData
	err  error
}

func (s *stubIVProvider) GetIVRDataLatest(underlying string) (*IVRData, error) {
	return s.data, s.err
}

func TestEnrichAnalysisWithIV_NilProvider_LeavesIVUnset(t *testing.T) {
	analysis := &StockAnalysis{Symbol: "SPY"}
	enrichAnalysisWithIV(analysis, nil, "SPY")
	if analysis.IV != nil {
		t.Errorf("expected analysis.IV=nil when provider is nil, got %+v", analysis.IV)
	}
}

func TestEnrichAnalysisWithIV_ProviderError_LeavesIVUnset(t *testing.T) {
	analysis := &StockAnalysis{Symbol: "SPY"}
	provider := &stubIVProvider{err: fmt.Errorf("DB down")}
	enrichAnalysisWithIV(analysis, provider, "SPY")
	if analysis.IV != nil {
		t.Errorf("expected analysis.IV=nil on provider error, got %+v", analysis.IV)
	}
}

func TestEnrichAnalysisWithIV_NoHistory_AttachesSentinels(t *testing.T) {
	// A newly added symbol has zero days yet — surface that explicitly so the
	// LLM can downweight rather than think we forgot the field.
	analysis := &StockAnalysis{Symbol: "NEW"}
	provider := &stubIVProvider{data: &IVRData{Underlying: "NEW", IVR: -1, IVPercentile: -1, DaysOfHistory: 0}}
	enrichAnalysisWithIV(analysis, provider, "NEW")
	if analysis.IV == nil {
		t.Fatal("expected IV summary attached even when no history")
	}
	if analysis.IV.Rank != -1 || analysis.IV.Percentile != -1 || analysis.IV.DaysOfHistory != 0 {
		t.Errorf("expected -1/-1/0 sentinels, got %+v", analysis.IV)
	}
}

func TestEnrichAnalysisWithIV_PopulatesFromData(t *testing.T) {
	analysis := &StockAnalysis{Symbol: "SPY"}
	provider := &stubIVProvider{data: &IVRData{
		Underlying:    "SPY",
		CurrentIV:     0.22,
		IVR:           42.5,
		IVPercentile:  35.0,
		DaysOfHistory: 187,
	}}
	enrichAnalysisWithIV(analysis, provider, "SPY")
	if analysis.IV == nil {
		t.Fatal("expected IV summary populated")
	}
	if analysis.IV.Rank != 42.5 || analysis.IV.Percentile != 35.0 || analysis.IV.DaysOfHistory != 187 {
		t.Errorf("unexpected IV summary: %+v", analysis.IV)
	}
}
