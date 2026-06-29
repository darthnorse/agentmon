package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadServerlessConfig(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	os.WriteFile(p, []byte(`
listen: "127.0.0.1:8080"
external_origin: "https://agentmon.lan"
trust_forwarded_proto: true
data_dir: "/data"
session_cookie: { name: "agentmon_session", ttl: "168h" }
login_rate_limit: { max_attempts: 5, window: "15m" }
enroll_rate_limit: { max_attempts: 30, window: "1m" }
`), 0o600)

	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ExternalOrigin != "https://agentmon.lan" || !cfg.TrustForwardedProto {
		t.Fatalf("bad cfg: %+v", cfg)
	}
	if cfg.SessionCookie.Name != "agentmon_session" || cfg.SessionCookie.TTL != 168*time.Hour {
		t.Fatalf("cookie: %+v", cfg.SessionCookie)
	}
	if cfg.EnrollRateLimit.MaxAttempts != 30 || cfg.EnrollRateLimit.Window != time.Minute {
		t.Fatalf("enroll rate limit: %+v", cfg.EnrollRateLimit)
	}
}

func TestLoadDefaultsCookieName(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	os.WriteFile(p, []byte(`listen: "127.0.0.1:8080"`+"\n"), 0o600)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SessionCookie.Name != "agentmon_session" {
		t.Fatalf("cookie name default not applied: %q", cfg.SessionCookie.Name)
	}
}

func TestStatePollInterval(t *testing.T) {
	t.Run("parses_explicit_value", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "config.yaml")
		os.WriteFile(p, []byte("listen: \"127.0.0.1:8080\"\nstate_poll_interval: \"10s\"\n"), 0o600)
		cfg, err := Load(p)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.StatePollInterval != 10*time.Second {
			t.Fatalf("state_poll_interval: got %v, want 10s", cfg.StatePollInterval)
		}
	})

	t.Run("zero_when_unset", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "config.yaml")
		os.WriteFile(p, []byte("listen: \"127.0.0.1:8080\"\n"), 0o600)
		cfg, err := Load(p)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.StatePollInterval != 0 {
			t.Fatalf("state_poll_interval: expected zero when unset, got %v", cfg.StatePollInterval)
		}
	})
}
