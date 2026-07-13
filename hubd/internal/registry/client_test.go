package registry

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agentmon/hubd/internal/db"
	"agentmon/shared"
)

func fakeAgent(t *testing.T, wantToken string) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/sessions", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+wantToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"sessions":[{"name":"proj","server":"WRONG","target":"default","cwd":"/home/dev/proj","command":"claude","windows":[]}]}`))
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	return httptest.NewServer(mux)
}

func TestClientSessionsStampsServerID(t *testing.T) {
	ts := fakeAgent(t, "tok-a")
	defer ts.Close()
	c := NewClient(2 * time.Second)
	srv := db.Server{ID: "server-a", URL: ts.URL, Bearer: "tok-a"}
	var got []shared.Session
	var err error
	got, err = c.Sessions(context.Background(), srv, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "proj" || got[0].Server != "server-a" {
		t.Fatalf("sessions: %+v", got)
	}
}

func TestClientTeardownWorktreePostsAndSwallows404(t *testing.T) {
	var gotPath, gotBranch, gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		var req shared.WorktreeTeardownRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotBranch = req.Branch
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	c := NewClient(2 * time.Second)
	if err := c.TeardownWorktree(context.Background(), db.Server{ID: "s", URL: ts.URL, Bearer: "b"}, "default", "/w", "epic/1-x"); err != nil {
		t.Fatalf("TeardownWorktree: %v", err)
	}
	if gotPath != "/worktrees/teardown" || gotBranch != "epic/1-x" || gotAuth != "Bearer b" {
		t.Fatalf("path=%q branch=%q auth=%q", gotPath, gotBranch, gotAuth)
	}

	// A 404 (agent predates the endpoint) must be swallowed as success.
	old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }))
	defer old.Close()
	if err := c.TeardownWorktree(context.Background(), db.Server{ID: "s2", URL: old.URL}, "", "/w", "epic/1-x"); err != nil {
		t.Fatalf("404 must be swallowed, got %v", err)
	}
}

func TestClientSessionsBadTokenErrors(t *testing.T) {
	ts := fakeAgent(t, "tok-a")
	defer ts.Close()
	c := NewClient(2 * time.Second)
	srv := db.Server{ID: "server-a", URL: ts.URL, Bearer: "WRONG"}
	if _, err := c.Sessions(context.Background(), srv, ""); err == nil {
		t.Fatal("bad token must error")
	}
}

