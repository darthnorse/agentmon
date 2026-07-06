package tmux

import (
	"context"
	"io"
	"os/exec"
	"testing"
	"time"
)

// TestReadLoopQuitUnblocksFullOutput is the regression test for the goroutine +
// process leak: when Output fills and the consumer stops draining, Close()
// (which closes quit) must still let readLoop exit AND reap the tmux process.
// On the pre-fix code (the select watched Done, which only readLoop itself
// closes, and there was no cmd.Wait) readLoop blocked forever, Done never
// closed, and the process was never reaped.
func TestReadLoopQuitUnblocksFullOutput(t *testing.T) {
	// A real but inert process so the deferred cmd.Wait() has something to reap.
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	c := &ControlClient{
		pane:   "%0",
		cmd:    cmd,
		quit:   make(chan struct{}),
		Output: make(chan []byte, 2), // tiny buffer fills immediately
		Done:   make(chan struct{}),
	}

	// Feed matching %output lines; readLoop parks on the full, undrained Output.
	pr, pw := io.Pipe()
	go func() {
		for {
			if _, err := pw.Write([]byte("%output %0 hi\n")); err != nil {
				return
			}
		}
	}()
	defer pw.Close()

	go c.readLoop(pr)
	time.Sleep(50 * time.Millisecond) // let Output fill and readLoop park in the select

	// Simulate Close(): close quit + kill the process.
	close(c.quit)
	_ = cmd.Process.Kill()

	select {
	case <-c.Done:
		// readLoop exited; defers run LIFO so cmd.Wait() already ran → reaped.
		if cmd.ProcessState == nil {
			t.Fatal("process not reaped (cmd.Wait not called before Done closed)")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("readLoop did not exit after quit closed — goroutine leak")
	}
}

// TestNewControlClientRejectsInvalidPane is the regression test for control-mode
// command injection via the pane id. Validation happens before any tmux exec, so
// these all return an error without spawning tmux (CI-safe, no tmux needed).
func TestNewControlClientRejectsInvalidPane(t *testing.T) {
	bad := []string{"", "0", "%", "%0 ", "% 0", "%0\nkill-server", "%0; ls", "%a", "pane0"}
	for _, p := range bad {
		if _, err := NewControlClient(context.Background(), "", "sess", p); err == nil {
			t.Errorf("pane %q: expected error, got nil", p)
		}
	}
}

// TestReadLoopSignalsAttached: AttachedChan closes on the FIRST %end (the attach
// reply terminator) and only once; %begin alone must not signal.
func TestReadLoopSignalsAttached(t *testing.T) {
	cmd := exec.Command("sleep", "30") // inert process so the deferred Wait can reap
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	c := &ControlClient{
		pane: "%0", cmd: cmd,
		quit: make(chan struct{}), Output: make(chan []byte, 8),
		Done: make(chan struct{}), attached: make(chan struct{}),
	}
	pr, pw := io.Pipe()
	go c.readLoop(pr)

	if _, err := pw.Write([]byte("%begin 1 0 0\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.AttachedChan():
		t.Fatal("attached signalled on begin — must wait for end")
	case <-time.After(50 * time.Millisecond):
	}
	if _, err := pw.Write([]byte("%end 1 0 0\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.AttachedChan():
	case <-time.After(time.Second):
		t.Fatal("attached not signalled after end")
	}
	// A second %end must not re-close (would panic).
	if _, err := pw.Write([]byte("%end 2 1 0\n")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)

	pw.Close()
	_ = cmd.Process.Kill()
	<-c.Done
}
