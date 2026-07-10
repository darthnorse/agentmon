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
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, id)
			b.mu.Unlock()
		})
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
			select { // drop oldest, then retry once
			case <-ch:
			default:
			}
			select {
			case ch <- c:
			default:
			}
		}
	}
}
