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

// GitHubCfg holds the hub-side GitHub credentials for the orchestrator.
// Token is a fine-grained PAT scoped to the registered repos only.
type GitHubCfg struct {
	Token         string `yaml:"token"`
	WebhookSecret string `yaml:"webhook_secret"`
}

// OrchestratorCfg tunes the epic pipeline engine.
type OrchestratorCfg struct {
	Tick                time.Duration `yaml:"tick"`
	PlanningTimeout     time.Duration `yaml:"planning_timeout"`
	ImplementingTimeout time.Duration `yaml:"implementing_timeout"`
	ReviewingTimeout    time.Duration `yaml:"reviewing_timeout"`
	MaxAttempts         int           `yaml:"max_attempts"`
}

type Config struct {
	Listen              string          `yaml:"listen"`
	ExternalOrigin      string          `yaml:"external_origin"`
	TrustForwardedProto bool            `yaml:"trust_forwarded_proto"`
	DataDir             string          `yaml:"data_dir"`
	SessionCookie       CookieCfg       `yaml:"session_cookie"`
	LoginRateLimit      RateLimitCfg    `yaml:"login_rate_limit"`
	EnrollRateLimit     RateLimitCfg    `yaml:"enroll_rate_limit"`
	StatePollInterval   time.Duration   `yaml:"state_poll_interval"`
	SSEHeartbeat        time.Duration   `yaml:"sse_heartbeat"`
	VAPIDSubject        string          `yaml:"vapid_subject"` // M9: Web-Push VAPID subject (mailto:/URL); defaults to external_origin
	GitHub              GitHubCfg       `yaml:"github"`
	Orchestrator        OrchestratorCfg `yaml:"orchestrator"`
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
	if c.Orchestrator.Tick == 0 {
		c.Orchestrator.Tick = 15 * time.Second
	}
	if c.Orchestrator.PlanningTimeout == 0 {
		c.Orchestrator.PlanningTimeout = 2 * time.Hour
	}
	if c.Orchestrator.ImplementingTimeout == 0 {
		c.Orchestrator.ImplementingTimeout = 8 * time.Hour
	}
	if c.Orchestrator.ReviewingTimeout == 0 {
		c.Orchestrator.ReviewingTimeout = 2 * time.Hour
	}
	if c.Orchestrator.MaxAttempts == 0 {
		c.Orchestrator.MaxAttempts = 2
	}
	return c, nil
}
