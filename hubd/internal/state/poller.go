package state

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"

	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/registry"
	"agentmon/shared"
)

// ─── Consumer interfaces ──────────────────────────────────────────────────────

// ServerLister is satisfied by *registry.Registry.
type ServerLister interface {
	List(ctx context.Context) ([]registry.ServerSummary, error)
	Get(ctx context.Context, id string) (db.Server, bool, error)
}

// AgentAPI is satisfied by *registry.Client.
type AgentAPI interface {
	State(ctx context.Context, srv db.Server, target string) (shared.AgentState, error)
	Sessions(ctx context.Context, srv db.Server, target string) ([]shared.Session, error)
}

// EventStore is satisfied by *db.DB.
type EventStore interface {
	AppendStateEvent(ctx context.Context, e db.StateEvent) error
	// LatestSessionEvent returns the most recent durable event for a session triple,
	// used by the hub-restart reseed path to avoid re-stamping received_at.
	LatestSessionEvent(ctx context.Context, serverID, target, session string) (db.StateEvent, bool, error)
}

// ─── Poller ───────────────────────────────────────────────────────────────────

type paneKey struct{ server, target, pane string }

// pendingEvent pairs a not-yet-written event with the lastSeen update it implies.
// lastSeen is only advanced once the event is durably written (so a failed write
// is retried next tick).
type pendingEvent struct {
	evt  db.StateEvent
	key  paneKey
	pane shared.PaneState
}

type backoffState struct {
	nextAttempt time.Time
	delay       time.Duration
}

// Poller polls each active agent's GET /state on a fixed interval, ingests
// state transitions into the EventStore, and updates the in-memory Projection.
type Poller struct {
	lister   ServerLister
	agent    AgentAPI
	store    EventStore
	proj     *Projection
	interval time.Duration
	now      func() time.Time
	bcast    *Broadcaster // nil-safe: may be nil in tests

	mu       sync.Mutex
	lastSeen map[paneKey]shared.PaneState
	backoffs map[string]*backoffState // by server ID
}

// NewPoller constructs a Poller. now is the hub clock (injected for testing).
// bcast may be nil (tests that do not exercise publishing pass nil).
func NewPoller(
	lister ServerLister,
	agent AgentAPI,
	store EventStore,
	proj *Projection,
	interval time.Duration,
	now func() time.Time,
	bcast *Broadcaster,
) *Poller {
	return &Poller{
		lister:   lister,
		agent:    agent,
		store:    store,
		proj:     proj,
		interval: interval,
		now:      now,
		bcast:    bcast,
		lastSeen: make(map[paneKey]shared.PaneState),
		backoffs: make(map[string]*backoffState),
	}
}

// Run ticks every interval until ctx is cancelled.
func (p *Poller) Run(ctx context.Context) {
	t := time.NewTicker(p.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.Tick(ctx)
		}
	}
}

// Tick runs one poll cycle across all active servers. No sleeping — safe to
// call directly from tests.
func (p *Poller) Tick(ctx context.Context) {
	servers, err := p.lister.List(ctx)
	if err != nil {
		return
	}

	const maxConcurrency = 4
	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup

	for _, summary := range servers {
		wg.Add(1)
		sem <- struct{}{}
		go func(id string) {
			defer wg.Done()
			defer func() { <-sem }()
			p.pollServer(ctx, id)
		}(summary.ID)
	}
	wg.Wait()
}

// ─── Per-server poll ─────────────────────────────────────────────────────────

