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

// ServerCreateSessionHandler handles POST /api/v1/servers/{id}/sessions: it
// validates the requested name at the browser boundary, authorizes session.create,
// asks the agent to create the tmux session, then re-lists and returns the full
// Session (with its pane id) so the web can open the new terminal atomically.
// CSRF is enforced upstream by RequireAuth on this mutating method; the agent
// enforces the cwd allow-list + rejects custom commands (mapped here from its 400).
func (d Deps) ServerCreateSessionHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var req shared.CreateSessionRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "bad request")
			return
		}
		// Validate the name at the browser boundary before doing any work; the
		// agent re-validates at the exec boundary (defense in depth).
		if err := shared.ValidateSessionName(req.Name); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		p, ok := d.authorizeOr403(w, r, authz.SessionCreate, "server:"+id)
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
		target := r.URL.Query().Get("target")
		if target == "" {
			// Normalize so the audit resource + re-list key on a concrete label,
			// matching SessionDetailHandler (empty → "default").
			target = "default"
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
		d.Audit.SessionCreate(r.Context(), p.ID, shared.SessionID(id, target, resp.Name), resp.Name,
			authn.ClientIP(r, d.TrustForwardedProto), r.UserAgent())

		sessions, err := d.Agent.Sessions(r.Context(), srv, target)
		if err != nil {
			// The session WAS created and audited; a re-list failure must not be
			// reported as a create failure. Report success with the bare session —
			// the client refetches the list to discover the pane.
			log.Printf("create-session re-list: agent %s: %v", id, err)
			writeJSON(w, http.StatusCreated, shared.Session{Name: resp.Name, Server: id, Target: target})
			return
		}
		_ = d.Reg.TouchLastSeen(r.Context(), id)
		d.overlayState(r.Context(), p.ID, id, sessions)
		for i := range sessions {
			if sessions[i].Name == resp.Name {
				writeJSON(w, http.StatusCreated, sessions[i])
				return
			}
		}
		// Created but gone before the re-list could observe it (rare race). Still
		// report success with the bare session so the client learns the name.
		writeJSON(w, http.StatusCreated, shared.Session{Name: resp.Name, Server: id, Target: target})
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
