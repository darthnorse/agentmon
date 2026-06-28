package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentmon/hubd/internal/audit"
	"agentmon/hubd/internal/authn"
	"agentmon/hubd/internal/config"
	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/registry"
)

func buildHub(t *testing.T, agentURL, agentToken string) (http.Handler, *db.DB) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "hub.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := authn.HashPassword("hunter2")
	if err := d.SetPassword(context.Background(), "u1", "patrik", "Patrik", hash); err != nil {
		t.Fatal(err)
	}
	store := authn.NewStore(time.Hour)
	auth := &authn.Authenticator{Store: store, CookieName: "agentmon_session"}
	rec := audit.NewRecorder(d)
	reg := registry.New([]config.Server{{ID: "server-a", Name: "A", URL: agentURL, Token: agentToken}})
	router := NewRouter(RouterDeps{
		Version: "test", Auth: auth,
		Login: authn.LoginDeps{Users: d, Store: store, Limiter: authn.NewLimiter(5, time.Minute),
			Audit: rec, CookieName: "agentmon_session", CookieTTL: time.Hour,
			ExternalOrigin: "https://agentmon.lan"},
		API:   Deps{Reg: reg, Agent: registry.NewClient(2 * time.Second), Audit: rec, AuditRepo: d, HealthTimeout: time.Second},
		WebUI: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }),
	})
	return router, d
}

func login(t *testing.T, h http.Handler) (*http.Cookie, string) {
	t.Helper()
	r := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"patrik","password":"hunter2"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("login %d: %s", w.Code, w.Body)
	}
	var body map[string]string
	json.NewDecoder(w.Body).Decode(&body)
	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("login response: no Set-Cookie")
	}
	return cookies[0], body["csrfToken"]
}

func TestEndToEndLoginListSessionsLogout(t *testing.T) {
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer agent-tok" {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`{"sessions":[{"name":"proj","server":"x","target":"default","cwd":"/p","command":"claude","windows":[]}]}`))
	}))
	defer agent.Close()
	h, d := buildHub(t, agent.URL, "agent-tok")
	defer d.Close()

	cookie, csrf := login(t, h)

	// assert login response carries a non-empty csrfToken (Set-Cookie is
	// guarded in the login helper's length check)
	if csrf == "" {
		t.Fatal("login response: csrfToken is empty")
	}

	// GET /servers
	req := httptest.NewRequest("GET", "/api/v1/servers", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "server-a") {
		t.Fatalf("servers: %d %s", w.Code, w.Body)
	}

	// GET /servers/server-a/sessions → stamped + project-labelled
	req = httptest.NewRequest("GET", "/api/v1/servers/server-a/sessions", nil)
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"name":"proj"`) || !strings.Contains(w.Body.String(), `"server":"server-a"`) {
		t.Fatalf("sessions: %d %s", w.Code, w.Body)
	}

	// GET /me
	req = httptest.NewRequest("GET", "/api/v1/me", nil)
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("me: %d %s", w.Code, w.Body)
	}

	// POST /logout requires CSRF
	req = httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("logout: %d", w.Code)
	}

	// audit shows the login.success
	rows, err := d.Recent(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	var sawLogin bool
	for _, e := range rows {
		if e.Action == "login.success" && e.PrincipalID == "u1" {
			sawLogin = true
		}
	}
	if !sawLogin {
		t.Fatal("login.success not audited")
	}
}

func TestEndToEndUnauthAndBadAgentToken(t *testing.T) {
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer agent.Close()
	h, d := buildHub(t, agent.URL, "agent-tok") // registry token mismatches → agent 401
	defer d.Close()

	// unauth → 401
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/servers", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauth servers %d", w.Code)
	}

	cookie, _ := login(t, h)
	req := httptest.NewRequest("GET", "/api/v1/servers/server-a/sessions", nil)
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("agent 401 → hub should 502, got %d", w.Code)
	}
}

func TestEndToEndOriginRejectAndRateLimit(t *testing.T) {
	h, d := buildHub(t, "http://unused", "x")
	defer d.Close()

	// origin reject
	r := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"patrik","password":"hunter2"}`))
	r.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("bad origin → 403, got %d", w.Code)
	}

	// rate-limit: 5 failed attempts exhaust the limiter, the 6th → 429
	for i := 0; i < 5; i++ {
		rr := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"patrik","password":"WRONG"}`))
		ww := httptest.NewRecorder()
		h.ServeHTTP(ww, rr)
		if ww.Code != http.StatusUnauthorized {
			t.Fatalf("failed attempt %d: want 401, got %d", i, ww.Code)
		}
	}
	rr := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"patrik","password":"WRONG"}`))
	ww := httptest.NewRecorder()
	h.ServeHTTP(ww, rr)
	if ww.Code != http.StatusTooManyRequests {
		t.Fatalf("6th attempt after limit: want 429, got %d", ww.Code)
	}
}
