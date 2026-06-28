package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agentmon/hubd/internal/authn"
	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/registry"
)

func TestRouterProtectsAPIAndOpensHealthz(t *testing.T) {
	auth := &authn.Authenticator{Store: authn.NewStore(time.Hour), CookieName: "agentmon_session"}
	rd := RouterDeps{
		Version: "test", Auth: auth,
		API:   testDeps(registry.New(fakeStore{servers: map[string]db.Server{"server-a": {ID: "server-a", Name: "A", Status: "active", URL: "http://x"}}})),
		WebUI: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }),
	}
	h := NewRouter(rd)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
	if w.Code != 200 {
		t.Fatalf("healthz %d", w.Code)
	}

	for _, rt := range []struct{ method, path string }{
		{"POST", "/api/v1/auth/logout"},
		{"GET", "/api/v1/me"},
		{"GET", "/api/v1/servers"},
		{"GET", "/api/v1/servers/server-a"},
		{"GET", "/api/v1/servers/server-a/sessions"},
		{"GET", "/api/v1/servers/server-a/sessions/proj"},
		{"GET", "/api/v1/audit"},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(rt.method, rt.path, nil))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s without cookie: want 401, got %d", rt.method, rt.path, w.Code)
		}
	}
}
