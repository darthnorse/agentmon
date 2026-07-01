package authn

import "sync"

// Gauge is a per-key live concurrency counter. Acquire takes a slot when the
// key is below the cap; Release frees one. Unlike Limiter (a sliding-window
// RATE counter), Gauge tracks slots currently held, so it bounds concurrent
// long-lived resources — e.g. terminal-WS relays, each of which makes the agent
// spawn a tmux control-mode subprocess. Reject-newest: at the cap Acquire
// returns false WITHOUT incrementing, so an existing holder is never evicted.
type Gauge struct {
	mu    sync.Mutex
	max   int
	inuse map[string]int
}

// NewGauge returns a Gauge that allows at most max concurrent slots per key.
func NewGauge(max int) *Gauge {
	return &Gauge{max: max, inuse: make(map[string]int)}
}

// Acquire reserves a slot for key and reports success. At the cap it returns
// false without incrementing.
func (g *Gauge) Acquire(key string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.inuse[key] >= g.max {
		return false
	}
	g.inuse[key]++
	return true
}

// Release frees a slot for key. The key is deleted at zero so a churn of
// distinct keys cannot grow the map unbounded. Releasing an unheld key is a no-op.
func (g *Gauge) Release(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.inuse[key] <= 0 {
		return // not held: nothing to release
	}
	g.inuse[key]--
	if g.inuse[key] == 0 {
		delete(g.inuse, key) // evict the zeroed key so the map stays bounded
	}
}

// InUse returns the number of slots currently held for key (0 if none).
func (g *Gauge) InUse(key string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.inuse[key]
}
