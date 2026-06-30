package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agentmon/hubd/internal/audit"
	"agentmon/hubd/internal/authn"
	"agentmon/hubd/internal/authz"
	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/registry"
	"agentmon/hubd/internal/state"
	"agentmon/shared"
)

type nopSink struct{}

func (nopSink) Append(_ context.Context, _ db.AuditEntry) error { return nil }

// withPrincipal injects a principal as RequireAuth would, for direct handler tests.
func withPrincipal(r *http.Request, p authz.Principal) *http.Request {
	return r.WithContext(authn.ContextWithPrincipal(r.Context(), p))
}

// fakeSeenStore is an in-memory SeenStore for unit tests. When getErr is set,
// GetSeen returns it (with no row) to exercise the non-fatal error path.
type fakeSeenStore struct {
	rows   map[string]db.PrincipalSeen
	getErr error
}

func seenKey(principalID, serverID, target, session string) string {
	return principalID + "|" + serverID + "|" + target + "|" + session
}

func (f *fakeSeenStore) UpsertSeen(_ context.Context, s db.PrincipalSeen) error {
	if f.rows == nil {
		f.rows = map[string]db.PrincipalSeen{}
	}
	f.rows[seenKey(s.PrincipalID, s.ServerID, s.TargetID, s.Session)] = s
	return nil
}

func (f *fakeSeenStore) GetSeen(_ context.Context, principalID, serverID, target, session string) (db.PrincipalSeen, bool, error) {
	if f.getErr != nil {
		return db.PrincipalSeen{}, false, f.getErr
	}
	if f.rows == nil {
		return db.PrincipalSeen{}, false, nil
	}
	s, ok := f.rows[seenKey(principalID, serverID, target, session)]
	return s, ok, nil
}

