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
