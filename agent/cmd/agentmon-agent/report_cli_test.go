package main

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// reportTestServer returns an httptest server and an agent.toml whose listen
// port points at it (mirrors the hook-test pattern: the CLI derives the intake
// URL from the config's listen port). config.Load resolves hub_token and
// directive_key unconditionally and every secret must be an env:/file: ref
// (bare literals are rejected) — mirror writeAgentConfig in hooks_cli_test.go.
func reportTestServer(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	t.Setenv("RPT_HUB", "h")
	t.Setenv("RPT_DK", "d")
	t.Setenv("RPT_HOOK", "htok")
	srv := httptest.NewServer(handler)
	_, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(t.TempDir(), "agent.toml")
	cfg := fmt.Sprintf("listen = \"127.0.0.1:%s\"\nserver_id = \"t\"\nhub_token = \"env:RPT_HUB\"\ndirective_key = \"env:RPT_DK\"\nhook_token = \"env:RPT_HOOK\"\n", port)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	return cfgPath
}

func TestReportPostsToIntake(t *testing.T) {
	t.Setenv("TMUX_PANE", "%3")
	t.Setenv("TMUX", "/tmp/tmux-0/agentmon,42,0")
	var gotAuth, gotPane, gotTmux, gotBody, gotPath string
	cfgPath := reportTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotPane = r.Header.Get("X-AgentMon-Pane")
		gotTmux = r.Header.Get("X-AgentMon-Tmux")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"session":"epic-p-7"}`))
	})
	var out bytes.Buffer
	err := reportMain([]string{"--config", cfgPath, "--epic", "7", "--stage", "pr_open", "--pr", "12", "--repo", "o/r", "--note", "done"}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/orchestrator/report" || gotAuth != "Bearer htok" || gotPane != "%3" || gotTmux != "/tmp/tmux-0/agentmon,42,0" {
		t.Fatalf("path=%q auth=%q pane=%q tmux=%q", gotPath, gotAuth, gotPane, gotTmux)
	}
	for _, want := range []string{`"epic":7`, `"stage":"pr_open"`, `"pr":12`, `"repo":"o/r"`, `"note":"done"`} {
		if !strings.Contains(gotBody, want) {
			t.Fatalf("body missing %s: %s", want, gotBody)
		}
	}
}

func TestReportValidation(t *testing.T) {
	t.Setenv("TMUX_PANE", "%3")
	cfgPath := reportTestServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	var out bytes.Buffer
	if err := reportMain([]string{"--config", cfgPath, "--epic", "7", "--stage", "merged", "--repo", "o/r"}, &out); err == nil {
		t.Fatal("hub-derived stage must be rejected client-side")
	}
	if err := reportMain([]string{"--config", cfgPath, "--stage", "planning", "--repo", "o/r"}, &out); err == nil {
		t.Fatal("missing --epic must error")
	}
}

func TestReportPROpenRequiresPR(t *testing.T) {
	t.Setenv("TMUX_PANE", "%3")
	called := false
	cfgPath := reportTestServer(t, func(w http.ResponseWriter, _ *http.Request) { called = true; w.WriteHeader(200) })
	var out bytes.Buffer
	err := reportMain([]string{"--config", cfgPath, "--epic", "7", "--stage", "pr_open", "--repo", "o/r"}, &out)
	if err == nil || !strings.Contains(err.Error(), "requires --pr") {
		t.Fatalf("pr_open without --pr must fail client-side: %v", err)
	}
	if called {
		t.Fatal("intake must not be reached")
	}
}

func TestReportTimesOutOnWedgedAgent(t *testing.T) {
	t.Setenv("TMUX_PANE", "%3")
	t.Setenv("TMUX", "/tmp/tmux-0/agentmon,42,0")
	old := loopbackHTTPTimeout
	loopbackHTTPTimeout = 50 * time.Millisecond
	defer func() { loopbackHTTPTimeout = old }()
	cfgPath := reportTestServer(t, func(http.ResponseWriter, *http.Request) {
		time.Sleep(500 * time.Millisecond)
	})
	var out bytes.Buffer
	if err := reportMain([]string{"--config", cfgPath, "--epic", "1", "--stage", "planning", "--repo", "o/r"}, &out); err == nil {
		t.Fatal("a wedged agent must time the report out, not hang it")
	}
}

func TestReportRejectionSurfacesBody(t *testing.T) {
	t.Setenv("TMUX_PANE", "%3")
	t.Setenv("TMUX", "/tmp/tmux-0/agentmon,42,0")
	cfgPath := reportTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"stage is not runner-reportable"}`))
	})
	var out bytes.Buffer
	err := reportMain([]string{"--config", cfgPath, "--epic", "7", "--stage", "planning", "--repo", "o/r"}, &out)
	if err == nil || !strings.Contains(err.Error(), "runner-reportable") {
		t.Fatalf("err = %v", err)
	}
}

func TestReportOutsideTmuxFailsFast(t *testing.T) {
	t.Setenv("TMUX_PANE", "")
	cfgPath := reportTestServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	var out bytes.Buffer
	if err := reportMain([]string{"--config", cfgPath, "--epic", "1", "--stage", "planning", "--repo", "o/r"}, &out); err == nil {
		t.Fatal("must fail outside tmux")
	}
}

func TestNormalizeRepoURL(t *testing.T) {
	cases := map[string]string{
		"git@github.com:owner/name.git":     "owner/name",
		"https://github.com/owner/name.git": "owner/name",
		"https://github.com/owner/name":     "owner/name",
		"ssh://git@github.com/owner/name":   "owner/name",
	}
	for in, want := range cases {
		got, err := normalizeRepoURL(in)
		if err != nil || got != want {
			t.Fatalf("%s → %q err=%v, want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"/srv/git/x", "https://github.com/onlyowner", ""} {
		if _, err := normalizeRepoURL(bad); err == nil {
			t.Fatalf("%q must error", bad)
		}
	}
}
