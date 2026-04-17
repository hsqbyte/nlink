package services

import (
	"testing"
)

func TestStatsHistoryBasic(t *testing.T) {
	h := NewStatsHistory(3)
	if got := h.Snapshot(0); len(got) != 0 {
		t.Fatalf("empty should return 0 len, got %d", len(got))
	}
	h.Append(StatsSnapshot{Timestamp: 1})
	h.Append(StatsSnapshot{Timestamp: 2})
	got := h.Snapshot(0)
	if len(got) != 2 || got[0].Timestamp != 1 || got[1].Timestamp != 2 {
		t.Fatalf("ordering wrong: %+v", got)
	}
}

func TestStatsHistoryRingOverwrite(t *testing.T) {
	h := NewStatsHistory(3)
	for i := int64(1); i <= 5; i++ {
		h.Append(StatsSnapshot{Timestamp: i})
	}
	got := h.Snapshot(0)
	if len(got) != 3 {
		t.Fatalf("expected 3, got %d", len(got))
	}
	// 应该是最近三条：3,4,5
	if got[0].Timestamp != 3 || got[1].Timestamp != 4 || got[2].Timestamp != 5 {
		t.Fatalf("ring order wrong: %+v", got)
	}
}

func TestStatsHistoryLimit(t *testing.T) {
	h := NewStatsHistory(10)
	for i := int64(1); i <= 10; i++ {
		h.Append(StatsSnapshot{Timestamp: i})
	}
	got := h.Snapshot(3)
	if len(got) != 3 || got[0].Timestamp != 8 || got[2].Timestamp != 10 {
		t.Fatalf("limit broke: %+v", got)
	}
}
