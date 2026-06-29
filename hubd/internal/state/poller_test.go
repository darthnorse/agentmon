package state

import (
	"context"
	"errors"
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
	state       map[string]shared.AgentState // by serverID
	sessions    map[string][]shared.Session
	stateErr    map[string]error
	sessionsErr map[string]error
	stateCalls  int // total State() invocations (used by the backoff test)
}

func (f *fakeAgent) State(_ context.Context, srv db.Server, _ string) (shared.AgentState, error) {
	f.stateCalls++
	if e := f.stateErr[srv.ID]; e != nil {
		return shared.AgentState{}, e
	}
	return f.state[srv.ID], nil
}
func (f *fakeAgent) Sessions(_ context.Context, srv db.Server, _ string) ([]shared.Session, error) {
	if e := f.sessionsErr[srv.ID]; e != nil {
		return nil, e
	}
	return f.sessions[srv.ID], nil
}

// fakeStore optionally fails AppendStateEvent when appendErr is set.
type fakeStore struct {
	events    []db.StateEvent
	appendErr error
}

func (f *fakeStore) AppendStateEvent(_ context.Context, e db.StateEvent) error {
	if f.appendErr != nil {
		return f.appendErr
	}
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
	p := NewPoller(lister, agent, store, proj, time.Second, func() time.Time { return clk }, nil)
	return p, agent, store, proj
}

// ─── Mutating helpers (operate on the fakeAgent directly) ────────────────────

func mutatePane(fa *fakeAgent, serverID, paneID string, fn func(*shared.PaneState)) {
	ag := fa.state[serverID]
	panes := append([]shared.PaneState(nil), ag.Panes...)
	for i := range panes {
		if panes[i].Pane == paneID {
			fn(&panes[i])
		}
	}
	fa.state[serverID] = shared.AgentState{Panes: panes}
}

func pollerSetDoneSeq(fa *fakeAgent, serverID, paneID string, seq uint64) {
	mutatePane(fa, serverID, paneID, func(p *shared.PaneState) { p.DoneSeq = seq })
}

func pollerSetEpoch(fa *fakeAgent, serverID, paneID, epoch string) {
	mutatePane(fa, serverID, paneID, func(p *shared.PaneState) { p.Epoch = epoch })
}

func pollerSetState(fa *fakeAgent, serverID, paneID string, st shared.State, transitionSeq uint64) {
	mutatePane(fa, serverID, paneID, func(p *shared.PaneState) {
		p.State = st
		p.TransitionSeq = transitionSeq
	})
}

func pollerSetCounters(fa *fakeAgent, serverID, paneID string, transitionSeq, doneSeq uint64) {
	mutatePane(fa, serverID, paneID, func(p *shared.PaneState) {
		p.TransitionSeq = transitionSeq
		p.DoneSeq = doneSeq
	})
}

func pollerForceStateErr(fa *fakeAgent, serverID string, err error) {
	if fa.stateErr == nil {
		fa.stateErr = make(map[string]error)
	}
	fa.stateErr[serverID] = err
}

// ─── Tests ───────────────────────────────────────────────────────────────────

// TestPollerNormalPathUsesSessionTarget: when both the session and its panes
// carry a non-empty Target label, the projection must be keyed on that label
// (not "") and the written event's TargetID must match it.
func TestPollerNormalPathUsesSessionTarget(t *testing.T) {
	const label = "default"
	lister := &fakeLister{
		servers: []registry.ServerSummary{{ID: "s"}},
		get:     map[string]db.Server{"s": {ID: "s", URL: "http://x", Bearer: "b"}},
	}
	agent := &fakeAgent{
		state: map[string]shared.AgentState{
			"s": {Panes: []shared.PaneState{{
				Target:        label,
				Pane:          "%0",
				State:         shared.StateDone,
				TransitionSeq: 1,
				DoneSeq:       1,
				Epoch:         "1",
			}}},
		},
		sessions: map[string][]shared.Session{
			"s": {{
				Name:   "api",
				Target: label,
				Windows: []shared.Window{{Panes: []shared.Pane{{ID: "%0"}}}},
			}},
		},
	}
	store := &fakeStore{}
	proj := NewProjection()
	clk := time.Unix(100, 0)
	p := NewPoller(lister, agent, store, proj, time.Second, func() time.Time { return clk }, nil)

	p.Tick(context.Background())

	// Projection must be stored under (server="s", target="default", session="api").
	if v, ok := proj.Session("s", label, "api"); !ok || v.Global != shared.StateDone {
		t.Fatalf("projection under target=%q: %+v ok=%v", label, v, ok)
	}
	// Must NOT be findable under the empty target.
	if _, ok := proj.Session("s", "", "api"); ok {
		t.Fatal("projection must NOT be stored under empty target")
	}
	// The written event's TargetID must match the session label.
	if len(store.events) != 1 {
		t.Fatalf("want 1 event, got %d", len(store.events))
	}
	if store.events[0].TargetID != label {
		t.Fatalf("event TargetID=%q, want %q", store.events[0].TargetID, label)
	}
}

