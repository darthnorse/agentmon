package tmux

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestSessionNameForPaneArgArray(t *testing.T) {
	var got []string
	run := recordRunner([]byte("epic-proj-16\n"), nil, &got)
	name, err := SessionNameForPane(context.Background(), run, "agentmon", "%5")
	if err != nil || name != "epic-proj-16" {
		t.Fatalf("name=%q err=%v", name, err)
	}
	want := []string{"-L", "agentmon", "display-message", "-p", "-t", "%5", "#{session_name}"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestSessionNameForPaneDefaultSocket(t *testing.T) {
	var got []string
	run := recordRunner([]byte("s1\n"), nil, &got)
	if _, err := SessionNameForPane(context.Background(), run, "", "%0"); err != nil {
		t.Fatal(err)
	}
	if got[0] != "display-message" {
		t.Fatalf("default socket must add no -L flag: %#v", got)
	}
}

func TestSessionNameForPaneErrors(t *testing.T) {
	run := recordRunner(nil, errors.New("can't find pane %9"), new([]string))
	if _, err := SessionNameForPane(context.Background(), run, "", "%9"); err == nil {
		t.Fatal("runner error must propagate")
	}
	empty := recordRunner([]byte("\n"), nil, new([]string))
	if _, err := SessionNameForPane(context.Background(), empty, "", "%1"); err == nil {
		t.Fatal("empty session name must error")
	}
}
