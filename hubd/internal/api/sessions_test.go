package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"agentmon/hubd/internal/audit"
	"agentmon/hubd/internal/authz"
	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/registry"
	"agentmon/hubd/internal/state"
	"agentmon/shared"
)

// captureSink records audit entries so the create-session tests can assert a
// session.create row was written with the right resource + meta.
type captureSink struct {
	mu      sync.Mutex
	entries []db.AuditEntry
}

func (s *captureSink) Append(_ context.Context, e db.AuditEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, e)
	return nil
}

func (s *captureSink) find(action string) (db.AuditEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.entries {
		if e.Action == action {
			return e, true
		}
	}
	return db.AuditEntry{}, false
}

// createFakeAgent is a fake agent that answers POST /sessions (create) with
// postStatus (200 → {"name":"newproj"}) and GET /sessions (re-list) with
// listBody. It 401s on a wrong bearer so the hub→agent auth is exercised.
func createFakeAgent(t *testing.T, token string, postStatus int, listBody string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.Method {
		case http.MethodPost:
			w.WriteHeader(postStatus)
			if postStatus == http.StatusOK || postStatus == http.StatusCreated {
				_, _ = w.Write([]byte(`{"name":"newproj"}`))
			}
		case http.MethodGet:
			_, _ = w.Write([]byte(listBody))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
}

const createListBody = `{"sessions":[{"name":"newproj","server":"x","target":"default","cwd":"/p","command":"","state":"unknown","windows":[{"id":"@1","index":"0","name":"main","panes":[{"id":"%9","command":"bash","cwd":"/p"}]}]}]}`

func createReq(t *testing.T, id, target, body string) (*http.Request, *httptest.ResponseRecorder) {
	t.Helper()
	url := "/api/v1/servers/" + id + "/sessions"
	if target != "" {
		url += "?target=" + target
	}
	r := withPrincipal(httptest.NewRequest("POST", url, strings.NewReader(body)), authz.Principal{ID: "u1"})
	r.SetPathValue("id", id)
	return r, httptest.NewRecorder()
}

// TestServerCreateSessionSuccess: a valid request creates via the agent, re-lists,
// and returns the full Session (with its pane id) as 201; a session.create audit
// row is recorded with the right resource + the name in meta.
func TestServerCreateSessionSuccess(t *testing.T) {
	ts := createFakeAgent(t, "tok-a", http.StatusOK, createListBody)
	defer ts.Close()
	d := depsWith(db.Server{ID: "server-a", URL: ts.URL, Bearer: "tok-a", Status: "active"})
	sink := &captureSink{}
	d.Audit = audit.NewRecorder(sink)

	r, w := createReq(t, "server-a", "default", `{"name":"newproj"}`)
	d.ServerCreateSessionHandler()(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("code %d body %s", w.Code, w.Body)
	}
	var got shared.Session
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Name != "newproj" || got.Server != "server-a" {
		t.Fatalf("session %+v", got)
	}
	if len(got.Windows) != 1 || len(got.Windows[0].Panes) != 1 || got.Windows[0].Panes[0].ID != "%9" {
		t.Fatalf("re-listed session missing pane id: %+v", got)
	}
	e, ok := sink.find("session.create")
	if !ok {
		t.Fatal("no session.create audit row")
	}
	if e.Resource != shared.SessionID("server-a", "default", "newproj") {
		t.Fatalf("audit resource %q", e.Resource)
	}
	if e.Result != "allow" || !strings.Contains(e.Meta, "newproj") {
		t.Fatalf("audit row %+v", e)
	}
}

// TestServerCreateSessionEmptyTargetAuditsDefault: an empty ?target= is normalized
// to "default" so the audit resource keys on a concrete label (not session:id//name).
func TestServerCreateSessionEmptyTargetAuditsDefault(t *testing.T) {
	ts := createFakeAgent(t, "tok-a", http.StatusOK, createListBody)
	defer ts.Close()
	d := depsWith(db.Server{ID: "server-a", URL: ts.URL, Bearer: "tok-a", Status: "active"})
	sink := &captureSink{}
	d.Audit = audit.NewRecorder(sink)

	r, w := createReq(t, "server-a", "", `{"name":"newproj"}`) // no ?target=
	d.ServerCreateSessionHandler()(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("code %d body %s", w.Code, w.Body)
	}
	e, ok := sink.find("session.create")
	if !ok {
		t.Fatal("no session.create audit row")
	}
	if e.Resource != shared.SessionID("server-a", "default", "newproj") {
		t.Fatalf("empty target must audit as default, got resource %q", e.Resource)
	}
}

// TestServerCreateSessionRelistFailureStill201: when the agent creates the session
// but the post-create re-list fails, the create still succeeded — the handler must
// return 201 (with the bare session) and still audit, NOT 502.
func TestServerCreateSessionRelistFailureStill201(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-a" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.Method {
		case http.MethodPost:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"name":"newproj"}`))
		case http.MethodGet:
			w.WriteHeader(http.StatusInternalServerError) // re-list fails
		}
	}))
	defer ts.Close()
	d := depsWith(db.Server{ID: "server-a", URL: ts.URL, Bearer: "tok-a", Status: "active"})
	sink := &captureSink{}
	d.Audit = audit.NewRecorder(sink)

	r, w := createReq(t, "server-a", "default", `{"name":"newproj"}`)
	d.ServerCreateSessionHandler()(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("re-list failure after a successful create must still be 201, got %d body %s", w.Code, w.Body)
	}
	var got shared.Session
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil || got.Name != "newproj" {
		t.Fatalf("bare session: err=%v got=%+v", err, got)
	}
	if _, ok := sink.find("session.create"); !ok {
		t.Fatal("create must still be audited even when re-list fails")
	}
}

// TestServerCreateSessionBadNameIs400: an invalid name is rejected at the browser
// boundary BEFORE the agent is contacted (the agent URL is unreachable, proving
// no agent call is made).
func TestServerCreateSessionBadNameIs400(t *testing.T) {
	d := depsWith(db.Server{ID: "server-a", URL: "http://127.0.0.1:0", Bearer: "tok-a", Status: "active"})
	r, w := createReq(t, "server-a", "", `{"name":"-bad name"}`)
	d.ServerCreateSessionHandler()(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad name must be 400, got %d body %s", w.Code, w.Body)
	}
}

// TestServerCreateSessionMalformedBodyIs400: a non-JSON body → 400.
func TestServerCreateSessionMalformedBodyIs400(t *testing.T) {
	d := depsWith(db.Server{ID: "server-a", URL: "http://127.0.0.1:0", Bearer: "tok-a", Status: "active"})
	r, w := createReq(t, "server-a", "", `{not json`)
	d.ServerCreateSessionHandler()(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("malformed body must be 400, got %d", w.Code)
	}
}

// TestServerCreateSessionCommandRejectedIs400: a non-empty command is rejected at
// the hub (shell-only v1) WITHOUT contacting the agent (its URL is unreachable).
func TestServerCreateSessionCommandRejectedIs400(t *testing.T) {
	d := depsWith(db.Server{ID: "server-a", URL: "http://127.0.0.1:0", Bearer: "tok-a", Status: "active"})
	r, w := createReq(t, "server-a", "", `{"name":"ok","command":"rm -rf /"}`)
	d.ServerCreateSessionHandler()(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("non-empty command must be 400, got %d body %s", w.Code, w.Body)
	}
}

// TestServerCreateSessionExistsIs409: the agent's 409 (duplicate) maps to a hub 409.
func TestServerCreateSessionExistsIs409(t *testing.T) {
	ts := createFakeAgent(t, "tok-a", http.StatusConflict, "")
	defer ts.Close()
	d := depsWith(db.Server{ID: "server-a", URL: ts.URL, Bearer: "tok-a", Status: "active"})
	r, w := createReq(t, "server-a", "", `{"name":"dup"}`)
	d.ServerCreateSessionHandler()(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate must be 409, got %d", w.Code)
	}
}

// TestServerCreateSessionInvalidFromAgentIs400: the agent's 400 (cwd/command
// rejected at the exec boundary) maps to a hub 400.
func TestServerCreateSessionInvalidFromAgentIs400(t *testing.T) {
	ts := createFakeAgent(t, "tok-a", http.StatusBadRequest, "")
	defer ts.Close()
	d := depsWith(db.Server{ID: "server-a", URL: ts.URL, Bearer: "tok-a", Status: "active"})
	r, w := createReq(t, "server-a", "", `{"name":"ok"}`)
	d.ServerCreateSessionHandler()(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("agent-invalid must be 400, got %d", w.Code)
	}
}

// TestServerCreateSessionAgentTransportErrorIs502: an unreachable agent → 502.
func TestServerCreateSessionAgentTransportErrorIs502(t *testing.T) {
	d := depsWith(db.Server{ID: "server-a", URL: "http://127.0.0.1:0", Bearer: "tok-a", Status: "active"})
	r, w := createReq(t, "server-a", "", `{"name":"ok"}`)
	d.ServerCreateSessionHandler()(w, r)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("transport error must be 502, got %d", w.Code)
	}
}

// TestServerCreateSessionUnknownServerIs404: a valid name on an unknown server → 404.
func TestServerCreateSessionUnknownServerIs404(t *testing.T) {
	d := depsWith(db.Server{ID: "server-a", URL: "http://x", Bearer: "tok-a", Status: "active"})
	r, w := createReq(t, "no-such", "", `{"name":"ok"}`)
	d.ServerCreateSessionHandler()(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown server must be 404, got %d", w.Code)
	}
}

// TestServerCreateSessionDeniesEmptyPrincipal: no principal → 403 (authz chokepoint).
func TestServerCreateSessionDeniesEmptyPrincipal(t *testing.T) {
	d := depsWith(db.Server{ID: "server-a", URL: "http://x", Bearer: "tok-a", Status: "active"})
	r := withPrincipal(httptest.NewRequest("POST", "/api/v1/servers/server-a/sessions", strings.NewReader(`{"name":"ok"}`)), authz.Principal{})
	r.SetPathValue("id", "server-a")
	w := httptest.NewRecorder()
	d.ServerCreateSessionHandler()(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("empty principal must be 403, got %d", w.Code)
	}
}

// TestServerCreateSessionRequiresCSRF: through the full router, a POST without an
// X-CSRF-Token is rejected (403) by RequireAuth before reaching the handler.
func TestServerCreateSessionRequiresCSRF(t *testing.T) {
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("agent must not be called when CSRF fails (method=%s)", r.Method)
	}))
	defer agent.Close()
	h, d := buildHub(t, agent.URL, "agent-tok")
	defer d.Close()
	cookie, _ := login(t, h)

	req := httptest.NewRequest("POST", "/api/v1/servers/server-a/sessions", strings.NewReader(`{"name":"x"}`))
	req.AddCookie(cookie)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF must be 403, got %d body %s", w.Code, w.Body)
	}
}

// TestServerCreateSessionEndToEnd: through the full router (route wired, CSRF
// passes), a valid create returns 201 with the re-listed session and persists a
// session.create audit row in the real DB.
func TestServerCreateSessionEndToEnd(t *testing.T) {
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer agent-tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"name":"newproj"}`))
			return
		}
		_, _ = w.Write([]byte(createListBody))
	}))
	defer agent.Close()
	h, d := buildHub(t, agent.URL, "agent-tok")
	defer d.Close()
	cookie, csrf := login(t, h)

	req := httptest.NewRequest("POST", "/api/v1/servers/server-a/sessions?target=default", strings.NewReader(`{"name":"newproj"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d body %s", w.Code, w.Body)
	}
	var got shared.Session
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Name != "newproj" || len(got.Windows) != 1 || got.Windows[0].Panes[0].ID != "%9" {
		t.Fatalf("session %+v", got)
	}
	rows, err := d.Recent(context.Background(), 20)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	found := false
	for _, e := range rows {
		if e.Action == "session.create" && strings.Contains(e.Meta, "newproj") {
			found = true
		}
	}
	if !found {
		t.Fatal("no session.create audit row persisted")
	}
}

func fakeAgentSrv(t *testing.T, token string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`{"sessions":[{"name":"proj","server":"x","target":"default","cwd":"/p","command":"claude","windows":[]}]}`))
	}))
}

func depsWith(srv db.Server) Deps {
	d := testDeps(registry.New(fakeStore{servers: map[string]db.Server{srv.ID: srv}}))
	d.Agent = registry.NewClient(2 * time.Second)
	return d
}

func TestServerSessionsReturnsStampedList(t *testing.T) {
	ts := fakeAgentSrv(t, "tok-a")
	defer ts.Close()
	d := depsWith(db.Server{ID: "server-a", URL: ts.URL, Bearer: "tok-a", Status: "active"})
	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/servers/server-a/sessions", nil), authz.Principal{ID: "u1"})
	r.SetPathValue("id", "server-a")
	w := httptest.NewRecorder()
	d.ServerSessionsHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("code %d body %s", w.Code, w.Body)
	}
	var got []shared.Session
	json.NewDecoder(w.Body).Decode(&got)
	if len(got) != 1 || got[0].Name != "proj" || got[0].Server != "server-a" {
		t.Fatalf("got %+v", got)
	}
}

func TestServerSessionsAgentErrorIs502(t *testing.T) {
	ts := fakeAgentSrv(t, "tok-a")
	defer ts.Close()
	d := depsWith(db.Server{ID: "server-a", URL: ts.URL, Bearer: "WRONG", Status: "active"}) // agent will 401
	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/servers/server-a/sessions", nil), authz.Principal{ID: "u1"})
	r.SetPathValue("id", "server-a")
	w := httptest.NewRecorder()
	d.ServerSessionsHandler()(w, r)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("agent error must be 502, got %d", w.Code)
	}
}

func TestServerSessionsUnknownServerIs404(t *testing.T) {
	d := depsWith(db.Server{ID: "server-a", URL: "http://x", Bearer: "tok-a", Status: "active"})
	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/servers/no-such/sessions", nil), authz.Principal{ID: "u1"})
	r.SetPathValue("id", "no-such")
	w := httptest.NewRecorder()
	d.ServerSessionsHandler()(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown server must be 404, got %d", w.Code)
	}
}

func TestSessionDetailUnknownServerIs404(t *testing.T) {
	d := depsWith(db.Server{ID: "server-a", URL: "http://x", Bearer: "tok-a", Status: "active"})
	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/servers/no-such/sessions/whatever", nil), authz.Principal{ID: "u1"})
	r.SetPathValue("id", "no-such")
	r.SetPathValue("name", "whatever")
	w := httptest.NewRecorder()
	d.SessionDetailHandler()(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown server must be 404, got %d", w.Code)
	}
}

func TestSessionDetailFoundAndNotFound(t *testing.T) {
	ts := fakeAgentSrv(t, "tok-a")
	defer ts.Close()
	d := depsWith(db.Server{ID: "server-a", URL: ts.URL, Bearer: "tok-a", Status: "active"})

	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/servers/server-a/sessions/proj", nil), authz.Principal{ID: "u1"})
	r.SetPathValue("id", "server-a")
	r.SetPathValue("name", "proj")
	w := httptest.NewRecorder()
	d.SessionDetailHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("found code %d", w.Code)
	}

	r2 := withPrincipal(httptest.NewRequest("GET", "/api/v1/servers/server-a/sessions/ghost", nil), authz.Principal{ID: "u1"})
	r2.SetPathValue("id", "server-a")
	r2.SetPathValue("name", "ghost")
	w2 := httptest.NewRecorder()
	d.SessionDetailHandler()(w2, r2)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("missing session must be 404, got %d", w2.Code)
	}
}

func TestSessionDetailHonorsTargetQuery(t *testing.T) {
	var gotTarget string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-a" {
			w.WriteHeader(401)
			return
		}
		gotTarget = r.URL.Query().Get("target")
		w.Write([]byte(`{"sessions":[{"name":"proj","server":"x","target":"work","cwd":"/p","command":"claude","windows":[]}]}`))
	}))
	defer ts.Close()
	d := depsWith(db.Server{ID: "server-a", URL: ts.URL, Bearer: "tok-a", Status: "active"})

	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/servers/server-a/sessions/proj?target=work", nil), authz.Principal{ID: "u1"})
	r.SetPathValue("id", "server-a")
	r.SetPathValue("name", "proj")
	w := httptest.NewRecorder()
	d.SessionDetailHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("code %d body %s", w.Code, w.Body)
	}
	if gotTarget != "work" {
		t.Fatalf("agent saw target %q, want work", gotTarget)
	}
}

// TestServerSessionsOverlaysProjectionState: the projection's Global state wins
// over the agent's inline state when the projection has an entry for the session.
func TestServerSessionsOverlaysProjectionState(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		// agent reports session "api" with state=unknown (pre-poll inline value)
		w.Write([]byte(`{"sessions":[{"name":"api","server":"s","target":"","state":"unknown","cwd":"/","command":"claude","windows":[]}]}`))
	}))
	defer ts.Close()

	proj := state.NewProjection()
	proj.Set(state.SessionView{ServerID: "s", Session: "api", Global: shared.StateBlocked})

	d := depsWith(db.Server{ID: "s", URL: ts.URL, Bearer: "tok", Status: "active"})
	d.Proj = proj

	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/servers/s/sessions", nil), authz.Principal{ID: "u1"})
	r.SetPathValue("id", "s")
	w := httptest.NewRecorder()
	d.ServerSessionsHandler()(w, r)

	if w.Code != 200 {
		t.Fatalf("code %d body %s", w.Code, w.Body)
	}
	var got []shared.Session
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].State != shared.StateBlocked {
		t.Fatalf("overlay: want state=%q, got %+v", shared.StateBlocked, got)
	}
}

// TestServerSessionsOverlaysProjectionStateBySessionTarget: when the projection holds
// a session keyed under a non-empty target label (the agent-reported session Target),
// the overlay must match on that label — not on a hardcoded "".
func TestServerSessionsOverlaysProjectionStateBySessionTarget(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		// agent reports session with target="default" and state=unknown (pre-poll inline)
		w.Write([]byte(`{"sessions":[{"name":"api","server":"s","target":"default","state":"unknown","cwd":"/","command":"claude","windows":[]}]}`))
	}))
	defer ts.Close()

	proj := state.NewProjection()
	// Store under the non-empty target matching what the agent returns.
	proj.Set(state.SessionView{ServerID: "s", Target: "default", Session: "api", Global: shared.StateWorking})

	d := depsWith(db.Server{ID: "s", URL: ts.URL, Bearer: "tok", Status: "active"})
	d.Proj = proj

	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/servers/s/sessions", nil), authz.Principal{ID: "u1"})
	r.SetPathValue("id", "s")
	w := httptest.NewRecorder()
	d.ServerSessionsHandler()(w, r)

	if w.Code != 200 {
		t.Fatalf("code %d body %s", w.Code, w.Body)
	}
	var got []shared.Session
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].State != shared.StateWorking {
		t.Fatalf("overlay by session target: want state=%q, got %+v", shared.StateWorking, got)
	}
}

// TestServerSessionsPrePollFallback: when the projection has no entry for a session
// the agent's inline state is kept unchanged (pre-poll fallback).
func TestServerSessionsPrePollFallback(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		// agent reports session "api" with state=working
		w.Write([]byte(`{"sessions":[{"name":"api","server":"s","target":"","state":"working","cwd":"/","command":"claude","windows":[]}]}`))
	}))
	defer ts.Close()

	d := depsWith(db.Server{ID: "s", URL: ts.URL, Bearer: "tok", Status: "active"})
	d.Proj = state.NewProjection() // empty projection — no entry for "api"

	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/servers/s/sessions", nil), authz.Principal{ID: "u1"})
	r.SetPathValue("id", "s")
	w := httptest.NewRecorder()
	d.ServerSessionsHandler()(w, r)

	if w.Code != 200 {
		t.Fatalf("code %d body %s", w.Code, w.Body)
	}
	var got []shared.Session
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].State != shared.StateWorking {
		t.Fatalf("fallback: want state=%q, got %+v", shared.StateWorking, got)
	}
}

// TestServerSessionsSeenProjection_DonePlusSeen_YieldsIdle: a session with
// projection Global=done and a seen row (LastFocusedAt >= LatestReceivedAt) for
// the requesting principal must render state=idle (B3 seen projection).
func TestServerSessionsSeenProjection_DonePlusSeen_YieldsIdle(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`{"sessions":[{"name":"api","server":"s","target":"","state":"unknown","cwd":"/","command":"claude","windows":[]}]}`))
	}))
	defer ts.Close()

	proj := state.NewProjection()
	proj.Set(state.SessionView{
		ServerID:         "s",
		Target:           "",
		Session:          "api",
		Global:           shared.StateDone,
		LatestReceivedAt: "2026-01-01T10:00:00.000",
	})

	seenStore := &fakeSeenStore{}
	_ = seenStore.UpsertSeen(context.Background(), db.PrincipalSeen{
		PrincipalID:   "u1",
		ServerID:      "s",
		TargetID:      "",
		Session:       "api",
		LastFocusedAt: "2026-01-01T10:00:01.000", // after LatestReceivedAt → done→idle
	})

	d := depsWith(db.Server{ID: "s", URL: ts.URL, Bearer: "tok", Status: "active"})
	d.Proj = proj
	d.Seen = seenStore

	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/servers/s/sessions", nil), authz.Principal{ID: "u1"})
	r.SetPathValue("id", "s")
	w := httptest.NewRecorder()
	d.ServerSessionsHandler()(w, r)

	if w.Code != 200 {
		t.Fatalf("code %d body %s", w.Code, w.Body)
	}
	var got []shared.Session
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].State != shared.StateIdle {
		t.Fatalf("seen projection: want state=%q, got %+v", shared.StateIdle, got)
	}
}

// TestServerSessionsSeenProjection_DoneNoSeen_YieldsDone: a session with
// projection Global=done and NO seen row for the requesting principal must
// render state=done (pass-through — no seen projection applied).
func TestServerSessionsSeenProjection_DoneNoSeen_YieldsDone(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`{"sessions":[{"name":"api","server":"s","target":"","state":"unknown","cwd":"/","command":"claude","windows":[]}]}`))
	}))
	defer ts.Close()

	proj := state.NewProjection()
	proj.Set(state.SessionView{
		ServerID:         "s",
		Target:           "",
		Session:          "api",
		Global:           shared.StateDone,
		LatestReceivedAt: "2026-01-01T10:00:00.000",
	})

	d := depsWith(db.Server{ID: "s", URL: ts.URL, Bearer: "tok", Status: "active"})
	d.Proj = proj
	d.Seen = &fakeSeenStore{} // empty — no seen row

	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/servers/s/sessions", nil), authz.Principal{ID: "u1"})
	r.SetPathValue("id", "s")
	w := httptest.NewRecorder()
	d.ServerSessionsHandler()(w, r)

	if w.Code != 200 {
		t.Fatalf("code %d body %s", w.Code, w.Body)
	}
	var got []shared.Session
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].State != shared.StateDone {
		t.Fatalf("no-seen passthrough: want state=%q, got %+v", shared.StateDone, got)
	}
}

// TestServerSessionsSeenProjection_GetSeenError_PassesThroughGlobal: a GetSeen
// error is non-fatal — the projection's global state passes through unmasked
// (no 500, no idle masking) so a transient seen-store failure can't hide a done.
func TestServerSessionsSeenProjection_GetSeenError_PassesThroughGlobal(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`{"sessions":[{"name":"api","server":"s","target":"","state":"unknown","cwd":"/","command":"claude","windows":[]}]}`))
	}))
	defer ts.Close()

	proj := state.NewProjection()
	proj.Set(state.SessionView{
		ServerID:         "s",
		Target:           "",
		Session:          "api",
		Global:           shared.StateDone,
		LatestReceivedAt: "2026-01-01T10:00:00.000",
	})

	d := depsWith(db.Server{ID: "s", URL: ts.URL, Bearer: "tok", Status: "active"})
	d.Proj = proj
	d.Seen = &fakeSeenStore{getErr: errors.New("seen store unavailable")}

	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/servers/s/sessions", nil), authz.Principal{ID: "u1"})
	r.SetPathValue("id", "s")
	w := httptest.NewRecorder()
	d.ServerSessionsHandler()(w, r)

	if w.Code != 200 {
		t.Fatalf("GetSeen error must not 500: code %d body %s", w.Code, w.Body)
	}
	var got []shared.Session
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].State != shared.StateDone {
		t.Fatalf("error passthrough: want state=%q, got %+v", shared.StateDone, got)
	}
}
