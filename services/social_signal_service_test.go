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

func TestMentionBaseline_Advance_PreservesCompletedBucket(t *testing.T) {
	now := time.Now()
	bucketA := bucketIdx(now)
	bl := &mentionBaseline{firstSeen: now.Add(-73 * time.Hour)}
	bl.lastBucket = bucketA
	bl.advance(now, 10)
	// total should be 10 from the completed advance
	if bl.total != 10 {
		t.Fatalf("expected total=10, got %d", bl.total)
	}

	// Advance to the next 30-min bucket
	nextBucketTime := now.Add(31 * time.Minute)
	bl.advance(nextBucketTime, 3)

	// The completed bucket (A) must still contribute to total
	if bl.buckets[bucketA] != 10 {
		t.Errorf("completed bucket should be preserved; got buckets[A]=%d", bl.buckets[bucketA])
	}
	if bl.total != 13 {
		t.Errorf("total should be 13 (10 from A + 3 from new bucket), got %d", bl.total)
	}
}

func TestMentionBaseline_Advance_RecyclesStaleSlot(t *testing.T) {
	now := time.Now()
	currentIdx := bucketIdx(now)
	bl := &mentionBaseline{firstSeen: now.Add(-200 * time.Hour)}

	// Manually place stale data in currentIdx (simulating 7-day-old occupancy)
	bl.buckets[currentIdx] = 7
	bl.total = 7
	// lastBucket is one slot before current (so advance triggers recycling of currentIdx)
	bl.lastBucket = (currentIdx - 1 + 336) % 336

	bl.advance(now, 3)

	if bl.buckets[currentIdx] != 3 {
		t.Errorf("recycled slot should hold only new count; got %d", bl.buckets[currentIdx])
	}
	if bl.total != 3 {
		t.Errorf("stale 7-day data should be subtracted; expected total=3, got %d", bl.total)
	}
}

func TestMentionBaseline_BaselinePer30min_Floor(t *testing.T) {
	bl := &mentionBaseline{total: 0, firstSeen: time.Now().Add(-73 * time.Hour)}
	got := bl.baselinePer30min()
	if got != 0.5 {
		t.Errorf("expected floor exactly 0.5 for zero total, got %f", got)
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
