package authn

import (
	"sync"
	"testing"
)

func TestGaugeAcquireUpToCapThenReject(t *testing.T) {
	g := NewGauge(2)
	if !g.Acquire("u1") || !g.Acquire("u1") {
		t.Fatal("first two acquires should succeed")
	}
	if g.Acquire("u1") {
		t.Fatal("third acquire should be rejected at cap")
	}
	if got := g.InUse("u1"); got != 2 {
		t.Fatalf("InUse = %d, want 2 (rejected acquire must not increment)", got)
	}
	// A different key has its own budget.
	if !g.Acquire("u2") {
		t.Fatal("distinct key should have its own budget")
	}
}

func TestGaugeReleaseFreesSlotAndDeletesAtZero(t *testing.T) {
	g := NewGauge(1)
	if !g.Acquire("u1") {
		t.Fatal("acquire should succeed")
	}
	if g.Acquire("u1") {
		t.Fatal("at cap")
	}
	g.Release("u1")
	if got := g.InUse("u1"); got != 0 {
		t.Fatalf("InUse after release = %d, want 0", got)
	}
	if len(g.inuse) != 0 {
		t.Fatalf("map should evict the zeroed key, len = %d", len(g.inuse))
	}
	if !g.Acquire("u1") {
		t.Fatal("acquire should succeed again after release")
	}
}

func TestGaugeReleaseUnheldIsNoop(t *testing.T) {
	g := NewGauge(1)
	g.Release("nobody") // must not panic or go negative
	if got := g.InUse("nobody"); got != 0 {
		t.Fatalf("InUse = %d, want 0", got)
	}
}

func TestGaugeConcurrentAcquireRelease(t *testing.T) {
	g := NewGauge(1000)
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if g.Acquire("u1") {
					g.Release("u1")
				}
			}
		}()
	}
	wg.Wait()
	if got := g.InUse("u1"); got != 0 {
		t.Fatalf("InUse after balanced acquire/release = %d, want 0", got)
	}
}
