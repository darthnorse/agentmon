package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentmon/hubd/internal/audit"
	"agentmon/hubd/internal/authn"
	"agentmon/hubd/internal/db"
)

var errNoRow = errors.New("no rows")

// memEnrollStore is an in-memory EnrollStore.
type memEnrollStore struct{ servers map[string]db.Server }

func (m *memEnrollStore) GetServer(_ context.Context, id string) (db.Server, error) {
	if s, ok := m.servers[id]; ok {
		return s, nil
	}
	return db.Server{}, errNoRow
}
func (m *memEnrollStore) EnrollServer(_ context.Context, s db.Server) error {
	m.servers[s.ID] = s
	return nil
}

func enrollDeps() (EnrollDeps, *memEnrollStore) {
	st := &memEnrollStore{servers: map[string]db.Server{}}
	return EnrollDeps{Servers: st, Audit: audit.NewRecorder(nopSink{})}, st
}

func TestEnrollCreatesPendingAndReturnsCreds(t *testing.T) {
	d, st := enrollDeps()
	body := `{"hostname":"web-01","os":"linux","arch":"amd64","agentVersion":"dev","target":{"osUser":"dev","label":"default"}}`
	r := httptest.NewRequest("POST", "/api/v1/enroll", strings.NewReader(body))
	r.RemoteAddr = "10.0.0.9:54000"
	w := httptest.NewRecorder()
	d.Handler()(w, r)
	if w.Code != 200 {
		t.Fatalf("code %d: %s", w.Code, w.Body)
	}
	var resp enrollResp
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.ServerID != "web-01" || resp.Bearer == "" || resp.SigningKey == "" {
		t.Fatalf("resp: %+v", resp)
	}
	if resp.Bearer == resp.SigningKey {
		t.Fatal("bearer and signing key must be independently generated")
	}
	got := st.servers["web-01"]
	if got.Status != "pending" || got.Bearer != resp.Bearer || got.URL != "http://10.0.0.9:8377" || got.Arch != "amd64" {
		t.Fatalf("stored row: %+v", got)
	}
}

func TestEnrollDuplicateIs409(t *testing.T) {
	d, st := enrollDeps()
	st.servers["web-01"] = db.Server{ID: "web-01", Status: "pending"}
	r := httptest.NewRequest("POST", "/api/v1/enroll", strings.NewReader(`{"hostname":"web-01","arch":"amd64"}`))
	r.RemoteAddr = "10.0.0.9:1"
	w := httptest.NewRecorder()
	d.Handler()(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("dup enroll: want 409, got %d", w.Code)
	}
}

func TestEnrollBadBodyIs400(t *testing.T) {
	d, _ := enrollDeps()
	for _, body := range []string{`{not json`, `{"hostname":"","arch":"amd64"}`, `{"hostname":"web-01","arch":"sparc"}`} {
		r := httptest.NewRequest("POST", "/api/v1/enroll", strings.NewReader(body))
		r.RemoteAddr = "10.0.0.9:1"
		w := httptest.NewRecorder()
		d.Handler()(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("body %q: want 400, got %d", body, w.Code)
		}
	}
}

func TestEnrollDialURLUsesForwardedClientIP(t *testing.T) {
	st := &memEnrollStore{servers: map[string]db.Server{}}
	d := EnrollDeps{Servers: st, Audit: audit.NewRecorder(nopSink{}), TrustForwardedProto: true}
	r := httptest.NewRequest("POST", "/api/v1/enroll", strings.NewReader(`{"hostname":"web-01","arch":"amd64"}`))
	r.RemoteAddr = "10.9.9.9:443"                                   // the proxy
	r.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.50") // ..., real agent peer (last hop)
	w := httptest.NewRecorder()
	d.Handler()(w, r)
	if w.Code != 200 {
		t.Fatalf("code %d: %s", w.Code, w.Body)
	}
	if got := st.servers["web-01"].URL; got != "http://10.0.0.50:8377" {
		t.Fatalf("dial URL must use the forwarded client IP (last XFF hop), got %q", got)
	}
}

func TestOnboardRateLimitReturns429(t *testing.T) {
	l := authn.NewLimiter(2, time.Minute)
	called := 0
	h := onboardRateLimit(l, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		w.WriteHeader(200)
	}))
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/install.sh", nil)
		r.RemoteAddr = "10.0.0.9:1"
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("attempt %d: want 200, got %d", i, w.Code)
		}
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/install.sh", nil)
	r.RemoteAddr = "10.0.0.9:1"
	h.ServeHTTP(w, r)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("3rd attempt: want 429, got %d", w.Code)
	}
	if called != 2 {
		t.Fatalf("rate-limited request must not reach the handler: called=%d", called)
	}
}
