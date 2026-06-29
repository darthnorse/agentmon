package shared

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

// SessionList is the agent's GET /sessions response envelope. The hub re-shapes
// these into its public /servers/{id}/sessions array; this is the agent↔hub form.
type SessionList struct {
	Sessions []Session `json:"sessions"`
}
