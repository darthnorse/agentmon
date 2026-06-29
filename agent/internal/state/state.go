// Package state derives a Claude Code session/pane state from hook events. It is
// pure (no tmux, no HTTP), in-memory, and safe for concurrent use.
package state

import (
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
	At               time.Time // event time; defaults to now() when zero
}

type paneState struct {
	State           shared.State
	LastEvent       string
	ClaudeSessionID string
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
	prior := m.panes[k].State
	if prior == "" {
		prior = shared.StateUnknown
	}
	if ev.Name == "SessionEnd" {
		// Delete the pane entry and report the transition directly; derive() is
		// not called for SessionEnd (the pane ceases to exist rather than changing state).
		delete(m.panes, k)
		return shared.StateUnknown, prior != shared.StateUnknown
	}
	next := prior
	if d, ok := derive(ev.Name, ev.NotificationKind); ok {
		next = d
	}
	at := ev.At
	if at.IsZero() {
		at = m.now()
	}
	m.panes[k] = paneState{State: next, LastEvent: ev.Name, ClaudeSessionID: ev.ClaudeSessionID, UpdatedAt: at}
	return next, next != prior
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
