package api

import (
	"net/http"

	"agentmon/hubd/internal/authz"
	"agentmon/shared"
)

func (d Deps) ServerSessionsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, ok := d.authorizeOr403(w, r, authz.SessionView, "server:"+id); !ok {
			return
		}
		srv, ok := d.Reg.Get(id)
		if !ok {
			writeJSONError(w, http.StatusNotFound, "unknown server")
			return
		}
		sessions, err := d.Agent.Sessions(r.Context(), srv, "")
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, "agent unavailable")
			return
		}
		writeJSON(w, http.StatusOK, sessions)
	}
}

func (d Deps) SessionDetailHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		name := r.PathValue("name")
		if _, ok := d.authorizeOr403(w, r, authz.SessionView, shared.SessionID(id, "default", name)); !ok {
			return
		}
		srv, ok := d.Reg.Get(id)
		if !ok {
			writeJSONError(w, http.StatusNotFound, "unknown server")
			return
		}
		sessions, err := d.Agent.Sessions(r.Context(), srv, "")
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, "agent unavailable")
			return
		}
		for _, s := range sessions {
			if s.Name == name {
				writeJSON(w, http.StatusOK, s)
				return
			}
		}
		writeJSONError(w, http.StatusNotFound, "unknown session")
	}
}
