package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"agentmon/hubd/internal/authn"
	"agentmon/hubd/internal/authz"
	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/registry"
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
		// Forward the RAW target (empty → the agent resolves its first target).
		// The board lists a project bound to a non-default tmux target via
		// ?target=; without threading it here a multi-target host would always
		// return the default socket and show the live runner as "session ended".
		target := r.URL.Query().Get("target")
		sessions, err := d.Agent.Sessions(r.Context(), srv, target)
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

// maxCreateSessionBody caps the create-session request body — a tiny JSON object
// ({name, cwd?, command?}); aligned with the agent's maxCreateBody.
const maxCreateSessionBody = 8 << 10 // 8 KiB

// ServerCreateSessionHandler handles POST /api/v1/servers/{id}/sessions: it
// validates the requested name at the browser boundary, authorizes session.create,
// asks the agent to create the tmux session, then re-lists and returns the full
// Session (with its pane id) so the web can open the new terminal atomically.
// CSRF is enforced upstream by RequireAuth on this mutating method; the agent
// enforces the cwd allow-list and executes an optional command (design doc D13:
// no new authz permission — session-create + send-keys already grant arbitrary
// exec on the target).
func (d Deps) ServerCreateSessionHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		// Authorize FIRST — before reading/validating the body — so the decision is
		// recorded (the deny path audits) ahead of any input handling, per §13.5.
		p, ok := d.authorizeOr403(w, r, authz.SessionCreate, "server:"+id)
		if !ok {
			return
		}
		var req shared.CreateSessionRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCreateSessionBody)).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "bad request")
			return
		}
		// Validate the name at the browser boundary; the agent re-validates at the
		// exec boundary (defense in depth).
		if err := shared.ValidateSessionName(req.Name); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
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
		// Forward the RAW target to the agent (empty → the agent resolves its first
		// target), exactly like ServerSessionsHandler — do NOT substitute "default"
		// for the agent calls, or an agent whose sole target is labeled non-"default"
		// would 404 on create while listing succeeds. Use a concrete label only for
		// the audit resource + the bare-session fallback.
		target := r.URL.Query().Get("target")
		auditTarget := target
		if auditTarget == "" {
			auditTarget = "default"
		}
		resp, err := d.Agent.CreateSession(r.Context(), srv, target, req)
		if err != nil {
			switch {
			case errors.Is(err, registry.ErrSessionExists):
				writeJSONError(w, http.StatusConflict, "session already exists")
			case errors.Is(err, registry.ErrInvalidSession):
				writeJSONError(w, http.StatusBadRequest, "invalid session request")
			default:
				log.Printf("create-session: agent %s: %v", id, err)
				writeJSONError(w, http.StatusBadGateway, "agent unavailable")
			}
			return
		}
		// Audit as soon as the agent confirms the create, so a created session is
		// always recorded even if the re-list below fails.
		d.Audit.SessionCreate(r.Context(), p.ID, shared.SessionID(id, auditTarget, resp.Name), resp.Name,
			authn.ClientIP(r, d.TrustForwardedProto), r.UserAgent())

		// Re-list and return the created session (state overlaid); a re-list failure
		// or a race where it vanished still reports success — the create was audited.
		d.writeReListedSession(w, r, p.ID, srv, id, target, auditTarget, resp.Name, "create-session")
	}
}

// writeReListedSession re-lists the server's sessions after a create/rename and
// writes the named session (with overlaid per-principal state) as 201. A re-list
// failure, or a session that vanished before the re-list could observe it, still
// reports 201 with a bare session — the mutation was already performed and audited,
// so it must never be surfaced as a failure. op labels the re-list log line.
func (d Deps) writeReListedSession(w http.ResponseWriter, r *http.Request, principalID string, srv db.Server, id, target, auditTarget, name, op string) {
	bare := shared.Session{Name: name, Server: id, Target: auditTarget}
	sessions, err := d.Agent.Sessions(r.Context(), srv, target)
	if err != nil {
		log.Printf("%s re-list: agent %s: %v", op, id, err)
		writeJSON(w, http.StatusCreated, bare)
		return
	}
	_ = d.Reg.TouchLastSeen(r.Context(), id)
	d.overlayState(r.Context(), principalID, id, sessions)
	for i := range sessions {
		if sessions[i].Name == name {
			writeJSON(w, http.StatusCreated, sessions[i])
			return
		}
	}
	writeJSON(w, http.StatusCreated, bare)
}