func TestPollerIngestsFirstSeenEventAndProjects(t *testing.T) {
	p, _, store, proj := newPollerFixture()
	p.Tick(context.Background())
	if len(store.events) != 1 || store.events[0].DerivedState != "done" || store.events[0].Session != "api" {
		t.Fatalf("events = %+v", store.events)
	}
	if store.events[0].Source != "hook" {
		t.Fatalf("source = %q, want hook", store.events[0].Source)
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

// Finding #7: a genuine state transition (working→done) for a pane already in
// lastSeen must fire a second event.
func TestPollerStateTransitionReingests(t *testing.T) {
	p, fa, store, proj := newPollerFixture()
	pollerSetState(fa, "s", "%0", shared.StateWorking, 1)
	p.Tick(context.Background()) // event 1: working
	pollerSetState(fa, "s", "%0", shared.StateDone, 2)
	p.Tick(context.Background()) // event 2: done
	if len(store.events) != 2 {
		t.Fatalf("working→done should re-ingest, got %d events", len(store.events))
	}
	if store.events[1].DerivedState != "done" {
		t.Fatalf("second event DerivedState = %q, want done", store.events[1].DerivedState)
	}
	if v, _ := proj.Session("s", "", "api"); v.Global != shared.StateDone {
		t.Fatalf("projection Global = %q, want done", v.Global)
	}
}

// Finding #8: counters going BACKWARDS (agent restart) must re-ingest even when
// the state and epoch are unchanged.
func TestPollerAgentRestartReingests(t *testing.T) {
	p, fa, store, _ := newPollerFixture()
	pollerSetCounters(fa, "s", "%0", 5, 5)
	p.Tick(context.Background()) // event 1 at high counters
	// Restart: lower counters, same state/epoch.
	pollerSetCounters(fa, "s", "%0", 1, 1)
	p.Tick(context.Background())
	if len(store.events) != 2 {
		t.Fatalf("agent restart (counters backwards) should re-ingest, got %d events", len(store.events))
	}
}

// Finding #6: the degraded /state-404 path must write exactly one snapshot event
// with the rolled-up session state and project it.
func TestPollerDegradesOn404(t *testing.T) {
	p, fa, store, proj := newPollerFixture()
	pollerForceStateErr(fa, "s", registry.ErrStateUnsupported)
	// Make the fallback session state meaningful.
	fa.sessions["s"] = []shared.Session{{Name: "api", Target: "", State: shared.StateDone}}
	p.Tick(context.Background())
	if len(store.events) != 1 {
		t.Fatalf("degraded path should write exactly 1 event, got %d", len(store.events))
	}
	e := store.events[0]
	if e.Source != "snapshot" || e.Session != "api" || e.DerivedState != "done" {
		t.Fatalf("degraded event = %+v", e)
	}
	if v, ok := proj.Session("s", "", "api"); !ok || v.Global != shared.StateDone {
		t.Fatalf("degraded projection = %+v ok=%v", v, ok)
	}
}

// Finding #3: in degraded mode an unchanged session must NOT keep re-advancing
// LatestReceivedAt nor re-write events every tick.
func TestPollerDegradedUnchangedDoesNotReadvance(t *testing.T) {
	p, fa, store, proj := newPollerFixture()
	pollerForceStateErr(fa, "s", registry.ErrStateUnsupported)
	fa.sessions["s"] = []shared.Session{{Name: "api", Target: "", State: shared.StateDone}}

	p.Tick(context.Background())
	v1, _ := proj.Session("s", "", "api")
	if len(store.events) != 1 || v1.LatestReceivedAt == "" {
		t.Fatalf("first degraded tick: events=%d latest=%q", len(store.events), v1.LatestReceivedAt)
	}
	// Second tick, identical snapshot: no new event, LatestReceivedAt carried.
	p.Tick(context.Background())
	v2, _ := proj.Session("s", "", "api")
	if len(store.events) != 1 {
		t.Fatalf("degraded unchanged: want 1 event, got %d", len(store.events))
	}
	if v2.LatestReceivedAt != v1.LatestReceivedAt {
		t.Fatalf("degraded LatestReceivedAt re-advanced: %q -> %q", v1.LatestReceivedAt, v2.LatestReceivedAt)
	}
}

// Finding #1: a Sessions() failure after a successful State() must skip the
// server for this tick WITHOUT wiping lastSeen — otherwise the next good tick
// re-ingests the whole snapshot.
func TestPollerSessionsErrorSkipsWithoutWipe(t *testing.T) {
	p, fa, store, proj := newPollerFixture()
	p.Tick(context.Background()) // baseline: 1 event, lastSeen populated
	if len(store.events) != 1 {
		t.Fatalf("setup: want 1 event, got %d", len(store.events))
	}

	// Sessions() now fails (State still succeeds).
	fa.sessionsErr = map[string]error{"s": errors.New("boom")}
	p.Tick(context.Background())
	if len(store.events) != 1 {
		t.Fatalf("sessions error must not write events, got %d", len(store.events))
	}
	// Projection must persist across the error.
	if v, ok := proj.Session("s", "", "api"); !ok || v.Global != shared.StateDone {
		t.Fatalf("projection should persist across sessions error; %+v ok=%v", v, ok)
	}

	// Recovery: Sessions() works again, snapshot unchanged → no re-ingest storm.
	fa.sessionsErr = nil
	p.Tick(context.Background())
	if len(store.events) != 1 {
		t.Fatalf("after recovery the unchanged snapshot must not re-ingest (lastSeen wiped?); got %d events", len(store.events))
	}
}

// Finding #2: a failed AppendStateEvent must NOT advance lastSeen, so the
// transition is retried (and persisted) on the next tick.
func TestPollerFailedWriteRetriesNextTick(t *testing.T) {
	p, _, store, _ := newPollerFixture()
	store.appendErr = errors.New("db down")
	p.Tick(context.Background())
	if len(store.events) != 0 {
		t.Fatalf("write failed, so no events should be stored, got %d", len(store.events))
	}
	// DB recovers; the same (unchanged) snapshot must still be ingested because
	// lastSeen was not advanced for the failed write.
	store.appendErr = nil
	p.Tick(context.Background())
	if len(store.events) != 1 {
		t.Fatalf("failed transition must be retried after recovery, got %d events", len(store.events))
	}
}

// Finding #9: a non-404 error triggers exponential backoff that skips the server
// until nextAttempt; a later success resets it. Time is driven by the injected
// clock — no sleeping.
func TestPollerBackoffDoublesAndResets(t *testing.T) {
	ctx := context.Background()
	lister := &fakeLister{
		servers: []registry.ServerSummary{{ID: "s"}},
		get:     map[string]db.Server{"s": {ID: "s", URL: "http://x", Bearer: "b"}},
	}
	agent := &fakeAgent{
		state: map[string]shared.AgentState{
			"s": {Panes: []shared.PaneState{{Pane: "%0", State: shared.StateDone, TransitionSeq: 1, DoneSeq: 1, Epoch: "1"}}},
		},
		sessions: map[string][]shared.Session{
			"s": {{Name: "api", Windows: []shared.Window{{Panes: []shared.Pane{{ID: "%0"}}}}}},
		},
		stateErr: map[string]error{"s": errors.New("dial timeout")},
	}
	store := &fakeStore{}
	now := time.Unix(100, 0)
	p := NewPoller(lister, agent, store, NewProjection(), time.Second, func() time.Time { return now }, nil)

	// Tick 1 (t=100.0): error → backoff delay = interval (1s), nextAttempt = 101.0.
	p.Tick(ctx)
	if agent.stateCalls != 1 {
		t.Fatalf("tick1: want 1 State call, got %d", agent.stateCalls)
	}

	// Tick 2 (t=100.0, unchanged): within backoff window → skipped.
	p.Tick(ctx)
	if agent.stateCalls != 1 {
		t.Fatalf("backoff should skip the immediate next tick, got %d State calls", agent.stateCalls)
	}

	// Advance past the 1s delay → retried; still errors → delay doubles to 2s.
	now = now.Add(1100 * time.Millisecond) // t=101.1, nextAttempt becomes 103.1
	p.Tick(ctx)
	if agent.stateCalls != 2 {
		t.Fatalf("want retry after the delay elapsed, got %d State calls", agent.stateCalls)
	}

	// Advance only 1.5s (< the new 2s delay): still skipped → proves it doubled.
	now = now.Add(1500 * time.Millisecond) // t=102.6 < 103.1
	p.Tick(ctx)
	if agent.stateCalls != 2 {
		t.Fatalf("delay should have doubled to 2s; got %d State calls", agent.stateCalls)
	}

	// Advance past 2s and clear the error → success resets the backoff.
	now = now.Add(1 * time.Second) // t=103.6 >= 103.1
	delete(agent.stateErr, "s")
	p.Tick(ctx)
	if agent.stateCalls != 3 {
		t.Fatalf("want retry once delay elapsed, got %d State calls", agent.stateCalls)
	}
	// Backoff reset → the very next tick (clock unchanged) polls again.
	p.Tick(ctx)
	if agent.stateCalls != 4 {
		t.Fatalf("backoff should be reset after success; got %d State calls", agent.stateCalls)
	}
}

// newBroadcastFixture builds a poller wired to the given broadcaster, with a
// single session ("api") whose pane+session carry the target label so tests can
// assert Change.Target is propagated (not just defaulted to "").
func newBroadcastFixture(bcast *Broadcaster, label string) (*Poller, *fakeAgent, *fakeStore, *Projection) {
	lister := &fakeLister{
		servers: []registry.ServerSummary{{ID: "s"}},
		get:     map[string]db.Server{"s": {ID: "s", URL: "http://x", Bearer: "b"}},
	}
	agent := &fakeAgent{
		state: map[string]shared.AgentState{
			"s": {Panes: []shared.PaneState{{
				Target:        label,
				Pane:          "%0",
				State:         shared.StateDone,
				TransitionSeq: 1,
				DoneSeq:       1,
				Epoch:         "1",
			}}},
		},
		sessions: map[string][]shared.Session{
			"s": {{Name: "api", Target: label, Windows: []shared.Window{{Panes: []shared.Pane{{ID: "%0"}}}}}},
		},
	}
	store := &fakeStore{}
	proj := NewProjection()
	clk := time.Unix(100, 0)
	p := NewPoller(lister, agent, store, proj, time.Second, func() time.Time { return clk }, bcast)
	return p, agent, store, proj
}

// TestPollerPublishesOnChange: first tick of a new session publishes a Change;
// an identical second tick (no new event, no global change) publishes nothing.
func TestPollerPublishesOnChange(t *testing.T) {
	const label = "default"
	bcast := NewBroadcaster()
	_, ch, cancel := bcast.Subscribe()
	defer cancel()
	p, _, _, _ := newBroadcastFixture(bcast, label)

	// Tick 1: session first seen → global changes (none→done) → must publish.
	p.Tick(context.Background())

	select {
	case c := <-ch:
		if c.ServerID != "s" || c.Target != label || c.Session != "api" || c.Global != shared.StateDone {
			t.Fatalf("tick 1: unexpected Change: %+v", c)
		}
	default:
		t.Fatal("tick 1: expected a Change to be published, got none")
	}

	// Tick 2: identical snapshot → no new event, no global change → nothing published.
	p.Tick(context.Background())

	select {
	case c := <-ch:
		t.Fatalf("tick 2: unexpected Change on unchanged tick: %+v", c)
	default:
		// correct: nothing published
	}
}

// TestPollerPublishesDoneReAlert locks the done→done re-alert publish path: a
// session that stays `done` but lands a NEW finished turn (DoneSeq bumps, so an
// event is written) must publish a Change on that tick — exercising the
// committedSessions arm of the publish condition, not just the first-seen arm.
func TestPollerPublishesDoneReAlert(t *testing.T) {
	const label = "default"
	bcast := NewBroadcaster()
	_, ch, cancel := bcast.Subscribe()
	defer cancel()
	p, fa, _, _ := newBroadcastFixture(bcast, label)

	// Tick 1: first-seen done → publishes; drain it.
	p.Tick(context.Background())
	select {
	case <-ch:
	default:
		t.Fatal("tick 1: expected first-seen Change, got none")
	}

	// Tick 2: still done, but a NEW finished turn lands (DoneSeq 1→2) so commit
	// writes an event. Global is unchanged (done) — only the committedSessions arm
	// can fire here.
	pollerSetDoneSeq(fa, "s", "%0", 2)
	p.Tick(context.Background())

	select {
	case c := <-ch:
		if c.ServerID != "s" || c.Target != label || c.Session != "api" || c.Global != shared.StateDone {
			t.Fatalf("tick 2 re-alert: unexpected Change: %+v", c)
		}
	default:
		t.Fatal("tick 2 re-alert: expected a Change on done→done re-alert, got none")
	}
}
