package tmux

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os/exec"
)

// ResolvePaneSession returns the tmux session id that owns paneID on the given
// socket, or ok=false if no such pane exists. It is authoritative: it lists every
// pane and looks the id up, because `display-message -t <bogus>` returns an empty
// string with exit 0 (it falls back to a stray session) rather than failing.
func ResolvePaneSession(ctx context.Context, run Runner, socket, paneID string) (string, bool, error) {
	base := socketArgs(socket)
	out, err := run(ctx, with(base, "list-panes", "-a", "-F", "#{pane_id}"+fieldSep+"#{session_id}")...)
	if err != nil {
		return "", false, err
	}
	for _, line := range nonEmptyLines(out) {
		f, err := splitFields(line, 2)
		if err != nil {
			// pane_id/session_id are structural (never escaped), so this is near-
			// impossible; if it ever happens, skip (logged) the one bad line rather
			// than failing the whole lookup. A miss falls through to ok=false → 404.
			log.Printf("resolve pane: skipping malformed list-panes record: %v", err)
			continue
		}
		if f[0] == paneID {
			return f[1], true, nil
		}
	}
	return "", false, nil
}

// CapturePane returns the pane's scrollback as a snapshot to bootstrap a new
// viewer: -e keeps colour escapes, -S -<lines> reaches back, and bare LFs become
// CRLF for xterm. socket "" uses the default tmux socket. (Ported from the spike.)
func CapturePane(ctx context.Context, socket, pane string, lines int) ([]byte, error) {
	if lines <= 0 {
		lines = 5000
	}
	base := socketArgs(socket)
	args := with(base, "capture-pane", "-p", "-e", "-t", pane, "-S", fmt.Sprintf("-%d", lines))
	out, err := exec.CommandContext(ctx, "tmux", args...).Output()
	if err != nil {
		return nil, err
	}
	return captureToCRLF(out), nil
}

func captureToCRLF(b []byte) []byte {
	return bytes.ReplaceAll(b, []byte("\n"), []byte("\r\n"))
}

// TuneSession makes the passive control client adopt the viewer's size on resize:
// window-size latest + aggressive-resize off, scoped to the session. Best-effort;
// errors are ignored (a tuning failure must not block a terminal). escape-time is
// NOT touched — send-keys -H is byte-exact regardless, so we avoid mutating a
// shared server's global option.
func TuneSession(ctx context.Context, socket, sessionID string) {
	base := socketArgs(socket)
	run := func(extra ...string) {
		_ = exec.CommandContext(ctx, "tmux", with(base, extra...)...).Run()
	}
	run("set-option", "-t", sessionID, "window-size", "latest")
	run("set-option", "-t", sessionID, "aggressive-resize", "off")
}
