package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"agentmon/hubd/internal/audit"
	"agentmon/hubd/internal/authn"
	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/registry"
)

// fakePushStore records calls for assertion in the api handler tests.
type fakePushStore struct {
	mu      sync.Mutex
	upserts []db.PushSubscription
	deletes []string
}

func (f *fakePushStore) UpsertSubscription(_ context.Context, s db.PushSubscription) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upserts = append(f.upserts, s)
	return nil
}

func (f *fakePushStore) DeleteSubscription(_ context.Context, endpoint string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletes = append(f.deletes, endpoint)
	return nil
}

func (f *fakePushStore) ListSubscriptionsForPrincipal(_ context.Context, _ string) ([]db.PushSubscription, error) {
	return nil, nil
}

func (f *fakePushStore) PrincipalIDsWithSubscriptions(_ context.Context) ([]string, error) {
	return nil, nil
}

// buildHubWithPush wires a fake PushStore + VAPID public key into api.Deps so
// the /api/v1/push/* routes are available.
func buildHubWithPush(t *testing.T, store PushStore, vapidPublic string) (http.Handler, *db.DB) {
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
	authStore := authn.NewStore(time.Hour)
	auth := &authn.Authenticator{Store: authStore, CookieName: "agentmon_session"}
	rec := audit.NewRecorder(d)
	router := NewRouter(RouterDeps{
		Version: "test",
		Auth:    auth,
		Login: authn.LoginDeps{
			Users: d, Store: authStore, Limiter: authn.NewLimiter(5, time.Minute),
			Audit: rec, CookieName: "agentmon_session", CookieTTL: time.Hour,
			ExternalOrigin: "https://agentmon.lan",
		},
		API: Deps{
			Reg: reg, Agent: registry.NewClient(2 * time.Second),
			Audit: rec, AuditRepo: d, HealthTimeout: time.Second,
			Push: store, VAPIDPublic: vapidPublic,
		},
		WebUI: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }),
	})
	return router, d
}

// TestVapidReturnsPublicKey: GET /push/vapid (authed) → 200 {"publicKey":...}.
func TestVapidReturnsPublicKey(t *testing.T) {
	h, d := buildHubWithPush(t, &fakePushStore{}, "PUBKEY-123")
	defer d.Close()

	cookie, _ := login(t, h)

	req := httptest.NewRequest("GET", "/api/v1/push/vapid", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body)
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["publicKey"] != "PUBKEY-123" {
		t.Fatalf("publicKey: want PUBKEY-123, got %q", body["publicKey"])
	}
}

// TestSubscribeUpserts: POST /push/subscribe with valid body + CSRF → 204 and the
// store records the upsert with the authed principal.
func TestSubscribeUpserts(t *testing.T) {
	store := &fakePushStore{}
	h, d := buildHubWithPush(t, store, "k")
	defer d.Close()

	cookie, csrf := login(t, h)

	body := `{"endpoint":"https://push.example/abc","keys":{"p256dh":"pp","auth":"aa"}}`
	req := httptest.NewRequest("POST", "/api/v1/push/subscribe", strings.NewReader(body))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d body=%s", w.Code, w.Body)
	}
	if len(store.upserts) != 1 {
		t.Fatalf("want 1 upsert, got %d", len(store.upserts))
	}
	got := store.upserts[0]
	if got.PrincipalID != "u1" {
		t.Fatalf("PrincipalID: want u1, got %q", got.PrincipalID)
	}
	if got.Endpoint != "https://push.example/abc" || got.P256dh != "pp" || got.Auth != "aa" {
		t.Fatalf("subscription fields wrong: %+v", got)
	}
}

// TestSubscribeRequiresCSRF: POST /push/subscribe without CSRF → 403, no upsert.
func TestSubscribeRequiresCSRF(t *testing.T) {
	store := &fakePushStore{}
	h, d := buildHubWithPush(t, store, "k")
	defer d.Close()

	cookie, _ := login(t, h)

	body := `{"endpoint":"https://push.example/abc","keys":{"p256dh":"pp","auth":"aa"}}`
	req := httptest.NewRequest("POST", "/api/v1/push/subscribe", strings.NewReader(body))
	req.AddCookie(cookie)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF must be 403, got %d body=%s", w.Code, w.Body)
	}
	if len(store.upserts) != 0 {
		t.Fatalf("want 0 upserts on CSRF failure, got %d", len(store.upserts))
	}
}

// TestSubscribeEmptyEndpoint: POST /push/subscribe with empty endpoint → 400.
func TestSubscribeEmptyEndpoint(t *testing.T) {
	store := &fakePushStore{}
	h, d := buildHubWithPush(t, store, "k")
	defer d.Close()

	cookie, csrf := login(t, h)

	body := `{"endpoint":"","keys":{"p256dh":"pp","auth":"aa"}}`
	req := httptest.NewRequest("POST", "/api/v1/push/subscribe", strings.NewReader(body))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty endpoint must be 400, got %d body=%s", w.Code, w.Body)
	}
	if len(store.upserts) != 0 {
		t.Fatalf("want 0 upserts on bad body, got %d", len(store.upserts))
	}
}

// TestUnsubscribeDeletes: POST /push/unsubscribe with {endpoint} + CSRF → 204 and
// the store records the delete.
func TestUnsubscribeDeletes(t *testing.T) {
	store := &fakePushStore{}
	h, d := buildHubWithPush(t, store, "k")
	defer d.Close()

	cookie, csrf := login(t, h)

	body := `{"endpoint":"https://push.example/abc"}`
	req := httptest.NewRequest("POST", "/api/v1/push/unsubscribe", strings.NewReader(body))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d body=%s", w.Code, w.Body)
	}
	if len(store.deletes) != 1 || store.deletes[0] != "https://push.example/abc" {
		t.Fatalf("delete not recorded: %+v", store.deletes)
	}
}

// TestUnsubscribeEmptyEndpoint: POST /push/unsubscribe with empty endpoint → 400.
func TestUnsubscribeEmptyEndpoint(t *testing.T) {
	store := &fakePushStore{}
	h, d := buildHubWithPush(t, store, "k")
	defer d.Close()

	cookie, csrf := login(t, h)

	body := `{"endpoint":""}`
	req := httptest.NewRequest("POST", "/api/v1/push/unsubscribe", strings.NewReader(body))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty endpoint must be 400, got %d body=%s", w.Code, w.Body)
	}
	if len(store.deletes) != 0 {
		t.Fatalf("want 0 deletes on bad body, got %d", len(store.deletes))
	}
}
