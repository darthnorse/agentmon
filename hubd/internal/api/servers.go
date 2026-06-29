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
	"agentmon/hubd/internal/directive"
	"agentmon/hubd/internal/registry"
	"agentmon/hubd/internal/state"
	"agentmon/shared"
)

// AuditReader is the read-side of the audit log. *db.DB satisfies it.
type AuditReader interface {
	Recent(ctx context.Context, limit int) ([]db.AuditEntry, error)
}

// SeenStore is the persistence interface for principal_seen. *db.DB satisfies it.
type SeenStore interface {
	UpsertSeen(ctx context.Context, s db.PrincipalSeen) error
	GetSeen(ctx context.Context, principalID, serverID, target, session string) (db.PrincipalSeen, bool, error)
	ListSeenForPrincipal(ctx context.Context, principalID string) ([]db.PrincipalSeen, error)
	LatestSessionEvent(ctx context.Context, serverID, target, session string) (db.StateEvent, bool, error)
}

// Deps holds the shared dependencies for all API handlers.
type Deps struct {
	Reg                 *registry.Registry
	Agent               *registry.Client
	Audit               *audit.Recorder
	AuditRepo           AuditReader
	HealthTimeout       time.Duration
	TrustForwardedProto bool
	Minter              directive.Minter // M4: mints hub→agent WS access directives
	ExternalOrigin      string           // M4: WS upgrade Origin check
	RelayPongWait       time.Duration    // M4 relay liveness; 0 → default (60s)
	RelayPingPeriod     time.Duration    // M4 relay ping cadence; 0 → default (20s). Must be < RelayPongWait.
	Proj                *state.Projection // M7: in-memory projection for server/session state rollup
	Seen                SeenStore         // M7: principal_seen persistence
}

// authorizeOr403 resolves the principal from the request context, calls
// Authorize, and on deny: audits the denial and writes a 403. Returns the
// principal and true only when the action is allowed.
func (d Deps) authorizeOr403(w http.ResponseWriter, r *http.Request, action authz.Action, resource string) (authz.Principal, bool) {
	p, _ := authn.PrincipalFrom(r.Context())
	dec, err := authz.Authorize(r.Context(), p, action, resource)
	if err != nil || !dec.Allow {
		d.Audit.Deny(r.Context(), p.ID, action, resource, authn.ClientIP(r, d.TrustForwardedProto), r.UserAgent(), "")
		writeJSONError(w, http.StatusForbidden, "forbidden")
		return p, false
	}
	return p, true
}

// serverRollup returns the §9.2 rollup of a server's per-principal
// seen-projected session states from the projection (empty string when the
// projection is nil or has no sessions yet, so json:"state,omitempty" suppresses
// the field rather than emitting a misleading "unknown"). GetSeen errors are
// non-fatal: treated as "no seen row" so the global state passes through.
func (d Deps) serverRollup(ctx context.Context, principalID, serverID string) shared.State {
	if d.Proj == nil {
		return ""
	}
	views := d.Proj.Server(serverID)
	if len(views) == 0 {
		return ""
	}
	states := make([]shared.State, 0, len(views))
	for _, v := range views {
		var (
			ps  db.PrincipalSeen
			has bool
		)
		if d.Seen != nil {
			ps, has, _ = d.Seen.GetSeen(ctx, principalID, serverID, v.Target, v.Session)
		}
		states = append(states, state.SeenProject(v.Global, v.LatestReceivedAt, ps, has))
	}
	return shared.RollUp(states...)
}

// ServersHandler handles GET /api/v1/servers: authorize ServerView on server:*,
// then return the full list of server summaries as JSON.
func (d Deps) ServersHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := d.authorizeOr403(w, r, authz.ServerView, "server:*")
		if !ok {
			return
		}
		list, err := d.Reg.List(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		for i := range list {
			list[i].State = d.serverRollup(r.Context(), p.ID, list[i].ID)
		}
		writeJSON(w, http.StatusOK, list)
	}
}

// ServerHandler handles GET /api/v1/servers/{id}: authorize, look up the server
// (404 if unknown), probe agent health with a bounded timeout, return ServerDetail.
func (d Deps) ServerHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		p, ok := d.authorizeOr403(w, r, authz.ServerView, "server:"+id)
		if !ok {
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
		ctx, cancel := context.WithTimeout(r.Context(), d.HealthTimeout)
		defer cancel()
		// serverRollup's seen lookups use r.Context() (SQLite reads), not the
		// agent health-check timeout ctx — otherwise a slow Health() probe could
		// exhaust the deadline and silently drop the seen projection.
		writeJSON(w, http.StatusOK, registry.ServerDetail{
			ID:      srv.ID,
			Name:    srv.Name,
			Labels:  registry.LabelsOrEmpty(srv.Labels),
			Enabled: true,
			Healthy: d.Agent.Health(ctx, srv),
			State:   d.serverRollup(r.Context(), p.ID, srv.ID),
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
