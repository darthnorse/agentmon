package hooks

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentmon/agent/internal/config"
	"agentmon/agent/internal/state"
	"agentmon/shared"
)

func testCfg() config.Config {
	return config.Config{
		HookToken: "hooktok",
		Targets:   []config.Target{{Label: "default", SocketName: ""}, {Label: "build", SocketName: "buildsock"}},
	}
}

func post(t *testing.T, h http.Handler, remote, auth, pane, tmuxEnv, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader(body))
	req.RemoteAddr = remote
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	if pane != "" {
		req.Header.Set("X-AgentMon-Pane", pane)
	}
	if tmuxEnv != "" {
		req.Header.Set("X-AgentMon-Tmux", tmuxEnv)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func handler(m *state.Machine) http.Handler {
	return RequireLoopback(RequireHookAuth("hooktok", HookHandler(testCfg(), m, nil)))
}

func TestHookValidPermissionRequest(t *testing.T) {
	m := state.New(nil)
	rr := post(t, handler(m), "127.0.0.1:5000", "Bearer hooktok", "%3",
		"/tmp/tmux-0/default,123,0",
		`{"hook_event_name":"PermissionRequest","tool_name":"Bash","session_id":"abc"}`)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("code %d, want 204", rr.Code)
	}
	if s, ok := m.Pane("default", "%3"); !ok || s != shared.StateBlocked {
		t.Fatalf("state = %q ok=%v, want blocked", s, ok)
	}
}

func TestHookSocketMapsToNamedTarget(t *testing.T) {
	m := state.New(nil)
	post(t, handler(m), "127.0.0.1:5000", "Bearer hooktok", "%0",
		"/tmp/tmux-0/buildsock,1,0", `{"hook_event_name":"Stop"}`)
	if s, _ := m.Pane("build", "%0"); s != shared.StateDone {
		t.Fatalf("build state = %q, want done", s)
	}
}

func TestHookBadToken401(t *testing.T) {
	m := state.New(nil)
	rr := post(t, handler(m), "127.0.0.1:5000", "Bearer wrong", "%3",
		"/tmp/tmux-0/default,1,0", `{"hook_event_name":"Stop"}`)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("code %d, want 401", rr.Code)
	}
}

func TestHookNonLoopback403(t *testing.T) {
	m := state.New(nil)
	rr := post(t, handler(m), "10.0.0.9:5000", "Bearer hooktok", "%3",
		"/tmp/tmux-0/default,1,0", `{"hook_event_name":"Stop"}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("code %d, want 403", rr.Code)
	}
}

func TestHookSoftDrops(t *testing.T) {
	m := state.New(nil)
	cases := []struct{ name, pane, tmuxEnv, body string }{
		{"unknown socket", "%3", "/tmp/tmux-0/ghost,1,0", `{"hook_event_name":"Stop"}`},
		{"missing tmux", "%3", "", `{"hook_event_name":"Stop"}`},
		{"bad pane", "bogus", "/tmp/tmux-0/default,1,0", `{"hook_event_name":"Stop"}`},
		{"malformed json", "%3", "/tmp/tmux-0/default,1,0", `{not json`},
		{"no event name", "%3", "/tmp/tmux-0/default,1,0", `{"foo":"bar"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := post(t, handler(m), "127.0.0.1:5000", "Bearer hooktok", c.pane, c.tmuxEnv, c.body)
			if rr.Code != http.StatusNoContent {
				t.Fatalf("%s: code %d, want 204", c.name, rr.Code)
			}
		})
	}
	if s, ok := m.Pane("default", "%3"); ok {
		t.Fatalf("soft drops must not record state, got %q", s)
	}
}

func TestHookToleratesExtraFields(t *testing.T) {
	m := state.New(nil)
	rr := post(t, handler(m), "127.0.0.1:5000", "Bearer hooktok", "%3", "/tmp/tmux-0/default,1,0",
		`{"hook_event_name":"UserPromptSubmit","prompt":"hi","permission_mode":"default","effort":{"level":"x"},"new_field":[1,2]}`)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("code %d", rr.Code)
	}
	if s, _ := m.Pane("default", "%3"); s != shared.StateWorking {
		t.Fatalf("state %q, want working", s)
	}
}

func TestHookCapturesEpoch(t *testing.T) {
	m := state.New(func() time.Time { return time.Unix(0, 0) })
	h := RequireLoopback(RequireHookAuth("hooktok", HookHandler(testCfg(), m, nil)))
	req := httptest.NewRequest("POST", "http://127.0.0.1/hook", strings.NewReader(`{"hook_event_name":"SessionStart"}`))
	req.Header.Set("Authorization", "Bearer hooktok")
	req.Header.Set("X-AgentMon-Pane", "%0")
	req.Header.Set("X-AgentMon-Tmux", "/tmp/tmux-0/default,8421,0")
	req.RemoteAddr = "127.0.0.1:5000"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 204 {
		t.Fatalf("status %d", w.Code)
	}
	if got := m.Snapshot("")[0].Epoch; got != "8421" { // testCfg() default target label
		t.Errorf("epoch = %q, want 8421", got)
	}
}

// TestHookLoopbackOracleRegression proves that non-loopback callers always get
// 403 regardless of token value, closing the remote token-validity oracle where
// a wrong token returned 401 but a correct token returned 403.
func TestHookLoopbackOracleRegression(t *testing.T) {
	m := state.New(nil)
	h := handler(m)
	body := `{"hook_event_name":"Stop"}`
	hdr := "/tmp/tmux-0/default,1,0"

	// Non-loopback with WRONG token — must be 403, not 401.
	rr := post(t, h, "10.0.0.9:5000", "Bearer wrongtoken", "%3", hdr, body)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("non-loopback wrong token: got %d, want 403", rr.Code)
	}

	// Non-loopback with CORRECT token — must also be 403, not 204.
	rr = post(t, h, "10.0.0.9:5000", "Bearer hooktok", "%3", hdr, body)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("non-loopback correct token: got %d, want 403", rr.Code)
	}

	// Loopback + bad token — must be 401 (auth layer is reachable on loopback).
	rr = post(t, h, "127.0.0.1:5000", "Bearer wrongtoken", "%3", hdr, body)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("loopback bad token: got %d, want 401", rr.Code)
	}
}
