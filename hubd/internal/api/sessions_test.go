package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agentmon/hubd/internal/authz"
	"agentmon/hubd/internal/config"
	"agentmon/hubd/internal/registry"
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

func depsWith(srv config.Server) Deps {
	d := testDeps(registry.New([]config.Server{srv}))
	d.Agent = registry.NewClient(2 * time.Second)
	return d
}

func TestServerSessionsReturnsStampedList(t *testing.T) {
	ts := fakeAgentSrv(t, "tok-a")
	defer ts.Close()
	d := depsWith(config.Server{ID: "server-a", URL: ts.URL, Token: "tok-a"})
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
	d := depsWith(config.Server{ID: "server-a", URL: ts.URL, Token: "WRONG"}) // agent will 401
	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/servers/server-a/sessions", nil), authz.Principal{ID: "u1"})
	r.SetPathValue("id", "server-a")
	w := httptest.NewRecorder()
	d.ServerSessionsHandler()(w, r)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("agent error must be 502, got %d", w.Code)
	}
}

func TestServerSessionsUnknownServerIs404(t *testing.T) {
	d := depsWith(config.Server{ID: "server-a", URL: "http://x", Token: "tok-a"})
	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/servers/no-such/sessions", nil), authz.Principal{ID: "u1"})
	r.SetPathValue("id", "no-such")
	w := httptest.NewRecorder()
	d.ServerSessionsHandler()(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown server must be 404, got %d", w.Code)
	}
}

func TestSessionDetailUnknownServerIs404(t *testing.T) {
	d := depsWith(config.Server{ID: "server-a", URL: "http://x", Token: "tok-a"})
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
	d := depsWith(config.Server{ID: "server-a", URL: ts.URL, Token: "tok-a"})

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
