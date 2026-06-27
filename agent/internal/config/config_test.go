package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadResolvesEnvRefs(t *testing.T) {
	t.Setenv("AGENTMON_AGENT_TOKEN", "secret-token")
	dir := t.TempDir()
	p := filepath.Join(dir, "agent.toml")
	os.WriteFile(p, []byte(`
listen = "10.0.0.5:8377"
server_id = "server-a"
hub_token = "env:AGENTMON_AGENT_TOKEN"
directive_key = "literal-key"
scrollback_lines = 4000
[[targets]]
  os_user = "dev"
  socket_name = ""
  label = "default"
`), 0o600)

	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HubToken != "secret-token" {
		t.Fatalf("env ref not resolved: %q", cfg.HubToken)
	}
	if cfg.DirectiveKey != "literal-key" {
		t.Fatalf("literal mangled: %q", cfg.DirectiveKey)
	}
	if cfg.ServerID != "server-a" || cfg.ScrollbackLines != 4000 {
		t.Fatalf("bad cfg: %+v", cfg)
	}
	if len(cfg.Targets) != 1 || cfg.Targets[0].Label != "default" {
		t.Fatalf("targets: %+v", cfg.Targets)
	}
}

func TestLoadMissingEnvRefErrors(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "agent.toml")
	os.WriteFile(p, []byte(`
listen = "x"
server_id = "s"
hub_token = "env:DEFINITELY_NOT_SET_AGENTMON"
directive_key = "k"
`), 0o600)
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for unset env ref")
	}
}

func TestResolveTarget(t *testing.T) {
	cfg := Config{Targets: []Target{
		{Label: "default", SocketName: ""},
		{Label: "build", SocketName: "buildsock"},
	}}

	if tg, ok := cfg.ResolveTarget(""); !ok || tg.Label != "default" {
		t.Fatalf("empty → %+v ok=%v, want default", tg, ok)
	}
	if tg, ok := cfg.ResolveTarget("build"); !ok || tg.SocketName != "buildsock" {
		t.Fatalf("build → %+v ok=%v", tg, ok)
	}
	if _, ok := cfg.ResolveTarget("nope"); ok {
		t.Fatal("unknown label should not resolve")
	}
}

func TestResolveTargetNoTargets(t *testing.T) {
	if _, ok := (Config{}).ResolveTarget(""); ok {
		t.Fatal("no targets configured should not resolve")
	}
}
