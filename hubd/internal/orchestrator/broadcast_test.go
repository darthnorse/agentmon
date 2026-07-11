package orchestrator

import (
	"agentmon/shared"
	"testing"
)

func TestBoardBroadcastFanOut(t *testing.T) {
	b := NewBoardBroadcaster()
	_, ch1, cancel1 := b.Subscribe()
	_, ch2, cancel2 := b.Subscribe()
	defer cancel1()
	defer cancel2()
	b.Publish(BoardChange{EpicID: "e1", Stage: shared.EpicMerged})
	for i, ch := range []<-chan BoardChange{ch1, ch2} {
		got := <-ch
		if got.EpicID != "e1" || got.Stage != shared.EpicMerged {
			t.Fatalf("sub %d got %+v", i, got)
		}
	}
}

func TestBoardBroadcastDropOldestNeverBlocks(t *testing.T) {
	b := NewBoardBroadcaster()
	_, ch, cancel := b.Subscribe()
	defer cancel()
	for i := 0; i < 200; i++ { // 3x the buffer; Publish must not block
		b.Publish(BoardChange{Issue: i})
	}
	got := <-ch
	if got.Issue == 0 {
		t.Fatal("oldest change should have been dropped")
	}
}

func TestBoardBroadcastCancelIdempotent(t *testing.T) {
	b := NewBoardBroadcaster()
	_, _, cancel := b.Subscribe()
	cancel()
	cancel() // must not panic
	b.Publish(BoardChange{EpicID: "x"})
}

func TestBoardBroadcastCancelClosesChannel(t *testing.T) {
	b := NewBoardBroadcaster()
	_, ch, cancel := b.Subscribe()
	cancel()
	if _, ok := <-ch; ok {
		t.Fatal("cancel must close the channel (state.Broadcaster contract)")
	}
}
