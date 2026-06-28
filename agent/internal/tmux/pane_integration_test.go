package tmux

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func TestResolvePaneSessionRealTmux(t *testing.T) {
	requireTmux(t)
	const sock = "agentmon-m2-pane"
	_ = exec.Command("tmux", "-L", sock, "kill-server").Run()
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", sock, "kill-server").Run() })
	if out, err := exec.Command("tmux", "-L", sock, "new-session", "-d", "-s", "s", "-x", "80", "-y", "24").CombinedOutput(); err != nil {
		t.Fatalf("new-session: %v: %s", err, out)
	}
	paneOut, _ := exec.Command("tmux", "-L", sock, "list-panes", "-F", "#{pane_id}").Output()
	pane := strings.TrimSpace(string(paneOut))

	sid, ok, err := ResolvePaneSession(context.Background(), ExecRunner, sock, pane)
	if err != nil || !ok {
		t.Fatalf("resolve: ok=%v err=%v", ok, err)
	}
	if !strings.HasPrefix(sid, "$") {
		t.Fatalf("session id = %q, want $N", sid)
	}

	snap, err := CapturePane(context.Background(), sock, pane, 100)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if strings.Contains(strings.ReplaceAll(string(snap), "\r\n", ""), "\n") {
		t.Fatal("capture output has a bare LF; want CRLF")
	}
}
