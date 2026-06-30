package config

import (
	"fmt"
	"os"
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

type Config struct {
	Listen              string        `yaml:"listen"`
	ExternalOrigin      string        `yaml:"external_origin"`
	TrustForwardedProto bool          `yaml:"trust_forwarded_proto"`
	DataDir             string        `yaml:"data_dir"`
	SessionCookie       CookieCfg     `yaml:"session_cookie"`
	LoginRateLimit      RateLimitCfg  `yaml:"login_rate_limit"`
	EnrollRateLimit     RateLimitCfg  `yaml:"enroll_rate_limit"`
	StatePollInterval   time.Duration `yaml:"state_poll_interval"`
	SSEHeartbeat        time.Duration `yaml:"sse_heartbeat"`
	VAPIDSubject        string        `yaml:"vapid_subject"` // M9: Web-Push VAPID subject (mailto:/URL); defaults to external_origin
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
	return c, nil
}
