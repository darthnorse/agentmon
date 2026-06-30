// Package registry holds the DB-backed server list and dials agents. db.Server
// (URL + bearer) is hub-side only; List/ServerSummary are the browser-safe
// projections (no secrets). The registry reads the DB live on every lookup, so a
// CLI approve/revoke/rm (a separate process on the shared WAL DB) takes effect on
// the running hub without a restart.
package registry

import (
	"context"
	"database/sql"
	"errors"

	"agentmon/hubd/internal/db"
	"agentmon/shared"
)

type ServerSummary struct {
	ID      string       `json:"id"`
	Name    string       `json:"name"`
	Labels  []string     `json:"labels"`
	Enabled bool         `json:"enabled"`
	State   shared.State `json:"state,omitempty"`
}

type ServerDetail struct {
	ID      string       `json:"id"`
	Name    string       `json:"name"`
	Labels  []string     `json:"labels"`
	Enabled bool         `json:"enabled"`
	Healthy bool         `json:"healthy"`
	State   shared.State `json:"state,omitempty"`
}

// Store is the subset of *db.DB the registry needs. Defined here so the registry
// is unit-testable with a fake.
type Store interface {
	ListServers(ctx context.Context, status string) ([]db.Server, error)
	GetServer(ctx context.Context, id string) (db.Server, error)
	TouchServerLastSeen(ctx context.Context, id string) error
	SetServerStatus(ctx context.Context, id, status string) (bool, error)
	DeleteServer(ctx context.Context, id string) (bool, error)
}

// PendingServer is a browser-safe projection of an agent awaiting admission —
// enough for the operator to verify it (hostname + dial URL + os/arch) before
// admitting, and NO secrets (no bearer / signing key).
type PendingServer struct {
	ID       string `json:"id"`
	Hostname string `json:"hostname"`
	URL      string `json:"url"`
	OS       string `json:"os,omitempty"`
	Arch     string `json:"arch,omitempty"`
}

type Registry struct{ store Store }

func New(store Store) *Registry { return &Registry{store: store} }

// LabelsOrEmpty returns l unchanged if non-nil, or an empty slice to avoid
// marshalling a JSON null for servers with no labels.
func LabelsOrEmpty(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

// List returns browser-safe summaries for ACTIVE servers only.
func (r *Registry) List(ctx context.Context) ([]ServerSummary, error) {
	servers, err := r.store.ListServers(ctx, "active")
	if err != nil {
		return nil, err
	}
	out := make([]ServerSummary, 0, len(servers))
	for _, s := range servers {
		out = append(out, ServerSummary{ID: s.ID, Name: s.Name, Labels: LabelsOrEmpty(s.Labels), Enabled: true})
	}
	return out, nil
}

// Get returns an ACTIVE server by id. (srv,true,nil) when found and active;
// (_,false,nil) when missing or not active; (_,false,err) on a genuine DB error.
func (r *Registry) Get(ctx context.Context, id string) (db.Server, bool, error) {
	s, err := r.store.GetServer(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Server{}, false, nil // no such row → not found, not an error
	}
	if err != nil {
		return db.Server{}, false, err // genuine DB failure → surface as 500
	}
	if s.Status != "active" {
		return db.Server{}, false, nil // pending/revoked → invisible to the API
	}
	return s, true, nil
}

// TouchLastSeen records a successful hub→agent dial. Best-effort.
func (r *Registry) TouchLastSeen(ctx context.Context, id string) error {
	return r.store.TouchServerLastSeen(ctx, id)
}

// ListPending returns agents awaiting admission (status="pending") as browser-safe
// projections for the admit UI.
func (r *Registry) ListPending(ctx context.Context) ([]PendingServer, error) {
	servers, err := r.store.ListServers(ctx, "pending")
	if err != nil {
		return nil, err
	}
	out := make([]PendingServer, 0, len(servers))
	for _, s := range servers {
		out = append(out, PendingServer{ID: s.ID, Hostname: s.Hostname, URL: s.URL, OS: s.OS, Arch: s.Arch})
	}
	return out, nil
}

// Approve admits a PENDING agent (status → active) and returns the affected server
// (for audit). ok=false when there is no such id OR it isn't pending — admitting is
// only ever a pending→active transition, never a way to resurrect a revoked server.
func (r *Registry) Approve(ctx context.Context, id string) (db.Server, bool, error) {
	s, err := r.store.GetServer(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Server{}, false, nil
	}
	if err != nil {
		return db.Server{}, false, err
	}
	if s.Status != "pending" {
		return db.Server{}, false, nil
	}
	ok, err := r.store.SetServerStatus(ctx, id, "active")
	return s, ok, err
}

// Reject removes a PENDING enrollment entirely and returns the affected server (for
// audit). ok=false when there is no such id OR it isn't pending — this never deletes
// an ACTIVE server (that is the more dangerous `server rm`, CLI-only).
func (r *Registry) Reject(ctx context.Context, id string) (db.Server, bool, error) {
	s, err := r.store.GetServer(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Server{}, false, nil
	}
	if err != nil {
		return db.Server{}, false, err
	}
	if s.Status != "pending" {
		return db.Server{}, false, nil
	}
	ok, err := r.store.DeleteServer(ctx, id)
	return s, ok, err
}
