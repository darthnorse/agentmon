package tmux

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestPaneInfoArgArrayAndParsing(t *testing.T) {
	var got []string
	run := recordRunner([]byte(p("1234", "/root/agentmon", "claude", "1752505200")+"\n"), nil, &got)
	pid, cwd, cmd, since, err := PaneInfo(context.Background(), run, "agentmon", "%5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pid != 1234 || cwd != "/root/agentmon" || cmd != "claude" {
		t.Fatalf("pid=%d cwd=%q cmd=%q", pid, cwd, cmd)
	}
	if !since.Equal(time.Unix(1752505200, 0)) {
		t.Fatalf("since = %v, want %v", since, time.Unix(1752505200, 0))
	}
	want := []string{"-L", "agentmon", "display-message", "-p", "-t", "%5",
		"#{pane_pid}" + fieldSep + "#{pane_current_path}" + fieldSep + "#{pane_current_command}" + fieldSep + "#{session_created}"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestPaneInfoDefaultSocket(t *testing.T) {
	var got []string
	run := recordRunner([]byte(p("1", "/tmp", "bash", "0")+"\n"), nil, &got)
	if _, _, _, _, err := PaneInfo(context.Background(), run, "", "%0"); err != nil {
		t.Fatal(err)
	}
	if got[0] != "display-message" {
		t.Fatalf("default socket must add no -L flag: %#v", got)
	}
}

func TestPaneInfoRunnerError(t *testing.T) {
	run := recordRunner(nil, errors.New("can't find pane %9"), new([]string))
	if _, _, _, _, err := PaneInfo(context.Background(), run, "", "%9"); err == nil {
		t.Fatal("runner error must propagate")
	}
}

func TestPaneInfoMalformedOutput(t *testing.T) {
	run := recordRunner([]byte("only-one-field\n"), nil, new([]string))
	if _, _, _, _, err := PaneInfo(context.Background(), run, "", "%1"); err == nil {
		t.Fatal("malformed record must error, not silently misparse")
	}
}
