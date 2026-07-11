package report

import (
	"fmt"
	"testing"

	"agentmon/shared"
)

func rep(epic int) shared.OrchestratorReport {
	return shared.OrchestratorReport{Repo: "o/r", Epic: epic, Stage: shared.EpicPlanning, Session: "s", Ts: "t"}
}

func TestDrainReturnsBufferedWithCursor(t *testing.T) {
	s := NewStore("inst", 10)
	s.Add("default", rep(1))
	s.Add("default", rep(2))
	b := s.Drain("default", "", 0)
	if b.Instance != "inst" || b.Cursor != 2 || len(b.Reports) != 2 || b.Reports[0].Epic != 1 {
		t.Fatalf("batch = %+v", b)
	}
	// Nothing acked yet: a re-drain redelivers the same batch.
	b2 := s.Drain("default", "", 0)
	if b2.Cursor != 2 || len(b2.Reports) != 2 {
		t.Fatalf("redelivery batch = %+v", b2)
	}
}

func TestAckDeletesOnlyMatchingInstanceTargetAndSeq(t *testing.T) {
	s := NewStore("inst", 10)
	s.Add("default", rep(1))
	s.Add("other", rep(2))
	s.Add("default", rep(3))
	// Wrong instance: deletes nothing.
	if b := s.Drain("default", "stale", 3); len(b.Reports) != 2 {
		t.Fatalf("stale instance must not delete: %+v", b)
	}
	// Right instance, ack seq 1: deletes only default's seq 1; seq 3 remains.
	b := s.Drain("default", "inst", 1)
	if len(b.Reports) != 1 || b.Reports[0].Epic != 3 || b.Cursor != 3 {
		t.Fatalf("batch = %+v", b)
	}
	// The other target was untouched.
	if b := s.Drain("other", "inst", 0); len(b.Reports) != 1 || b.Reports[0].Epic != 2 {
		t.Fatalf("other target = %+v", b)
	}
}

func TestEmptyDrainHasZeroCursor(t *testing.T) {
	s := NewStore("inst", 10)
	b := s.Drain("default", "", 0)
	if b.Cursor != 0 || b.Reports == nil || len(b.Reports) != 0 {
		t.Fatalf("empty batch = %+v", b)
	}
}

func TestOverflowDropsOldest(t *testing.T) {
	s := NewStore("inst", 3)
	for i := 1; i <= 5; i++ {
		s.Add("default", rep(i))
	}
	b := s.Drain("default", "", 0)
	if len(b.Reports) != 3 || b.Reports[0].Epic != 3 || b.Reports[2].Epic != 5 {
		t.Fatalf("overflow batch = %+v", b)
	}
}

func TestNewInstanceID(t *testing.T) {
	a, b := NewInstanceID(), NewInstanceID()
	if len(a) != 16 || a == b {
		t.Fatalf("instance ids: %q %q", a, b)
	}
	if _, err := fmt.Sscanf(a, "%x", new(uint64)); err != nil {
		t.Fatalf("not hex: %q", a)
	}
}
