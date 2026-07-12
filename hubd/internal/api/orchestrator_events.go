package api

import (
	"fmt"
	"net/http"
	"time"

	"agentmon/hubd/internal/authz"
	"agentmon/hubd/internal/orchestrator"
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
		// Subscribe BEFORE the snapshot (no delta lost in the gap) when live. A
		// dormant hub (no GitHub token) has no broadcaster: ch stays nil, so the
		// delta arm of the select below never fires and the stream stays open+idle
		// rather than 503 — the app-wide EventSource must not reconnect-loop. The
		// dormant hub still serves the REAL snapshot: projects can live in the DB
		// (registered while enabled, then restarted dormant), and boardSnapshot's
		// invariant is that this payload never drifts from GET /board.
		var ch <-chan orchestrator.BoardChange
		if d.BoardBcast != nil {
			_, sub, cancel := d.BoardBcast.Subscribe()
			ch = sub
			defer cancel()
		}
		// Query BEFORE setting SSE headers so a failing DB yields a loud 500, not a
		// silent empty 200 the EventSource reconnect-loops against.
		projDTOs, epics, err := d.boardSnapshot(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		// Board viewers are online viewers: suppress redundant web-push for
		// escalations they're already watching (mirrors events.go). Live only — a
		// dormant hub never pushes.
		if d.BoardBcast != nil && d.Presence != nil {
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
			case c, ok := <-ch: // nil ch (dormant) never fires
				if !ok {
					return // live broadcaster closed
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
