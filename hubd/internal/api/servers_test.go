package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agentmon/hubd/internal/audit"
	"agentmon/hubd/internal/authn"
	"agentmon/hubd/internal/authz"
	"agentmon/hubd/internal/config"
	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/registry"
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

func TestServersHandlerListsForAuthedPrincipal(t *testing.T) {
	reg := registry.New([]config.Server{{ID: "server-a", Name: "A", Token: "t", URL: "http://x"}})
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
	d := testDeps(registry.New(nil))
	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/servers", nil), authz.Principal{})
	w := httptest.NewRecorder()
	d.ServersHandler()(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("code %d", w.Code)
	}
}

func TestServerHandlerUnknownIDIs404(t *testing.T) {
	d := testDeps(registry.New(nil))
	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/servers/nope", nil), authz.Principal{ID: "u1"})
	r.SetPathValue("id", "nope")
	w := httptest.NewRecorder()
	d.ServerHandler()(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("code %d", w.Code)
	}
}
