package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"time"

	"agentmon/agent/internal/config"
	"agentmon/agent/internal/state"
	"agentmon/agent/internal/tmux"
	"agentmon/shared"
)

// agentTmuxTimeout bounds each tmux-shelling handler so a hung `tmux` invocation
// cannot pin the request goroutine — the http.Server ReadTimeout only covers
// reading the request, not the handler's shell-out. A var (not const) so tests
// can shorten it.
var agentTmuxTimeout = 10 * time.Second

// withTmuxTimeout derives the per-request context that bounds a tmux shell-out
// (see agentTmuxTimeout). The caller must defer the returned cancel.
func withTmuxTimeout(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), agentTmuxTimeout)
}

// Discoverer resolves a target's live session tree. Injected so the handler is
// testable without a real tmux (production binds tmux.DiscoverDetailed + tmux.ExecRunner).
type Discoverer func(ctx context.Context, opts tmux.DiscoverOpts) (tmux.Discovery, error)

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
		ctx, cancel := withTmuxTimeout(r)
		defer cancel()
		discoveryStartedAt := time.Now()
		discovery, err := discover(ctx, tmux.DiscoverOpts{
			ServerID:    cfg.ServerID,
			TargetLabel: t.Label,
			SocketName:  t.SocketName,
		})
		if err != nil {
			log.Printf("sessions: discovery failed (target=%q): %v", t.Label, err)
			writeJSONError(w, http.StatusInternalServerError, "discovery failed")
			return
		}
		sessions := discovery.Sessions
		if sessions == nil {
			sessions = []shared.Session{}
		}
		if m != nil && !discovery.Partial {
			m.Reconcile(t.Label, discoveredPaneIDs(sessions), discoveryStartedAt)
		}
		stampState(m, t.Label, sessions)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(shared.SessionList{Sessions: sessions})
	}
}

func discoveredPaneIDs(sessions []shared.Session) []string {
	var panes []string
	for _, session := range sessions {
		panes = append(panes, sessionPaneIDs(session)...)
	}
	return panes
}

func sessionPaneIDs(session shared.Session) []string {
	var panes []string
	for _, window := range session.Windows {
		for _, pane := range window.Panes {
			panes = append(panes, pane.ID)
		}
	}
	return panes
}

// maxCreateBody caps the POST /sessions request body. The body is a tiny JSON
// object (name + optional cwd); anything larger is malformed or hostile.
const maxCreateBody = 8 << 10 // 8 KiB

// SessionCreator creates a detached tmux session named name with working
// directory cwd on the given socket. It is the DI seam for CreateSessionHandler
// (mirrors Discoverer): production binds tmux.CreateSession + tmux.ExecRunner;
// tests inject a fake that records its arguments.
type SessionCreator func(ctx context.Context, socket, name, cwd string) error

// CreateSessionHandler serves POST /sessions?target=<label>. It is the agent's
// exec boundary for session creation (§12.2 / §13.6): the body's name is
// re-validated against the shared charset rule, a non-empty command is rejected
// (custom commands are not supported in v1), the target resolves via config, and
// the requested cwd is allow-listed against cfg.SessionDirs (defaulting to the
// agent user's home) before any tmux invocation. The SessionCreator does the
// actual no-shell exec. On success it returns 200 {"name":...}; the hub re-lists
// and returns the full Session.
func CreateSessionHandler(cfg config.Config, create SessionCreator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxCreateBody)
		var req shared.CreateSessionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if err := shared.ValidateSessionName(req.Name); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		if req.Command != "" {
			writeJSONError(w, http.StatusBadRequest, "custom commands are not supported")
			return
		}
		t, ok := cfg.ResolveTarget(r.URL.Query().Get("target"))
		if !ok {
			writeJSONError(w, http.StatusNotFound, "unknown target")
			return
		}
		allowed := cfg.SessionDirs
		if len(allowed) == 0 {
			if home, err := os.UserHomeDir(); err == nil && home != "" {
				allowed = []string{home}
			}
		}
		cwd, err := tmux.ValidateCwd(req.Cwd, allowed)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		ctx, cancel := withTmuxTimeout(r)
		defer cancel()
		if err := create(ctx, t.SocketName, req.Name, cwd); err != nil {
			if errors.Is(err, tmux.ErrSessionExists) {
				writeJSONError(w, http.StatusConflict, "a session with that name already exists")
				return
			}
			log.Printf("sessions: create failed (target=%q name=%q): %v", t.Label, req.Name, err)
			writeJSONError(w, http.StatusInternalServerError, "create failed")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(shared.CreateSessionResponse{Name: req.Name})
	}
}

