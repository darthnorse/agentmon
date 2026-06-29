package state

import (
	"strconv"
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

// TestBroadcasterDropsOldestWhenFull verifies the drop-OLDEST semantics (not
// merely "some buffered changes"): with no concurrent reader, publishing more
// than cap distinguishable values must leave exactly the most-recent cap items
// in FIFO order. This distinguishes drop-oldest from drop-newest.
func TestBroadcasterDropsOldestWhenFull(t *testing.T) {
	b := NewBroadcaster()
	_, ch, cancel := b.Subscribe()
	defer cancel()

	const total = 200
	for i := 0; i < total; i++ {
		b.Publish(Change{Session: strconv.Itoa(i)}) // never blocks despite no reader
	}

	// Publishing is finished and there is no other reader, so len(ch) is stable.
	n := len(ch)
	if n != subBufCap {
		t.Fatalf("buffer len = %d, want cap %d", n, subBufCap)
	}

	var got []string
	for i := 0; i < n; i++ {
		got = append(got, (<-ch).Session)
	}

	// Expect the most-recent subBufCap items: total-subBufCap .. total-1, in order.
	for i, sess := range got {
		want := strconv.Itoa(total - subBufCap + i)
		if sess != want {
			t.Fatalf("retained[%d] = %q, want %q (drop-oldest violated)", i, sess, want)
		}
	}
}

// TestBroadcasterPublishNeverBlocks is a regression test for the drop-oldest
// deadlock: a subscriber with a CONCURRENT draining consumer is hammered by a
// publisher. With the old blocking `<-ch` drain, the consumer could empty the
// buffer between the failed non-blocking send and the receive, blocking Publish
// forever while it held the lock. The publisher must always finish; a hang is
// caught by the timeout instead of wedging the whole suite.
func TestBroadcasterPublishNeverBlocks(t *testing.T) {
	t.Parallel()
	b := NewBroadcaster()
	_, ch, cancel := b.Subscribe()

	// Concurrent consumer drains continuously, racing the drop-oldest path so
	// the buffer can be emptied at any instant.
	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		for range ch {
		}
	}()

	const n = 500000
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < n; i++ {
			b.Publish(Change{Session: strconv.Itoa(i)})
		}
	}()

	select {
	case <-done:
		// Publish never blocked — good.
	case <-time.After(10 * time.Second):
		t.Fatal("Publish blocked: drop-oldest path deadlocked against concurrent consumer")
	}

	cancel()
	<-consumerDone
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