func TestClientSessionsMalformedJSONErrors(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{not json`))
	}))
	defer ts.Close()
	c := NewClient(2 * time.Second)
	if _, err := c.Sessions(context.Background(), db.Server{ID: "s", URL: ts.URL, Bearer: "t"}, ""); err == nil {
		t.Fatal("malformed json must error")
	}
}

func TestClientHealth(t *testing.T) {
	ts := fakeAgent(t, "tok-a")
	defer ts.Close()
	c := NewClient(2 * time.Second)
	if !c.Health(context.Background(), db.Server{URL: ts.URL}) {
		t.Fatal("healthy agent must report true")
	}
	ts.Close()
	if c.Health(context.Background(), db.Server{URL: ts.URL}) {
		t.Fatal("dead agent must report false")
	}
}

func TestClientStateDecodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/state" || r.Header.Get("Authorization") != "Bearer b" {
			w.WriteHeader(401)
			return
		}
		json.NewEncoder(w).Encode(shared.AgentState{Panes: []shared.PaneState{{Pane: "%0", State: shared.StateBlocked, DoneSeq: 2}}})
	}))
	defer srv.Close()
	got, err := NewClient(time.Second).State(context.Background(), db.Server{ID: "s", URL: srv.URL, Bearer: "b"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Panes) != 1 || got.Panes[0].State != shared.StateBlocked || got.Panes[0].DoneSeq != 2 {
		t.Fatalf("got %+v", got.Panes)
	}
}

func TestClientStateUnsupportedOn404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }))
	defer srv.Close()
	_, err := NewClient(time.Second).State(context.Background(), db.Server{ID: "s", URL: srv.URL, Bearer: "b"}, "")
	if !errors.Is(err, ErrStateUnsupported) {
		t.Fatalf("err = %v, want ErrStateUnsupported", err)
	}
}

func TestClientCreateSessionOK(t *testing.T) {
	var gotMethod, gotPath, gotTarget, gotAuth, gotCT string
	var gotBody shared.CreateSessionRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotTarget = r.URL.Query().Get("target")
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(shared.CreateSessionResponse{Name: "proj"})
	}))
	defer ts.Close()
	c := NewClient(2 * time.Second)
	srv := db.Server{ID: "server-a", URL: ts.URL, Bearer: "tok-a"}
	resp, err := c.CreateSession(context.Background(), srv, "host1", shared.CreateSessionRequest{Name: "proj", Cwd: "/home/dev/proj"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Name != "proj" {
		t.Fatalf("resp = %+v", resp)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q", gotMethod)
	}
	if gotPath != "/sessions" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotTarget != "host1" {
		t.Fatalf("target = %q", gotTarget)
	}
	if gotAuth != "Bearer tok-a" {
		t.Fatalf("auth = %q", gotAuth)
	}
	if gotCT != "application/json" {
		t.Fatalf("content-type = %q", gotCT)
	}
	if gotBody.Name != "proj" || gotBody.Cwd != "/home/dev/proj" {
		t.Fatalf("body = %+v", gotBody)
	}
}

func TestClientCreateSessionInvalidOn400(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer ts.Close()
	c := NewClient(2 * time.Second)
	_, err := c.CreateSession(context.Background(), db.Server{ID: "s", URL: ts.URL, Bearer: "t"}, "", shared.CreateSessionRequest{Name: "x"})
	if !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("err = %v, want ErrInvalidSession", err)
	}
}

func TestClientCreateSessionExistsOn409(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	defer ts.Close()
	c := NewClient(2 * time.Second)
	_, err := c.CreateSession(context.Background(), db.Server{ID: "s", URL: ts.URL, Bearer: "t"}, "", shared.CreateSessionRequest{Name: "x"})
	if !errors.Is(err, ErrSessionExists) {
		t.Fatalf("err = %v, want ErrSessionExists", err)
	}
}

func TestClientCreateSessionWrapsServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()
	c := NewClient(2 * time.Second)
	_, err := c.CreateSession(context.Background(), db.Server{ID: "s", URL: ts.URL, Bearer: "t"}, "", shared.CreateSessionRequest{Name: "x"})
	if err == nil {
		t.Fatal("5xx must error")
	}
	if errors.Is(err, ErrInvalidSession) || errors.Is(err, ErrSessionExists) {
		t.Fatalf("5xx must not map to a sentinel: %v", err)
	}
}

func TestClientCreateSessionTransportError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	ts.Close() // dead server
	c := NewClient(500 * time.Millisecond)
	_, err := c.CreateSession(context.Background(), db.Server{ID: "s", URL: ts.URL, Bearer: "t"}, "", shared.CreateSessionRequest{Name: "x"})
	if err == nil {
		t.Fatal("transport failure must error")
	}
}

func TestClientRenameSessionOK(t *testing.T) {
	var gotMethod, gotPath, gotTarget, gotAuth string
	var gotBody shared.RenameSessionRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotTarget = r.Method, r.URL.Path, r.URL.Query().Get("target")
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	c := NewClient(2 * time.Second)
	if err := c.RenameSession(context.Background(), db.Server{ID: "server-a", URL: ts.URL, Bearer: "tok-a"}, "host1", "old", "new"); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPost || gotPath != "/sessions/rename" || gotTarget != "host1" || gotAuth != "Bearer tok-a" {
		t.Fatalf("req: %s %s ?target=%s auth=%q", gotMethod, gotPath, gotTarget, gotAuth)
	}
	if gotBody.From != "old" || gotBody.To != "new" {
		t.Fatalf("body = %+v", gotBody)
	}
}

func TestClientRenameSessionErrorMapping(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{http.StatusBadRequest, ErrInvalidSession},
		{http.StatusConflict, ErrSessionExists},
		{http.StatusNotFound, ErrNoSession},
	}
	for _, tc := range cases {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status) }))
		err := NewClient(2*time.Second).RenameSession(context.Background(), db.Server{ID: "s", URL: ts.URL, Bearer: "t"}, "", "old", "new")
		ts.Close()
		if !errors.Is(err, tc.want) {
			t.Fatalf("status %d → err %v, want %v", tc.status, err, tc.want)
		}
	}
}

func TestDrainReports(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/orchestrator/reports" || q.Get("ack") != "7" || q.Get("instance") != "i1" || q.Get("target") != "tgt" {
			w.WriteHeader(404)
			return
		}
		if r.Header.Get("Authorization") != "Bearer btok" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"instance":"i1","cursor":9,"reports":[{"repo":"o/r","epic":16,"stage":"implementing","session":"epic-16","ts":"t1"}]}`))
	}))
	defer srv.Close()
	c := NewClient(time.Second)
	got, err := c.DrainReports(context.Background(), db.Server{URL: srv.URL, Bearer: "btok"}, "tgt", "i1", 7)
	if err != nil || got.Instance != "i1" || got.Cursor != 9 || len(got.Reports) != 1 || got.Reports[0].Stage != shared.EpicImplementing {
		t.Fatalf("got %+v err=%v", got, err)
	}
}

func TestDrainReportsUnknownTarget404Errors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"error":"unknown target"}`))
	}))
	defer srv.Close()
	c := NewClient(time.Second)
	if _, err := c.DrainReports(context.Background(), db.Server{URL: srv.URL, Bearer: "b"}, "typo", "", 0); err == nil {
		t.Fatal("a new agent's unknown-target 404 must surface as an error, not drain as permanent silence")
	}
}

func TestDrainReportsOldAgent404(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	c := NewClient(time.Second)
	got, err := c.DrainReports(context.Background(), db.Server{URL: srv.URL, Bearer: "b"}, "", "", 0)
	if err != nil || got.Instance != "" || len(got.Reports) != 0 {
		t.Fatalf("404 must be tolerated as an empty batch: got %+v err=%v", got, err)
	}
}