// SessionRenamer renames an existing tmux session on the given socket. DI seam for
// RenameSessionHandler (mirrors SessionCreator): production binds tmux.RenameSession
// + tmux.ExecRunner; tests inject a fake.
type SessionRenamer func(ctx context.Context, socket, from, to string) error

// RenameSessionHandler serves POST /sessions/rename?target=<label>. The body's `to`
// is re-validated against the shared charset rule; `from` must be non-empty (an
// existing tmux name). Maps tmux.ErrSessionExists→409 and tmux.ErrNoSession→404.
func RenameSessionHandler(cfg config.Config, rename SessionRenamer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxCreateBody)
		var req shared.RenameSessionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if req.From == "" {
			writeJSONError(w, http.StatusBadRequest, "from is required")
			return
		}
		if err := shared.ValidateSessionName(req.To); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		t, ok := cfg.ResolveTarget(r.URL.Query().Get("target"))
		if !ok {
			writeJSONError(w, http.StatusNotFound, "unknown target")
			return
		}
		ctx, cancel := withTmuxTimeout(r)
		defer cancel()
		if err := rename(ctx, t.SocketName, req.From, req.To); err != nil {
			switch {
			case errors.Is(err, tmux.ErrSessionExists):
				writeJSONError(w, http.StatusConflict, "a session with that name already exists")
			case errors.Is(err, tmux.ErrNoSession):
				writeJSONError(w, http.StatusNotFound, "no such session")
			default:
				log.Printf("sessions: rename failed (target=%q from=%q to=%q): %v", t.Label, req.From, req.To, err)
				writeJSONError(w, http.StatusInternalServerError, "rename failed")
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(shared.CreateSessionResponse{Name: req.To})
	}
}

// SessionKiller terminates an existing tmux session on the given socket. DI seam
// for KillSessionHandler (mirrors SessionRenamer): production binds tmux.KillSession
// + tmux.ExecRunner; tests inject a fake.
type SessionKiller func(ctx context.Context, socket, name string) error

// KillSessionHandler serves POST /sessions/kill?target=<label>. The body's `name`
// must be a non-empty existing tmux session name; the target resolves via config
// (the agent's own socket — never client-controlled). Maps tmux.ErrNoSession→404.
func KillSessionHandler(cfg config.Config, kill SessionKiller) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxCreateBody)
		var req shared.KillSessionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		// Only non-empty is checked here — no charset constraint. This is deliberate:
		// the name comes from the agent's own session list and is routed through the
		// config-scoped socket, and tmux.KillSession pins it with the "=" exact-match
		// prefix so a glob/`:` in a name can't select a different session (there is no
		// shell either — the name is a direct argv token). We do NOT ValidateSessionName
		// here because a session may have been created directly via tmux with a name
		// outside the create/rename charset, and the operator must still be able to
		// kill it; exact-match keeps that safe.
		if req.Name == "" {
			writeJSONError(w, http.StatusBadRequest, "name is required")
			return
		}
		t, ok := cfg.ResolveTarget(r.URL.Query().Get("target"))
		if !ok {
			writeJSONError(w, http.StatusNotFound, "unknown target")
			return
		}
		ctx, cancel := withTmuxTimeout(r)
		defer cancel()
		if err := kill(ctx, t.SocketName, req.Name); err != nil {
			if errors.Is(err, tmux.ErrNoSession) {
				writeJSONError(w, http.StatusNotFound, "no such session")
				return
			}
			log.Printf("sessions: kill failed (target=%q name=%q): %v", t.Label, req.Name, err)
			writeJSONError(w, http.StatusInternalServerError, "kill failed")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(shared.CreateSessionResponse{Name: req.Name})
	}
}

// stampState fills Session.State from the machine's per-pane states (rolled up).
// A nil machine (hooks disabled) leaves every session StateUnknown.
func stampState(m *state.Machine, target string, sessions []shared.Session) {
	for i := range sessions {
		sessions[i].State = shared.StateUnknown
	}
	if m == nil {
		return
	}
	for i := range sessions {
		sessions[i].State = m.Rollup(target, sessionPaneIDs(sessions[i]))
	}
}

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