func (p *Poller) pollServer(ctx context.Context, id string) {
	if p.shouldSkip(id) {
		return
	}

	srv, ok, err := p.lister.Get(ctx, id)
	if err != nil || !ok {
		return
	}

	st, err := p.agent.State(ctx, srv, "")
	if err != nil {
		if errors.Is(err, registry.ErrStateUnsupported) {
			// 404 means the agent IS reachable; clear any prior transient backoff.
			p.resetBackoff(id)
			p.pollDegraded(ctx, srv)
		} else {
			p.bumpBackoff(id)
		}
		return
	}
	p.resetBackoff(id)

	// Build pane-ID → session-name map from the live session tree.
	sessions, err := p.agent.Sessions(ctx, srv, "")
	if err != nil {
		// sessions error → skip this server's processing this tick.
		// State() succeeded, so treat this as transient: no backoff bump, and
		// crucially no ghost-prune (which would wipe lastSeen and cause a re-ingest
		// storm on the next successful tick), no projection update, no event writes.
		log.Printf("poller: sessions (server=%s): %v", id, err)
		return
	}
	paneToSession := make(map[string]string, len(sessions)*4)
	// sessionTarget maps session name → agent-reported Target label.  This is
	// the canonical external key used by the projection and state events.
	sessionTarget := make(map[string]string, len(sessions))
	for _, sess := range sessions {
		sessionTarget[sess.Name] = sess.Target
		for _, win := range sess.Windows {
			for _, pane := range win.Panes {
				paneToSession[pane.ID] = sess.Name
			}
		}
	}

	receivedAt := HubTS(p.now())

	var toWrite []pendingEvent
	// Panes present in the live tree for this server this tick.
	activePanes := make(map[paneKey]struct{})
	// Per-session pane states (for the rolled-up projection Global).
	sessionStates := make(map[string][]shared.State)
	// reseedTS holds the durable ReceivedAt for sessions that were restart-reseeded
	// on this tick (first-seen pane whose state matches the durable log). Used to
	// anchor the projection's LatestReceivedAt to the pre-restart timestamp so the
	// seen-projection math is not disrupted by the hub going down and coming back up.
	reseedTS := map[string]string{}

	p.mu.Lock()
	for _, pane := range st.Panes {
		sessName, inLiveTree := paneToSession[pane.Pane]
		if !inLiveTree {
			// Ghost pane: not in the live session tree — skip and prune below.
			continue
		}

		// key uses the agent-reported session target for consistency with the projection and event TargetID.
		key := paneKey{id, sessionTarget[sessName], pane.Pane}
		activePanes[key] = struct{}{}

		last, seen := p.lastSeen[key]
		shouldWrite := !seen ||
			pane.State != last.State ||
			pane.DoneSeq > last.DoneSeq ||
			pane.Epoch != last.Epoch ||
			// Counter went backwards → agent restart, treat as new.
			pane.TransitionSeq < last.TransitionSeq ||
			pane.DoneSeq < last.DoneSeq

		// Hub-restart reseed: on first-seen, reconcile against the durable log.
		// If the latest persisted state matches the current live state, this is a
		// hub restart (not a new transition) — suppress the re-stamp so the old
		// received_at is preserved and seen-projection boundaries are not broken.
		if !seen {
			sessTarget := sessionTarget[sessName]
			if latest, found, _ := p.store.LatestSessionEvent(ctx, id, sessTarget, sessName); found && latest.DerivedState == string(pane.State) {
				// State already in the durable log — hub came back up to the same state.
				// Seed lastSeen so subsequent ticks compare against the current snapshot.
				// NOTE: if the agent completed a new turn with the same state string
				// (e.g., done→done via a higher DoneSeq) during the hub-down window, we
				// will miss that re-alert here. This is an acceptable trade-off far
				// better than re-alerting every session on every deploy.
				p.lastSeen[key] = pane
				reseedTS[sessName] = latest.ReceivedAt
				shouldWrite = false
			}
		}

		if shouldWrite {
			raw, _ := json.Marshal(pane)
			toWrite = append(toWrite, pendingEvent{
				evt: db.StateEvent{
					ID:           uuid.New().String(),
					ServerID:     id,
					TargetID:     sessionTarget[sessName], // agent-reported session label
					Session:      sessName,
					Pane:         pane.Pane,
					Source:       "poll",
					RawEvent:     string(raw),
					DerivedState: string(pane.State),
					EventTs:      HubTS(pane.LastChangeAt),
					ReceivedAt:   receivedAt,
				},
				key:  key,
				pane: pane,
			})
		}

		sessionStates[sessName] = append(sessionStates[sessName], pane.State)
	}

	// Prune lastSeen entries for panes no longer in the live tree.
	p.pruneLastSeen(id, activePanes)
	p.mu.Unlock()

	// Write events outside the lock. Only advance lastSeen for events that were
	// durably written (a failed write is retried next tick).
	committedSessions := p.commit(ctx, id, toWrite)

	// Build and replace projection views for this server.
	views := make([]SessionView, 0, len(sessionStates))
	for sessName, states := range sessionStates {
		tgt := sessionTarget[sessName]
		lra := p.latestReceivedAt(id, tgt, sessName, committedSessions[sessName], receivedAt)
		// For restart-reseeded sessions, carry the durable timestamp instead of ""
		// (the projection is empty after restart, so latestReceivedAt would return "").
		if lra == "" {
			if ts, ok := reseedTS[sessName]; ok {
				lra = ts
			}
		}
		views = append(views, SessionView{
			ServerID:         id,
			Target:           tgt,
			Session:          sessName,
			Global:           shared.RollUp(states...),
			LatestReceivedAt: lra,
		})
	}

	// Collect changes, replace projection, publish deltas.
	p.finalize(id, views, committedSessions, reseedTS)
}

