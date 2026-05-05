package services

import (
	"testing"
	"time"
)

func TestDecayEntry_EffectiveScore_AtZero(t *testing.T) {
	d := DecayEntry{BaseScore: 40.0, EventTime: time.Now(), HalfLifeHrs: 2.0}
	got := d.EffectiveScore()
	if got < 39.9 || got > 40.0 {
		t.Errorf("expected ~40.0 at t=0, got %f", got)
	}
}

func TestDecayEntry_EffectiveScore_AtHalfLife(t *testing.T) {
	d := DecayEntry{BaseScore: 40.0, EventTime: time.Now().Add(-2 * time.Hour), HalfLifeHrs: 2.0}
	got := d.EffectiveScore()
	if got < 19.5 || got > 20.5 {
		t.Errorf("expected ~20.0 at half-life, got %f", got)
	}
}

func TestDecayEntry_EffectiveScore_Floor(t *testing.T) {
	// 9h with 2h half-life: 40 * 0.5^4.5 ≈ 1.77, < 5% of 40 (=2.0) → 0
	d := DecayEntry{BaseScore: 40.0, EventTime: time.Now().Add(-9 * time.Hour), HalfLifeHrs: 2.0}
	got := d.EffectiveScore()
	if got != 0 {
		t.Errorf("expected 0 at decay floor, got %f", got)
	}
}

func TestDecayEntry_EffectiveScore_JustAboveFloor(t *testing.T) {
	// 8h with 2h half-life: 40 * 0.5^4 = 2.5, > 5% of 40 (=2.0) → not floored
	d := DecayEntry{BaseScore: 40.0, EventTime: time.Now().Add(-8 * time.Hour), HalfLifeHrs: 2.0}
	got := d.EffectiveScore()
	if got <= 0 {
		t.Errorf("expected positive score at 4 half-lives (above floor), got %f", got)
	}
}

func TestDecayEntry_EffectiveScore_ZeroBaseScore(t *testing.T) {
	d := DecayEntry{BaseScore: 0, EventTime: time.Now(), HalfLifeHrs: 2.0}
	got := d.EffectiveScore()
	if got != 0 {
		t.Errorf("expected 0 for zero base score, got %f", got)
	}
}

func TestScoreWithDecay_NoDecayAtZeroElapsed(t *testing.T) {
	// At t=0 decay factor is 1.0 so score is unchanged.
	got := scoreWithDecay(40.0, time.Now(), 2.0)
	if got < 39.9 || got > 40.0 {
		t.Errorf("expected ~40.0 at t=0, got %f", got)
	}
}

func TestScoreWithDecay_HalfAtHalfLife(t *testing.T) {
	detectedAt := time.Now().Add(-2 * time.Hour) // 2 hours ago, halfLife=2h
	got := scoreWithDecay(40.0, detectedAt, 2.0)
	if got < 19.5 || got > 20.5 {
		t.Errorf("expected ~20.0 at half-life, got %f", got)
	}
}

func TestDominantSignal(t *testing.T) {
	tests := []struct {
		tech, reg, soc float64
		want           string
	}{
		{40, 0, 0, "technical"},
		{0, 40, 0, "regulatory"},
		{0, 0, 20, "social"},
		{20, 30, 10, "regulatory"},
	}
	for _, tc := range tests {
		got := dominantSignal(tc.tech, tc.reg, tc.soc)
		if got != tc.want {
			t.Errorf("dominantSignal(%v,%v,%v)=%v, want %v", tc.tech, tc.reg, tc.soc, got, tc.want)
		}
	}
}
