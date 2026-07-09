package tmux

import (
	"context"
	"log"
	"strings"

	"agentmon/shared"
)

// fieldSep is the ASCII Unit Separator (0x1f) we inject into -F format templates
// as the field delimiter. It cannot occur in a session/window name or a path, so
// it safely separates fields that may contain spaces. tmux escapes this control
// byte in its output, rendering it as the literal token `\037` (delimToken), which
// is what Discover actually splits on — see escape.go.
const fieldSep = "\x1f"

// Runner executes `tmux <args...>` and returns stdout. Production uses
// ExecRunner; tests inject a fake. On a tmux command failure the returned error
// SHOULD carry tmux's stderr text (ExecRunner does) so Discover can recognise the
// benign "no server running" case.
//
// CONTRACT: a Runner returns tmux's list-* -F output VERBATIM — Discover does all
// de-escaping (splitFields on delimToken, unescapeName on the C-escaped name
// fields; command/path are emitted raw). ExecRunner returns stdout unchanged; the
// unit-test fake emits the same faithful form (token delimiters, \\-escaped names).
type Runner func(ctx context.Context, args ...string) ([]byte, error)

// DiscoverOpts carries the primitive inputs of one discovery pass (no config
// coupling — the api layer maps a config.Target into this).
type DiscoverOpts struct {
	ServerID    string
	TargetLabel string
	SocketName  string // "" → tmux default socket
}

// Discovery is one tmux snapshot plus whether malformed records were skipped.
// Partial snapshots remain useful for display but must not drive destructive
// reconciliation because omitted panes may still be live.
type Discovery struct {
	Sessions []shared.Session
	Partial  bool
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
	result, err := DiscoverDetailed(ctx, run, opts)
	return result.Sessions, err
}

// DiscoverDetailed is Discover with completeness metadata for callers that
// reconcile state against the returned pane set.
func DiscoverDetailed(ctx context.Context, run Runner, opts DiscoverOpts) (Discovery, error) {
	base := socketArgs(opts.SocketName)

	sessOut, err := run(ctx, with(base, "list-sessions", "-F",
		"#{session_id}"+fieldSep+"#{session_name}")...)
	if err != nil {
		if isNoServer(err) {
			return Discovery{Sessions: []shared.Session{}}, nil
		}
		return Discovery{}, err
	}

	sessions := []shared.Session{}
	partial := false
	for _, line := range nonEmptyLines(sessOut) {
		f, err := splitFields(line, 2)
		if err != nil {
			// A single un-decodable record (a name carrying a raw 0x1f or literal
			// \037) is logged and skipped, not fatal: one oddly-named session must
			// not blind the operator to every other session on this target. Logged,
			// so it is never a *silent* drop (the M1 failure this replaces).
			log.Printf("discovery: skipping malformed session record (server=%s target=%s): %v", opts.ServerID, opts.TargetLabel, err)
			partial = true
			continue
		}
		sid, name := f[0], unescapeName(f[1])
		windows, cwd, command, panesPartial, err := discoverPanes(ctx, run, base, sid)
		if err != nil {
			return Discovery{}, err
		}
		partial = partial || panesPartial
		sessions = append(sessions, shared.Session{
			Name:    name,
			Server:  opts.ServerID,
			Target:  opts.TargetLabel,
			Cwd:     cwd,
			Command: command,
			Windows: windows,
		})
	}
	return Discovery{Sessions: sessions, Partial: partial}, nil
}

// discoverPanes runs one `list-panes -s` for the session and assembles its
// windows (first-seen order, which follows tmux's window-index order) along with
// the session-level cwd/command taken from the active window's active pane.
func discoverPanes(ctx context.Context, run Runner, base []string, sessionID string) (windows []shared.Window, sessCwd, sessCommand string, partial bool, err error) {
	out, err := run(ctx, with(base, "list-panes", "-s", "-t", sessionID, "-F", paneFmt)...)
	if err != nil {
		return nil, "", "", false, err
	}
	pos := map[string]int{} // window_id → index in windows
	for _, line := range nonEmptyLines(out) {
		f, err := splitFields(line, 8)
		if err != nil {
			// Skip (but log) a single malformed pane record rather than dropping the
			// whole session — see the session-loop rationale above.
			log.Printf("discovery: skipping malformed pane record (session=%s): %v", sessionID, err)
			partial = true
			continue
		}
		wid, windex, wname, wactive := f[0], f[1], unescapeName(f[2]), f[3]
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
	return windows, sessCwd, sessCommand, partial, nil
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