func (f *fakeSeenStore) ListSeenForPrincipal(_ context.Context, principalID string) ([]db.PrincipalSeen, error) {
	var out []db.PrincipalSeen
	for _, s := range f.rows {
		if s.PrincipalID == principalID {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeSeenStore) LatestSessionEvent(_ context.Context, _, _, _ string) (db.StateEvent, bool, error) {
	return db.StateEvent{}, false, nil
}

func testDeps(reg *registry.Registry) Deps {
	return Deps{Reg: reg, Agent: registry.NewClient(time.Second),
		Audit: audit.NewRecorder(nopSink{}), HealthTimeout: time.Second}
}

type fakeStore struct{ servers map[string]db.Server }

func (f fakeStore) ListServers(_ context.Context, status string) ([]db.Server, error) {
	var out []db.Server
	for _, s := range f.servers {
		if status == "" || s.Status == status {
			out = append(out, s)
		}
	}
	return out, nil
}
func (f fakeStore) GetServer(_ context.Context, id string) (db.Server, error) {
	if s, ok := f.servers[id]; ok {
		return s, nil
	}
	return db.Server{}, sql.ErrNoRows
}
func (f fakeStore) TouchServerLastSeen(_ context.Context, _ string) error { return nil }
func (f fakeStore) ApproveIfPending(_ context.Context, id string) (bool, error) {
	s, ok := f.servers[id]
	if !ok || s.Status != "pending" {
		return false, nil
	}
	s.Status = "active"
	f.servers[id] = s // shared map → mutation persists despite the value receiver
	return true, nil
}
func (f fakeStore) RejectIfPending(_ context.Context, id string) (bool, error) {
	s, ok := f.servers[id]
	if !ok || s.Status != "pending" {
		return false, nil
	}
	delete(f.servers, id)
	return true, nil
}

func TestServersHandlerListsForAuthedPrincipal(t *testing.T) {
	reg := registry.New(fakeStore{servers: map[string]db.Server{"server-a": {ID: "server-a", Name: "A", Status: "active", URL: "http://x"}}})
	d := testDeps(reg)
	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/servers", nil), authz.Principal{ID: "u1"})
	w := httptest.NewRecorder()
	d.ServersHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("code %d", w.Code)
	}
	var got []registry.ServerSummary
	json.NewDecoder(w.Body).Decode(&got)
	if len(got) != 1 || got[0].ID != "server-a" {
		t.Fatalf("got %+v", got)
	}
}

func TestServersHandlerDeniesEmptyPrincipal(t *testing.T) {
	d := testDeps(registry.New(fakeStore{}))
	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/servers", nil), authz.Principal{})
	w := httptest.NewRecorder()
	d.ServersHandler()(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("code %d", w.Code)
	}
}

func TestServerHandlerUnknownIDIs404(t *testing.T) {
	d := testDeps(registry.New(fakeStore{}))
	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/servers/nope", nil), authz.Principal{ID: "u1"})
	r.SetPathValue("id", "nope")
	w := httptest.NewRecorder()
	d.ServerHandler()(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("code %d", w.Code)
	}
}

// TestServerRollupEmptyNonNilProjectionNoUnknown: when the projection is non-nil
// but has no sessions for the server yet (normal at hub start, before first poll),
// serverRollup must return "" so the json:"state,omitempty" tag suppresses the field
// entirely — not "unknown".
func TestServerRollupEmptyNonNilProjectionNoUnknown(t *testing.T) {
	d := testDeps(registry.New(fakeStore{}))
	d.Proj = state.NewProjection() // non-nil but empty

	got := d.serverRollup(context.Background(), "", "any-server")
	if got != "" {
		t.Fatalf("empty projection must return \"\", got %q (json would emit \"state\":%q)", got, got)
	}
}

// TestServerRollupEmptyNonNilProjectionNoUnknown_ViaAPI: same assertion via the
// full GET /servers handler so the json:"state,omitempty" suppression is verified
// end-to-end in the JSON payload.
func TestServerRollupEmptyNonNilProjectionNoUnknown_ViaAPI(t *testing.T) {
	reg := registry.New(fakeStore{servers: map[string]db.Server{
		"s": {ID: "s", Name: "S", URL: "http://x", Status: "active"},
	}})
	d := testDeps(reg)
	d.Proj = state.NewProjection() // non-nil but empty

	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/servers", nil), authz.Principal{ID: "u1"})
	w := httptest.NewRecorder()
	d.ServersHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("code %d", w.Code)
	}
	var got []registry.ServerSummary
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 server, got %d", len(got))
	}
	if got[0].State != "" {
		t.Fatalf("State must be empty (omitted), got %q", got[0].State)
	}
}

// TestServerDetailHasRollupState: GET /servers/{id} includes a State field rolled
// up from the projection's sessions. With done+blocked the rollup must be blocked.
func TestServerDetailHasRollupState(t *testing.T) {
	// Minimal agent stub: just answers healthz so ServerHandler completes.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	proj := state.NewProjection()
	proj.Set(state.SessionView{ServerID: "s", Session: "a", Global: shared.StateDone})
	proj.Set(state.SessionView{ServerID: "s", Session: "b", Global: shared.StateBlocked})

	reg := registry.New(fakeStore{servers: map[string]db.Server{
		"s": {ID: "s", Name: "Server S", URL: ts.URL, Bearer: "tok", Status: "active"},
	}})
	d := testDeps(reg)
	d.Proj = proj

	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/servers/s", nil), authz.Principal{ID: "u1"})
	r.SetPathValue("id", "s")
	w := httptest.NewRecorder()
	d.ServerHandler()(w, r)

	if w.Code != 200 {
		t.Fatalf("code %d body %s", w.Code, w.Body)
	}
	var got registry.ServerDetail
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.State != shared.StateBlocked {
		t.Fatalf("rollup: want state=%q, got %q", shared.StateBlocked, got.State)
	}
}

