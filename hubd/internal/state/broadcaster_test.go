package state

import (
	"sync"
	"testing"
	"time"

	"agentmon/shared"
)

func TestBroadcasterDelivers(t *testing.T) {
	b := NewBroadcaster()
	_, ch, cancel := b.Subscribe()
	defer cancel()
	b.Publish(Change{Session: "a", Global: shared.StateBlocked})
	select {
	case c := <-ch:
		if c.Session != "a" || c.Global != shared.StateBlocked {
			t.Fatalf("got %+v", c)
		}
	case <-time.After(time.Second):
		t.Fatal("no delivery")
	}
}

func TestBroadcasterCancelStopsDelivery(t *testing.T) {
	b := NewBroadcaster()
	_, ch, cancel := b.Subscribe()
	cancel()
	b.Publish(Change{Session: "a"})
	if _, open := <-ch; open {
		t.Fatal("channel should be closed after cancel")
	}
}

func TestBroadcasterDropsOldestWhenFull(t *testing.T) {
	b := NewBroadcaster()
	_, ch, cancel := b.Subscribe()
	defer cancel()
	for i := 0; i < 200; i++ {
		b.Publish(Change{Session: "x"}) // never blocks despite no reader
	}
	if len(ch) == 0 {
		t.Fatal("expected buffered changes")
	}
}

// TestBroadcasterConcurrent checks for data races: multiple goroutines publish
// simultaneously while one goroutine reads and another cancels.
func TestBroadcasterConcurrent(t *testing.T) {
	t.Parallel()
	b := NewBroadcaster()
	_, ch, cancel := b.Subscribe()

	const publishers = 4
	const publishes = 200

	var wg sync.WaitGroup
	wg.Add(publishers)
	for i := 0; i < publishers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < publishes; j++ {
				b.Publish(Change{Session: "concurrent"})
			}
		}()
	}

	// Drain in background; stops when cancel() closes the channel.
	drainDone := make(chan struct{})
	go func() {
		defer close(drainDone)
		for range ch {
		}
	}()

	wg.Wait()
	cancel()
	<-drainDone
}

// TestBroadcasterDoubleCancel ensures a second cancel() call does not panic.
func TestBroadcasterDoubleCancel(t *testing.T) {
	b := NewBroadcaster()
	_, _, cancel := b.Subscribe()
	cancel()
	cancel() // must not panic
}
