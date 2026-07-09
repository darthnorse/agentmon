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

func TestHooksMainCodexInstallUninstallPreservesUserHooks(t *testing.T) {
	cfg := writeAgentConfig(t)
	settings := filepath.Join(t.TempDir(), "hooks.json")
	userConfig := `{
  "hooks": {
    "Stop": [{"hooks": [{"type": "command", "command": "echo user"}]}]
  },
  "custom": true
}`
	if err := os.WriteFile(settings, []byte(userConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	args := []string{"install", "--provider", "codex", "--config", cfg, "--settings", settings}
	if err := hooksMain(args, &out); err != nil {
		t.Fatal(err)
	}
	if err := hooksMain(args, &out); err != nil { // reinstall must remain idempotent
		t.Fatal(err)
	}
	b, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	var installed map[string]any
	if err := json.Unmarshal(b, &installed); err != nil {
		t.Fatal(err)
	}
	h := installed["hooks"].(map[string]any)
	if len(h["Stop"].([]any)) != 2 {
		t.Fatalf("Stop should contain one user and one AgentMon group: %+v", h["Stop"])
	}
	for _, excluded := range []string{"Notification", "SessionEnd", "SubagentStop"} {
		if _, ok := h[excluded]; ok {
			t.Fatalf("Codex install included unsupported event %s", excluded)
		}
	}
	if !strings.Contains(out.String(), "AgentMon codex hooks") {
		t.Fatalf("output does not name provider: %s", out.String())
	}

	if err := hooksMain([]string{"uninstall", "--provider", "codex", "--config", cfg, "--settings", settings}, &out); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(settings)
	if strings.Contains(string(b), "agentmon-hook") || !strings.Contains(string(b), "echo user") {
		t.Fatalf("uninstall must remove only AgentMon hooks:\n%s", b)
	}
	if !strings.Contains(string(b), `"custom": true`) {
		t.Fatalf("uninstall lost unrelated settings:\n%s", b)
	}
}

func TestHooksMainInvalidProviderDoesNotCreateSettings(t *testing.T) {
	settings := filepath.Join(t.TempDir(), "hooks.json")
	err := hooksMain([]string{
		"install", "--provider", "other", "--config", filepath.Join(t.TempDir(), "missing.toml"), "--settings", settings,
	}, new(bytes.Buffer))
	if err == nil || !strings.Contains(err.Error(), "unknown hook provider") {
		t.Fatalf("error = %v, want unknown provider", err)
	}
	if _, statErr := os.Stat(settings); !os.IsNotExist(statErr) {
		t.Fatalf("invalid provider must not create settings, stat err=%v", statErr)
	}
}

func TestHooksMainInstallRequiresSettings(t *testing.T) {
	cfg := writeAgentConfig(t)
	if err := hooksMain([]string{"install", "--config", cfg}, new(bytes.Buffer)); err == nil {
		t.Fatal("install without --settings must error")
	}
}
