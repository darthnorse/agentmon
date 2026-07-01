package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// createTestCfg builds an agent config whose default target carries socket and
// whose session_dirs allow-list is rooted at dir (a t.TempDir()).
func createTestCfg(dir string) config.Config {
	return config.Config{
		ServerID:    "server-a",
		Targets:     []config.Target{{Label: "default", SocketName: "sock-1"}},
		SessionDirs: []string{dir},
	}
}

func TestCreateSessionHandlerValid(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "proj")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	wantCwd, err := filepath.EvalSymlinks(sub)
	if err != nil {
		t.Fatal(err)
	}

	var gotSocket, gotName, gotCwd string
	create := func(ctx context.Context, socket, name, cwd string) error {
		gotSocket, gotName, gotCwd = socket, name, cwd
		return nil
	}
	h := CreateSessionHandler(createTestCfg(dir), create)

	body := `{"name":"proj","cwd":"` + sub + `"}`
	req := httptest.NewRequest(http.MethodPost, "/sessions?target=default", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("code %d: %s", rr.Code, rr.Body.String())
	}
	var resp shared.CreateSessionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, rr.Body.String())
	}
	if resp.Name != "proj" {
		t.Fatalf("resp.Name = %q", resp.Name)
	}
	if gotSocket != "sock-1" || gotName != "proj" || gotCwd != wantCwd {
		t.Fatalf("creator got socket=%q name=%q cwd=%q (want sock-1/proj/%s)", gotSocket, gotName, gotCwd, wantCwd)
	}
}

func TestCreateSessionHandlerEmptyCwdDefaultsToFirstRoot(t *testing.T) {
	dir := t.TempDir()
	wantCwd, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	var gotCwd string
	create := func(ctx context.Context, socket, name, cwd string) error {
		gotCwd = cwd
		return nil
	}
	h := CreateSessionHandler(createTestCfg(dir), create)
	req := httptest.NewRequest(http.MethodPost, "/sessions?target=default", strings.NewReader(`{"name":"proj"}`))
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("code %d: %s", rr.Code, rr.Body.String())
	}
	if gotCwd != wantCwd {
		t.Fatalf("cwd = %q, want default root %q", gotCwd, wantCwd)
	}
}

func TestCreateSessionHandlerBadName400(t *testing.T) {
	dir := t.TempDir()
	create := func(ctx context.Context, socket, name, cwd string) error {
		t.Fatal("creator must not be called for an invalid name")
		return nil
	}
	h := CreateSessionHandler(createTestCfg(dir), create)
	for _, name := range []string{"", "-leading", "has space", "a/b", "a.b", "a:b"} {
		body := `{"name":"` + name + `"}`
		req := httptest.NewRequest(http.MethodPost, "/sessions?target=default", strings.NewReader(body))
		rr := httptest.NewRecorder()
		h(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("name %q: code %d, want 400", name, rr.Code)
		}
	}
}

func TestCreateSessionHandlerCommandRejected400(t *testing.T) {
	dir := t.TempDir()
	create := func(ctx context.Context, socket, name, cwd string) error {
		t.Fatal("creator must not be called when a command is supplied")
		return nil
	}
	h := CreateSessionHandler(createTestCfg(dir), create)
	body := `{"name":"proj","command":"rm -rf /"}`
	req := httptest.NewRequest(http.MethodPost, "/sessions?target=default", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code %d, want 400", rr.Code)
	}
}

func TestCreateSessionHandlerCwdOutsideRoot400(t *testing.T) {
	dir := t.TempDir()
	create := func(ctx context.Context, socket, name, cwd string) error {
		t.Fatal("creator must not be called for a cwd outside the allow-list")
		return nil
	}
	h := CreateSessionHandler(createTestCfg(dir), create)
	body := `{"name":"proj","cwd":"/etc"}`
	req := httptest.NewRequest(http.MethodPost, "/sessions?target=default", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code %d, want 400", rr.Code)
	}
}

func TestCreateSessionHandlerUnknownTarget404(t *testing.T) {
	dir := t.TempDir()
	create := func(ctx context.Context, socket, name, cwd string) error {
		t.Fatal("creator must not be called for an unknown target")
		return nil
	}
	h := CreateSessionHandler(createTestCfg(dir), create)
	req := httptest.NewRequest(http.MethodPost, "/sessions?target=ghost", strings.NewReader(`{"name":"proj"}`))
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("code %d, want 404", rr.Code)
	}
}

func TestCreateSessionHandlerDuplicate409(t *testing.T) {
	dir := t.TempDir()
	create := func(ctx context.Context, socket, name, cwd string) error {
		return tmux.ErrSessionExists
	}
	h := CreateSessionHandler(createTestCfg(dir), create)
	req := httptest.NewRequest(http.MethodPost, "/sessions?target=default", strings.NewReader(`{"name":"proj"}`))
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("code %d, want 409", rr.Code)
	}
}

func TestCreateSessionHandlerCreateError500(t *testing.T) {
	dir := t.TempDir()
	create := func(ctx context.Context, socket, name, cwd string) error {
		return errors.New("tmux boom")
	}
	h := CreateSessionHandler(createTestCfg(dir), create)
	req := httptest.NewRequest(http.MethodPost, "/sessions?target=default", strings.NewReader(`{"name":"proj"}`))
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("code %d, want 500", rr.Code)
	}
}

