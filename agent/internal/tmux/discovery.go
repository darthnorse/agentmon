package tmux

import (
	"context"
	"strings"

	"agentmon/shared"
)

// fieldSep is ASCII Unit Separator (0x1f). We use it as the tmux -F delimiter
// because it cannot appear in a session/window name or a filesystem path, so it
// safely separates fields that may contain spaces.
const fieldSep = "\x1f"

// Runner executes `tmux <args...>` and returns stdout. Production uses
// ExecRunner; tests inject a fake. On a tmux command failure the returned error
// SHOULD carry tmux's stderr text (ExecRunner does) so Discover can recognise the
// benign "no server running" case.
//
// CONTRACT: a Runner must return list-* output in which fieldSep (0x1f) appears
// as a real 0x1f byte — any tmux -F escaping already normalised. ExecRunner does
// this; the unit-test fake emits clean 0x1f directly. (See ExecRunner for the
// known limitation of the current normalisation.)
type Runner func(ctx context.Context, args ...string) ([]byte, error)

// DiscoverOpts carries the primitive inputs of one discovery pass (no config
// coupling — the api layer maps a config.Target into this).
type DiscoverOpts struct {
	ServerID    string
	TargetLabel string
	SocketName  string // "" → tmux default socket
}

// paneFmt lists, per pane, the owning window's id/index/name/active flag and the
// pane's id/command/cwd/active flag — enough to rebuild the window→pane tree and
// pick the session's active pane from a single `list-panes -s` call.
const paneFmt = "#{window_id}" + fieldSep + "#{window_index}" + fieldSep +
	"#{window_name}" + fieldSep + "#{window_active}" + fieldSep +
	"#{pane_id}" + fieldSep + "#{pane_current_command}" + fieldSep +
	"#{pane_current_path}" + fieldSep + "#{pane_active}"

// Discover returns the live session tree for one target. A target whose tmux
// server is not running yields an empty (non-nil) slice, not an error.
func Discover(ctx context.Context, run Runner, opts DiscoverOpts) ([]shared.Session, error) {
	base := socketArgs(opts.SocketName)

	sessOut, err := run(ctx, with(base, "list-sessions", "-F",
		"#{session_id}"+fieldSep+"#{session_name}")...)
	if err != nil {
		if isNoServer(err) {
			return []shared.Session{}, nil
		}
		return nil, err
	}

	sessions := []shared.Session{}
	for _, line := range nonEmptyLines(sessOut) {
		sid, name, ok := cut2(line)
		if !ok {
			continue
		}
		windows, cwd, command, err := discoverPanes(ctx, run, base, sid)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, shared.Session{
			Name:    name,
			Server:  opts.ServerID,
			Target:  opts.TargetLabel,
			Cwd:     cwd,
			Command: command,
			Windows: windows,
		})
	}
	return sessions, nil
}

// discoverPanes runs one `list-panes -s` for the session and assembles its
// windows (first-seen order, which follows tmux's window-index order) along with
// the session-level cwd/command taken from the active window's active pane.
func discoverPanes(ctx context.Context, run Runner, base []string, sessionID string) (windows []shared.Window, sessCwd, sessCommand string, err error) {
	out, err := run(ctx, with(base, "list-panes", "-s", "-t", sessionID, "-F", paneFmt)...)
	if err != nil {
		return nil, "", "", err
	}
	pos := map[string]int{} // window_id → index in windows
	for _, line := range nonEmptyLines(out) {
		f := strings.Split(line, fieldSep)
		if len(f) != 8 {
			continue
		}
		wid, windex, wname, wactive := f[0], f[1], f[2], f[3]
		pid, pcmd, pcwd, pactive := f[4], f[5], f[6], f[7]
		i, seen := pos[wid]
		if !seen {
			i = len(windows)
			pos[wid] = i
			windows = append(windows, shared.Window{ID: wid, Index: windex, Name: wname})
		}
		windows[i].Panes = append(windows[i].Panes, shared.Pane{ID: pid, Command: pcmd, Cwd: pcwd})
		if wactive == "1" && pactive == "1" {
			sessCwd, sessCommand = pcwd, pcmd
		}
	}
	if sessCwd == "" && sessCommand == "" && len(windows) > 0 && len(windows[0].Panes) > 0 {
		sessCwd = windows[0].Panes[0].Cwd
		sessCommand = windows[0].Panes[0].Command
	}
	return windows, sessCwd, sessCommand, nil
}

// with returns base followed by extra, never aliasing base's backing array.
func with(base []string, extra ...string) []string {
	args := make([]string, 0, len(base)+len(extra))
	args = append(args, base...)
	return append(args, extra...)
}

func socketArgs(socket string) []string {
	if socket == "" {
		return nil
	}
	return []string{"-L", socket}
}

func isNoServer(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no server running")
}

func nonEmptyLines(b []byte) []string {
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func cut2(line string) (a, b string, ok bool) {
	before, after, found := strings.Cut(line, fieldSep)
	return before, after, found
}
