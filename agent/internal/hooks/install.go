package hooks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"agentmon/agent/internal/config"
)

// Marker tags AgentMon-installed hook commands so install is idempotent and
// uninstall removes exactly our entries (and nothing the user added).
const Marker = "agentmon-hook"

// events are the Claude Code hook events AgentMon installs (verified v2.1.195).
var events = []string{
	"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse",
	"Notification", "PermissionRequest", "Stop", "SubagentStop", "SessionEnd",
}

// shellSingleQuote wraps s in single quotes for safe shell interpolation.
// Embedded single quotes are escaped as '\''.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Command builds the shell command Claude runs for each hook event. It pipes the
// event JSON (stdin) to the agent and carries pane/socket correlation in headers.
// curl failures are swallowed (|| true) so a hook never fails Claude's turn.
// The hook token and token-file path are shell-single-quoted to prevent injection
// from values containing shell metacharacters (spaces, quotes, $, backticks, etc.).
func Command(cfg config.Config) (string, error) {
	_, port, err := net.SplitHostPort(cfg.Listen)
	if err != nil {
		return "", fmt.Errorf("parse listen %q: %w", cfg.Listen, err)
	}
	var authHeader string
	if cfg.HookTokenFile != "" {
		// Token read at hook-fire time; single-quote the path inside $( ).
		authHeader = `-H "Authorization: Bearer $(cat ` + shellSingleQuote(cfg.HookTokenFile) + `)"`
	} else {
		// Adjacent single-quoted words concatenate in sh: 'Authorization: Bearer ''tok'
		authHeader = `-H 'Authorization: Bearer '` + shellSingleQuote(cfg.HookToken)
	}
	return `curl -sS -m 2 ` + authHeader + ` ` +
		`-H "X-AgentMon-Pane: $TMUX_PANE" -H "X-AgentMon-Tmux: $TMUX" ` +
		`--data-binary @- http://127.0.0.1:` + port + `/hook >/dev/null 2>&1 || true  # ` + Marker, nil
}

func group(cmd string) map[string]any {
	return map[string]any{"hooks": []any{map[string]any{"type": "command", "command": cmd}}}
}

// Snippet returns the {"hooks":{...}} settings block AgentMon installs.
func Snippet(cfg config.Config) (map[string]any, error) {
	cmd, err := Command(cfg)
	if err != nil {
		return nil, err
	}
	h := map[string]any{}
	for _, e := range events {
		h[e] = []any{group(cmd)}
	}
	return map[string]any{"hooks": h}, nil
}

// Merge splices AgentMon's hooks into an existing settings map idempotently:
// existing AgentMon groups are removed first, so re-running never duplicates and the
// user's own hooks are untouched. Returns the same (or a fresh) map.
func Merge(existing map[string]any, cfg config.Config) (map[string]any, error) {
	cmd, err := Command(cfg)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		existing = map[string]any{}
	}
	hooks, _ := existing["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	for _, e := range events {
		arr, _ := hooks[e].([]any)
		arr = append(dropAgentmon(arr), group(cmd))
		hooks[e] = arr
	}
	existing["hooks"] = hooks
	return existing, nil
}

// Unmerge removes only AgentMon groups, pruning empty arrays and an empty hooks map.
func Unmerge(existing map[string]any) map[string]any {
	if existing == nil {
		return map[string]any{}
	}
	hooks, _ := existing["hooks"].(map[string]any)
	if hooks == nil {
		return existing
	}
	for e, v := range hooks {
		arr, _ := v.([]any)
		arr = dropAgentmon(arr)
		if len(arr) == 0 {
			delete(hooks, e)
		} else {
			hooks[e] = arr
		}
	}
	if len(hooks) == 0 {
		delete(existing, "hooks")
	} else {
		existing["hooks"] = hooks
	}
	return existing
}

func dropAgentmon(arr []any) []any {
	out := arr[:0:0] // fresh backing array (nil when arr is nil/empty)
	for _, g := range arr {
		if !isAgentmonGroup(g) {
			out = append(out, g)
		}
	}
	return out
}

func isAgentmonGroup(g any) bool {
	gm, ok := g.(map[string]any)
	if !ok {
		return false
	}
	hs, ok := gm["hooks"].([]any)
	if !ok {
		return false
	}
	for _, h := range hs {
		hm, ok := h.(map[string]any)
		if !ok {
			continue
		}
		if cmd, ok := hm["command"].(string); ok && strings.Contains(cmd, Marker) {
			return true
		}
	}
	return false
}

// InstallWarnings returns zero or more human-readable warning strings about the
// given config that may cause silent hook failures. It performs no I/O.
func InstallWarnings(cfg config.Config) []string {
	var warnings []string

	// (a) Warn unless the listen host is exactly 127.0.0.1 or a wildcard
	// (empty/""/0.0.0.0/::). ::1 (IPv6-only loopback) MUST warn because the
	// installed hook always POSTs to 127.0.0.1 which is not reachable via ::1.
	host, port, err := net.SplitHostPort(cfg.Listen)
	if err == nil {
		warnListen := true
		if host == "" {
			// bare ":port" — wildcard, reachable on all interfaces including loopback
			warnListen = false
		} else if ip := net.ParseIP(host); ip != nil {
			if ip.IsUnspecified() || ip.Equal(net.ParseIP("127.0.0.1")) {
				warnListen = false
			}
		}
		if warnListen {
			warnings = append(warnings, fmt.Sprintf(
				"agent listen host %q is not loopback or a wildcard; hooks POST to 127.0.0.1:%s"+
					" and will silently no-op unless the agent is reachable on loopback"+
					" (bind 0.0.0.0 or include loopback).",
				host, port))
		}
	}

	// (b) Literal token embedded in settings file.
	if cfg.HookTokenFile == "" && cfg.HookToken != "" {
		warnings = append(warnings, "hook token will be embedded in the settings file;"+
			" set hook_token_file to keep the secret out of the settings file.")
	}

	return warnings
}

// LoadSettings reads a Claude Code settings JSON file. A missing or empty file
// loads as an empty map (so install can create it).
func LoadSettings(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

// SaveSettings writes a settings map as pretty JSON (0600). It explicitly
// chmods after writing so a pre-existing file with loose permissions is tightened.
func SaveSettings(path string, m map[string]any) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// WriteTokenFile writes token to path (0600), creating parent dirs (0700).
// It explicitly chmods after writing so a pre-existing file with loose
// permissions is tightened to 0600.
func WriteTokenFile(path, token string) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	if err := os.WriteFile(path, []byte(token), 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}
