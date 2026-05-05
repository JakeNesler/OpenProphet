package services

import (
	"testing"
	"time"
)

func bucketIdx(t time.Time) int {
	return int(t.Unix()/1800) % 336
}

func TestMentionBaseline_Advance_AccumulatesInSameBucket(t *testing.T) {
	bl := &mentionBaseline{firstSeen: time.Now().Add(-73 * time.Hour)}
	now := time.Now()
	bl.lastBucket = bucketIdx(now)
	bl.advance(now, 5)
	bl.advance(now, 3)
	if bl.total != 8 {
		t.Errorf("expected total=8, got %d", bl.total)
	}
	if bl.buckets[bucketIdx(now)] != 8 {
		t.Errorf("expected current bucket=8, got %d", bl.buckets[bucketIdx(now)])
	}
}

func TestMentionBaseline_Advance_ZerosPassedBuckets(t *testing.T) {
	bl := &mentionBaseline{firstSeen: time.Now().Add(-73 * time.Hour)}
	old := time.Now().Add(-2 * time.Hour)
	bl.lastBucket = bucketIdx(old)
	oldIdx := bucketIdx(old)
	bl.buckets[oldIdx] = 10
	bl.total = 10

	now := time.Now()
	bl.advance(now, 3)

	if bl.buckets[oldIdx] != 0 {
		t.Errorf("expected old bucket zeroed, got %d", bl.buckets[oldIdx])
	}
	if bl.total != 3 {
		t.Errorf("expected total=3 after clearing old bucket, got %d", bl.total)
	}
}

func TestMentionBaseline_BaselinePer30min_Floor(t *testing.T) {
	bl := &mentionBaseline{total: 0, firstSeen: time.Now().Add(-73 * time.Hour)}
	got := bl.baselinePer30min()
	if got < 0.5 {
		t.Errorf("expected floor 0.5, got %f", got)
	}
}

func TestMentionBaseline_BaselinePer30min_BelowFloor(t *testing.T) {
	// total=10 / 336 ≈ 0.03 < 0.5 → floor applies
	bl := &mentionBaseline{total: 10, firstSeen: time.Now().Add(-73 * time.Hour)}
	got := bl.baselinePer30min()
	if got != 0.5 {
		t.Errorf("expected 0.5 floor for low total, got %f", got)
	}
}

func TestSocialService_NewTicker_72hGuard(t *testing.T) {
	svc := &SocialSignalService{
		entries:   make(map[string]socialEntry),
		baselines: make(map[string]*mentionBaseline),
		logger:    newTestLogger(),
		universe:  &PennyUniverseService{},
	}
	now := time.Now()
	// Ticker first seen < 72h ago
	counts := map[string]int{"NEW": 50}
	svc.recomputeRedditScores(now, counts)

	entry, ok := svc.entries["NEW"]
	if !ok {
		t.Fatal("expected entry for NEW")
	}
	if entry.MentionPts != 0 {
		t.Errorf("expected MentionPts=0 for new ticker (<72h), got %f", entry.MentionPts)
	}
}

func TestSocialService_UniverseExitCleanup(t *testing.T) {
	universe := &PennyUniverseService{}
	universe.universe = []UniverseSymbol{{Ticker: "KEEP"}}
	svc := &SocialSignalService{
		entries:   make(map[string]socialEntry),
		baselines: make(map[string]*mentionBaseline),
		logger:    newTestLogger(),
		universe:  universe,
	}
	svc.baselines["KEEP"] = &mentionBaseline{firstSeen: time.Now().Add(-73 * time.Hour)}
	svc.baselines["GONE"] = &mentionBaseline{firstSeen: time.Now().Add(-73 * time.Hour)}

	now := time.Now()
	svc.recomputeRedditScores(now, map[string]int{"KEEP": 5})

	if _, ok := svc.baselines["GONE"]; ok {
		t.Error("expected GONE removed from baselines after universe exit cleanup")
	}
	if _, ok := svc.baselines["KEEP"]; !ok {
		t.Error("expected KEEP preserved in baselines")
	}
}
