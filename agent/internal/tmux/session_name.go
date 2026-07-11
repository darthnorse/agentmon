package tmux

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// SessionNameForPane resolves the session that owns pane (an exact %N pane
// id — the caller validates with ValidatePaneID) on the given socket, via the
// arg-array Runner (no shell). This is the report intake's server-side session
// stamp: the CLI's own session claim would be unauthenticated, so the agent
// asks tmux which session the calling pane actually belongs to (design doc §3).
func SessionNameForPane(ctx context.Context, run Runner, socket, pane string) (string, error) {
	out, err := run(ctx, with(socketArgs(socket), "display-message", "-p", "-t", pane, "#{session_name}")...)
	if err != nil {
		return "", fmt.Errorf("tmux display-message: %w", err)
	}
	name := strings.TrimSpace(string(out))
	if name == "" {
		return "", errors.New("tmux returned an empty session name")
	}
	return name, nil
}
