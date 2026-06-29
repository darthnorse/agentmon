// Package state derives a Claude Code session/pane state from hook events. It is
// pure (no tmux, no HTTP), in-memory, and safe for concurrent use.
package state

import (
	"sort"
	"strings"
	"sync"
	"time"

	"agentmon/shared"
)

// Event is a parsed, correlated hook signal handed to the machine.
type Event struct {
	Target           string    // resolved config.Target.Label
	Pane             string    // tmux pane id, e.g. "%3"
	Name             string    // hook_event_name
	NotificationKind string    // notification_type (Notification only; else "")
	ClaudeSessionID  string    // session_id (UUID) — informational
	Epoch            string    // $TMUX server pid; "" if unknown
	At               time.Time // event time; defaults to now() when zero
}

type paneState struct {
	State           shared.State
	LastEvent       string
	ClaudeSessionID string
	Epoch           string
	TransitionSeq   uint64
	DoneSeq         uint64
	ChangedAt       time.Time
	UpdatedAt       time.Time
}

type key struct{ target, pane string }

// Machine holds current state per (target, pane).
type Machine struct {
	mu    sync.Mutex
	panes map[key]paneState
	now   func() time.Time
}

// New builds a Machine. now defaults to time.Now when nil.
func New(now func() time.Time) *Machine {
	if now == nil {
		now = time.Now
	}
	return &Machine{panes: map[key]paneState{}, now: now}
}

// derive maps a hook event to a state, or (_, false) to preserve the prior state.
func derive(name, notificationKind string) (shared.State, bool) {
	switch name {
	case "SessionStart":
		return shared.StateIdle, true
	case "UserPromptSubmit", "PreToolUse", "PostToolUse":
		return shared.StateWorking, true
	case "PermissionRequest":
		return shared.StateBlocked, true
	case "Notification":
		if strings.Contains(strings.ToLower(notificationKind), "permission") {
			return shared.StateBlocked, true
		}
		return shared.StateDone, true
	case "Stop":
		return shared.StateDone, true
	default: // SubagentStop and any unknown event preserve the prior state
		return "", false
	}
}

// Apply records the event and returns the new pane state plus whether it changed.
func (m *Machine) Apply(ev Event) (shared.State, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key{ev.Target, ev.Pane}
	ps := m.panes[k]            // zero value for a new pane (counters 0)
	prior := ps.State
	if prior == "" {
		prior = shared.StateUnknown
	}
	at := ev.At
	if at.IsZero() {
		at = m.now()
	}
	if ev.Name == "SessionEnd" {
		delete(m.panes, k) // counters die with the entry
		return shared.StateUnknown, prior != shared.StateUnknown
	}
	next := prior
	d, ok := derive(ev.Name, ev.NotificationKind)
	if ok {
		next = d
	}
	changed := next != prior
	if changed {
		ps.TransitionSeq++
		ps.ChangedAt = at
	}
	if ok && d == shared.StateDone { // every finished turn, incl. done→done
		ps.DoneSeq++
	}
	ps.State = next
	ps.LastEvent = ev.Name
	ps.ClaudeSessionID = ev.ClaudeSessionID
	ps.Epoch = ev.Epoch
	ps.UpdatedAt = at
	m.panes[k] = ps
	return next, changed
}

// Pane returns the current state for one pane (ok=false if never seen).
func (m *Machine) Pane(target, pane string) (shared.State, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ps, ok := m.panes[key{target, pane}]
	if !ok {
		return shared.StateUnknown, false
	}
	return ps.State, true
}

// Rollup reduces the known states of the given panes to one. Panes with no
// recorded state are excluded; no known panes → StateUnknown.
func (m *Machine) Rollup(target string, panes []string) shared.State {
	m.mu.Lock()
	defer m.mu.Unlock()
	var states []shared.State
	for _, p := range panes {
		if ps, ok := m.panes[key{target, p}]; ok {
			states = append(states, ps.State)
		}
	}
	return shared.RollUp(states...)
}

// Snapshot returns the per-pane state for all panes matching target (or all panes
// if target is ""). Results are sorted by target then pane for determinism.
func (m *Machine) Snapshot(target string) []shared.PaneState {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]shared.PaneState, 0, len(m.panes))
	for k, ps := range m.panes {
		if target != "" && k.target != target {
			continue
		}
		out = append(out, shared.PaneState{
			Target: k.target, Pane: k.pane, State: ps.State,
			TransitionSeq: ps.TransitionSeq, DoneSeq: ps.DoneSeq,
			Epoch: ps.Epoch, ClaudeSessionID: ps.ClaudeSessionID,
			LastChangeAt: ps.ChangedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Target != out[j].Target {
			return out[i].Target < out[j].Target
		}
		return out[i].Pane < out[j].Pane
	})
	return out
}
