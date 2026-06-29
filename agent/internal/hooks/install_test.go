package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmon/agent/internal/config"
)

func installCfg() config.Config {
	return config.Config{Listen: "10.0.0.5:8377", HookToken: "tok"}
}

func TestCommandPortTokenFileAndEnv(t *testing.T) {
	c := installCfg()
	c.HookTokenFile = "/run/agentmon/hook-token"
	cmd, err := Command(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"127.0.0.1:8377/hook", "$(cat /run/agentmon/hook-token)", "$TMUX_PANE", "$TMUX", Marker} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("command missing %q: %s", want, cmd)
		}
	}
}

func TestCommandLiteralTokenWhenNoFile(t *testing.T) {
	cmd, _ := Command(installCfg())
	if !strings.Contains(cmd, "Bearer tok") {
		t.Fatalf("want literal token: %s", cmd)
	}
}

func TestSnippetCoversAllEvents(t *testing.T) {
	s, err := Snippet(installCfg())
	if err != nil {
		t.Fatal(err)
	}
	h := s["hooks"].(map[string]any)
	for _, e := range events {
		if _, ok := h[e]; !ok {
			t.Fatalf("snippet missing event %s", e)
		}
	}
}

func TestMergeIdempotentPreservesUserHooks(t *testing.T) {
	existing := map[string]any{
		"hooks": map[string]any{
			"Stop": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "echo user"}}}},
		},
		"otherSetting": true,
	}
	m1, err := Merge(existing, installCfg())
	if err != nil {
		t.Fatal(err)
	}
	m2, _ := Merge(m1, installCfg()) // second run must not duplicate
	stop := m2["hooks"].(map[string]any)["Stop"].([]any)
	user, agent := 0, 0
	for _, g := range stop {
		if isAgentmonGroup(g) {
			agent++
		} else {
			user++
		}
	}
	if user != 1 || agent != 1 {
		t.Fatalf("Stop groups user=%d agent=%d, want 1/1", user, agent)
	}
	if m2["otherSetting"] != true {
		t.Fatal("unrelated setting lost")
	}
}

func TestUnmergeRemovesOnlyOurs(t *testing.T) {
	existing := map[string]any{
		"hooks": map[string]any{
			"Stop": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "echo user"}}}},
		},
	}
	merged, _ := Merge(existing, installCfg())
	cleaned := Unmerge(merged)
	hooks := cleaned["hooks"].(map[string]any)
	stop := hooks["Stop"].([]any)
	if len(stop) != 1 || isAgentmonGroup(stop[0]) {
		t.Fatalf("user hook should remain alone: %+v", stop)
	}
	if _, ok := hooks["PreToolUse"]; ok {
		t.Fatal("PreToolUse (ours only) should be pruned")
	}
}

func TestUnmergeEmptyDropsHooksKey(t *testing.T) {
	merged, _ := Merge(map[string]any{}, installCfg())
	cleaned := Unmerge(merged)
	if _, ok := cleaned["hooks"]; ok {
		t.Fatal("hooks key should be gone when empty")
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	if m, err := LoadSettings(p); err != nil || len(m) != 0 {
		t.Fatalf("missing file should load empty: %v %+v", err, m)
	}
	merged, _ := Merge(map[string]any{}, installCfg())
	if err := SaveSettings(p, merged); err != nil {
		t.Fatal(err)
	}
	back, err := LoadSettings(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := back["hooks"].(map[string]any); !ok {
		t.Fatalf("round-trip lost hooks: %+v", back)
	}
}

func TestInstallWarnings(t *testing.T) {
	t.Run("loopback+tokenfile_no_warnings", func(t *testing.T) {
		cfg := config.Config{Listen: "127.0.0.1:8377", HookToken: "tok", HookTokenFile: "/run/agentmon/hook-token"}
		if w := InstallWarnings(cfg); len(w) != 0 {
			t.Fatalf("expected no warnings, got: %v", w)
		}
	})

	t.Run("wildcard_no_listen_warning", func(t *testing.T) {
		cfg := config.Config{Listen: "0.0.0.0:8377", HookToken: "tok", HookTokenFile: "/run/agentmon/hook-token"}
		for _, w := range InstallWarnings(cfg) {
			if strings.Contains(w, "loopback") {
				t.Fatalf("wildcard should not produce a listen warning, got: %s", w)
			}
		}
	})

	t.Run("concrete_nonloopback_listen_warning", func(t *testing.T) {
		cfg := config.Config{Listen: "10.0.0.5:8377", HookToken: "tok", HookTokenFile: "/run/agentmon/hook-token"}
		warnings := InstallWarnings(cfg)
		found := false
		for _, w := range warnings {
			if strings.Contains(w, "loopback") {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected a listen warning containing 'loopback', got: %v", warnings)
		}
	})

	t.Run("literal_token_warning", func(t *testing.T) {
		cfg := config.Config{Listen: "127.0.0.1:8377", HookToken: "tok", HookTokenFile: ""}
		warnings := InstallWarnings(cfg)
		found := false
		for _, w := range warnings {
			if strings.Contains(w, "settings file") || strings.Contains(w, "hook_token_file") {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected a literal-token warning, got: %v", warnings)
		}
	})
}

func TestWriteTokenFilePerms(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "hook-token")
	if err := WriteTokenFile(p, "s3cr3t"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "s3cr3t" {
		t.Fatalf("token contents = %q", b)
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %v, want 0600", fi.Mode().Perm())
	}
}
