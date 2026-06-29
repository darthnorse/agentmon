package state

import (
	"agentmon/hubd/internal/db"
	"agentmon/shared"
)

// SeenProject masks done→idle for a principal who has focused the session at or
// after its latest finish. Only done is maskable; blocked/working/idle/unknown
// pass through. Comparison is hub-clock string compare (single-clock invariant).
func SeenProject(global shared.State, latestReceivedAt string, seen db.PrincipalSeen, ok bool) shared.State {
	if global != shared.StateDone || !ok {
		return global
	}
	if seen.LastFocusedAt >= latestReceivedAt {
		return shared.StateIdle
	}
	return global
}
