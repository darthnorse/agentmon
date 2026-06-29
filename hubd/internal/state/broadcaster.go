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
// Drop-oldest semantics: if a subscriber's buffer is full we evict the oldest
// queued item and enqueue c so the freshest state always wins.
//
// Both the eviction and the send are non-blocking. The subtle part is WHY the
// send can't block. We hold b.mu, and Publish is the ONLY sender (close also
// happens under b.mu in cancel). Consumers receive WITHOUT the lock, so they
// can only REMOVE items between our statements — never add. Therefore:
//   - The eviction MUST be non-blocking: a consumer may have drained the buffer
//     since the failed send, so a plain `<-ch` could block forever (deadlock:
//     Publish stuck holding the lock, no one can ever send again). We use a
//     select/default so it removes at most one item and never waits.
//   - After the non-blocking eviction the buffer holds <= cap-1 items, and
//     because we are the sole sender and consumers only remove, it stays
//     <= cap-1 until our send. So `ch <- c` is guaranteed not to block.
//
// No other goroutine can send to or close any ch while b.mu is held, which also
// rules out send-on-closed-channel panics.
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
			// Buffer full: evict oldest (non-blocking — a racing consumer may
			// have already drained it), then enqueue newest.
			select {
			case <-ch:
			default:
			}
			ch <- c // non-blocking: sole sender under lock + consumer-only-removes ⇒ room exists
		}
	}
}
