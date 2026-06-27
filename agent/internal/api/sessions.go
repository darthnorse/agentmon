package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	"agentmon/agent/internal/config"
	"agentmon/agent/internal/tmux"
	"agentmon/shared"
)

// Discoverer resolves a target's live session tree. Injected so the handler is
// testable without a real tmux (production binds tmux.Discover + tmux.ExecRunner).
type Discoverer func(ctx context.Context, opts tmux.DiscoverOpts) ([]shared.Session, error)

// SessionsHandler serves GET /sessions?target=<label>. Target resolves via config
// (empty → default); discovery runs through the injected Discoverer.
func SessionsHandler(cfg config.Config, discover Discoverer) http.HandlerFunc {
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
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(shared.SessionList{Sessions: sessions})
	}
}

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
