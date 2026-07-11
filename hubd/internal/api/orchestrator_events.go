package api

import (
	"fmt"
	"net/http"
	"time"

	"agentmon/hubd/internal/authz"
)

func (d Deps) OrchestratorEventsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := d.authorizeOr403(w, r, authz.OrchestratorView, "orchestrator:*"); !ok {
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeJSONError(w, 500, "stream unsupported")
			return
		}
		if d.BoardBcast == nil {
			writeJSONError(w, 503, "board streaming not configured")
			return
		}
		_, ch, cancel := d.BoardBcast.Subscribe()
		defer cancel()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		projects, err := d.DB.ListProjects(r.Context())
		if err != nil {
			return
		}
		var epics []epicDTO
		for _, p := range projects {
			es, _ := d.DB.ListEpicsByProject(r.Context(), p.ID)
			for _, e := range es {
				epics = append(epics, toEpicDTO(e))
			}
		}
		writeSSE(w, "board-snapshot", map[string]any{"projects": projects, "epics": epics})
		flusher.Flush()
		hb := d.SSEHeartbeat
		if hb == 0 {
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
