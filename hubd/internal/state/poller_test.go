package state

import (
	"context"
	"testing"
	"time"

	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/registry"
	"agentmon/shared"
)

// ─── Fakes ───────────────────────────────────────────────────────────────────

type fakeLister struct {
	servers []registry.ServerSummary
	get     map[string]db.Server
}

func (f *fakeLister) List(_ context.Context) ([]registry.ServerSummary, error) {
	return f.servers, nil
}
func (f *fakeLister) Get(_ context.Context, id string) (db.Server, bool, error) {
	s, ok := f.get[id]
	return s, ok, nil
}

type fakeAgent struct {
	state    map[string]shared.AgentState // by serverID
	sessions map[string][]shared.Session
	stateErr map[string]error
}

func (f *fakeAgent) State(_ context.Context, srv db.Server, _ string) (shared.AgentState, error) {
	if e := f.stateErr[srv.ID]; e != nil {
		return shared.AgentState{}, e
	}
	return f.state[srv.ID], nil
}
func (f *fakeAgent) Sessions(_ context.Context, srv db.Server, _ string) ([]shared.Session, error) {
	return f.sessions[srv.ID], nil
}

type fakeStore struct{ events []db.StateEvent }

func (f *fakeStore) AppendStateEvent(_ context.Context, e db.StateEvent) error {
	f.events = append(f.events, e)
	return nil
}

// ─── Fixture ─────────────────────────────────────────────────────────────────

func newPollerFixture() (*Poller, *fakeAgent, *fakeStore, *Projection) {
	lister := &fakeLister{
		servers: []registry.ServerSummary{{ID: "s"}},
		get:     map[string]db.Server{"s": {ID: "s", URL: "http://x", Bearer: "b"}},
	}
	agent := &fakeAgent{
		state: map[string]shared.AgentState{
			"s": {Panes: []shared.PaneState{{
				Target:        "",
				Pane:          "%0",
				State:         shared.StateDone,
				TransitionSeq: 1,
				DoneSeq:       1,
				Epoch:         "1",
			}}},
		},
		sessions: map[string][]shared.Session{
			"s": {{Name: "api", Windows: []shared.Window{{Panes: []shared.Pane{{ID: "%0"}}}}}},
		},
	}
	store := &fakeStore{}
	proj := NewProjection()
	clk := time.Unix(100, 0)
	p := NewPoller(lister, agent, store, proj, time.Second, func() time.Time { return clk })
	return p, agent, store, proj
}

// ─── Mutating helpers (operate on the fakeAgent directly) ────────────────────

func pollerSetDoneSeq(fa *fakeAgent, serverID, paneID string, seq uint64) {
	ag := fa.state[serverID]
	panes := make([]shared.PaneState, len(ag.Panes))
	copy(panes, ag.Panes)
	for i, p := range panes {
		if p.Pane == paneID {
			panes[i].DoneSeq = seq
		}
	}
	fa.state[serverID] = shared.AgentState{Panes: panes}
}

func pollerSetEpoch(fa *fakeAgent, serverID, paneID string, epoch string) {
	ag := fa.state[serverID]
	panes := make([]shared.PaneState, len(ag.Panes))
	copy(panes, ag.Panes)
	for i, p := range panes {
		if p.Pane == paneID {
			panes[i].Epoch = epoch
		}
	}
	fa.state[serverID] = shared.AgentState{Panes: panes}
}

func pollerForceStateErr(fa *fakeAgent, serverID string, err error) {
	if fa.stateErr == nil {
		fa.stateErr = make(map[string]error)
	}
	fa.stateErr[serverID] = err
}

// ─── Tests ───────────────────────────────────────────────────────────────────

func TestPollerIngestsFirstSeenEventAndProjects(t *testing.T) {
	p, _, store, proj := newPollerFixture()
	p.Tick(context.Background())
	if len(store.events) != 1 || store.events[0].DerivedState != "done" || store.events[0].Session != "api" {
		t.Fatalf("events = %+v", store.events)
	}
	if v, ok := proj.Session("s", "", "api"); !ok || v.Global != shared.StateDone {
		t.Fatalf("projection = %+v ok=%v", v, ok)
	}
}

func TestPollerDoneToDoneViaDoneSeq(t *testing.T) {
	p, fa, store, _ := newPollerFixture()
	p.Tick(context.Background()) // first event (done, doneSeq=1)
	// Same state (done) but a NEW finished turn → doneSeq bumps; expect a 2nd event.
	pollerSetDoneSeq(fa, "s", "%0", 2)
	p.Tick(context.Background())
	if len(store.events) != 2 {
		t.Fatalf("want 2 events (done→done re-alert), got %d", len(store.events))
	}
}

func TestPollerNoChangeNoEvent(t *testing.T) {
	p, _, store, _ := newPollerFixture()
	p.Tick(context.Background())
	p.Tick(context.Background()) // identical snapshot → no new event
	if len(store.events) != 1 {
		t.Fatalf("want 1 event, got %d", len(store.events))
	}
}

func TestPollerEpochChangeReingests(t *testing.T) {
	p, fa, store, _ := newPollerFixture()
	p.Tick(context.Background())
	pollerSetEpoch(fa, "s", "%0", "999")
	p.Tick(context.Background())
	if len(store.events) != 2 {
		t.Fatalf("epoch change should re-ingest, got %d events", len(store.events))
	}
}

func TestPollerDegradesOn404(t *testing.T) {
	p, fa, store, proj := newPollerFixture()
	pollerForceStateErr(fa, "s", registry.ErrStateUnsupported)
	p.Tick(context.Background())
	// Falls back to Sessions(): the fake returns session "api" with state "" → unknown.
	if v, ok := proj.Session("s", "", "api"); !ok {
		t.Fatalf("degraded path must still project; ok=%v %+v", ok, v)
	}
	_ = store
}
