package api

import (
	"net/http"

	"agentmon/hubd/internal/authz"
)

// AuditHandler handles GET /api/v1/audit: authorize AuditRead on audit:*,
// then return the 100 most-recent audit entries as a browser-safe JSON array
// (id, principalId, action, resource, result — no ip/user_agent/meta).
func (d Deps) AuditHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := d.authorizeOr403(w, r, authz.AuditRead, "audit:*"); !ok {
			return
		}
		rows, err := d.AuditRepo.Recent(r.Context(), 100)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "audit read failed")
			return
		}
		out := make([]map[string]string, 0, len(rows))
		for _, e := range rows {
			out = append(out, map[string]string{
				"id":          e.ID,
				"principalId": e.PrincipalID,
				"action":      e.Action,
				"resource":    e.Resource,
				"result":      e.Result,
			})
		}
		writeJSON(w, http.StatusOK, out)
	}
}
