package authn

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agentmon/hubd/internal/authz"
)

func newAuth(t *testing.T) (*Authenticator, *Store) {
	t.Helper()
	st := NewStore(time.Hour)
	return &Authenticator{Store: st, CookieName: "agentmon_session"}, st
}

func TestRequireAuthRejectsNoCookie(t *testing.T) {
	a, _ := newAuth(t)
	h := a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("must not reach handler")
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/servers", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("code: %d", w.Code)
	}
}

func TestRequireAuthStampsPrincipal(t *testing.T) {
	a, st := newAuth(t)
	sess, _ := st.New("u1", "patrik", "Patrik")
	var seen authz.Principal
	h := a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = PrincipalFrom(r.Context())
	}))
	r := httptest.NewRequest("GET", "/api/v1/servers", nil)
	r.AddCookie(&http.Cookie{Name: "agentmon_session", Value: sess.Token})
	h.ServeHTTP(httptest.NewRecorder(), r)
	if seen.ID != "u1" || seen.Username != "patrik" {
		t.Fatalf("principal: %+v", seen)
	}
}

func TestRequireAuthEnforcesCSRFOnMutations(t *testing.T) {
	a, st := newAuth(t)
	sess, _ := st.New("u1", "patrik", "Patrik")
	h := a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	r := httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
	r.AddCookie(&http.Cookie{Name: "agentmon_session", Value: sess.Token})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r) // no X-CSRF-Token
	if w.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF must be 403, got %d", w.Code)
	}
	r.Header.Set("X-CSRF-Token", sess.CSRFToken)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("valid CSRF must pass, got %d", w.Code)
	}
}

func TestRequireAuthRejectsUnknownToken(t *testing.T) {
	a, _ := newAuth(t)
	h := a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("must not reach handler")
	}))
	r := httptest.NewRequest("GET", "/api/v1/servers", nil)
	r.AddCookie(&http.Cookie{Name: "agentmon_session", Value: "bogus-token"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unknown token: code %d", w.Code)
	}
}

func TestContextWithPrincipalRoundTrip(t *testing.T) {
	p := authz.Principal{ID: "u42", Username: "tester", DisplayName: "Test User"}
	ctx := ContextWithPrincipal(context.Background(), p)
	got, ok := PrincipalFrom(ctx)
	if !ok {
		t.Fatal("PrincipalFrom returned ok=false")
	}
	if got != p {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", got, p)
	}
}
