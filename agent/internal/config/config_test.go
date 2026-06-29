package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadResolvesEnvRefs(t *testing.T) {
	t.Setenv("AGENTMON_AGENT_TOKEN", "secret-token")
	t.Setenv("AGENTMON_AGENT_DKEY", "dkey-value")
	dir := t.TempDir()
	p := filepath.Join(dir, "agent.toml")
	if err := os.WriteFile(p, []byte(`
listen = "10.0.0.5:8377"
server_id = "server-a"
hub_token = "env:AGENTMON_AGENT_TOKEN"
directive_key = "env:AGENTMON_AGENT_DKEY"
scrollback_lines = 4000
[[targets]]
  os_user = "dev"
  socket_name = ""
  label = "default"
`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HubToken != "secret-token" {
		t.Fatalf("env ref not resolved: %q", cfg.HubToken)
	}
	if cfg.DirectiveKey != "dkey-value" {
		t.Fatalf("env ref not resolved: %q", cfg.DirectiveKey)
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
	if err := os.WriteFile(p, []byte(`
listen = "x"
server_id = "s"
hub_token = "env:DEFINITELY_NOT_SET_AGENTMON"
directive_key = "env:DEFINITELY_NOT_SET_AGENTMON_DKEY"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for unset env ref")
	}
}

func TestLoadRejectsBareLiteralSecret(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "agent.toml")
	if err := os.WriteFile(p, []byte(`
listen = "10.0.0.5:8377"
server_id = "server-a"
hub_token = "plain-literal-token"
directive_key = "env:SOMEKEY"
[[targets]]
  os_user = "dev"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("bare-literal hub_token must be rejected")
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

func TestLoadResolvesHookToken(t *testing.T) {
	t.Setenv("HK_HUB", "h")
	t.Setenv("HK_DK", "d")
	t.Setenv("AGENTMON_HOOK_TOKEN", "hooksecret")
	dir := t.TempDir()
	p := filepath.Join(dir, "agent.toml")
	if err := os.WriteFile(p, []byte(`
listen = "10.0.0.5:8377"
server_id = "s"
hub_token = "env:HK_HUB"
directive_key = "env:HK_DK"
hook_token = "env:AGENTMON_HOOK_TOKEN"
hook_token_file = "/run/agentmon/hook-token"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HookToken != "hooksecret" {
		t.Fatalf("hook token = %q", cfg.HookToken)
	}
	if cfg.HookTokenFile != "/run/agentmon/hook-token" {
		t.Fatalf("hook token file = %q", cfg.HookTokenFile)
	}
}

func TestLoadHookTokenOptional(t *testing.T) {
	t.Setenv("HK_HUB2", "h")
	t.Setenv("HK_DK2", "d")
	dir := t.TempDir()
	p := filepath.Join(dir, "agent.toml")
	if err := os.WriteFile(p, []byte(`
listen = "x"
server_id = "s"
hub_token = "env:HK_HUB2"
directive_key = "env:HK_DK2"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("optional hook_token should not error: %v", err)
	}
	if cfg.HookToken != "" {
		t.Fatalf("want empty hook token, got %q", cfg.HookToken)
	}
}

func TestLoadHookTokenBareLiteralRejected(t *testing.T) {
	t.Setenv("HK_HUB3", "h")
	t.Setenv("HK_DK3", "d")
	dir := t.TempDir()
	p := filepath.Join(dir, "agent.toml")
	if err := os.WriteFile(p, []byte(`
listen = "x"
server_id = "s"
hub_token = "env:HK_HUB3"
directive_key = "env:HK_DK3"
hook_token = "plain-literal"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("bare-literal hook_token must be rejected")
	}
}
