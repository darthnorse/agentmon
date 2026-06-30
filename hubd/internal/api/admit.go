package api

import (
	"net/http"

	"agentmon/hubd/internal/authz"
)

// PendingServersHandler handles GET /api/v1/servers/pending: authorize ServerAdmit,
// then return the agents awaiting admission as browser-safe projections (no secrets).
func (d Deps) PendingServersHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := d.authorizeOr403(w, r, authz.ServerAdmit, "server:*"); !ok {
			return
		}
		list, err := d.Reg.ListPending(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, list)
	}
}

// ServerApproveHandler handles POST /api/v1/servers/{id}/approve: authorize
// ServerAdmit, admit the PENDING agent (→ active), and audit. CSRF is enforced by
// RequireAuth on this mutating method. 404 when there is no pending agent with that id.
func (d Deps) ServerApproveHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, ok := d.authorizeOr403(w, r, authz.ServerAdmit, "server:"+id); !ok {
			return
		}
		srv, ok, err := d.Reg.Approve(r.Context(), id)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if !ok {
			writeJSONError(w, http.StatusNotFound, "no pending agent with that id")
			return
		}
		d.Audit.ServerApprove(r.Context(), id, srv.Hostname)
		w.WriteHeader(http.StatusNoContent)
	}
}

// ServerRejectHandler handles POST /api/v1/servers/{id}/reject: authorize
// ServerAdmit, remove the PENDING enrollment, and audit. It never deletes an ACTIVE
// server (that is the CLI-only `server rm`). 404 when there is no pending agent.
func (d Deps) ServerRejectHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, ok := d.authorizeOr403(w, r, authz.ServerAdmit, "server:"+id); !ok {
			return
		}
		srv, ok, err := d.Reg.Reject(r.Context(), id)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if !ok {
			writeJSONError(w, http.StatusNotFound, "no pending agent with that id")
			return
		}
		d.Audit.ServerRemove(r.Context(), id, srv.Hostname)
		w.WriteHeader(http.StatusNoContent)
	}
}
