package state

import (
	"agentmon/hubd/internal/db"
	"agentmon/shared"
)

// SeenProject masks done→idle for a principal who has focused the session at or
// after its latest finish. Only done is maskable; blocked/working/idle/unknown
// pass through. Comparison is hub-clock string compare (single-clock invariant).
//
// When latestReceivedAt is empty (no anchor yet) the comparison is undefined —
// any non-empty LastFocusedAt would compare as ≥ "", wrongly masking done as
// idle.  Guard: an empty anchor means we cannot establish ordering, so done
// must pass through unchanged.
func SeenProject(global shared.State, latestReceivedAt string, seen db.PrincipalSeen, ok bool) shared.State {
	if global != shared.StateDone || !ok || latestReceivedAt == "" {
		return global
	}
	if seen.LastFocusedAt >= latestReceivedAt {
		return shared.StateIdle
	}
	return global
}
