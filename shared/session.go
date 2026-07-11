package shared

import (
	"fmt"
	"regexp"
)

// State is an agent session/pane state. The agent emits only these five global
// states; the per-principal done→idle "seen" projection is hub-side.
type State string

const (
	StateBlocked State = "blocked" // needs human input/approval — highest priority
	StateDone    State = "done"    // finished a turn (globally unseen)
	StateWorking State = "working" // actively processing / running tools
	StateIdle    State = "idle"    // calm: agent present at its prompt, not working
	StateUnknown State = "unknown" // plain shell, or no hook signal yet
)

// statePriority orders states for rollup: blocked > done > working > idle > unknown.
var statePriority = map[State]int{
	StateBlocked: 5, StateDone: 4, StateWorking: 3, StateIdle: 2, StateUnknown: 1,
}

// RollUp reduces pane states to one session/server state using the priority ordering.
// Empty input or any unrecognized state contributes as StateUnknown.
func RollUp(states ...State) State {
	best, bestP := StateUnknown, statePriority[StateUnknown]
	for _, s := range states {
		p, ok := statePriority[s]
		if !ok {
			continue // unrecognized → contributes as unknown (no-op)
		}
		if p > bestP {
			best, bestP = s, p
		}
	}
	return best
}

// Session is the project-identifiable unit shown in every client surface.
type Session struct {
	Name    string   `json:"name"`
	Server  string   `json:"server"`
	Target  string   `json:"target"`
	Cwd     string   `json:"cwd"`
	Command string   `json:"command"`
	State   State    `json:"state"` // rolled up from this session's panes; "unknown" if no hook seen
	Windows []Window `json:"windows"`
}

type Window struct {
	ID    string `json:"id"`
	Index string `json:"index"`
	Name  string `json:"name"`
	Panes []Pane `json:"panes"`
}

type Pane struct {
	ID      string `json:"id"`
	Command string `json:"command"`
	Cwd     string `json:"cwd"`
}

// CreateSessionRequest is the body of POST /sessions (agent) and
// POST /api/v1/servers/{id}/sessions (hub). Name is required and validated by
// ValidateSessionName; Cwd is optional (agent allow-lists it); Command is
// optional — when non-empty the agent runs it as the tmux session's
// shell-command (the session ends when it exits; design doc D13: no new
// capability beyond session-create + send-keys).
type CreateSessionRequest struct {
	Name    string `json:"name"`
	Cwd     string `json:"cwd,omitempty"`
	Command string `json:"command,omitempty"`
}

// CreateSessionResponse is the agent's POST /sessions success body. The hub
// re-lists after create and returns the full Session instead.
type CreateSessionResponse struct {
	Name string `json:"name"`
}

// RenameSessionRequest is the body of POST /sessions/rename (agent) and
// POST /api/v1/servers/{id}/sessions/rename (hub). From is the current session
// name (any existing tmux name); To is the new name, validated by
// ValidateSessionName. The agent returns CreateSessionResponse{Name: To}.
type RenameSessionRequest struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// KillSessionRequest is the body of POST /sessions/kill (agent) and
// POST /api/v1/servers/{id}/sessions/kill (hub). Name is an existing tmux
// session name on the target socket.
type KillSessionRequest struct {
	Name string `json:"name"`
}

// sessionNameRe is the single name rule enforced at both the hub (browser
// boundary) and the agent (exec boundary): 1–64 chars, must start with an
// alphanumeric, then only A–Z a–z 0–9 _ -. This excludes '.' and ':' (tmux
// disallows them in session names), whitespace, slashes, and a leading '-'
// (tmux option confusion).
var sessionNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// ValidateSessionName reports whether name is an acceptable tmux session name
// under the shared charset rule. It returns a descriptive error on rejection.
func ValidateSessionName(name string) error {
	if name == "" {
		return fmt.Errorf("session name is required")
	}
	if !sessionNameRe.MatchString(name) {
		return fmt.Errorf("invalid session name %q: must be 1-64 chars, start with a letter or digit, and contain only letters, digits, '_' or '-'", name)
	}
	return nil
}

// SessionList is the agent's GET /sessions response envelope. The hub re-shapes
// these into its public /servers/{id}/sessions array; this is the agent↔hub form.
type SessionList struct {
	Sessions []Session `json:"sessions"`
}
