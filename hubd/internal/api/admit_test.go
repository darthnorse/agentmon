package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"agentmon/hubd/internal/audit"
	"agentmon/hubd/internal/authz"
	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/registry"
)

func admitReg() *registry.Registry {
	return registry.New(fakeStore{servers: map[string]db.Server{
		"web-01":   {ID: "web-01", Hostname: "web-01", URL: "http://10.0.0.5:8377", Status: "pending", OS: "linux", Arch: "amd64", Bearer: "SECRETtok", SigningKey: "SECRETkey"},
		"active-1": {ID: "active-1", Hostname: "active-1", Status: "active"},
	}})
}

func TestPendingServersHandlerListsPendingOnlyNoSecrets(t *testing.T) {
	d := testDeps(admitReg())
	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/servers/pending", nil), authz.Principal{ID: "u1"})
	w := httptest.NewRecorder()
	d.PendingServersHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("code %d: %s", w.Code, w.Body)
	}
	if bytes.Contains(w.Body.Bytes(), []byte("SECRET")) {
		t.Fatalf("pending projection leaked a secret: %s", w.Body)
	}
	var got []registry.PendingServer
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "web-01" || got[0].URL != "http://10.0.0.5:8377" || got[0].Arch != "amd64" {
		t.Fatalf("pending: %+v", got)
	}
}

func TestPendingServersHandlerDeniesEmptyPrincipal(t *testing.T) {
	d := testDeps(admitReg())
	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/servers/pending", nil), authz.Principal{})
	w := httptest.NewRecorder()
	d.PendingServersHandler()(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("code %d, want 403", w.Code)
	}
}

func approveReq(t *testing.T, id string, p authz.Principal) (*http.Request, *httptest.ResponseRecorder) {
	t.Helper()
	r := withPrincipal(httptest.NewRequest("POST", "/api/v1/servers/"+id+"/approve", nil), p)
	r.SetPathValue("id", id)
	return r, httptest.NewRecorder()
}

func TestServerApproveAdmitsPendingAndAudits(t *testing.T) {
	reg := admitReg()
	d := testDeps(reg)
	sink := &captureSink{}
	d.Audit = audit.NewRecorder(sink)

	r, w := approveReq(t, "web-01", authz.Principal{ID: "u1"})
	d.ServerApproveHandler()(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("code %d: %s", w.Code, w.Body)
	}
	// now visible to the active-only registry
	if _, ok, _ := reg.Get(httptest.NewRequest("GET", "/", nil).Context(), "web-01"); !ok {
		t.Fatal("approved server must be active/visible")
	}
	e, ok := sink.find("server.approve")
	if !ok || e.Resource != "server:web-01" {
		t.Fatalf("approve audit: %+v ok=%v", e, ok)
	}
}

func TestServerApproveRejects(t *testing.T) {
	cases := []struct {
		name, id string
		p        authz.Principal
		want     int
	}{
		{"empty principal", "web-01", authz.Principal{}, http.StatusForbidden},
		{"unknown id", "ghost", authz.Principal{ID: "u1"}, http.StatusNotFound},
		{"already active", "active-1", authz.Principal{ID: "u1"}, http.StatusNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := testDeps(admitReg())
			r, w := approveReq(t, c.id, c.p)
			d.ServerApproveHandler()(w, r)
			if w.Code != c.want {
				t.Fatalf("code %d, want %d", w.Code, c.want)
			}
		})
	}
}

func TestServerRejectRemovesPendingAndAudits(t *testing.T) {
	reg := admitReg()
	d := testDeps(reg)
	sink := &captureSink{}
	d.Audit = audit.NewRecorder(sink)

	r := withPrincipal(httptest.NewRequest("POST", "/api/v1/servers/web-01/reject", nil), authz.Principal{ID: "u1"})
	r.SetPathValue("id", "web-01")
	w := httptest.NewRecorder()
	d.ServerRejectHandler()(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("code %d: %s", w.Code, w.Body)
	}
	list, _ := reg.ListPending(r.Context())
	if len(list) != 0 {
		t.Fatalf("rejected agent must be gone, still pending: %+v", list)
	}
	if e, ok := sink.find("server.remove"); !ok || e.Resource != "server:web-01" {
		t.Fatalf("reject audit: %+v ok=%v", e, ok)
	}
}

func TestServerRejectRefusesActiveServer(t *testing.T) {
	reg := admitReg()
	d := testDeps(reg)
	r := withPrincipal(httptest.NewRequest("POST", "/api/v1/servers/active-1/reject", nil), authz.Principal{ID: "u1"})
	r.SetPathValue("id", "active-1")
	w := httptest.NewRecorder()
	d.ServerRejectHandler()(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("rejecting an ACTIVE server must be 404 (CLI-only rm), got %d", w.Code)
	}
}
