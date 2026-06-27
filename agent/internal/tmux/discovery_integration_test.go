package tmux

import (
	"context"
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
