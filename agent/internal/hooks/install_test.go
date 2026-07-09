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
	// Path must be single-quoted inside the $( ) subshell.
	for _, want := range []string{"127.0.0.1:8377/hook", "$(cat '/run/agentmon/hook-token')", "$TMUX_PANE", "$TMUX", Marker} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("command missing %q: %s", want, cmd)
		}
	}
}

func TestCommandLiteralTokenWhenNoFile(t *testing.T) {
	cmd, _ := Command(installCfg())
	// Token must be single-quoted, not interpolated raw.
	if !strings.Contains(cmd, "'tok'") {
		t.Fatalf("want single-quoted token: %s", cmd)
	}
}

func TestCommandTokenWithSingleQuote(t *testing.T) {
	c := installCfg()
	c.HookToken = "ab'cd"
	cmd, err := Command(c)
	if err != nil {
		t.Fatal(err)
	}
	// ab'cd must be escaped as 'ab'\''cd' (single-quote escape sequence).
	if !strings.Contains(cmd, `'ab'\''cd'`) {
		t.Fatalf("expected single-quote escape in command: %s", cmd)
	}
}

func TestCommandTokenFileWithSpace(t *testing.T) {
	c := installCfg()
	c.HookTokenFile = "/tmp/my dir/tok"
	cmd, err := Command(c)
	if err != nil {
		t.Fatal(err)
	}
	// Path with space must be single-quoted inside the $( ).
	if !strings.Contains(cmd, "$(cat '/tmp/my dir/tok')") {
		t.Fatalf("expected quoted path in command: %s", cmd)
	}
}

func TestParseProvider(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Provider
	}{
		{"claude", ProviderClaude},
		{"CODEX", ProviderCodex},
		{" codex ", ProviderCodex},
	} {
		got, err := ParseProvider(tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("ParseProvider(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
	if _, err := ParseProvider("other"); err == nil {
		t.Fatal("unknown provider must error")
	}
}

func TestSnippetUsesProviderEvents(t *testing.T) {
	tests := []struct {
		provider Provider
		want     []string
		excluded []string
	}{
		{
			provider: ProviderClaude,
			want: []string{
				"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse",
				"Notification", "PermissionRequest", "Stop", "SubagentStop", "SessionEnd",
			},
		},
		{
			provider: ProviderCodex,
			want: []string{
				"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse",
				"PermissionRequest", "Stop",
			},
			excluded: []string{"Notification", "SessionEnd", "SubagentStart", "SubagentStop"},
		},
	}
	for _, tc := range tests {
		t.Run(string(tc.provider), func(t *testing.T) {
			s, err := Snippet(installCfg(), tc.provider)
			if err != nil {
				t.Fatal(err)
			}
			h := s["hooks"].(map[string]any)
			if len(h) != len(tc.want) {
				t.Fatalf("events = %v, want exactly %v", h, tc.want)
			}
			for _, e := range tc.want {
				if _, ok := h[e]; !ok {
					t.Fatalf("snippet missing event %s", e)
				}
			}
			for _, e := range tc.excluded {
				if _, ok := h[e]; ok {
					t.Fatalf("snippet unexpectedly includes event %s", e)
				}
			}
			start := h["SessionStart"].([]any)[0].(map[string]any)
			if tc.provider == ProviderCodex {
				if got := start["matcher"]; got != "startup|resume|clear" {
					t.Fatalf("Codex SessionStart matcher = %v", got)
				}
				stop := h["Stop"].([]any)[0].(map[string]any)
				if _, ok := stop["matcher"]; ok {
					t.Fatal("Codex Stop must remain matcherless")
				}
			} else if _, ok := start["matcher"]; ok {
				t.Fatal("Claude SessionStart shape changed unexpectedly")
			}
		})
	}
}

func TestMergeIdempotentPreservesUserHooks(t *testing.T) {
	for _, provider := range []Provider{ProviderClaude, ProviderCodex} {
		t.Run(string(provider), func(t *testing.T) {
			existing := map[string]any{
				"hooks": map[string]any{
					"Stop": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "echo user"}}}},
				},
				"otherSetting": true,
			}
			m1, err := Merge(existing, installCfg(), provider)
			if err != nil {
				t.Fatal(err)
			}
			m2, _ := Merge(m1, installCfg(), provider) // second run must not duplicate
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
		})
	}
	if _, err := Merge(map[string]any{}, installCfg(), Provider("other")); err == nil {
		t.Fatal("unknown provider must error")
	}
}

