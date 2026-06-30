package state

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"agentmon/hubd/internal/db"
	"agentmon/shared"
)

// fakeDispatchStore is an in-memory PushDispatchStore for the dispatcher tests.
type fakeDispatchStore struct {
	mu      sync.Mutex
	ids     []string
	subs    map[string][]db.PushSubscription
	deleted []string
}

func (f *fakeDispatchStore) PrincipalIDsWithSubscriptions(ctx context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.ids...), nil
}

func (f *fakeDispatchStore) ListSubscriptionsForPrincipal(ctx context.Context, principalID string) ([]db.PushSubscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]db.PushSubscription(nil), f.subs[principalID]...), nil
}

func (f *fakeDispatchStore) DeleteSubscription(ctx context.Context, endpoint string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, endpoint)
	return nil
}

func (f *fakeDispatchStore) deletedEndpoints() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deleted...)
}

// recordingSender captures the (subscription, payload) pairs it is asked to send
// and returns a programmable status/error.
type recordingSender struct {
	mu      sync.Mutex
	subs    []db.PushSubscription
	payload []byte
	status  int
	err     error
}

func (s *recordingSender) send(ctx context.Context, sub db.PushSubscription, payload []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subs = append(s.subs, sub)
	s.payload = payload
	return s.status, s.err
}

func (s *recordingSender) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.subs)
}

func (s *recordingSender) lastPayload() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.payload
}

func TestDispatch_BlockedSendsToEverySubscription(t *testing.T) {
	store := &fakeDispatchStore{
		ids: []string{"p1"},
		subs: map[string][]db.PushSubscription{
			"p1": {{PrincipalID: "p1", Endpoint: "e1"}, {PrincipalID: "p1", Endpoint: "e2"}},
		},
	}
	sender := &recordingSender{status: 201}
	d := DispatcherDeps{
		Presence:   NewPresence(),
		Store:      store,
		Send:       sender.send,
		NowRFC3339: func() string { return "2026-06-29T00:00:00Z" },
	}

	dispatch(context.Background(), d, Change{ServerID: "s1", Target: "t1", Session: "api", Global: shared.StateBlocked})

	if got := sender.count(); got != 2 {
		t.Fatalf("sender called %d times, want 2", got)
	}
	var msg pushMsg
	if err := json.Unmarshal(sender.lastPayload(), &msg); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}
	if msg.Type != "blocked" || msg.Server != "s1" || msg.Target != "t1" || msg.Session != "api" || msg.Ts != "2026-06-29T00:00:00Z" {
		t.Fatalf("unexpected payload: %+v", msg)
	}
}

func TestDispatch_OnlinePrincipalIsSuppressed(t *testing.T) {
	store := &fakeDispatchStore{
		ids: []string{"p1"},
		subs: map[string][]db.PushSubscription{
			"p1": {{PrincipalID: "p1", Endpoint: "e1"}},
		},
	}
	sender := &recordingSender{status: 201}
	pres := NewPresence()
	pres.Add("p1") // p1 has a live SSE stream → Tier 1/2 covers them.
	d := DispatcherDeps{Presence: pres, Store: store, Send: sender.send, NowRFC3339: func() string { return "t" }}

	dispatch(context.Background(), d, Change{ServerID: "s1", Session: "api", Global: shared.StateBlocked})

	if got := sender.count(); got != 0 {
		t.Fatalf("sender called %d times for online principal, want 0", got)
	}
}

func TestDispatch_NonBlockedChangesAreIgnored(t *testing.T) {
	for _, st := range []shared.State{shared.StateWorking, shared.StateDone, shared.StateIdle, shared.StateUnknown} {
		store := &fakeDispatchStore{
			ids:  []string{"p1"},
			subs: map[string][]db.PushSubscription{"p1": {{PrincipalID: "p1", Endpoint: "e1"}}},
		}
		sender := &recordingSender{status: 201}
		d := DispatcherDeps{Presence: NewPresence(), Store: store, Send: sender.send, NowRFC3339: func() string { return "t" }}

		dispatch(context.Background(), d, Change{ServerID: "s1", Session: "api", Global: st})

		if got := sender.count(); got != 0 {
			t.Fatalf("state %q: sender called %d times, want 0", st, got)
		}
	}
}

