package tmux

import (
	"context"
	"os"
	"os/exec"
	"testing"
)

// requireTmux skips on hosts without tmux (e.g. CI). Integration tests run on the
// dev box / real servers only, per the Phase 1 testing strategy.
func requireTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed; skipping integration test")
	}
}

const testSocket = "agentmon-m1-test"

func killTestServer() { _ = exec.Command("tmux", "-L", testSocket, "kill-server").Run() }

func TestExecRunnerDiscoversRealSession(t *testing.T) {
	requireTmux(t)
	killTestServer()
	t.Cleanup(killTestServer)

	mk := exec.Command("tmux", "-L", testSocket, "new-session", "-d", "-s", "proj", "-c", "/tmp")
	if out, err := mk.CombinedOutput(); err != nil {
		t.Fatalf("new-session: %v: %s", err, out)
	}

	got, err := Discover(context.Background(), ExecRunner,
		DiscoverOpts{ServerID: "srv", TargetLabel: "default", SocketName: testSocket})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 1 || got[0].Name != "proj" {
		t.Fatalf("sessions = %+v", got)
	}
	s := got[0]
	if s.Server != "srv" || s.Target != "default" {
		t.Fatalf("server/target = %q/%q", s.Server, s.Target)
	}
	if len(s.Windows) == 0 || len(s.Windows[0].Panes) == 0 {
		t.Fatalf("expected at least one window/pane: %+v", s.Windows)
	}
	if s.Cwd == "" || s.Command == "" {
		t.Fatalf("session cwd/command empty: %q/%q", s.Cwd, s.Command)
	}
}

// TestExecRunnerDecodesBackslashAndSpaceNames drives REAL tmux 3.5a to confirm the
// faithful de-escaper (escape.go) reverses tmux's actual -F escaping: session and
// window names containing a backslash AND a space come back exact, and a
// pane_current_path with a backslash + space is preserved raw. This is the case
// the M1 normalisation heuristic mishandled.
func TestExecRunnerDecodesBackslashAndSpaceNames(t *testing.T) {
	requireTmux(t)
	killTestServer()
	t.Cleanup(killTestServer)

	dir, err := os.MkdirTemp("", "agentmon m2") // space in the path...
	if err != nil {
		t.Fatal(err)
	}
	dir = dir + `\back` // ...and a backslash
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	tmux := func(args ...string) {
		t.Helper()
		full := append([]string{"-L", testSocket}, args...)
		if out, err := exec.Command("tmux", full...).CombinedOutput(); err != nil {
			t.Fatalf("tmux %v: %v: %s", args, err, out)
		}
	}
	tmux("new-session", "-d", "-s", "base", "-x", "80", "-y", "24", "-c", dir)
	sid, err := exec.Command("tmux", "-L", testSocket, "list-sessions", "-F", "#{session_id}").Output()
	if err != nil {
		t.Fatalf("list-sessions: %v", err)
	}
	target := string(trimNL(sid))
	tmux("rename-session", "-t", target, `proj a\b`)
	tmux("rename-window", "-t", target, `win x\y`)

	got, err := Discover(context.Background(), ExecRunner,
		DiscoverOpts{ServerID: "srv", TargetLabel: "default", SocketName: testSocket})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 session, got %d: %+v", len(got), got)
	}
	if got[0].Name != `proj a\b` {
		t.Fatalf("session name = %q, want %q", got[0].Name, `proj a\b`)
	}
	if len(got[0].Windows) != 1 || got[0].Windows[0].Name != `win x\y` {
		t.Fatalf("window = %+v, want name %q", got[0].Windows, `win x\y`)
	}
	if got[0].Cwd != dir {
		t.Fatalf("session cwd = %q, want raw %q", got[0].Cwd, dir)
	}
}

func TestExecRunnerEmptyWhenNoServer(t *testing.T) {
	requireTmux(t)
	killTestServer() // ensure no server on this socket

	got, err := Discover(context.Background(), ExecRunner,
		DiscoverOpts{ServerID: "srv", TargetLabel: "default", SocketName: testSocket})
	if err != nil {
		t.Fatalf("want nil error for no-server, got %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty, got %+v", got)
	}
}
