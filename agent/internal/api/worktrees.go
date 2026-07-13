package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"agentmon/agent/internal/config"
	"agentmon/agent/internal/tmux"
	"agentmon/shared"
)

// worktreeTeardownTimeout bounds the git work so a hung/locked repo can't pin the
// handler goroutine indefinitely (the create path has an analogous tmux timeout).
const worktreeTeardownTimeout = 30 * time.Second

// WorktreeTeardowner removes the worktree+branch under workdir. Production binds
// worktree.Teardown + worktree.ExecRunner; tests inject a fake.
type WorktreeTeardowner func(ctx context.Context, workdir, branch string) error

// WorktreeTeardownHandler serves POST /worktrees/teardown. It validates workdir
// against the agent's session_dirs roots (same allow-list as session creation)
// and rejects a branch value git could misparse, before any git runs. Teardown is
// idempotent, so success is 200 whether or not a worktree existed; a teardown
// error (e.g. a dirty-worktree refusal) is logged and still 200 so the hub's merge
// flow is never blocked or tempted into a retry.
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
		if !safeBranch(req.Branch) {
			writeJSONError(w, http.StatusBadRequest, "invalid branch")
			return
		}
		if req.Workdir == "" {
			writeJSONError(w, http.StatusBadRequest, "workdir required")
			return
		}
		resolved, err := tmux.ValidateCwd(req.Workdir, cfg.AllowedDirs())
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), worktreeTeardownTimeout)
		defer cancel()
		if err := teardown(ctx, resolved, req.Branch); err != nil {
			log.Printf("worktree teardown %q %q: %v", resolved, req.Branch, err)
		}
		w.WriteHeader(http.StatusOK)
	}
}

// safeBranch rejects branch values git could misparse as an option or revision
// expression rather than a literal ref: a leading '-', an '@{...}' revspec, or
// whitespace/control characters. The legitimate value is an epic branch such as
// "epic/47-slug"; anything exotic is refused before it reaches `git branch -d`.
func safeBranch(b string) bool {
	if strings.HasPrefix(b, "-") || strings.Contains(b, "@{") {
		return false
	}
	for _, r := range b {
		if r < 0x20 || r == 0x7f || r == ' ' {
			return false
		}
	}
	return true
}
