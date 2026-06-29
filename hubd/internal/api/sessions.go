package api

import (
	"log"
	"net/http"

	"agentmon/hubd/internal/authz"
	"agentmon/shared"
)

// overlayState replaces each session's State with the hub projection's global
// state when known; otherwise keeps the agent's inline state (pre-poll fallback).
// (B3 extends this with the per-principal seen projection.)
func (d Deps) overlayState(serverID, target string, sessions []shared.Session) {
	if d.Proj == nil {
		return
	}
	for i := range sessions {
		if v, ok := d.Proj.Session(serverID, target, sessions[i].Name); ok {
			sessions[i].State = v.Global
		}
	}
}

func (d Deps) ServerSessionsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, ok := d.authorizeOr403(w, r, authz.SessionView, "server:"+id); !ok {
			return
		}
		srv, ok, err := d.Reg.Get(r.Context(), id)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if !ok {
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
		d.overlayState(id, "", sessions)
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
		if _, ok := d.authorizeOr403(w, r, authz.SessionView, shared.SessionID(id, target, name)); !ok {
			return
		}
		srv, ok, err := d.Reg.Get(r.Context(), id)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if !ok {
			writeJSONError(w, http.StatusNotFound, "unknown server")
			return
		}
		sessions, err := d.Agent.Sessions(r.Context(), srv, target)
		if err != nil {
			log.Printf("sessions: agent %s: %v", id, err)
			writeJSONError(w, http.StatusBadGateway, "agent unavailable")
			return
		}
		d.overlayState(id, target, sessions)
		for _, s := range sessions {
			if s.Name == name {
				writeJSON(w, http.StatusOK, s)
				return
			}
		}
		writeJSONError(w, http.StatusNotFound, "unknown session")
	}
}
