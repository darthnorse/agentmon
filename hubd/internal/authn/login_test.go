package authn

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentmon/hubd/internal/audit"
	"agentmon/hubd/internal/db"
)

type stubUsers struct{ u db.User; err error }

func (s stubUsers) GetUserByUsername(_ context.Context, _ string) (db.User, error) {
	return s.u, s.err
}

func deps(t *testing.T, u db.User, err error) LoginDeps {
	return LoginDeps{
		Users: stubUsers{u: u, err: err}, Store: NewStore(time.Hour),
		Limiter: NewLimiter(5, time.Minute), Audit: audit.NewRecorder(&countSink{}),
		CookieName: "agentmon_session", CookieTTL: time.Hour, ExternalOrigin: "https://agentmon.lan",
	}
}

type countSink struct{ n int }

func (c *countSink) Append(_ context.Context, _ db.AuditEntry) error { c.n++; return nil }

func TestLoginSuccessSetsCookieAndReturnsCSRF(t *testing.T) {
	hash, _ := HashPassword("pw")
	d := deps(t, db.User{ID: "u1", Username: "patrik", DisplayName: "Patrik", PasswordHash: hash, Status: "active"}, nil)
	body := strings.NewReader(`{"username":"patrik","password":"pw"}`)
	r := httptest.NewRequest("POST", "/api/v1/auth/login", body)
	w := httptest.NewRecorder()
	d.LoginHandler()(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("code %d body %s", w.Code, w.Body)
	}
	if len(w.Result().Cookies()) == 0 || w.Result().Cookies()[0].Name != "agentmon_session" {
		t.Fatal("no session cookie set")
	}
	if !w.Result().Cookies()[0].HttpOnly {
		t.Fatal("session cookie must be HttpOnly")
	}
	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["principalId"] != "u1" || resp["csrfToken"] == "" {
		t.Fatalf("resp: %+v", resp)
	}
}

func TestLoginWrongPasswordIs401(t *testing.T) {
	hash, _ := HashPassword("pw")
	d := deps(t, db.User{ID: "u1", Username: "patrik", PasswordHash: hash}, nil)
	r := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"patrik","password":"NOPE"}`))
	w := httptest.NewRecorder()
	d.LoginHandler()(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("code %d", w.Code)
	}
}

func TestLoginInactiveUserRejected(t *testing.T) {
	hash, _ := HashPassword("pw")
	// Correct password, but the account is disabled: the status gate must reject.
	d := deps(t, db.User{ID: "u1", Username: "patrik", PasswordHash: hash, Status: "disabled"}, nil)
	r := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"patrik","password":"pw"}`))
	w := httptest.NewRecorder()
	d.LoginHandler()(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("code %d", w.Code)
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("inactive user must not get a session cookie")
	}
}

func TestLoginUnknownUser401(t *testing.T) {
	// Lookup returns an error → user not found; exercises the dummy-hash timing-flat branch.
	d := deps(t, db.User{}, sql.ErrNoRows)
	r := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"ghost","password":"whatever"}`))
	w := httptest.NewRecorder()
	d.LoginHandler()(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("code %d", w.Code)
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("unknown user must not get a session cookie")
	}
}

func TestLoginOriginMismatchIs403(t *testing.T) {
	d := deps(t, db.User{}, nil)
	r := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{}`))
	r.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	d.LoginHandler()(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("code %d", w.Code)
	}
}

func TestLoginRateLimited(t *testing.T) {
	hash, _ := HashPassword("pw")
	d := deps(t, db.User{ID: "u1", Username: "patrik", PasswordHash: hash}, nil)
	d.Limiter = NewLimiter(1, time.Minute)
	r1 := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"patrik","password":"NOPE"}`))
	d.LoginHandler()(httptest.NewRecorder(), r1) // 1 failure → limiter now full
	r2 := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"patrik","password":"NOPE"}`))
	w := httptest.NewRecorder()
	d.LoginHandler()(w, r2)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", w.Code)
	}
}
