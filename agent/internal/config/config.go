package config

import (
	"fmt"

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
	ScrollbackLines int      `toml:"scrollback_lines"`
	Targets         []Target `toml:"targets"`
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

