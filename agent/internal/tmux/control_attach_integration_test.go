package tmux

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// A real tmux attach must signal AttachedChan promptly (the handshake's empty
// %begin/%end reply arrives immediately after the server registers the client).
func TestControlClientAttachedSignal_Integration(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	const sock = "agentmon-attach-it" // dedicated socket; never the default one
	_ = exec.Command("tmux", "-L", sock, "kill-server").Run()
	if out, err := exec.Command("tmux", "-L", sock, "new-session", "-d", "-s", "s", "-x", "80", "-y", "24").CombinedOutput(); err != nil {
		t.Fatalf("new-session: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", sock, "kill-server").Run() })

	out, err := exec.Command("tmux", "-L", sock, "list-panes", "-a", "-F", "#{pane_id}").Output()
	if err != nil {
		t.Fatalf("list-panes: %v", err)
	}
	pane := strings.TrimSpace(string(out))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cc, err := NewControlClient(ctx, sock, "s", pane)
	if err != nil {
		t.Fatalf("control client: %v", err)
	}
	defer cc.Close()
	go func() { // keep the parser drained so it can never park on Output
		for range cc.Output {
		}
	}()

	select {
	case <-cc.AttachedChan():
	case <-time.After(3 * time.Second):
		t.Fatal("attach handshake never signalled")
	}
}
