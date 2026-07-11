package orchestrator

import (
	"sync"

	"agentmon/shared"
)

const boardSubBufCap = 64

// BoardChange is one epic's board-relevant delta, fanned to SSE + push.
type BoardChange struct {
	ProjectID string
	EpicID    string
	Issue     int
	Stage     shared.EpicStage
	Needs     string
	Title     string
}

type BoardBroadcaster struct {
	mu     sync.Mutex
	nextID uint64
	subs   map[uint64]chan BoardChange
}

func NewBoardBroadcaster() *BoardBroadcaster {
	return &BoardBroadcaster{subs: map[uint64]chan BoardChange{}}
}

func (b *BoardBroadcaster) Subscribe() (uint64, <-chan BoardChange, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.nextID
	b.nextID++
	ch := make(chan BoardChange, boardSubBufCap)
	b.subs[id] = ch
	// Mirror state.Broadcaster exactly: cancel removes the subscription and
	// CLOSES ch (consumers use `c, ok := <-ch` close-detection); the map
	// lookup makes double-cancel safe without a sync.Once.
	cancel := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if existing, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(existing)
		}
	}
	return id, ch, cancel
}

// Publish never blocks: a slow subscriber loses its oldest queued change.
func (b *BoardBroadcaster) Publish(c BoardChange) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.subs {
		select {
		case ch <- c:
		default:
			// Buffer full: evict the oldest, then send. The post-eviction
			// send cannot block — Publish is the sole sender and holds b.mu,
			// and consumers only ever remove (same invariant as
			// state/broadcaster.go).
			select {
			case <-ch:
			default:
			}
			ch <- c
		}
	}
}
