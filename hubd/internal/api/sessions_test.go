package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agentmon/hubd/internal/authz"
	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/registry"
	"agentmon/hubd/internal/state"
	"agentmon/shared"
)

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