func TestDispatch_PrunesExpiredSubscriptions(t *testing.T) {
	for _, status := range []int{404, 410} {
		store := &fakeDispatchStore{
			ids:  []string{"p1"},
			subs: map[string][]db.PushSubscription{"p1": {{PrincipalID: "p1", Endpoint: "gone"}}},
		}
		sender := &recordingSender{status: status}
		d := DispatcherDeps{Presence: NewPresence(), Store: store, Send: sender.send, NowRFC3339: func() string { return "t" }}

		dispatch(context.Background(), d, Change{ServerID: "s1", Session: "api", Global: shared.StateBlocked})

		del := store.deletedEndpoints()
		if len(del) != 1 || del[0] != "gone" {
			t.Fatalf("status %d: deleted=%v, want [gone]", status, del)
		}
	}
}

func TestDispatch_KeepsSubscriptionOnTransientError(t *testing.T) {
	store := &fakeDispatchStore{
		ids:  []string{"p1"},
		subs: map[string][]db.PushSubscription{"p1": {{PrincipalID: "p1", Endpoint: "e1"}}},
	}
	// Transport error (status 0) — must NOT prune.
	sender := &recordingSender{status: 0, err: context.DeadlineExceeded}
	d := DispatcherDeps{Presence: NewPresence(), Store: store, Send: sender.send, NowRFC3339: func() string { return "t" }}

	dispatch(context.Background(), d, Change{ServerID: "s1", Session: "api", Global: shared.StateBlocked})

	if del := store.deletedEndpoints(); len(del) != 0 {
		t.Fatalf("transient error pruned %v, want none", del)
	}
}

func TestBlockedGate_FreshOnlyOnTransitionIntoBlocked(t *testing.T) {
	g := newBlockedGate()
	mk := func(s shared.State) Change { return Change{ServerID: "s", Target: "t", Session: "api", Global: s} }

	if !g.fresh(mk(shared.StateBlocked)) {
		t.Fatal("first entry into blocked must be fresh")
	}
	if g.fresh(mk(shared.StateBlocked)) {
		t.Fatal("re-published blocked (still blocked) must NOT be fresh — no double push")
	}
	if g.fresh(mk(shared.StateWorking)) {
		t.Fatal("working is never fresh")
	}
	if !g.fresh(mk(shared.StateBlocked)) {
		t.Fatal("blocked after leaving blocked must be fresh again (re-alert)")
	}
	// A different session is tracked independently.
	other := Change{ServerID: "s", Target: "t", Session: "web", Global: shared.StateBlocked}
	if !g.fresh(other) {
		t.Fatal("a different session entering blocked must be fresh")
	}
}

func TestRunPushDispatcher_DeliversPublishedBlockedChange(t *testing.T) {
	store := &fakeDispatchStore{
		ids:  []string{"p1"},
		subs: map[string][]db.PushSubscription{"p1": {{PrincipalID: "p1", Endpoint: "e1"}}},
	}
	fired := make(chan []byte, 16)
	sender := func(ctx context.Context, sub db.PushSubscription, payload []byte) (int, error) {
		select {
		case fired <- payload:
		default: // never block the dispatcher; one delivery is enough to assert.
		}
		return 201, nil
	}
	b := NewBroadcaster()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go RunPushDispatcher(ctx, DispatcherDeps{
		Bcast:      b,
		Presence:   NewPresence(),
		Store:      store,
		Send:       sender,
		NowRFC3339: func() string { return "ts" },
	})

	// Publish repeatedly until the dispatcher (which subscribes asynchronously)
	// has wired up and delivered. Avoids the subscribe/publish startup race.
	deadline := time.After(2 * time.Second)
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	var payload []byte
	for payload == nil {
		select {
		case payload = <-fired:
		case <-tick.C:
			b.Publish(Change{ServerID: "s1", Target: "t1", Session: "api", Global: shared.StateBlocked})
		case <-deadline:
			t.Fatal("no push delivered within 2s")
		}
	}

	var msg pushMsg
	if err := json.Unmarshal(payload, &msg); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}
	if msg.Type != "blocked" || msg.Server != "s1" || msg.Session != "api" {
		t.Fatalf("unexpected payload: %+v", msg)
	}
}

func TestRunPushDispatcher_StopsOnContextCancel(t *testing.T) {
	b := NewBroadcaster()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RunPushDispatcher(ctx, DispatcherDeps{
			Bcast:      b,
			Presence:   NewPresence(),
			Store:      &fakeDispatchStore{},
			Send:       func(context.Context, db.PushSubscription, []byte) (int, error) { return 201, nil },
			NowRFC3339: func() string { return "t" },
		})
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunPushDispatcher did not return after context cancel")
	}
}