func TestProviderSnippetCommandHasSharedTransport(t *testing.T) {
	for _, provider := range []Provider{ProviderClaude, ProviderCodex} {
		s, err := Snippet(installCfg(), provider)
		if err != nil {
			t.Fatal(err)
		}
		stop := s["hooks"].(map[string]any)["Stop"].([]any)
		group := stop[0].(map[string]any)
		hook := group["hooks"].([]any)[0].(map[string]any)
		cmd := hook["command"].(string)
		for _, want := range []string{"$TMUX_PANE", "$TMUX", "127.0.0.1:8377/hook", ">/dev/null 2>&1 || true", Marker} {
			if !strings.Contains(cmd, want) {
				t.Fatalf("%s command missing %q: %s", provider, want, cmd)
			}
		}
	}
}

func TestMergeCorrectsWrongProviderAndPreservesUserHooks(t *testing.T) {
	wrong, err := Merge(map[string]any{"custom": true}, installCfg(), ProviderClaude)
	if err != nil {
		t.Fatal(err)
	}
	h := wrong["hooks"].(map[string]any)
	h["Notification"] = append(h["Notification"].([]any), group("echo user-notification"))

	fixed, err := Merge(wrong, installCfg(), ProviderCodex)
	if err != nil {
		t.Fatal(err)
	}
	h = fixed["hooks"].(map[string]any)
	if fixed["custom"] != true {
		t.Fatal("unrelated top-level setting lost")
	}
	for _, stale := range []string{"SessionEnd", "SubagentStop"} {
		if _, ok := h[stale]; ok {
			t.Fatalf("stale Claude-only AgentMon event %s remains", stale)
		}
	}
	notification := h["Notification"].([]any)
	if len(notification) != 1 || isAgentmonGroup(notification[0]) {
		t.Fatalf("user Notification hook not preserved alone: %+v", notification)
	}
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PermissionRequest", "Stop"} {
		groups := h[event].([]any)
		if len(groups) != 1 || !isAgentmonGroup(groups[0]) {
			t.Fatalf("Codex event %s groups = %+v", event, groups)
		}
	}
}

func TestUnmergeRemovesOnlyOurs(t *testing.T) {
	existing := map[string]any{
		"hooks": map[string]any{
			"Stop": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "echo user"}}}},
		},
	}
	merged, _ := Merge(existing, installCfg(), ProviderClaude)
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
	merged, _ := Merge(map[string]any{}, installCfg(), ProviderClaude)
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
	merged, _ := Merge(map[string]any{}, installCfg(), ProviderClaude)
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

	t.Run("ipv6_loopback_warns", func(t *testing.T) {
		// ::1 is loopback but the hook posts to 127.0.0.1; binding ::1 only
		// means hooks can't reach the agent — emit a listen warning.
		cfg := config.Config{Listen: "[::1]:8377", HookToken: "tok", HookTokenFile: "/run/agentmon/hook-token"}
		warnings := InstallWarnings(cfg)
		found := false
		for _, w := range warnings {
			if strings.Contains(w, "loopback") {
				found = true
			}
		}
		if !found {
			t.Fatalf("[::1] should produce a listen warning, got: %v", warnings)
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

func TestWriteTokenFileChmodPreExisting(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "hook-token")
	// Pre-create with loose permissions.
	if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteTokenFile(p, "newtoken"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("perm after WriteTokenFile = %v, want 0600", fi.Mode().Perm())
	}
}

func TestSaveSettingsChmodPreExisting(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	// Pre-create with loose permissions.
	if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveSettings(p, map[string]any{"x": 1}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("perm after SaveSettings = %v, want 0600", fi.Mode().Perm())
	}
}
