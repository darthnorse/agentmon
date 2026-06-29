package state

import (
	"sync"

	"agentmon/shared"
)

const subBufCap = 64

// Change is a state-change event fanned out to all active SSE and terminal-WS
// subscribers. Fields mirror SessionView; consumers may snapshot the Projection
// to reconcile any dropped changes (see drop-oldest note on Publish).
type Change struct {
	ServerID         string
	Target           string
	Session          string
	Global           shared.State
	LatestReceivedAt string
}

// Broadcaster fans Change values out to all current subscribers in a
// non-blocking fashion.  It is safe for concurrent use.
type Broadcaster struct {
	mu     sync.Mutex
	subs   map[uint64]chan Change
	nextID uint64
}

// NewBroadcaster returns a ready-to-use Broadcaster.
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{subs: make(map[uint64]chan Change)}
}

// Subscribe registers a new subscriber.  It returns a monotonic id, a
// read-only channel that receives changes, and a cancel function.
//
// cancel removes the subscription and closes ch; it is idempotent (calling it
// more than once does not panic or double-close).
func (b *Broadcaster) Subscribe() (id uint64, ch <-chan Change, cancel func()) {
	b.mu.Lock()
	defer b.mu.Unlock()

	id = b.nextID
	b.nextID++

	c := make(chan Change, subBufCap)
	b.subs[id] = c

	cancel = func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		// Guard with a map lookup so double-cancel is a no-op.
		if existing, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(existing)
		}
	}
	return id, c, cancel
}

// Publish fans c out to every subscriber without blocking.
//
// Drop-oldest semantics: if a subscriber's buffer is full we drain the oldest
// queued item and enqueue c so the freshest state always wins.  Both
// operations are guaranteed non-blocking under the lock:
//   - <-ch succeeds because len(ch)==cap(ch) (the non-blocking send just failed)
//   - ch<-c succeeds because we just freed exactly one slot
//
// No other goroutine can send to or close any ch while b.mu is held:
// Publish itself holds the lock, and cancel() also acquires b.mu before
// closing/deleting.  This rules out send-on-closed-channel panics and races.
//
// Subscribers that fall behind see only the latest cap(ch) changes; a
// subsequent SSE or WS full-state snapshot reconciles any gaps.
func (b *Broadcaster) Publish(c Change) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for _, ch := range b.subs {
		select {
		case ch <- c:
		default:
			// Buffer full: evict oldest, enqueue newest.
			<-ch    // non-blocking: buffer was full, so at least one item is present
			ch <- c // non-blocking: we just freed one slot; no other sender under lock
		}
	}
}
