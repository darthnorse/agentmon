package orchestrator

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/state"
	"agentmon/shared"
)

type fakePushStore struct{}

func (fakePushStore) PrincipalIDsWithSubscriptions(ctx context.Context) ([]string, error) {
	return []string{"admin"}, nil
}
func (fakePushStore) ListSubscriptionsForPrincipal(ctx context.Context, id string) ([]db.PushSubscription, error) {
	return []db.PushSubscription{{PrincipalID: id, Endpoint: "https://push/x"}}, nil
}
func (fakePushStore) DeleteSubscription(ctx context.Context, endpoint string) error { return nil }

func TestBoardPushFiresOnEscalatedOnly(t *testing.T) {
	b := NewBoardBroadcaster()
	var mu sync.Mutex
	var payloads []map[string]any
	send := func(_ context.Context, _ db.PushSubscription, payload []byte) (int, error) {
		var m map[string]any
		json.Unmarshal(payload, &m)
		mu.Lock()
		payloads = append(payloads, m)
		mu.Unlock()
		return 201, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go RunBoardPushDispatcher(ctx, BoardPushDeps{
		Bcast: b, Presence: state.NewPresence(), Store: fakePushStore{},
		Send: send, Now: func() string { return "t" }})
	time.Sleep(20 * time.Millisecond) // let it subscribe
	b.Publish(BoardChange{ProjectID: "p1", Issue: 15, Stage: shared.EpicMerged})
	b.Publish(BoardChange{ProjectID: "p1", Issue: 15, Stage: shared.EpicEscalated, Needs: "2 findings", Title: "GDPR"})
	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		n := len(payloads)
		mu.Unlock()
		if n >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("no push within 2s")
		case <-time.After(10 * time.Millisecond):
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(payloads) != 1 || payloads[0]["type"] != "epic" || payloads[0]["stage"] != "escalated" {
		t.Fatalf("payloads = %+v", payloads)
	}
}
