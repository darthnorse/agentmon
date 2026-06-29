package state

import (
	"context"
	"encoding/json"
	"errors"
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
}

// ─── Poller ───────────────────────────────────────────────────────────────────

type paneKey struct{ server, target, pane string }

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

	mu       sync.Mutex
	lastSeen map[paneKey]shared.PaneState
	backoffs map[string]*backoffState // by server ID
}

// NewPoller constructs a Poller. now is the hub clock (injected for testing).
func NewPoller(
	lister ServerLister,
	agent AgentAPI,
	store EventStore,
	proj *Projection,
	interval time.Duration,
	now func() time.Time,
) *Poller {
	return &Poller{
		lister:   lister,
		agent:    agent,
		store:    store,
		proj:     proj,
		interval: interval,
		now:      now,
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
			p.pollDegraded(ctx, srv)
		} else {
			p.bumpBackoff(id)
		}
		return
	}
	p.resetBackoff(id)

	// Build pane-ID → session-name map from the live session tree.
	sessions, _ := p.agent.Sessions(ctx, srv, "")
	paneToSession := make(map[string]string, len(sessions)*4)
	for _, sess := range sessions {
		for _, win := range sess.Windows {
			for _, pane := range win.Panes {
				paneToSession[pane.ID] = sess.Name
			}
		}
	}

	receivedAt := hubTS(p.now())

	// Collect events to write (computed under lock, written after).
	type evtEntry struct {
		evt  db.StateEvent
		key  paneKey
		pane shared.PaneState
	}
	var toWrite []evtEntry

	// Track panes that are present in the live tree for this server this tick.
	activePanes := make(map[paneKey]struct{})

	// Per-session aggregation for projection.
	type sessData struct {
		states      []shared.State
		hadNewEvent bool
	}
	sessionMap := make(map[string]*sessData)

	p.mu.Lock()
	for _, pane := range st.Panes {
		sessName, inLiveTree := paneToSession[pane.Pane]
		if !inLiveTree {
			// Ghost pane: not in the live session tree — skip and prune below.
			continue
		}

		key := paneKey{id, pane.Target, pane.Pane}
		activePanes[key] = struct{}{}

		last, seen := p.lastSeen[key]
		shouldWrite := !seen ||
			pane.State != last.State ||
			pane.DoneSeq > last.DoneSeq ||
			pane.Epoch != last.Epoch ||
			// Counter went backwards → agent restart, treat as new.
			pane.TransitionSeq < last.TransitionSeq ||
			pane.DoneSeq < last.DoneSeq

		if shouldWrite {
			raw, _ := json.Marshal(pane)
			toWrite = append(toWrite, evtEntry{
				evt: db.StateEvent{
					ID:           uuid.New().String(),
					ServerID:     id,
					TargetID:     pane.Target,
					Session:      sessName,
					Pane:         pane.Pane,
					Source:       "hook",
					RawEvent:     string(raw),
					DerivedState: string(pane.State),
					EventTs:      hubTS(pane.LastChangeAt),
					ReceivedAt:   receivedAt,
				},
				key:  key,
				pane: pane,
			})
		}

		sd := sessionMap[sessName]
		if sd == nil {
			sd = &sessData{}
			sessionMap[sessName] = sd
		}
		sd.states = append(sd.states, pane.State)
		if shouldWrite {
			sd.hadNewEvent = true
		}
	}

	// Prune lastSeen entries for panes no longer in the live tree.
	for key := range p.lastSeen {
		if key.server == id {
			if _, active := activePanes[key]; !active {
				delete(p.lastSeen, key)
			}
		}
	}
	p.mu.Unlock()

	// Write events outside the lock (I/O).
	for _, e := range toWrite {
		_ = p.store.AppendStateEvent(ctx, e.evt)
	}

	// Update lastSeen after successful writes.
	p.mu.Lock()
	for _, e := range toWrite {
		p.lastSeen[e.key] = e.pane
	}
	p.mu.Unlock()

	// Build and replace projection views for this server.
	views := make([]SessionView, 0, len(sessionMap))
	for sessName, sd := range sessionMap {
		latestRecvAt := ""
		if sd.hadNewEvent {
			latestRecvAt = receivedAt
		} else {
			// Carry the prior LatestReceivedAt from the projection when nothing changed.
			if prior, ok := p.proj.Session(id, "", sessName); ok {
				latestRecvAt = prior.LatestReceivedAt
			}
		}
		views = append(views, SessionView{
			ServerID:         id,
			Target:           "",
			Session:          sessName,
			Global:           shared.RollUp(sd.states...),
			LatestReceivedAt: latestRecvAt,
		})
	}
	p.proj.ReplaceServer(id, views)
}

// pollDegraded is the fallback when the agent does not support GET /state
// (returns 404). It synthesises one PaneState per session from Sessions().
func (p *Poller) pollDegraded(ctx context.Context, srv db.Server) {
	sessions, err := p.agent.Sessions(ctx, srv, "")
	if err != nil {
		return
	}

	receivedAt := hubTS(p.now())

	type evtEntry struct {
		evt  db.StateEvent
		key  paneKey
		pane shared.PaneState
	}
	var toWrite []evtEntry

	views := make([]SessionView, 0, len(sessions))

	p.mu.Lock()
	for _, sess := range sessions {
		// Synthesise a single PaneState from the rolled-up session state.
		synth := shared.PaneState{
			Target: sess.Target,
			State:  sess.State,
		}
		key := paneKey{srv.ID, sess.Target, "snapshot:" + sess.Name}

		last, seen := p.lastSeen[key]
		if !seen || synth.State != last.State {
			raw, _ := json.Marshal(synth)
			toWrite = append(toWrite, evtEntry{
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

		views = append(views, SessionView{
			ServerID:         srv.ID,
			Target:           sess.Target,
			Session:          sess.Name,
			Global:           sess.State,
			LatestReceivedAt: receivedAt,
		})
	}
	p.mu.Unlock()

	for _, e := range toWrite {
		_ = p.store.AppendStateEvent(ctx, e.evt)
	}

	p.mu.Lock()
	for _, e := range toWrite {
		p.lastSeen[e.key] = e.pane
	}
	p.mu.Unlock()

	p.proj.ReplaceServer(srv.ID, views)
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
