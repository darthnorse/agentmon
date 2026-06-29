package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	"agentmon/agent/internal/config"
	"agentmon/agent/internal/state"
	"agentmon/agent/internal/tmux"
	"agentmon/shared"
)

// Discoverer resolves a target's live session tree. Injected so the handler is
// testable without a real tmux (production binds tmux.Discover + tmux.ExecRunner).
type Discoverer func(ctx context.Context, opts tmux.DiscoverOpts) ([]shared.Session, error)

// SessionsHandler serves GET /sessions?target=<label>. Target resolves via config
// (empty → default); discovery runs through the injected Discoverer.
// m is the state machine used to stamp each session's rolled-up state;
// a nil machine leaves every session with StateUnknown (hooks disabled).
func SessionsHandler(cfg config.Config, discover Discoverer, m *state.Machine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t, ok := cfg.ResolveTarget(r.URL.Query().Get("target"))
		if !ok {
			writeJSONError(w, http.StatusNotFound, "unknown target")
			return
		}
		sessions, err := discover(r.Context(), tmux.DiscoverOpts{
			ServerID:    cfg.ServerID,
			TargetLabel: t.Label,
			SocketName:  t.SocketName,
		})
		if err != nil {
			log.Printf("sessions: discovery failed (target=%q): %v", t.Label, err)
			writeJSONError(w, http.StatusInternalServerError, "discovery failed")
			return
		}
		if sessions == nil {
			sessions = []shared.Session{}
		}
		stampState(m, t.Label, sessions)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(shared.SessionList{Sessions: sessions})
	}
}

// stampState fills Session.State from the machine's per-pane states (rolled up).
// A nil machine (hooks disabled) leaves every session StateUnknown.
func stampState(m *state.Machine, target string, sessions []shared.Session) {
	for i := range sessions {
		if m == nil {
			sessions[i].State = shared.StateUnknown
			continue
		}
		var panes []string
		for _, win := range sessions[i].Windows {
			for _, p := range win.Panes {
				panes = append(panes, p.ID)
			}
		}
		sessions[i].State = m.Rollup(target, panes)
	}
}

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
