package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestRouterUnknownAPIPathIsJSON404(t *testing.T) {
	auth := &authn.Authenticator{Store: authn.NewStore(time.Hour), CookieName: "agentmon_session"}
	rd := RouterDeps{
		Version: "test", Auth: auth,
		API:   testDeps(registry.New(fakeStore{servers: map[string]db.Server{"server-a": {ID: "server-a", Name: "A", Status: "active", URL: "http://x"}}})),
		WebUI: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }),
	}
	h := NewRouter(rd)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/does-not-exist", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("want JSON, got %q", ct)
	}
	if !strings.Contains(w.Body.String(), `"error"`) {
		t.Fatalf("want JSON error envelope, got %s", w.Body)
	}
}

func TestRouterRelayRouteRequiresAuth(t *testing.T) {
	auth := &authn.Authenticator{Store: authn.NewStore(time.Hour), CookieName: "agentmon_session"}
	rd := RouterDeps{
		Version: "test", Auth: auth,
		API:   testDeps(registry.New(fakeStore{servers: map[string]db.Server{"server-a": {ID: "server-a", Name: "A", Status: "active", URL: "http://x"}}})),
		WebUI: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }),
	}
	h := NewRouter(rd)

	w := httptest.NewRecorder()
	// A plain GET (no Upgrade, no cookie) must be rejected by RequireAuth before
	// any upgrade is attempted.
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/servers/aigallery/panes/%253/io", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("relay route must require auth, got %d", w.Code)
	}
}
