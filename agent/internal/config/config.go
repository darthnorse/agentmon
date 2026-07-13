package config

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"

	"agentmon/shared"
)

type Target struct {
	OSUser     string `toml:"os_user"`
	SocketName string `toml:"socket_name"`
	Label      string `toml:"label"`
}

type Config struct {
	Listen          string   `toml:"listen"`
	ServerID        string   `toml:"server_id"`
	HubToken        string   `toml:"hub_token"`
	DirectiveKey    string   `toml:"directive_key"`
	HookToken       string   `toml:"hook_token"`      // optional; enables /hook when set
	HookTokenFile   string   `toml:"hook_token_file"` // optional path the agent writes the token to
	ScrollbackLines int      `toml:"scrollback_lines"`
	Targets         []Target `toml:"targets"`
	// SessionDirs is the allow-list of roots in which POST /sessions may create a
	// new tmux session (§13.6 directory policy). An empty/unset list defaults to
	// the agent user's home directory (os.UserHomeDir) at the handler.
	SessionDirs []string `toml:"session_dirs"`
}

func Load(path string) (Config, error) {
	var c Config
	if _, err := toml.DecodeFile(path, &c); err != nil {
		return Config{}, fmt.Errorf("decode %s: %w", path, err)
	}
	if c.ScrollbackLines == 0 {
		c.ScrollbackLines = 5000
	}
	for _, p := range []*string{&c.HubToken, &c.DirectiveKey} {
		v, err := shared.ResolveSecretRef(*p)
		if err != nil {
			return Config{}, err
		}
		*p = v
	}
	if c.HookToken != "" {
		v, err := shared.ResolveSecretRef(c.HookToken)
		if err != nil {
			return Config{}, err
		}
		c.HookToken = v
	}
	return c, nil
}

// ResolveTarget selects a target by label. An empty label resolves to the first
// configured target (the Phase 1 default). Returns false when no target matches
// or none are configured.
func (c Config) ResolveTarget(label string) (Target, bool) {
	if len(c.Targets) == 0 {
		return Target{}, false
	}
	if label == "" {
		return c.Targets[0], true
	}
	for _, t := range c.Targets {
		if t.Label == label {
			return t, true
		}
	}
	return Target{}, false
}

// AllowedDirs is the allow-list of roots against which a caller-supplied path
// (session cwd, worktree workdir) is authorised before any tmux/git runs. It is
// SessionDirs, defaulting to the agent user's home when none are configured.
// Single source of truth so the fallback can't drift between the handlers that
// gate on it.
func (c Config) AllowedDirs() []string {
	if len(c.SessionDirs) > 0 {
		return c.SessionDirs
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return []string{home}
	}
	return nil
}