// pollDegraded is the fallback when the agent does not support GET /state
// (returns 404). It synthesises one PaneState per session from Sessions().
// NOTE: the hub-restart reseed (LatestSessionEvent reconciliation) is not applied
// here — the degraded path is rare (old agents) and out of scope for the reseed fix.
func (p *Poller) pollDegraded(ctx context.Context, srv db.Server) {
	sessions, err := p.agent.Sessions(ctx, srv, "")
	if err != nil {
		log.Printf("poller: degraded sessions (server=%s): %v", srv.ID, err)
		return
	}

	receivedAt := HubTS(p.now())

	var toWrite []pendingEvent
	activeKeys := make(map[paneKey]struct{})

	p.mu.Lock()
	for _, sess := range sessions {
		// Synthesise a single PaneState from the rolled-up session state.
		synth := shared.PaneState{Target: sess.Target, State: sess.State}
		key := paneKey{srv.ID, sess.Target, "snapshot:" + sess.Name}
		activeKeys[key] = struct{}{}

		last, seen := p.lastSeen[key]
		if !seen || synth.State != last.State {
			raw, _ := json.Marshal(synth)
			toWrite = append(toWrite, pendingEvent{
				evt: db.StateEvent{
					ID:           uuid.New().String(),
					ServerID:     srv.ID,
					TargetID:     sess.Target,
					Session:      sess.Name,
					Source:       "snapshot",
					RawEvent:     string(raw),
					DerivedState: string(sess.State),
					EventTs:      receivedAt, // no agent-side timestamp available
					ReceivedAt:   receivedAt,
				},
				key:  key,
				pane: synth,
			})
		}
	}

	// Prune stale synthetic lastSeen keys for sessions that vanished.
	p.pruneLastSeen(srv.ID, activeKeys)
	p.mu.Unlock()

	committedSessions := p.commit(ctx, srv.ID, toWrite)

	views := make([]SessionView, 0, len(sessions))
	for _, sess := range sessions {
		views = append(views, SessionView{
			ServerID:         srv.ID,
			Target:           sess.Target,
			Session:          sess.Name,
			Global:           sess.State,
			LatestReceivedAt: p.latestReceivedAt(srv.ID, sess.Target, sess.Name, committedSessions[sess.Name], receivedAt),
		})
	}

	// Collect changes, replace projection, publish deltas.
	p.finalize(srv.ID, views, committedSessions, nil)
}

// pruneLastSeen deletes p.lastSeen entries for the given server whose paneKey is
// absent from the active set. Must be called under p.mu (the caller holds the lock).
func (p *Poller) pruneLastSeen(serverID string, active map[paneKey]struct{}) {
	for key := range p.lastSeen {
		if key.server == serverID {
			if _, ok := active[key]; !ok {
				delete(p.lastSeen, key)
			}
		}
	}
}

// finalize collects per-session publish candidates, replaces the server's
// projection, and broadcasts deltas. reseedTS suppresses publishes for
// hub-restart-reseeded sessions with no real state change; pass nil to skip
// suppression (degraded path).
func (p *Poller) finalize(serverID string, views []SessionView, committed map[string]bool, reseedTS map[string]string) {
	var toPublish []Change
	if p.bcast != nil {
		for _, v := range views {
			prior, hasPrior := p.proj.Session(v.ServerID, v.Target, v.Session)
			if !hasPrior || prior.Global != v.Global || committed[v.Session] {
				if _, reseeded := reseedTS[v.Session]; reseeded && !committed[v.Session] {
					continue
				}
				toPublish = append(toPublish, Change{
					ServerID:         v.ServerID,
					Target:           v.Target,
					Session:          v.Session,
					Global:           v.Global,
					LatestReceivedAt: v.LatestReceivedAt,
				})
			}
		}
	}
	p.proj.ReplaceServer(serverID, views)
	for _, c := range toPublish {
		p.bcast.Publish(c)
	}
}

// commit writes pending events to the store and advances lastSeen only for those
// that were durably written. It returns the set of sessions that had at least one
// event written this tick (used to advance the projection's LatestReceivedAt).
func (p *Poller) commit(ctx context.Context, serverID string, toWrite []pendingEvent) map[string]bool {
	committedSessions := make(map[string]bool)
	var committed []pendingEvent
	for _, e := range toWrite {
		if err := p.store.AppendStateEvent(ctx, e.evt); err != nil {
			// Drop the lastSeen update so this transition is retried next tick.
			log.Printf("poller: append state event (server=%s pane=%s): %v", serverID, e.evt.Pane, err)
			continue
		}
		committed = append(committed, e)
		committedSessions[e.evt.Session] = true
	}
	p.mu.Lock()
	for _, e := range committed {
		p.lastSeen[e.key] = e.pane
	}
	p.mu.Unlock()
	return committedSessions
}

// latestReceivedAt returns the LatestReceivedAt to record for a session view:
// the freshly-written received_at if the session had a new event this tick,
// otherwise the prior value carried forward from the projection (so an unchanged
// — already-seen — session does not keep re-advancing).
func (p *Poller) latestReceivedAt(serverID, target, session string, hadNewEvent bool, receivedAt string) string {
	if hadNewEvent {
		return receivedAt
	}
	if prior, ok := p.proj.Session(serverID, target, session); ok {
		return prior.LatestReceivedAt
	}
	return ""
}

// ─── Backoff helpers ─────────────────────────────────────────────────────────

func (p *Poller) shouldSkip(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	bo, ok := p.backoffs[id]
	if !ok {
		return false
	}
	return p.now().Before(bo.nextAttempt)
}

func (p *Poller) bumpBackoff(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	bo, ok := p.backoffs[id]
	if !ok {
		bo = &backoffState{}
		p.backoffs[id] = bo
	}
	if bo.delay == 0 {
		bo.delay = p.interval
	} else {
		bo.delay *= 2
		if bo.delay > 30*time.Second {
			bo.delay = 30 * time.Second
		}
	}
	bo.nextAttempt = p.now().Add(bo.delay)
}

func (p *Poller) resetBackoff(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.backoffs, id)
}
