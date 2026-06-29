package state

import (
	"sync"

	"agentmon/shared"
)

type SessionView struct {
	ServerID, Target, Session string
	Global                    shared.State
	LatestReceivedAt          string // hub-clock received_at of the session's latest event
}

type sessKey struct{ server, target, session string }

// Projection is the in-memory current-state derived from ingested events. It is a
// cache (the durable record is session_state_events); empty after a hub restart
// until the next poll repopulates it.
type Projection struct {
	mu       sync.RWMutex
	sessions map[sessKey]SessionView
}

func NewProjection() *Projection { return &Projection{sessions: map[sessKey]SessionView{}} }

func (p *Projection) Set(v SessionView) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sessions[sessKey{v.ServerID, v.Target, v.Session}] = v
}

func (p *Projection) Session(server, target, session string) (SessionView, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	v, ok := p.sessions[sessKey{server, target, session}]
	return v, ok
}

func (p *Projection) Server(server string) []SessionView {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var out []SessionView
	for k, v := range p.sessions {
		if k.server == server {
			out = append(out, v)
		}
	}
	return out
}

func (p *Projection) All() []SessionView {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]SessionView, 0, len(p.sessions))
	for _, v := range p.sessions {
		out = append(out, v)
	}
	return out
}

// ReplaceServer atomically swaps all of a server's sessions, pruning any not present
// in views (vanished sessions disappear from the projection).
func (p *Projection) ReplaceServer(server string, views []SessionView) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k := range p.sessions {
		if k.server == server {
			delete(p.sessions, k)
		}
	}
	for _, v := range views {
		p.sessions[sessKey{v.ServerID, v.Target, v.Session}] = v
	}
}
