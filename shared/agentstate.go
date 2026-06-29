package shared

import "time"

// PaneState is one pane's current derived state plus the transition counters the
// hub poller uses to ingest every transition exactly once (transport decision B).
type PaneState struct {
	Target          string    `json:"target"`
	Pane            string    `json:"pane"`
	State           State     `json:"state"`
	TransitionSeq   uint64    `json:"transitionSeq"` // bumped on every state change
	DoneSeq         uint64    `json:"doneSeq"`       // bumped on every entry into done (incl. done→done)
	Epoch           string    `json:"epoch"`         // $TMUX server pid; "" if unknown
	ClaudeSessionID string    `json:"claudeSessionId"`
	LastChangeAt    time.Time `json:"lastChangeAt"`
}

// AgentState is the agent's GET /state response: the per-pane snapshot the hub polls.
type AgentState struct {
	Panes []PaneState `json:"panes"`
}
