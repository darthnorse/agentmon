package shared

// Session is the project-identifiable unit shown in every client surface.
// State is intentionally omitted in Phase 1 (hooks land in Phase 3).
type Session struct {
	Name    string   `json:"name"`
	Server  string   `json:"server"`
	Target  string   `json:"target"`
	Cwd     string   `json:"cwd"`
	Command string   `json:"command"`
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
