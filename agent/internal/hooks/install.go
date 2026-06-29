package hooks

import (
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

// Command builds the shell command Claude runs for each hook event. It pipes the
// event JSON (stdin) to the agent and carries pane/socket correlation in headers.
// curl failures are swallowed (|| true) so a hook never fails Claude's turn.
func Command(cfg config.Config) (string, error) {
	_, port, err := net.SplitHostPort(cfg.Listen)
	if err != nil {
		return "", fmt.Errorf("parse listen %q: %w", cfg.Listen, err)
	}
	tokenExpr := cfg.HookToken
	if cfg.HookTokenFile != "" {
		tokenExpr = "$(cat " + cfg.HookTokenFile + ")"
	}
	return fmt.Sprintf(
		`curl -sS -m 2 -H "Authorization: Bearer %s" `+
			`-H "X-AgentMon-Pane: $TMUX_PANE" -H "X-AgentMon-Tmux: $TMUX" `+
			`--data-binary @- http://127.0.0.1:%s/hook >/dev/null 2>&1 || true  # %s`,
		tokenExpr, port, Marker), nil
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

	// (a) Non-loopback listen host: hooks always POST to 127.0.0.1:<port>.
	host, port, err := net.SplitHostPort(cfg.Listen)
	if err == nil {
		ip := net.ParseIP(host)
		// Warn when host is a concrete address that is neither loopback nor wildcard.
		concreteNonLoopback := false
		if host == "" {
			// bare ":port" — wildcard, OK
		} else if ip == nil {
			// hostname (not an IP literal) — conservatively warn
			concreteNonLoopback = true
		} else if !ip.IsLoopback() && !ip.IsUnspecified() {
			concreteNonLoopback = true
		}
		if concreteNonLoopback {
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
	if len(strings.TrimSpace(string(b))) == 0 {
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

// SaveSettings writes a settings map as pretty JSON (0600).
func SaveSettings(path string, m map[string]any) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

// WriteTokenFile writes token to path (0600), creating parent dirs (0700).
func WriteTokenFile(path, token string) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(token), 0o600)
}
