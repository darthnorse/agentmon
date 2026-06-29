package state

import (
	"testing"
	"time"

	"agentmon/shared"
)

func fixedNow() func() time.Time {
	t := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return t }
}

func TestApplyMapping(t *testing.T) {
	cases := []struct {
		name, event, kind string
		want              shared.State
	}{
		{"session start", "SessionStart", "", shared.StateIdle},
		{"prompt", "UserPromptSubmit", "", shared.StateWorking},
		{"pretool", "PreToolUse", "", shared.StateWorking},
		{"posttool", "PostToolUse", "", shared.StateWorking},
		{"permission request", "PermissionRequest", "", shared.StateBlocked},
		{"notif permission", "Notification", "permission_prompt", shared.StateBlocked},
		{"notif idle", "Notification", "idle", shared.StateDone},
		{"stop", "Stop", "", shared.StateDone},
		{"session end", "SessionEnd", "", shared.StateUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := New(fixedNow())
			got, changed := m.Apply(Event{Target: "default", Pane: "%0", Name: c.event, NotificationKind: c.kind})
			if got != c.want {
				t.Fatalf("Apply(%s/%s) = %q, want %q", c.event, c.kind, got, c.want)
			}
			if c.want != shared.StateUnknown && !changed {
				t.Fatalf("first %s should report changed", c.event)
			}
		})
	}
}

func TestApplyPreservesOnSubagentStopAndUnknownEvent(t *testing.T) {
	for _, name := range []string{"SubagentStop", "TotallyNewEventV9"} {
		m := New(fixedNow())
		m.Apply(Event{Target: "default", Pane: "%0", Name: "PreToolUse"}) // working
		got, changed := m.Apply(Event{Target: "default", Pane: "%0", Name: name})
		if got != shared.StateWorking || changed {
			t.Fatalf("%s: got %q changed=%v, want working/false", name, got, changed)
		}
	}
}

func TestApplyChangedFlag(t *testing.T) {
	m := New(fixedNow())
	if _, changed := m.Apply(Event{Target: "default", Pane: "%0", Name: "Stop"}); !changed {
		t.Fatal("first Stop should be changed")
	}
	if _, changed := m.Apply(Event{Target: "default", Pane: "%0", Name: "Stop"}); changed {
		t.Fatal("repeat Stop should not be changed")
	}
}

func TestPaneAndRollup(t *testing.T) {
	m := New(fixedNow())
	if s, ok := m.Pane("default", "%9"); ok || s != shared.StateUnknown {
		t.Fatalf("unknown pane → %q ok=%v", s, ok)
	}
	m.Apply(Event{Target: "default", Pane: "%0", Name: "Stop"})              // done
	m.Apply(Event{Target: "default", Pane: "%1", Name: "PermissionRequest"}) // blocked
	if got := m.Rollup("default", []string{"%0", "%1"}); got != shared.StateBlocked {
		t.Fatalf("rollup = %q, want blocked", got)
	}
	if got := m.Rollup("default", []string{"%0"}); got != shared.StateDone {
		t.Fatalf("rollup = %q, want done", got)
	}
	if got := m.Rollup("default", []string{"%none"}); got != shared.StateUnknown {
		t.Fatalf("rollup unknown panes = %q, want unknown", got)
	}
	if got := m.Rollup("other", []string{"%0"}); got != shared.StateUnknown {
		t.Fatalf("rollup wrong target = %q, want unknown", got)
	}
}
