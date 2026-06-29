package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"agentmon/hubd/internal/authz"
	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/state"
	"agentmon/shared"
)

// stateEvent is the JSON payload for both snapshot entries and delta events.
type stateEvent struct {
	Server  string       `json:"server"`
	Target  string       `json:"target"`
	Session string       `json:"session"`
	State   shared.State `json:"state"`
}

// sseKey uniquely identifies a session for the per-principal seen map.
type sseKey struct{ server, target, session string }

// EventsHandler handles GET /api/v1/events: authenticates the principal (via
// RequireAuth middleware), sends an initial event:snapshot of all projection
// sessions (seen-projected for this principal), then fans out broadcaster
// Changes as event:state deltas. A ": ping\n\n" comment is sent on each
// heartbeat tick to keep the connection alive through proxies.
//
// SSE is a GET and relies on RequireAuth (cookie); no CSRF check is needed.
//
// Limitation (M7): the seen map is captured once at connect time. A POST /seen
// during an active stream is not reflected until the client reconnects.
func (d Deps) EventsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := d.authorizeOr403(w, r, authz.ServerView, "server:*")
		if !ok {
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			writeJSONError(w, http.StatusInternalServerError, "stream unsupported")
			return
		}

		// FIX 2: guard nil Bcast before any subscription attempt.
		if d.Bcast == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "state streaming not configured")
			return
		}

		// FIX 1: subscribe BEFORE reading the projection snapshot so that any
		// Publish in the window between snapshot-send and select entry is buffered
		// in the channel rather than lost.  A delta that duplicates a snapshotted
		// entry is harmless (idempotent current-state); a missed change is not.
		_, ch, cancel := d.Bcast.Subscribe()
		defer cancel()

		// M9: mark this principal online for the lifetime of the stream so the
		// push dispatcher suppresses redundant Web-Push (Tier 3) while in-app
		// alerts (Tier 1/2) are live. The defer fires on any stream exit
		// (context-done, error, or return). Nil-guarded: Deps without a
		// Presence (existing tests, push-disabled builds) are unaffected.
		if d.Presence != nil {
			d.Presence.Add(p.ID)
			defer d.Presence.Remove(p.ID)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		// Build per-principal seen map (connect-time snapshot).
		seen := map[sseKey]db.PrincipalSeen{}
		if rows, err := d.Seen.ListSeenForPrincipal(r.Context(), p.ID); err == nil {
			for _, s := range rows {
				seen[sseKey{s.ServerID, s.TargetID, s.Session}] = s
			}
		}
		project := func(v state.SessionView) shared.State {
			s, has := seen[sseKey{v.ServerID, v.Target, v.Session}]
			return state.SeenProject(v.Global, v.LatestReceivedAt, s, has)
		}

		// Send initial snapshot.
		snap := []stateEvent{}
		for _, v := range d.Proj.All() {
			snap = append(snap, stateEvent{v.ServerID, v.Target, v.Session, project(v)})
		}
		writeSSE(w, "snapshot", snap)
		flusher.Flush()

		hb := time.NewTicker(d.SSEHeartbeat)
		defer hb.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case c := <-ch:
				s, has := seen[sseKey{c.ServerID, c.Target, c.Session}]
				writeSSE(w, "state", stateEvent{c.ServerID, c.Target, c.Session,
					state.SeenProject(c.Global, c.LatestReceivedAt, s, has)})
				flusher.Flush()
			case <-hb.C:
				fmt.Fprint(w, ": ping\n\n")
				flusher.Flush()
			}
		}
	}
}

// writeSSE writes a single SSE event in the standard wire format:
//
//	event: <type>\ndata: <json>\n\n
func writeSSE(w http.ResponseWriter, event string, data any) {
	b, _ := json.Marshal(data)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
}
