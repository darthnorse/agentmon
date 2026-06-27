package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type CookieCfg struct {
	Name string        `yaml:"name"`
	TTL  time.Duration `yaml:"ttl"`
}

type RateLimitCfg struct {
	MaxAttempts int           `yaml:"max_attempts"`
	Window      time.Duration `yaml:"window"`
}

type Server struct {
	ID            string   `yaml:"id"`
	Name          string   `yaml:"name"`
	URL           string   `yaml:"url"`
	TokenRef      string   `yaml:"token_ref"`
	SigningKeyRef string   `yaml:"signing_key_ref"`
	Labels        []string `yaml:"labels"`
	// resolved at load:
	Token      string `yaml:"-"`
	SigningKey string `yaml:"-"`
}

type Config struct {
	Listen              string       `yaml:"listen"`
	ExternalOrigin      string       `yaml:"external_origin"`
	TrustForwardedProto bool         `yaml:"trust_forwarded_proto"`
	DataDir             string       `yaml:"data_dir"`
	SessionCookie       CookieCfg    `yaml:"session_cookie"`
	LoginRateLimit      RateLimitCfg `yaml:"login_rate_limit"`
	Servers             []Server     `yaml:"servers"`
}

func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if c.SessionCookie.Name == "" {
		c.SessionCookie.Name = "agentmon_session"
	}
	for i := range c.Servers {
		tok, err := resolveRef(c.Servers[i].TokenRef)
		if err != nil {
			return Config{}, fmt.Errorf("server %s token: %w", c.Servers[i].ID, err)
		}
		key, err := resolveRef(c.Servers[i].SigningKeyRef)
		if err != nil {
			return Config{}, fmt.Errorf("server %s signing_key: %w", c.Servers[i].ID, err)
		}
		c.Servers[i].Token = tok
		c.Servers[i].SigningKey = key
	}
	return c, nil
}

func resolveRef(v string) (string, error) {
	switch {
	case v == "":
		return "", fmt.Errorf("empty secret ref")
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
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	default:
		return v, nil
	}
}
