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
			writeJSONError(w, http.StatusServiceUnavailable, "board streaming not configured")
			return
		}
		// Subscribe BEFORE the snapshot (no delta lost in the gap), but query
		// BEFORE setting SSE headers so a failing DB yields a loud 500, not a
		// silent empty 200 the EventSource reconnect-loops against.
		_, ch, cancel := d.BoardBcast.Subscribe()
		defer cancel()
		projects, err := d.DB.ListProjects(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		projDTOs := make([]projectDTO, 0, len(projects))
		var epics []epicDTO
		for _, pr := range projects {
			projDTOs = append(projDTOs, projectOut(pr, nil))
			es, err := d.DB.ListBoardEpics(r.Context(), pr.ID)
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, "internal error")
				return
			}
			for _, e := range es {
				epics = append(epics, toEpicDTO(e))
			}
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
