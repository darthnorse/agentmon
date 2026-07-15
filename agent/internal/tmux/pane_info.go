package tmux

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// PaneInfo resolves a pane's owning process id, working directory, foreground
// command, and the OWNING SESSION's creation time (session_created) in one
// display-message call. It is the usage capturer's DI seam (usage.NewCapturer
// binds this in production): sinceCreated bounds child-transcript enumeration
// to the current session's lifetime, which matters because retries reuse the
// same worktree — a naive since=epoch would pull a prior attempt's rollouts
// into this attempt's usage. A retry's session is always created later than
// the one it replaces, so session_created is a safe, tight lower bound.
//
// The four fields are all raw tmux fields (never name-escaped like
// session_name/window_name — see escape.go), so fieldSep + splitFields is
// enough; no unescapeName pass is needed, mirroring paneFmt in discovery.go.
func PaneInfo(ctx context.Context, run Runner, socket, pane string) (pid int, cwd, command string, sinceCreated time.Time, err error) {
	format := "#{pane_pid}" + fieldSep + "#{pane_current_path}" + fieldSep +
		"#{pane_current_command}" + fieldSep + "#{session_created}"
	out, runErr := run(ctx, with(socketArgs(socket), "display-message", "-p", "-t", pane, format)...)
	if runErr != nil {
		return 0, "", "", time.Time{}, fmt.Errorf("tmux display-message: %w", runErr)
	}
	line := strings.TrimSpace(string(out))
	fields, splitErr := splitFields(line, 4)
	if splitErr != nil {
		return 0, "", "", time.Time{}, splitErr
	}
	pid, err = strconv.Atoi(fields[0])
	if err != nil {
		return 0, "", "", time.Time{}, fmt.Errorf("pane_pid: %w", err)
	}
	created, err := strconv.ParseInt(fields[3], 10, 64)
	if err != nil {
		return 0, "", "", time.Time{}, fmt.Errorf("session_created: %w", err)
	}
	return pid, fields[1], fields[2], time.Unix(created, 0), nil
}
