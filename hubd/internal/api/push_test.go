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

func (f *fakePushStore) DeleteSubscriptionForPrincipal(_ context.Context, principalID, endpoint string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Record "<principal> <endpoint>" so a test can assert the handler scoped the
	// delete to the authenticated principal, not an attacker-supplied one.
	f.deletes = append(f.deletes, principalID+" "+endpoint)
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

// TestIsSafePushEndpoint unit-tests the SSRF endpoint guard directly.
func TestIsSafePushEndpoint(t *testing.T) {
	safe := []string{
		"https://fcm.googleapis.com/fcm/send/abc",
		"https://updates.push.services.mozilla.com/wpush/v2/xyz",
		"https://web.push.apple.com/abc",
		"https://push.example/abc",
	}
	unsafe := []string{
		"http://push.example/abc",        // not https
		"https://127.0.0.1/x",            // loopback v4
		"https://[::1]/x",                // loopback v6
		"https://localhost/x",            // localhost
		"https://app.localhost/x",        // *.localhost
		"https://10.0.0.5/x",             // private
		"https://192.168.1.9/x",          // private
		"https://172.16.4.4/x",           // private
		"https://169.254.169.254/latest", // link-local (cloud metadata)
		"https://0.0.0.0/x",              // unspecified
		"ftp://push.example/x",           // wrong scheme
		"https:///nohost",                // empty host
		"not a url",                      // unparseable / no scheme
	}
	for _, e := range safe {
		if !isSafePushEndpoint(e) {
			t.Errorf("isSafePushEndpoint(%q) = false, want true", e)
		}
	}
	for _, e := range unsafe {
		if isSafePushEndpoint(e) {
			t.Errorf("isSafePushEndpoint(%q) = true, want false", e)
		}
	}
}

// TestSubscribeRejectsNonHttps: a non-https endpoint (SSRF vector) → 400, no upsert.
func TestSubscribeRejectsNonHttps(t *testing.T) {
	store := &fakePushStore{}
	h, d := buildHubWithPush(t, store, "k")
	defer d.Close()

	cookie, csrf := login(t, h)

	body := `{"endpoint":"http://169.254.169.254/latest/meta-data","keys":{"p256dh":"pp","auth":"aa"}}`
	req := httptest.NewRequest("POST", "/api/v1/push/subscribe", strings.NewReader(body))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("non-https endpoint must be 400, got %d body=%s", w.Code, w.Body)
	}
	if len(store.upserts) != 0 {
		t.Fatalf("want 0 upserts for non-https endpoint, got %d", len(store.upserts))
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
	// Scoped to the authenticated principal (u1), not an arbitrary endpoint owner.
	if len(store.deletes) != 1 || store.deletes[0] != "u1 https://push.example/abc" {
		t.Fatalf("delete not recorded with scoping principal: %+v", store.deletes)
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
