package authn

import (
	"sync"
	"time"
)

// Limiter is a per-key sliding-window failed-attempt counter for login throttling.
type Limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	fails  map[string][]time.Time
	now    func() time.Time
}

func NewLimiter(maxAttempts int, window time.Duration) *Limiter {
	return &Limiter{max: maxAttempts, window: window, fails: make(map[string][]time.Time), now: time.Now}
}

func (l *Limiter) prune(key string, t time.Time) []time.Time {
	cutoff := t.Add(-l.window)
	kept := l.fails[key][:0]
	for _, ts := range l.fails[key] {
		if ts.After(cutoff) {
			kept = append(kept, ts)
		}
	}
	// Evict empty keys instead of retaining an empty slice, so a flood of distinct
	// (attacker-supplied) usernames cannot grow the map unbounded. Fail re-creates
	// the key via append-to-nil; Allowed treats an absent key as zero failures.
	if len(kept) == 0 {
		delete(l.fails, key)
	} else {
		l.fails[key] = kept
	}
	return kept
}

func (l *Limiter) Allowed(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.prune(key, l.now())) < l.max
}

func (l *Limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	t := l.now()
	l.prune(key, t)
	l.fails[key] = append(l.fails[key], t)
}

func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	delete(l.fails, key)
	l.mu.Unlock()
}
