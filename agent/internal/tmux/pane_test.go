package tmux

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestResolvePaneSessionFindsOwningSession(t *testing.T) {
	run := func(ctx context.Context, args ...string) ([]byte, error) {
		if !contains(args, "list-panes") || !contains(args, "-a") {
			t.Fatalf("expected list-panes -a, got %v", args)
		}
		// pane_id <delim> session_id, faithful tmux form (token delimiter).
		return []byte(p("%0", "$0") + "\n" + p("%3", "$1") + "\n" + p("%4", "$1") + "\n"), nil
	}
	sid, ok, err := ResolvePaneSession(context.Background(), run, "", "%3")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if sid != "$1" {
		t.Fatalf("session = %q, want $1", sid)
	}
}

func TestResolvePaneSessionMissIsNotFound(t *testing.T) {
	run := func(ctx context.Context, args ...string) ([]byte, error) {
		return []byte(p("%0", "$0") + "\n"), nil
	}
	_, ok, err := ResolvePaneSession(context.Background(), run, "", "%9")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("want ok=false for an unknown pane")
	}
}

func TestResolvePaneSessionThreadsSocket(t *testing.T) {
	var sawSocket bool
	run := func(ctx context.Context, args ...string) ([]byte, error) {
		if contains(args, "-L") && argAfter(args, "-L") == "devsock" {
			sawSocket = true
		}
		return []byte(p("%1", "$0") + "\n"), nil
	}
	_, _, _ = ResolvePaneSession(context.Background(), run, "devsock", "%1")
	if !sawSocket {
		t.Fatal("expected -L devsock in tmux args")
	}
}

func TestResolvePaneSessionPropagatesRunnerError(t *testing.T) {
	run := func(ctx context.Context, args ...string) ([]byte, error) {
		return nil, errors.New("tmux exploded")
	}
	if _, _, err := ResolvePaneSession(context.Background(), run, "", "%1"); err == nil {
		t.Fatal("want error when the runner fails")
	}
}

func TestCaptureArgsConvertLFtoCRLF(t *testing.T) {
	// captureToCRLF is the pure post-processing helper CapturePane uses.
	got := captureToCRLF([]byte("a\nb\n"))
	if string(got) != "a\r\nb\r\n" {
		t.Fatalf("got %q, want CRLF-translated", got)
	}
	if strings.Contains(strings.ReplaceAll(string(got), "\r\n", ""), "\n") {
		t.Fatal("a bare LF survived translation")
	}
}
