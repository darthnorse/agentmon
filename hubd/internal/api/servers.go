package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"agentmon/hubd/internal/audit"
	"agentmon/hubd/internal/authn"
	"agentmon/hubd/internal/authz"
	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/registry"
)

// AuditReader is the read-side of the audit log. *db.DB satisfies it.
type AuditReader interface {
	Recent(ctx context.Context, limit int) ([]db.AuditEntry, error)
}

// Deps holds the shared dependencies for all API handlers.
type Deps struct {
	Reg           *registry.Registry
	Agent         *registry.Client
	Audit         *audit.Recorder
	AuditRepo     AuditReader
	HealthTimeout time.Duration
}

// authorizeOr403 resolves the principal from the request context, calls
// Authorize, and on deny: audits the denial and writes a 403. Returns the
// principal and true only when the action is allowed.
func (d Deps) authorizeOr403(w http.ResponseWriter, r *http.Request, action authz.Action, resource string) (authz.Principal, bool) {
	p, _ := authn.PrincipalFrom(r.Context())
	dec, err := authz.Authorize(r.Context(), p, action, resource)
	if err != nil || !dec.Allow {
		d.Audit.Deny(r.Context(), p.ID, action, resource, clientIP(r), r.UserAgent(), "")
		writeJSONError(w, http.StatusForbidden, "forbidden")
		return p, false
	}
	return p, true
}

// ServersHandler handles GET /api/v1/servers: authorize ServerView on server:*,
// then return the full list of server summaries as JSON.
func (d Deps) ServersHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := d.authorizeOr403(w, r, authz.ServerView, "server:*"); !ok {
			return
		}
		writeJSON(w, http.StatusOK, d.Reg.List())
	}
}

// ServerHandler handles GET /api/v1/servers/{id}: authorize, look up the server
// (404 if unknown), probe agent health with a bounded timeout, return ServerDetail.
func (d Deps) ServerHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, ok := d.authorizeOr403(w, r, authz.ServerView, "server:"+id); !ok {
			return
		}
		srv, ok := d.Reg.Get(id)
		if !ok {
			writeJSONError(w, http.StatusNotFound, "unknown server")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), d.HealthTimeout)
		defer cancel()
		writeJSON(w, http.StatusOK, registry.ServerDetail{
			ID:      srv.ID,
			Name:    srv.Name,
			Labels:  labelsOrEmpty(srv.Labels),
			Enabled: true,
			Healthy: d.Agent.Health(ctx, srv),
		})
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	return r.RemoteAddr
}

func labelsOrEmpty(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}