// ServerRenameSessionHandler handles POST /api/v1/servers/{id}/sessions/rename:
// authorizes session.rename, validates the new name, asks the agent to rename the
// tmux session, then re-lists and returns the renamed Session. CSRF is enforced by
// RequireAuth. Maps the agent's 409 (duplicate) / 404 (no such source) / 400.
func (d Deps) ServerRenameSessionHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		p, ok := d.authorizeOr403(w, r, authz.SessionRename, "server:"+id)
		if !ok {
			return
		}
		var req shared.RenameSessionRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCreateSessionBody)).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "bad request")
			return
		}
		if req.From == "" {
			writeJSONError(w, http.StatusBadRequest, "from is required")
			return
		}
		// Validate the new name at the browser boundary; the agent re-validates.
		if err := shared.ValidateSessionName(req.To); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
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
		target := r.URL.Query().Get("target") // raw → agent resolves; "default" only for audit
		auditTarget := target
		if auditTarget == "" {
			auditTarget = "default"
		}
		if err := d.Agent.RenameSession(r.Context(), srv, target, req.From, req.To); err != nil {
			switch {
			case errors.Is(err, registry.ErrSessionExists):
				writeJSONError(w, http.StatusConflict, "a session with that name already exists")
			case errors.Is(err, registry.ErrNoSession):
				writeJSONError(w, http.StatusNotFound, "no such session")
			case errors.Is(err, registry.ErrInvalidSession):
				writeJSONError(w, http.StatusBadRequest, "invalid session request")
			default:
				log.Printf("rename-session: agent %s: %v", id, err)
				writeJSONError(w, http.StatusBadGateway, "agent unavailable")
			}
			return
		}
		d.Audit.SessionRename(r.Context(), p.ID, shared.SessionID(id, auditTarget, req.To), req.From, req.To,
			authn.ClientIP(r, d.TrustForwardedProto), r.UserAgent())

		d.writeReListedSession(w, r, p.ID, srv, id, target, auditTarget, req.To, "rename-session")
	}
}

// ServerKillSessionHandler handles POST /api/v1/servers/{id}/sessions/kill:
// authorizes session.kill, terminates the tmux session via the agent, audits, and
// returns 200 {"name": ...}. CSRF is enforced by RequireAuth. Maps the agent's
// 404 (no such session) / 400.
func (d Deps) ServerKillSessionHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		p, ok := d.authorizeOr403(w, r, authz.SessionKill, "server:"+id)
		if !ok {
			return
		}
		var req shared.KillSessionRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCreateSessionBody)).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "bad request")
			return
		}
		if req.Name == "" {
			writeJSONError(w, http.StatusBadRequest, "name is required")
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
		target := r.URL.Query().Get("target")
		auditTarget := target
		if auditTarget == "" {
			auditTarget = "default"
		}
		if _, _, err := d.Agent.KillSession(r.Context(), srv, target, req.Name); err != nil {
			switch {
			case errors.Is(err, registry.ErrNoSession):
				writeJSONError(w, http.StatusNotFound, "no such session")
			case errors.Is(err, registry.ErrInvalidSession):
				writeJSONError(w, http.StatusBadRequest, "invalid session request")
			default:
				log.Printf("kill-session: agent %s: %v", id, err)
				writeJSONError(w, http.StatusBadGateway, "agent unavailable")
			}
			return
		}
		d.Audit.SessionKill(r.Context(), p.ID, shared.SessionID(id, auditTarget, req.Name), req.Name,
			authn.ClientIP(r, d.TrustForwardedProto), r.UserAgent())
		writeJSON(w, http.StatusOK, map[string]string{"name": req.Name})
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
