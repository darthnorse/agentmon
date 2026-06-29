package api

import (
	"context"
	"log"
	"net/http"

	"agentmon/hubd/internal/authz"
	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/state"
	"agentmon/shared"
)

// overlayState replaces each session's State with the per-principal seen-projected
// global state when the projection has an entry; otherwise keeps the agent's inline
// state (pre-poll fallback). GetSeen errors are non-fatal: treated as "no seen row"
// so the global state passes through unchanged.
func (d Deps) overlayState(ctx context.Context, principalID, serverID string, sessions []shared.Session) {
	if d.Proj == nil {
		return
	}
	for i := range sessions {
		v, ok := d.Proj.Session(serverID, sessions[i].Target, sessions[i].Name)
		if !ok {
			continue // pre-poll fallback: keep agent inline state
		}
		var (
			ps  db.PrincipalSeen
			has bool
		)
		if d.Seen != nil {
			ps, has, _ = d.Seen.GetSeen(ctx, principalID, serverID, sessions[i].Target, sessions[i].Name)
		}
		sessions[i].State = state.SeenProject(v.Global, v.LatestReceivedAt, ps, has)
	}
}

func (d Deps) ServerSessionsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		p, ok := d.authorizeOr403(w, r, authz.SessionView, "server:"+id)
		if !ok {
			return
		}
		srv, found, err := d.Reg.Get(r.Context(), id)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if !found {
			writeJSONError(w, http.StatusNotFound, "unknown server")
			return
		}
		sessions, err := d.Agent.Sessions(r.Context(), srv, "")
		if err != nil {
			log.Printf("sessions: agent %s: %v", id, err)
			writeJSONError(w, http.StatusBadGateway, "agent unavailable")
			return
		}
		_ = d.Reg.TouchLastSeen(r.Context(), id)
		d.overlayState(r.Context(), p.ID, id, sessions)
		writeJSON(w, http.StatusOK, sessions)
	}
}

func (d Deps) SessionDetailHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		name := r.PathValue("name")
		target := r.URL.Query().Get("target")
		if target == "" {
			target = "default"
		}
		p, ok := d.authorizeOr403(w, r, authz.SessionView, shared.SessionID(id, target, name))
		if !ok {
			return
		}
		srv, found, err := d.Reg.Get(r.Context(), id)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if !found {
			writeJSONError(w, http.StatusNotFound, "unknown server")
			return
		}
		sessions, err := d.Agent.Sessions(r.Context(), srv, target)
		if err != nil {
			log.Printf("sessions: agent %s: %v", id, err)
			writeJSONError(w, http.StatusBadGateway, "agent unavailable")
			return
		}
		d.overlayState(r.Context(), p.ID, id, sessions)
		for _, s := range sessions {
			if s.Name == name {
				writeJSON(w, http.StatusOK, s)
				return
			}
		}
		writeJSONError(w, http.StatusNotFound, "unknown session")
	}
}
