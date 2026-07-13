package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"

	"agentmon/agent/internal/config"
	"agentmon/agent/internal/tmux"
	"agentmon/shared"
)

// WorktreeTeardowner removes the worktree+branch under workdir. Production binds
// worktree.Teardown + worktree.ExecRunner; tests inject a fake.
type WorktreeTeardowner func(ctx context.Context, workdir, branch string) error

// WorktreeTeardownHandler serves POST /worktrees/teardown. It validates workdir
// against the agent's session_dirs roots (same allow-list as session creation)
// before any git runs. Teardown is idempotent, so success is 200 whether or not a
// worktree existed; a teardown error (e.g. a dirty-worktree refusal) is logged and
// still 200 so the hub's merge flow is never blocked or tempted into a retry.
func WorktreeTeardownHandler(cfg config.Config, teardown WorktreeTeardowner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxCreateBody)
		var req shared.WorktreeTeardownRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if req.Branch == "" {
			writeJSONError(w, http.StatusBadRequest, "branch required")
			return
		}
		if req.Workdir == "" {
			writeJSONError(w, http.StatusBadRequest, "workdir required")
			return
		}
		allowed := cfg.SessionDirs
		if len(allowed) == 0 {
			if home, err := os.UserHomeDir(); err == nil && home != "" {
				allowed = []string{home}
			}
		}
		resolved, err := tmux.ValidateCwd(req.Workdir, allowed)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := teardown(r.Context(), resolved, req.Branch); err != nil {
			log.Printf("worktree teardown %q %q: %v", resolved, req.Branch, err)
		}
		w.WriteHeader(http.StatusOK)
	}
}
