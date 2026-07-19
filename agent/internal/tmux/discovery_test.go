package tmux

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeRunner returns canned output keyed by tmux subcommand. For list-panes it
// keys on the "-t <sessionId>" argument so multi-session cases are exercised.
func fakeRunner(t *testing.T, sessions string, panes map[string]string, sessErr error) Runner {
	t.Helper()
	return func(ctx context.Context, args ...string) ([]byte, error) {
		switch {
		case contains(args, "list-sessions"):
			if sessErr != nil {
				return nil, sessErr
			}
			return []byte(sessions), nil
		case contains(args, "list-panes"):
			sid := argAfter(args, "-t")
			out, ok := panes[sid]
			if !ok {
				t.Fatalf("no canned list-panes for %q (args=%v)", sid, args)
			}
			return []byte(out), nil
		default:
			t.Fatalf("unexpected tmux args: %v", args)
			return nil, nil
		}
	}
}

func contains(args []string, s string) bool {
	for _, a := range args {
		if a == s {
			return true
		}
	}
	return false
}

func argAfter(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// p builds a record from fields delimited the way tmux renders our 0x1f field
// separator: the literal token `\037`. A Runner returns tmux's -F output verbatim,
// so fixtures must use the token (not a raw 0x1f byte), and name fields must carry
// tmux's C-escaping (see pe).
func p(fields ...string) string { return strings.Join(fields, delimToken) }

// pe escapes a name field the way tmux's -F does (backslash -> \\), for fixtures
// whose session/window names contain a backslash.
func pe(name string) string { return strings.ReplaceAll(name, `\`, `\\`) }

func TestDiscoverNoServerIsEmpty(t *testing.T) {
	run := fakeRunner(t, "", nil, errors.New("tmux list-sessions: exit status 1: no server running on /tmp/tmux-0/default"))
	got, err := Discover(context.Background(), run, DiscoverOpts{ServerID: "srv", TargetLabel: "default"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("want empty non-nil slice, got %#v", got)
	}
}

func TestDiscoverMissingOrStaleSocketIsEmpty(t *testing.T) {
	// When the socket is absent or stale, tmux does not say "no server running" —
	// it says "error connecting to <path> (<reason>)". That still means "no live
	// server, zero sessions", so discovery must return an empty slice, not an error.
	// Returning the error makes SessionsHandler emit a 500 and the hub paint an
	// idle-but-healthy host as offline.
	for _, msg := range []string{
		"tmux -L agentmon list-sessions: exit status 1: error connecting to /tmp/tmux-0/agentmon (No such file or directory)",
		"tmux -L agentmon list-sessions: exit status 1: error connecting to /tmp/tmux-0/agentmon (Connection refused)",
	} {
		run := fakeRunner(t, "", nil, errors.New(msg))
		got, err := Discover(context.Background(), run, DiscoverOpts{ServerID: "srv", TargetLabel: "default"})
		if err != nil {
			t.Fatalf("%q: unexpected error: %v", msg, err)
		}
		if got == nil || len(got) != 0 {
			t.Fatalf("%q: want empty non-nil slice, got %#v", msg, got)
		}
	}
}

func TestDiscoverNonBenignConnectErrorSurfaces(t *testing.T) {
	// tmux prints "error connecting to <path> (<reason>)" for MANY errnos, not just
	// the benign missing/stale-socket ones. "(Permission denied)" (EACCES) and
	// "(Connection timed out)" (ETIMEDOUT) mean the socket exists / a server may be
	// there but unreachable — a real fault, NOT zero sessions. These must surface as
	// an error (a 500), never be masked as an empty session set that paints the host
	// healthy while live sessions are hidden.
	for _, msg := range []string{
		"tmux -L agentmon list-sessions: exit status 1: error connecting to /tmp/tmux-0/agentmon (Permission denied)",
		"tmux -L agentmon list-sessions: exit status 1: error connecting to /tmp/tmux-0/agentmon (Connection timed out)",
	} {
		run := fakeRunner(t, "", nil, errors.New(msg))
		if _, err := Discover(context.Background(), run, DiscoverOpts{ServerID: "srv", TargetLabel: "default"}); err == nil {
			t.Fatalf("%q: a non-benign connect error must surface, not be masked as empty", msg)
		}
	}
}

func TestDiscoverBenignPhraseOutsideReasonStillErrors(t *testing.T) {
	// The benign phrases must be matched as tmux's PARENTHESIZED reason, not anywhere
	// in the wrapped message. A socket name or echoed command carrying "No such file
	// or directory" while the actual reason is non-benign ("(Permission denied)") must
	// still surface as an error, not be masked as an empty session set.
	msg := "tmux -L No such file or directory list-sessions: exit status 1: error connecting to /tmp/tmux-0/x (Permission denied)"
	run := fakeRunner(t, "", nil, errors.New(msg))
	if _, err := Discover(context.Background(), run, DiscoverOpts{ServerID: "srv", TargetLabel: "default"}); err == nil {
		t.Fatal("a benign phrase outside the parenthesized reason must not mask a real (Permission denied) fault")
	}
}

func TestDiscoverGroupsPanesIntoWindowsInOrder(t *testing.T) {
	sessions := p("$1", "proj") + "\n"
	panes := map[string]string{
		"$1": strings.Join([]string{
			// window_id windex wname wactive  pane_id pcmd pcwd pactive
			p("@1", "0", "main", "1", "%0", "zsh", "/home/dev/proj", "1"),
			p("@1", "0", "main", "1", "%1", "vim", "/home/dev/proj", "0"),
			p("@2", "1", "logs", "0", "%2", "tail", "/var/log", "1"),
		}, "\n") + "\n",
	}
	got, err := Discover(context.Background(), fakeRunner(t, sessions, panes, nil),
		DiscoverOpts{ServerID: "srv", TargetLabel: "default"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 session, got %d", len(got))
	}
	s := got[0]
	if len(s.Windows) != 2 {
		t.Fatalf("want 2 windows, got %d (%+v)", len(s.Windows), s.Windows)
	}
	if s.Windows[0].ID != "@1" || s.Windows[0].Index != "0" || s.Windows[0].Name != "main" {
		t.Fatalf("window[0] = %+v", s.Windows[0])
	}
	if len(s.Windows[0].Panes) != 2 || s.Windows[0].Panes[0].ID != "%0" || s.Windows[0].Panes[1].ID != "%1" {
		t.Fatalf("window[0] panes = %+v", s.Windows[0].Panes)
	}
	if len(s.Windows[1].Panes) != 1 || s.Windows[1].Panes[0].Command != "tail" {
		t.Fatalf("window[1] panes = %+v", s.Windows[1].Panes)
	}
}

func TestDiscoverSessionCwdCommandFromActivePane(t *testing.T) {
	sessions := p("$1", "proj") + "\n"
	panes := map[string]string{
		"$1": strings.Join([]string{
			p("@1", "0", "main", "0", "%0", "zsh", "/home/dev/inactive", "1"),
			p("@2", "1", "logs", "1", "%2", "claude", "/home/dev/active", "1"),
		}, "\n") + "\n",
	}
	got, _ := Discover(context.Background(), fakeRunner(t, sessions, panes, nil),
		DiscoverOpts{ServerID: "srv", TargetLabel: "default"})
	if got[0].Cwd != "/home/dev/active" || got[0].Command != "claude" {
		t.Fatalf("session cwd/command = %q/%q, want active pane", got[0].Cwd, got[0].Command)
	}
}

func TestDiscoverSessionFallsBackToFirstPaneWhenNoActiveFlag(t *testing.T) {
	sessions := p("$1", "proj") + "\n"
	panes := map[string]string{
		"$1": p("@1", "0", "main", "0", "%0", "bash", "/srv/app", "0") + "\n",
	}
	got, _ := Discover(context.Background(), fakeRunner(t, sessions, panes, nil),
		DiscoverOpts{ServerID: "srv", TargetLabel: "default"})
	if got[0].Cwd != "/srv/app" || got[0].Command != "bash" {
		t.Fatalf("fallback cwd/command = %q/%q", got[0].Cwd, got[0].Command)
	}
}

func TestDiscoverStampsServerTargetAndHandlesMultipleSessions(t *testing.T) {
	sessions := p("$1", "alpha") + "\n" + p("$2", "beta") + "\n"
	panes := map[string]string{
		"$1": p("@1", "0", "w", "1", "%0", "zsh", "/a", "1") + "\n",
		"$2": p("@2", "0", "w", "1", "%1", "zsh", "/b", "1") + "\n",
	}
	got, _ := Discover(context.Background(), fakeRunner(t, sessions, panes, nil),
		DiscoverOpts{ServerID: "server-a", TargetLabel: "default"})
	if len(got) != 2 {
		t.Fatalf("want 2 sessions, got %d", len(got))
	}
	for _, s := range got {
		if s.Server != "server-a" || s.Target != "default" {
			t.Fatalf("server/target not stamped: %+v", s)
		}
	}
	if got[0].Name != "alpha" || got[1].Name != "beta" {
		t.Fatalf("names/order: %q %q", got[0].Name, got[1].Name)
	}
}

func TestDiscoverDecodesEscapedNames(t *testing.T) {
	// Real names contain a backslash AND a space; tmux C-escapes the name fields
	// (session_name, window_name) but emits pane_current_path raw.
	sessions := p("$1", pe(`proj a\b`)) + "\n"
	panes := map[string]string{
		"$1": p("@1", "0", pe(`win x\y`), "1", "%0", "bash", `/home/dev/a\b`, "1") + "\n",
	}
	got, err := Discover(context.Background(), fakeRunner(t, sessions, panes, nil),
		DiscoverOpts{ServerID: "srv", TargetLabel: "default"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 session, got %d", len(got))
	}
	if got[0].Name != `proj a\b` {
		t.Fatalf("session name = %q, want %q (un-doubled backslash)", got[0].Name, `proj a\b`)
	}
	if len(got[0].Windows) != 1 || got[0].Windows[0].Name != `win x\y` {
		t.Fatalf("window name = %+v, want %q", got[0].Windows, `win x\y`)
	}
	if got[0].Windows[0].Panes[0].Cwd != `/home/dev/a\b` {
		t.Fatalf("pane cwd = %q, want raw %q", got[0].Windows[0].Panes[0].Cwd, `/home/dev/a\b`)
	}
}

func TestDiscoverSkipsMalformedPaneRecord(t *testing.T) {
	// A pane record whose field count is wrong (here: a raw path containing the
	// literal token text \037 splits into an extra field) is logged and SKIPPED —
	// not silently dropped (it's logged) and not fatal to the whole target (the
	// other panes/sessions survive). One oddly-named record must not 500 /sessions.
	sessions := p("$1", "proj") + "\n"
	panes := map[string]string{
		"$1": strings.Join([]string{
			p("@1", "0", "main", "1", "%0", "bash", `/weird`+delimToken+`path`, "1"), // malformed: 9 fields
			p("@1", "0", "main", "1", "%1", "vim", "/home/dev", "1"),                 // well-formed
		}, "\n") + "\n",
	}
	result, err := DiscoverDetailed(context.Background(), fakeRunner(t, sessions, panes, nil),
		DiscoverOpts{ServerID: "srv", TargetLabel: "default"})
	if err != nil {
		t.Fatalf("a malformed record must be skipped, not fatal: %v", err)
	}
	if !result.Partial {
		t.Fatal("malformed pane record must mark discovery partial")
	}
	got := result.Sessions
	if len(got) != 1 {
		t.Fatalf("want the session to survive, got %d sessions", len(got))
	}
	if len(got[0].Windows) != 1 || len(got[0].Windows[0].Panes) != 1 || got[0].Windows[0].Panes[0].ID != "%1" {
		t.Fatalf("want only the well-formed pane %%1 to survive, got %+v", got[0].Windows)
	}
}

func TestDiscoverSkipsMalformedSessionRecord(t *testing.T) {
	// A malformed session-list record is logged and skipped; well-formed sessions
	// survive (one bad session name does not blind the operator to all others).
	sessions := p("$1", "good") + "\n" + ("$2" + delimToken + "na" + delimToken + "me") + "\n" // 2nd: 3 fields
	panes := map[string]string{
		"$1": p("@1", "0", "w", "1", "%0", "bash", "/a", "1") + "\n",
	}
	result, err := DiscoverDetailed(context.Background(), fakeRunner(t, sessions, panes, nil),
		DiscoverOpts{ServerID: "srv", TargetLabel: "default"})
	if err != nil {
		t.Fatalf("a malformed session record must be skipped, not fatal: %v", err)
	}
	if !result.Partial {
		t.Fatal("malformed session record must mark discovery partial")
	}
	got := result.Sessions
	if len(got) != 1 || got[0].Name != "good" {
		t.Fatalf("want only the well-formed session, got %+v", got)
	}
}

func TestDiscoverPassesSocketFlag(t *testing.T) {
	var sawSocket bool
	run := func(ctx context.Context, args ...string) ([]byte, error) {
		if contains(args, "-L") && argAfter(args, "-L") == "mysock" {
			sawSocket = true
		}
		if contains(args, "list-sessions") {
			return []byte(""), errors.New("no server running")
		}
		return nil, nil
	}
	_, _ = Discover(context.Background(), run, DiscoverOpts{ServerID: "srv", SocketName: "mysock"})
	if !sawSocket {
		t.Fatal("expected -L mysock in tmux args")
	}
}
