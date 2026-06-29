package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeAgentConfig writes a minimal valid agent.toml (env: secret refs) and returns its path.
func writeAgentConfig(t *testing.T) string {
	t.Helper()
	t.Setenv("M6_HUB", "h")
	t.Setenv("M6_DK", "d")
	t.Setenv("M6_HOOK", "hooktok")
	dir := t.TempDir()
	p := filepath.Join(dir, "agent.toml")
	if err := os.WriteFile(p, []byte(`
listen = "10.0.0.5:8377"
server_id = "s"
hub_token = "env:M6_HUB"
directive_key = "env:M6_DK"
hook_token = "env:M6_HOOK"
[[targets]]
  socket_name = ""
  label = "default"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestHooksMainPrint(t *testing.T) {
	cfg := writeAgentConfig(t)
	var out bytes.Buffer
	if err := hooksMain([]string{"print", "--config", cfg}, &out); err != nil {
		t.Fatal(err)
	}
	var snip map[string]any
	if err := json.Unmarshal(out.Bytes(), &snip); err != nil {
		t.Fatalf("print is not valid JSON: %v\n%s", err, out.String())
	}
	if _, ok := snip["hooks"].(map[string]any)["Stop"]; !ok {
		t.Fatal("print missing Stop hook")
	}
	if !strings.Contains(out.String(), "127.0.0.1:8377/hook") {
		t.Fatal("print missing endpoint")
	}
}

func TestHooksMainInstallUninstallRoundTrip(t *testing.T) {
	cfg := writeAgentConfig(t)
	settings := filepath.Join(t.TempDir(), "settings.json")
	var out bytes.Buffer
	if err := hooksMain([]string{"install", "--config", cfg, "--settings", settings}, &out); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(settings)
	if !strings.Contains(string(b), "agentmon-hook") {
		t.Fatalf("install did not write our marker:\n%s", b)
	}
	if err := hooksMain([]string{"uninstall", "--config", cfg, "--settings", settings}, &out); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(settings)
	if strings.Contains(string(b), "agentmon-hook") {
		t.Fatalf("uninstall left our marker:\n%s", b)
	}
}

func TestHooksMainInstallRequiresSettings(t *testing.T) {
	cfg := writeAgentConfig(t)
	if err := hooksMain([]string{"install", "--config", cfg}, new(bytes.Buffer)); err == nil {
		t.Fatal("install without --settings must error")
	}
}
