package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentmon/hubd/internal/authn"
	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/registry"
	"agentmon/hubd/internal/audit"
	"time"
)

// buildHubWithSeen is like buildHub but also wires Seen into api.Deps so the
// /api/v1/seen route is available.
func buildHubWithSeen(t *testing.T) (http.Handler, *db.DB) {
	t.Helper()
	d, err := db.Open(t.TempDir() + "/hub.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := authn.HashPassword("hunter2")
	if err := d.SetPassword(context.Background(), "u1", "patrik", "Patrik", hash); err != nil {
		t.Fatal(err)
	}
	if err := d.EnrollServer(context.Background(), db.Server{
		ID: "srv", Name: "S", Hostname: "srv", URL: "http://unused",
		Status: "active", Bearer: "tok", SigningKey: "k",
	}); err != nil {
		t.Fatal(err)
	}
	reg := registry.New(d)
	store := authn.NewStore(time.Hour)
	auth := &authn.Authenticator{Store: store, CookieName: "agentmon_session"}
	rec := audit.NewRecorder(d)
	router := NewRouter(RouterDeps{
		Version: "test",
		Auth:    auth,
		Login: authn.LoginDeps{
			Users: d, Store: store, Limiter: authn.NewLimiter(5, time.Minute),
			Audit: rec, CookieName: "agentmon_session", CookieTTL: time.Hour,
			ExternalOrigin: "https://agentmon.lan",
		},
		API: Deps{
			Reg: reg, Agent: registry.NewClient(2 * time.Second),
			Audit: rec, AuditRepo: d, HealthTimeout: time.Second,
			Seen: d,
		},
		WebUI: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }),
	})
	return router, d
}

// TestSeenUpsertsAndAnchors: POST /seen with a valid principal+CSRF when a
// state event exists for the session → 204 and GetSeen returns a row whose
// LastSeenEventID matches the seeded event's ID.
func TestSeenUpsertsAndAnchors(t *testing.T) {
	h, d := buildHubWithSeen(t)
	defer d.Close()

	// Seed a state event for (srv, "", "api").
	ev := db.StateEvent{
		ID: "evt-001", ServerID: "srv", TargetID: "", Session: "api",
		Source: "hook", RawEvent: "{}", DerivedState: "done",
		EventTs: "2026-01-01 00:00:00.000", ReceivedAt: "2026-01-01 00:00:00.000",
	}
	if err := d.AppendStateEvent(context.Background(), ev); err != nil {
		t.Fatalf("AppendStateEvent: %v", err)
	}

	cookie, csrf := login(t, h)

	body := `{"serverId":"srv","target":"","sessionName":"api"}`
	req := httptest.NewRequest("POST", "/api/v1/seen", strings.NewReader(body))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d body=%s", w.Code, w.Body)
	}

	got, ok, err := d.GetSeen(context.Background(), "u1", "srv", "", "api")
	if err != nil || !ok {
		t.Fatalf("GetSeen ok=%v err=%v", ok, err)
	}
	if got.LastSeenEventID != ev.ID {
		t.Fatalf("LastSeenEventID: want %q, got %q", ev.ID, got.LastSeenEventID)
	}
}

// TestSeenRequiresCSRF: POST /seen without CSRF token → 403.
func TestSeenRequiresCSRF(t *testing.T) {
	h, d := buildHubWithSeen(t)
	defer d.Close()

	cookie, _ := login(t, h)

	body := `{"serverId":"srv","target":"","sessionName":"api"}`
	req := httptest.NewRequest("POST", "/api/v1/seen", strings.NewReader(body))
	req.AddCookie(cookie)
	// No X-CSRF-Token header.
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF must be 403, got %d body=%s", w.Code, w.Body)
	}
}

// TestSeenBadBody: POST /seen with missing sessionName → 400.
func TestSeenBadBody(t *testing.T) {
	h, d := buildHubWithSeen(t)
	defer d.Close()

	cookie, csrf := login(t, h)

	body := `{"serverId":"srv","target":""}`
	req := httptest.NewRequest("POST", "/api/v1/seen", strings.NewReader(body))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing sessionName must be 400, got %d body=%s", w.Code, w.Body)
	}
}
