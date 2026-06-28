package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"agentmon/shared"
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
	EnrollRateLimit     RateLimitCfg `yaml:"enroll_rate_limit"`
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
		tok, err := shared.ResolveSecretRef(c.Servers[i].TokenRef)
		if err != nil {
			return Config{}, fmt.Errorf("server %s token: %w", c.Servers[i].ID, err)
		}
		key, err := shared.ResolveSecretRef(c.Servers[i].SigningKeyRef)
		if err != nil {
			return Config{}, fmt.Errorf("server %s signing_key: %w", c.Servers[i].ID, err)
		}
		c.Servers[i].Token = tok
		c.Servers[i].SigningKey = key
	}
	return c, nil
}

