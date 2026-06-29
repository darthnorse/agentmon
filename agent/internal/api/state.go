package api

import (
	"encoding/json"
	"net/http"

	"agentmon/agent/internal/config"
	"agentmon/agent/internal/state"
	"agentmon/shared"
)

// StateHandler serves GET /state?target=<label> — the per-pane transition snapshot
// the hub poller ingests (transport decision B). Internal agent↔hub surface; sits
// behind the same RequireBearer as /sessions. A nil machine yields {panes:[]}.
func StateHandler(cfg config.Config, m *state.Machine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		target := ""
		if q := r.URL.Query().Get("target"); q != "" {
			t, ok := cfg.ResolveTarget(q)
			if !ok {
				writeJSONError(w, http.StatusNotFound, "unknown target")
				return
			}
			target = t.Label
		}
		panes := []shared.PaneState{}
		if m != nil {
			if s := m.Snapshot(target); s != nil {
				panes = s
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(shared.AgentState{Panes: panes})
	}
}