// TestServerRollupSeenProjection_SeenPrincipalGetsIdle: a server whose only
// session has projection Global=done, when the requesting principal has a seen
// row (LastFocusedAt >= LatestReceivedAt), the GET /servers/{id} state reads idle.
// A principal with no seen row reads done (B3 per-principal seen projection).
func TestServerRollupSeenProjection_SeenPrincipalGetsIdle(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK) // minimal agent stub for health check
	}))
	defer ts.Close()

	proj := state.NewProjection()
	proj.Set(state.SessionView{
		ServerID:         "s",
		Target:           "",
		Session:          "api",
		Global:           shared.StateDone,
		LatestReceivedAt: "2026-01-01T10:00:00.000",
	})

	seenStore := &fakeSeenStore{}
	_ = seenStore.UpsertSeen(context.Background(), db.PrincipalSeen{
		PrincipalID:   "u1",
		ServerID:      "s",
		TargetID:      "",
		Session:       "api",
		LastFocusedAt: "2026-01-01T10:00:01.000", // after LatestReceivedAt → masks done→idle
	})

	reg := registry.New(fakeStore{servers: map[string]db.Server{
		"s": {ID: "s", Name: "S", URL: ts.URL, Bearer: "tok", Status: "active"},
	}})
	d := testDeps(reg)
	d.Proj = proj
	d.Seen = seenStore

	// principal u1 has seen the session → rollup must be idle
	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/servers/s", nil), authz.Principal{ID: "u1"})
	r.SetPathValue("id", "s")
	w := httptest.NewRecorder()
	d.ServerHandler()(w, r)

	if w.Code != 200 {
		t.Fatalf("code %d body %s", w.Code, w.Body)
	}
	var got registry.ServerDetail
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.State != shared.StateIdle {
		t.Fatalf("seen principal: want state=%q, got %q", shared.StateIdle, got.State)
	}

	// principal u2 has NOT seen the session → rollup must still be done
	r2 := withPrincipal(httptest.NewRequest("GET", "/api/v1/servers/s", nil), authz.Principal{ID: "u2"})
	r2.SetPathValue("id", "s")
	w2 := httptest.NewRecorder()
	d.ServerHandler()(w2, r2)

	if w2.Code != 200 {
		t.Fatalf("code %d body %s", w2.Code, w2.Body)
	}
	var got2 registry.ServerDetail
	if err := json.NewDecoder(w2.Body).Decode(&got2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got2.State != shared.StateDone {
		t.Fatalf("unseen principal: want state=%q, got %q", shared.StateDone, got2.State)
	}
}

// TestServersListSeenProjection: GET /servers list reflects per-principal seen
// projection — the State field in the list entry is idle for a principal who
// has seen the only done session.
func TestServersListSeenProjection(t *testing.T) {
	proj := state.NewProjection()
	proj.Set(state.SessionView{
		ServerID:         "s",
		Target:           "",
		Session:          "api",
		Global:           shared.StateDone,
		LatestReceivedAt: "2026-01-01T10:00:00.000",
	})

	seenStore := &fakeSeenStore{}
	_ = seenStore.UpsertSeen(context.Background(), db.PrincipalSeen{
		PrincipalID:   "u1",
		ServerID:      "s",
		TargetID:      "",
		Session:       "api",
		LastFocusedAt: "2026-01-01T10:00:01.000",
	})

	reg := registry.New(fakeStore{servers: map[string]db.Server{
		"s": {ID: "s", Name: "S", URL: "http://x", Status: "active"},
	}})
	d := testDeps(reg)
	d.Proj = proj
	d.Seen = seenStore

	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/servers", nil), authz.Principal{ID: "u1"})
	w := httptest.NewRecorder()
	d.ServersHandler()(w, r)

	if w.Code != 200 {
		t.Fatalf("code %d body %s", w.Code, w.Body)
	}
	var got []registry.ServerSummary
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].State != shared.StateIdle {
		t.Fatalf("seen list projection: want state=%q, got %+v", shared.StateIdle, got)
	}
}
