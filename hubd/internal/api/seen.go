package api

import (
	"encoding/json"
	"net/http"
	"time"

	"agentmon/hubd/internal/authz"
	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/state"
	"agentmon/shared"
)

type seenRequest struct {
	ServerID    string `json:"serverId"`
	Target      string `json:"target"`
	SessionName string `json:"sessionName"`
}

// SeenHandler handles POST /api/v1/seen: records that the authenticated
// principal has focused the given session, anchoring to the latest event.
func (d Deps) SeenHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req seenRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "bad request")
			return
		}
		if req.ServerID == "" || req.SessionName == "" {
			writeJSONError(w, http.StatusBadRequest, "serverId and sessionName required")
			return
		}
		p, ok := d.authorizeOr403(w, r, authz.SessionView, shared.SessionID(req.ServerID, req.Target, req.SessionName))
		if !ok {
			return
		}
		latestID := ""
		if ev, found, err := d.Seen.LatestSessionEvent(r.Context(), req.ServerID, req.Target, req.SessionName); err == nil && found {
			latestID = ev.ID
		}
		if err := d.Seen.UpsertSeen(r.Context(), db.PrincipalSeen{
			PrincipalID:     p.ID,
			ServerID:        req.ServerID,
			TargetID:        req.Target,
			Session:         req.SessionName,
			LastSeenEventID: latestID,
			LastFocusedAt:   state.HubTS(time.Now()),
		}); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
