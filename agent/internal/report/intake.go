package report

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"agentmon/agent/internal/config"
	"agentmon/agent/internal/hooks"
	"agentmon/agent/internal/tmux"
	"agentmon/shared"
)

// maxReportBody caps the intake body — a small JSON object (aligned with the
// agent's maxCreateBody). The note rides inside this cap.
const maxReportBody = 8 << 10

// reportTmuxTimeout bounds the session-resolution shell-out (mirrors
// api.agentTmuxTimeout).
const reportTmuxTimeout = 10 * time.Second

// SessionResolver resolves the session name owning a pane on a socket — the
// DI seam for IntakeHandler (production binds tmux.SessionNameForPane).
type SessionResolver func(ctx context.Context, socket, pane string) (string, error)

type intakeBody struct {
	Repo  string `json:"repo"`
	Epic  int    `json:"epic"`
	Stage string `json:"stage"`
	Note  string `json:"note"`
	PR    int    `json:"pr"`
}

// IntakeHandler serves POST /orchestrator/report — loopback + hook-token
// (mounted behind the same middleware as /hook). Unlike /hook, which soft-
// drops so a coding agent never stalls, intake failures are HARD 400s: the
// report CLI is load-bearing and must know. Session and Ts are stamped
// SERVER-SIDE — the CLI's session claim would be unauthenticated, so the
// agent resolves the calling pane's session via tmux instead (design doc §3).
// ?dry_run=1 validates everything (including session resolution) without
// buffering — the doctor's connectivity probe.
func IntakeHandler(cfg config.Config, st *Store, resolve SessionResolver, now func() time.Time) http.HandlerFunc {
	if now == nil {
		now = time.Now
	}
	return func(w http.ResponseWriter, r *http.Request) {
		pane := r.Header.Get("X-AgentMon-Pane")
		socket := hooks.SocketFromTmux(r.Header.Get("X-AgentMon-Tmux"))
		t, matched := hooks.MatchTarget(cfg, socket)
		if !tmux.ValidatePaneID(pane) || !matched {
			writeError(w, http.StatusBadRequest, "report must originate from a tmux pane on a configured target")
			return
		}
		var body intakeBody
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxReportBody)).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if body.Epic <= 0 {
			writeError(w, http.StatusBadRequest, "epic must be a positive issue number")
			return
		}
		if !shared.ReportableStage(shared.EpicStage(body.Stage)) {
			writeError(w, http.StatusBadRequest, "stage is not runner-reportable")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), reportTmuxTimeout)
		defer cancel()
		session, err := resolve(ctx, t.SocketName, pane)
		if err != nil {
			writeError(w, http.StatusBadRequest, "cannot resolve tmux session for pane")
			return
		}
		rep := shared.OrchestratorReport{
			Repo: body.Repo, Epic: body.Epic, Stage: shared.EpicStage(body.Stage),
			Note: body.Note, PR: body.PR, Session: session,
			Ts: now().UTC().Format(time.RFC3339),
		}
		if r.URL.Query().Get("dry_run") != "1" {
			st.Add(t.Label, rep)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"session": session})
	}
}

// writeError matches api.writeJSONError's wire shape ({"error": msg}); keep
// the two in sync.
func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
