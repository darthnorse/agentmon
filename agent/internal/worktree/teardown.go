// Package worktree removes an epic's git worktree + branch on the agent host.
package worktree

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"agentmon/agent/internal/tmux"
)

// Runner runs `git -C <dir> <args...>` (arg-array; no shell). Injectable for tests.
type Runner func(ctx context.Context, dir string, args ...string) ([]byte, error)

// ExecRunner is the production Runner.
var ExecRunner Runner = func(ctx context.Context, dir string, args ...string) ([]byte, error) {
	c := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	// Force a stable locale so the messages we parse below ("not found", "not
	// fully merged") stay English regardless of the host's configured locale.
	c.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	return c.CombinedOutput()
}

// Teardown removes the worktree checked out at `branch` under `workdir`'s repo,
// then safe-deletes `branch`. Idempotent: a missing worktree or branch is not an
// error. A non-forced `worktree remove` on a DIRTY worktree fails — surfaced so
// the caller logs + swallows, never destroying uncommitted work. allowedRoots is
// the same session_dirs allow-list the workdir was validated against: a worktree
// path resolved OUTSIDE it is skipped, never removed (a leaked worktree is benign;
// deleting outside the configured boundary is not).
func Teardown(ctx context.Context, run Runner, workdir, branch string, allowedRoots []string) error {
	path, err := worktreePathForBranch(ctx, run, workdir, branch)
	if err != nil {
		return err
	}
	if path != "" {
		// The path comes from `git worktree list`, which can point anywhere on the
		// host; re-authorise it against the roots before removing anything.
		if _, err := tmux.ValidateCwd(path, allowedRoots); err != nil {
			return fmt.Errorf("worktree %q for branch %q is outside the allowed roots; skipping: %w", path, branch, err)
		}
		if out, err := run(ctx, workdir, "worktree", "remove", path); err != nil {
			return fmt.Errorf("worktree remove %q: %w: %s", path, err, bytes.TrimSpace(out))
		}
	}
	// Safe-delete the branch; ignore "not found" / "not fully merged" (idempotent,
	// never force). A leftover branch is harmless; force-deleting would lose commits.
	if out, err := run(ctx, workdir, "branch", "-d", "--", branch); err != nil {
		low := strings.ToLower(string(out))
		if !strings.Contains(low, "not found") && !strings.Contains(low, "not fully merged") {
			return fmt.Errorf("branch -d %q: %w: %s", branch, err, bytes.TrimSpace(out))
		}
	}
	_, _ = run(ctx, workdir, "worktree", "prune")
	return nil
}

// worktreePathForBranch parses `git worktree list --porcelain` for the worktree
// whose checked-out branch is refs/heads/<branch>. "" if none.
func worktreePathForBranch(ctx context.Context, run Runner, workdir, branch string) (string, error) {
	out, err := run(ctx, workdir, "worktree", "list", "--porcelain")
	if err != nil {
		return "", fmt.Errorf("worktree list: %w: %s", err, bytes.TrimSpace(out))
	}
	want := "refs/heads/" + branch
	var curPath string
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "worktree "):
			curPath = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch "):
			if strings.TrimPrefix(line, "branch ") == want {
				return curPath, nil
			}
		case line == "":
			curPath = ""
		}
	}
	return "", nil
}
