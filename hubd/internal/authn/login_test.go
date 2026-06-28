package authn

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

// sliceSink is an audit sink that records every entry so tests can inspect them.
type sliceSink struct {
	mu      sync.Mutex
	entries []db.AuditEntry
}

func (s *sliceSink) Append(_ context.Context, e db.AuditEntry) error {
	s.mu.Lock()
	s.entries = append(s.entries, e)
	s.mu.Unlock()
	return nil
}

func (s *sliceSink) countAction(action string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, e := range s.entries {
		if e.Action == action {
			n++
		}
	}
	return n
}

// TestLogin429IsAudited asserts that a throttled (429) login attempt still
// produces a login.failure audit entry — so a sustained brute-force leaves a
// full trail even after the per-username limiter has fired.
func TestLogin429IsAudited(t *testing.T) {
	sink := &sliceSink{}
	hash, _ := HashPassword("pw")
	d := LoginDeps{
		Users:          stubUsers{u: db.User{ID: "u1", Username: "patrik", PasswordHash: hash}},
		Store:          NewStore(time.Hour),
		Limiter:        NewLimiter(1, time.Minute), // 1 attempt before lockout
		Audit:          audit.NewRecorder(sink),
		CookieName:     "agentmon_session",
		CookieTTL:      time.Hour,
		ExternalOrigin: "https://agentmon.lan",
	}
	// First bad login: real failure, limiter not yet full.
	r1 := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"patrik","password":"NOPE"}`))
	w1 := httptest.NewRecorder()
	d.LoginHandler()(w1, r1)
	if w1.Code != http.StatusUnauthorized {
		t.Fatalf("first attempt: got %d, want 401", w1.Code)
	}
	if got := sink.countAction("login.failure"); got != 1 {
		t.Fatalf("after first failure: want 1 login.failure audit entry, got %d", got)
	}
	// Second attempt: limiter fires → 429; must still record login.failure.
	r2 := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"patrik","password":"NOPE"}`))
	w2 := httptest.NewRecorder()
	d.LoginHandler()(w2, r2)
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("second attempt: got %d, want 429", w2.Code)
	}
	if got := sink.countAction("login.failure"); got != 2 {
		t.Fatalf("after 429: want 2 login.failure audit entries (real + throttled), got %d", got)
	}
}

// TestLoginVerifyConcurrencyIsBounded fires 12 concurrent logins for distinct
// unknown usernames against the handler and asserts that they all complete
// without deadlock and each returns 401. This exercises verifySem under -race:
// the semaphore (capacity 4) must queue the surplus goroutines rather than
// dropping or deadlocking them.
func TestLoginVerifyConcurrencyIsBounded(t *testing.T) {
	const n = 12
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			d := deps(t, db.User{}, sql.ErrNoRows)
			body := strings.NewReader(fmt.Sprintf(`{"username":"ghost%d","password":"x"}`, i))
			r := httptest.NewRequest("POST", "/api/v1/auth/login", body)
			w := httptest.NewRecorder()
			d.LoginHandler()(w, r)
			codes[i] = w.Code
		}()
	}
	wg.Wait()
	for i, c := range codes {
		if c != http.StatusUnauthorized {
			t.Errorf("goroutine %d: got %d, want 401", i, c)
		}
	}
}
