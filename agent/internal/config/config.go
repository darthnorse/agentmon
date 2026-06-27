package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
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
		v, err := resolveRef(*p)
		if err != nil {
			return Config{}, err
		}
		*p = v
	}
	return c, nil
}

// resolveRef expands "env:NAME" and "file:/path" secret references; any other
// value is returned literally.
func resolveRef(v string) (string, error) {
	switch {
	case strings.HasPrefix(v, "env:"):
		name := strings.TrimPrefix(v, "env:")
		s, ok := os.LookupEnv(name)
		if !ok {
			return "", fmt.Errorf("env ref %q not set", name)
		}
		return s, nil
	case strings.HasPrefix(v, "file:"):
		b, err := os.ReadFile(strings.TrimPrefix(v, "file:"))
		if err != nil {
			return "", fmt.Errorf("file ref: %w", err)
		}
		return strings.TrimSpace(string(b)), nil
	default:
		return v, nil
	}
}