func TestCreateSessionHandlerBadJSON400(t *testing.T) {
	dir := t.TempDir()
	create := func(ctx context.Context, socket, name, cwd string) error {
		t.Fatal("creator must not be called for a malformed body")
		return nil
	}
	h := CreateSessionHandler(createTestCfg(dir), create)
	req := httptest.NewRequest(http.MethodPost, "/sessions?target=default", strings.NewReader(`{not json`))
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code %d, want 400", rr.Code)
	}
}

func TestCreateSessionHandlerRequiresBearer(t *testing.T) {
	dir := t.TempDir()
	create := func(ctx context.Context, socket, name, cwd string) error {
		t.Fatal("creator must not be called without a valid bearer token")
		return nil
	}
	h := RequireBearer("secret-token", CreateSessionHandler(createTestCfg(dir), create))

	// No Authorization header → 401.
	req := httptest.NewRequest(http.MethodPost, "/sessions?target=default", strings.NewReader(`{"name":"proj"}`))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("missing bearer: code %d, want 401", rr.Code)
	}
}

func renameReq(body string) (*http.Request, *httptest.ResponseRecorder) {
	return httptest.NewRequest(http.MethodPost, "/sessions/rename?target=default", strings.NewReader(body)),
		httptest.NewRecorder()
}

func TestRenameSessionHandlerValid(t *testing.T) {
	var gotSocket, gotFrom, gotTo string
	rename := func(ctx context.Context, socket, from, to string) error {
		gotSocket, gotFrom, gotTo = socket, from, to
		return nil
	}
	h := RenameSessionHandler(createTestCfg(t.TempDir()), rename)
	req, rr := renameReq(`{"from":"old","to":"new-name"}`)
	h(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("code %d: %s", rr.Code, rr.Body.String())
	}
	var resp shared.CreateSessionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil || resp.Name != "new-name" {
		t.Fatalf("resp = %+v err %v", resp, err)
	}
	if gotSocket != "sock-1" || gotFrom != "old" || gotTo != "new-name" {
		t.Fatalf("renamer got socket=%q from=%q to=%q", gotSocket, gotFrom, gotTo)
	}
}

func TestSessionsHandlerTimesOutOnHungTmux(t *testing.T) {
	old := agentTmuxTimeout
	agentTmuxTimeout = 30 * time.Millisecond
	defer func() { agentTmuxTimeout = old }()

	// A discoverer that blocks until the request context is cancelled — i.e. a hung tmux.
	slow := func(ctx context.Context, _ tmux.DiscoverOpts) ([]shared.Session, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	h := SessionsHandler(testCfg(), slow, nil)
	rr := httptest.NewRecorder()
	start := time.Now()
	h(rr, httptest.NewRequest(http.MethodGet, "/sessions?target=default", nil))
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("handler did not bound the hung discoverer: took %v", elapsed)
	}
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("want 500 on discovery timeout, got %d", rr.Code)
	}
}

func TestCreateSessionHandlerTimesOutOnHungTmux(t *testing.T) {
	old := agentTmuxTimeout
	agentTmuxTimeout = 30 * time.Millisecond
	defer func() { agentTmuxTimeout = old }()

	slow := func(ctx context.Context, _, _, _ string) error {
		<-ctx.Done()
		return ctx.Err()
	}
	h := CreateSessionHandler(testCfg(), slow)
	rr := httptest.NewRecorder()
	body := strings.NewReader(`{"name":"ok"}`)
	start := time.Now()
	h(rr, httptest.NewRequest(http.MethodPost, "/sessions?target=default", body))
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("create did not bound the hung creator: took %v", elapsed)
	}
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("want 500 on create timeout, got %d", rr.Code)
	}
}

func TestRenameSessionHandlerRejects(t *testing.T) {
	noCall := func(context.Context, string, string, string) error {
		t.Helper()
		t.Fatal("must not call renamer")
		return nil
	}
	cfg := createTestCfg(t.TempDir())
	cases := []struct {
		name, body, target string
		rename             SessionRenamer
		want               int
	}{
		{"bad to", `{"from":"old","to":"-bad name"}`, "default", noCall, http.StatusBadRequest},
		{"empty from", `{"from":"","to":"new"}`, "default", noCall, http.StatusBadRequest},
		{"unknown target", `{"from":"old","to":"new"}`, "ghost", noCall, http.StatusNotFound},
		{"duplicate", `{"from":"old","to":"taken"}`, "default", func(context.Context, string, string, string) error { return tmux.ErrSessionExists }, http.StatusConflict},
		{"no such session", `{"from":"ghost","to":"new"}`, "default", func(context.Context, string, string, string) error { return tmux.ErrNoSession }, http.StatusNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := RenameSessionHandler(cfg, c.rename)
			req := httptest.NewRequest(http.MethodPost, "/sessions/rename?target="+c.target, strings.NewReader(c.body))
			rr := httptest.NewRecorder()
			h(rr, req)
			if rr.Code != c.want {
				t.Fatalf("code %d, want %d (%s)", rr.Code, c.want, rr.Body.String())
			}
		})
	}
}
