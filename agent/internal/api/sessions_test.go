package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentmon/agent/internal/config"
	"agentmon/agent/internal/state"
	"agentmon/agent/internal/tmux"
	"agentmon/shared"
)

func testCfg() config.Config {
	return config.Config{
		ServerID: "server-a",
		Targets:  []config.Target{{Label: "default", SocketName: ""}},
	}
}

func TestSessionsHandlerReturnsTree(t *testing.T) {
	var gotOpts tmux.DiscoverOpts
	disc := func(ctx context.Context, opts tmux.DiscoverOpts) ([]shared.Session, error) {
		gotOpts = opts
		return []shared.Session{{Name: "proj", Server: "server-a", Target: "default",
			Cwd: "/home/dev/proj", Command: "zsh",
			Windows: []shared.Window{{ID: "@1", Index: "0", Name: "main",
				Panes: []shared.Pane{{ID: "%0", Command: "zsh", Cwd: "/home/dev/proj"}}}}}}, nil
	}
	h := SessionsHandler(testCfg(), disc, nil)
	req := httptest.NewRequest(http.MethodGet, "/sessions?target=default", nil)
	rr := httptest.NewRecorder()
	h(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("code %d", rr.Code)
	}
	var body shared.SessionList
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, rr.Body.String())
	}
	if len(body.Sessions) != 1 || body.Sessions[0].Name != "proj" {
		t.Fatalf("sessions = %+v", body.Sessions)
	}
	if gotOpts.ServerID != "server-a" || gotOpts.TargetLabel != "default" {
		t.Fatalf("discover opts = %+v", gotOpts)
	}
}

func TestSessionsHandlerEmptyTargetUsesDefault(t *testing.T) {
	called := false
	disc := func(ctx context.Context, opts tmux.DiscoverOpts) ([]shared.Session, error) {
		called = true
		if opts.TargetLabel != "default" {
			t.Fatalf("want default target, got %q", opts.TargetLabel)
		}
		return []shared.Session{}, nil
	}
	h := SessionsHandler(testCfg(), disc, nil)
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/sessions", nil))
	if rr.Code != http.StatusOK || !called {
		t.Fatalf("code=%d called=%v", rr.Code, called)
	}
}

func TestSessionsHandlerUnknownTarget404(t *testing.T) {
	disc := func(ctx context.Context, opts tmux.DiscoverOpts) ([]shared.Session, error) {
		t.Fatal("discover must not be called for unknown target")
		return nil, nil
	}
	h := SessionsHandler(testCfg(), disc, nil)
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/sessions?target=ghost", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("code %d, want 404", rr.Code)
	}
}

func TestSessionsHandlerDiscoveryError500(t *testing.T) {
	disc := func(ctx context.Context, opts tmux.DiscoverOpts) ([]shared.Session, error) {
		return nil, errors.New("tmux boom")
	}
	h := SessionsHandler(testCfg(), disc, nil)
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/sessions?target=default", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("code %d, want 500", rr.Code)
	}
}

func TestSessionsHandlerNilDiscoveryEncodesEmptyArray(t *testing.T) {
	disc := func(ctx context.Context, opts tmux.DiscoverOpts) ([]shared.Session, error) {
		return nil, nil // a Discoverer is not contractually required to return non-nil
	}
	h := SessionsHandler(testCfg(), disc, nil)
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/sessions?target=default", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("code %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"sessions":[]`) || strings.Contains(body, `"sessions":null`) {
		t.Fatalf("want sessions:[] not null, got %s", body)
	}
}

func TestSessionsHandlerStampsState(t *testing.T) {
	m := state.New(nil)
	m.Apply(state.Event{Target: "default", Pane: "%0", Name: "PermissionRequest"})
	disc := func(ctx context.Context, opts tmux.DiscoverOpts) ([]shared.Session, error) {
		return []shared.Session{{Name: "proj", Target: "default",
			Windows: []shared.Window{{Panes: []shared.Pane{{ID: "%0"}}}}}}, nil
	}
	h := SessionsHandler(testCfg(), disc, m)
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/sessions?target=default", nil))
	var body shared.SessionList
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Sessions[0].State != shared.StateBlocked {
		t.Fatalf("state = %q, want blocked", body.Sessions[0].State)
	}
}

func TestSessionsHandlerNilMachineUnknown(t *testing.T) {
	disc := func(ctx context.Context, opts tmux.DiscoverOpts) ([]shared.Session, error) {
		return []shared.Session{{Name: "p", Target: "default",
			Windows: []shared.Window{{Panes: []shared.Pane{{ID: "%0"}}}}}}, nil
	}
	h := SessionsHandler(testCfg(), disc, nil)
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/sessions?target=default", nil))
	var body shared.SessionList
	json.Unmarshal(rr.Body.Bytes(), &body)
	if body.Sessions[0].State != shared.StateUnknown {
		t.Fatalf("state = %q, want unknown", body.Sessions[0].State)
	}
}
