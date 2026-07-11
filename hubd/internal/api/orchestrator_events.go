package api

import (
	"fmt"
	"net/http"
	"time"

	"agentmon/hubd/internal/authz"
)

func (d Deps) OrchestratorEventsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := d.authorizeOr403(w, r, authz.OrchestratorView, "orchestrator:*")
		if !ok {
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeJSONError(w, http.StatusInternalServerError, "stream unsupported")
			return
		}
		if d.BoardBcast == nil {
			// Dormant hub (no GitHub token): keep the stream OPEN and idle rather
			// than 503, so the app-wide EventSource doesn't reconnect-loop. No
			// deltas ever arrive (nothing publishes), but projects can still live
			// in the DB (registered while enabled, then restarted dormant), so
			// serve the REAL snapshot — GET /board does, and boardSnapshot's
			// invariant is that the two must never drift.
			projDTOs, epics, err := d.boardSnapshot(r.Context())
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, "internal error")
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			writeSSE(w, "board-snapshot", map[string]any{"projects": projDTOs, "epics": epics})
			flusher.Flush()
			hb := d.SSEHeartbeat
			if hb <= 0 {
				hb = 25 * time.Second
			}
			t := time.NewTicker(hb)
			defer t.Stop()
			for {
				select {
				case <-r.Context().Done():
					return
				case <-t.C:
					fmt.Fprint(w, ": ping\n\n")
					flusher.Flush()
				}
			}
		}
		// Subscribe BEFORE the snapshot (no delta lost in the gap), but query
		// BEFORE setting SSE headers so a failing DB yields a loud 500, not a
		// silent empty 200 the EventSource reconnect-loops against.
		_, ch, cancel := d.BoardBcast.Subscribe()
		defer cancel()
		projDTOs, epics, err := d.boardSnapshot(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		// Board viewers are online viewers: suppress redundant web-push for
		// escalations they are already watching (mirrors events.go).
		if d.Presence != nil {
			d.Presence.Add(p.ID)
			defer d.Presence.Remove(p.ID)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		writeSSE(w, "board-snapshot", map[string]any{"projects": projDTOs, "epics": epics})
		flusher.Flush()
		hb := d.SSEHeartbeat
		if hb <= 0 {
			hb = 25 * time.Second
		}
		t := time.NewTicker(hb)
		defer t.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case c, ok := <-ch:
				if !ok {
					return
				}
				writeSSE(w, "board", map[string]any{"project_id": c.ProjectID, "epic_id": c.EpicID, "issue": c.Issue, "stage": c.Stage, "needs": c.Needs, "title": c.Title})
				flusher.Flush()
			case <-t.C:
				fmt.Fprint(w, ": ping\n\n")
				flusher.Flush()
			}
		}
	}
}
