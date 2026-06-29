package state

import (
	"testing"

	"agentmon/hubd/internal/db"
	"agentmon/shared"
)

func TestSeenProject(t *testing.T) {
	doneAt := "2026-06-29 10:00:05.000"
	cases := []struct {
		name   string
		global shared.State
		latest string
		seen   db.PrincipalSeen
		ok     bool
		want   shared.State
	}{
		{"done unseen (no record)", shared.StateDone, doneAt, db.PrincipalSeen{}, false, shared.StateDone},
		{"done focused before finish", shared.StateDone, doneAt, db.PrincipalSeen{LastFocusedAt: "2026-06-29 10:00:01.000"}, true, shared.StateDone},
		{"done focused after finish", shared.StateDone, doneAt, db.PrincipalSeen{LastFocusedAt: "2026-06-29 10:00:09.000"}, true, shared.StateIdle},
		{"done focused exactly at finish", shared.StateDone, doneAt, db.PrincipalSeen{LastFocusedAt: doneAt}, true, shared.StateIdle},
		{"blocked never masked", shared.StateBlocked, doneAt, db.PrincipalSeen{LastFocusedAt: "2026-06-29 23:00:00.000"}, true, shared.StateBlocked},
		{"working passes through", shared.StateWorking, doneAt, db.PrincipalSeen{LastFocusedAt: "2026-06-29 23:00:00.000"}, true, shared.StateWorking},
		// FIX 3: empty anchor (latestReceivedAt=="") must not mask done→idle;
		// any non-empty LastFocusedAt >= "" is always true, so without the guard
		// a done session with no anchor would be wrongly reported as idle.
		{"done empty anchor with seen record", shared.StateDone, "", db.PrincipalSeen{LastFocusedAt: "2026-06-29 10:00:00.000"}, true, shared.StateDone},
	}
	for _, c := range cases {
		if got := SeenProject(c.global, c.latest, c.seen, c.ok); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}
