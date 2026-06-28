package authn

import (
	"testing"
	"time"
)

func TestLimiterBlocksAfterMaxThenRecoversAfterWindow(t *testing.T) {
	l := NewLimiter(3, time.Minute)
	base := time.Unix(1_700_000_000, 0)
	l.now = func() time.Time { return base }
	for i := 0; i < 3; i++ {
		if !l.Allowed("patrik") {
			t.Fatalf("attempt %d should be allowed", i)
		}
		l.Fail("patrik")
	}
	if l.Allowed("patrik") {
		t.Fatal("4th attempt must be blocked")
	}
	l.now = func() time.Time { return base.Add(2 * time.Minute) }
	if !l.Allowed("patrik") {
		t.Fatal("must recover after window")
	}
}

func TestLimiterEvictsStaleKeysAfterWindow(t *testing.T) {
	l := NewLimiter(3, time.Minute)
	base := time.Unix(1_700_000_000, 0)
	l.now = func() time.Time { return base }
	// A flood of distinct usernames must not leave permanent map entries.
	for i := 0; i < 50; i++ {
		l.Fail(string(rune('a' + i%26)))
	}
	if len(l.fails) == 0 {
		t.Fatal("precondition: keys should exist while inside the window")
	}
	// Advance past the window; touching each key (via Allowed/prune) must evict it.
	l.now = func() time.Time { return base.Add(2 * time.Minute) }
	for i := 0; i < 50; i++ {
		l.Allowed(string(rune('a' + i%26)))
	}
	if len(l.fails) != 0 {
		t.Fatalf("stale keys not evicted after window: %d remain", len(l.fails))
	}
}

func TestLimiterResetOnSuccess(t *testing.T) {
	l := NewLimiter(2, time.Minute)
	l.Fail("p")
	l.Fail("p")
	if l.Allowed("p") {
		t.Fatal("should be blocked")
	}
	l.Reset("p")
	if !l.Allowed("p") {
		t.Fatal("reset must clear failures")
	}
}
