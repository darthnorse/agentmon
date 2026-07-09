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

func TestSessionEndDeletesEntry(t *testing.T) {
	m := New(fixedNow())
	// SessionEnd on a working pane: returns (StateUnknown, true) and deletes the entry.
	m.Apply(Event{Target: "default", Pane: "%5", Name: "PreToolUse"}) // working
	got, changed := m.Apply(Event{Target: "default", Pane: "%5", Name: "SessionEnd"})
	if got != shared.StateUnknown {
		t.Fatalf("SessionEnd on working pane: got %q, want StateUnknown", got)
	}
	if !changed {
		t.Fatal("SessionEnd on working pane should report changed=true")
	}
	if _, ok := m.Pane("default", "%5"); ok {
		t.Fatal("SessionEnd should delete pane entry (Pane ok should be false)")
	}
	if s := m.Rollup("default", []string{"%5"}); s != shared.StateUnknown {
		t.Fatalf("Rollup after SessionEnd = %q, want StateUnknown", s)
	}
}

func TestSessionEndNeverSeenPane(t *testing.T) {
	m := New(fixedNow())
	// SessionEnd on a never-seen pane: returns (StateUnknown, false) and creates no entry.
	got, changed := m.Apply(Event{Target: "default", Pane: "%99", Name: "SessionEnd"})
	if got != shared.StateUnknown {
		t.Fatalf("SessionEnd on unseen pane: got %q, want StateUnknown", got)
	}
	if changed {
		t.Fatal("SessionEnd on never-seen pane should report changed=false")
	}
	if _, ok := m.Pane("default", "%99"); ok {
		t.Fatal("SessionEnd on never-seen pane must not create an entry")
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

func TestReconcileRemovesOnlyAbsentOlderPanesForTarget(t *testing.T) {
	base := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	m := New(nil)
	m.Apply(Event{Target: "dev", Pane: "%live", Name: "Stop", At: base})
	m.Apply(Event{Target: "dev", Pane: "%gone", Name: "PermissionRequest", At: base})
	m.Apply(Event{Target: "dev", Pane: "%raced", Name: "SessionStart", At: base.Add(2 * time.Second)})
	m.Apply(Event{Target: "other", Pane: "%other", Name: "Stop", At: base})

	m.Reconcile("dev", []string{"%live"}, base.Add(time.Second))

	if _, ok := m.Pane("dev", "%live"); !ok {
		t.Fatal("live pane was pruned")
	}
	if _, ok := m.Pane("dev", "%gone"); ok {
		t.Fatal("absent pane older than discovery was not pruned")
	}
	if _, ok := m.Pane("dev", "%raced"); !ok {
		t.Fatal("hook arriving during discovery was pruned")
	}
	if _, ok := m.Pane("other", "%other"); !ok {
		t.Fatal("pane from another target was pruned")
	}
}

func TestReconcileEmptyDiscoveryClearsTarget(t *testing.T) {
	m := New(fixedNow())
	m.Apply(Event{Target: "dev", Pane: "%0", Name: "Stop"})
	m.Apply(Event{Target: "dev", Pane: "%1", Name: "PermissionRequest"})
	m.Reconcile("dev", nil, fixedNow()().Add(time.Second))
	if got := m.Snapshot("dev"); len(got) != 0 {
		t.Fatalf("snapshot after empty discovery = %+v", got)
	}
}

func TestApplyCountersTransitionAndDone(t *testing.T) {
	m := New(func() time.Time { return time.Unix(0, 0) })
	// SessionStart: unknown→idle = first change
	m.Apply(Event{Target: "dev", Pane: "%0", Name: "SessionStart"})
	// UserPromptSubmit: idle→working = change
	m.Apply(Event{Target: "dev", Pane: "%0", Name: "UserPromptSubmit"})
	// Stop: working→done = change AND a finished turn
	m.Apply(Event{Target: "dev", Pane: "%0", Name: "Stop"})
	// Stop again: done→done = NOT a change, but a NEW finished turn
	m.Apply(Event{Target: "dev", Pane: "%0", Name: "Stop"})

	snap := m.Snapshot("dev")
	if len(snap) != 1 {
		t.Fatalf("want 1 pane, got %d", len(snap))
	}
	got := snap[0]
	if got.State != shared.StateDone {
		t.Errorf("state = %q, want done", got.State)
	}
	if got.TransitionSeq != 3 { // unknown→idle→working→done (the 2nd Stop is not a change)
		t.Errorf("TransitionSeq = %d, want 3", got.TransitionSeq)
	}
	if got.DoneSeq != 2 { // both Stops are finished turns
		t.Errorf("DoneSeq = %d, want 2", got.DoneSeq)
	}
}

func TestApplyPreserveDoesNotBumpCounters(t *testing.T) {
	m := New(func() time.Time { return time.Unix(0, 0) })
	m.Apply(Event{Target: "dev", Pane: "%0", Name: "Stop"})         // →done: Transition=1, Done=1
	m.Apply(Event{Target: "dev", Pane: "%0", Name: "SubagentStop"}) // preserve
	got := m.Snapshot("dev")[0]
	if got.TransitionSeq != 1 || got.DoneSeq != 1 {
		t.Errorf("counters = (%d,%d), want (1,1)", got.TransitionSeq, got.DoneSeq)
	}
}

func TestApplyCapturesEpoch(t *testing.T) {
	m := New(func() time.Time { return time.Unix(0, 0) })
	m.Apply(Event{Target: "dev", Pane: "%0", Name: "SessionStart", Epoch: "8421"})
	if got := m.Snapshot("dev")[0].Epoch; got != "8421" {
		t.Errorf("Epoch = %q, want 8421", got)
	}
}

func TestSnapshotFiltersByTargetAndSorts(t *testing.T) {
	m := New(func() time.Time { return time.Unix(0, 0) })
	m.Apply(Event{Target: "dev", Pane: "%1", Name: "Stop"})
	m.Apply(Event{Target: "dev", Pane: "%0", Name: "Stop"})
	m.Apply(Event{Target: "prod", Pane: "%0", Name: "Stop"})
	dev := m.Snapshot("dev")
	if len(dev) != 2 || dev[0].Pane != "%0" || dev[1].Pane != "%1" {
		t.Fatalf("dev snapshot wrong: %+v", dev)
	}
	if len(m.Snapshot("")) != 3 {
		t.Errorf("empty target should return all panes")
	}
}
