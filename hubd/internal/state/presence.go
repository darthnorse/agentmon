package state

import "sync"

// Presence is a concurrency-safe, ref-counted online tracker for principals
// (or any string id). Each live SSE connection for a principal increments the
// count via Add and decrements it via Remove; the principal is considered
// "online" while at least one connection is held.
//
// It is used by the M9 push dispatcher for server-side de-dup: when a principal
// has a live SSE stream (online), the in-app Tier 1/2 alerts cover them, so the
// dispatcher suppresses the Web-Push (Tier 3) for that principal.
//
// The zero value is not usable; construct with NewPresence.
type Presence struct {
	mu     sync.Mutex
	counts map[string]int
}

// NewPresence returns an empty Presence tracker ready for use.
func NewPresence() *Presence {
	return &Presence{counts: make(map[string]int)}
}

// Add records a new live connection for id, incrementing its reference count.
func (p *Presence) Add(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.counts[id]++
}

// Remove releases one live connection for id, decrementing its reference count.
// When the count reaches zero the key is deleted so the map does not grow
// unbounded. Remove on an id with no live connections is a no-op: the count is
// never driven negative and it never panics.
func (p *Presence) Remove(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	n, ok := p.counts[id]
	if !ok {
		return
	}
	if n <= 1 {
		delete(p.counts, id)
		return
	}
	p.counts[id] = n - 1
}

// Online reports whether id currently holds at least one live connection.
func (p *Presence) Online(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.counts[id] > 0
}
