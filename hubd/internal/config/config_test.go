package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadResolvesServerSecretRefs(t *testing.T) {
	t.Setenv("SRVA_TOKEN", "tok-a")
	t.Setenv("SRVA_KEY", "key-a")
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	os.WriteFile(p, []byte(`
listen: "127.0.0.1:8080"
external_origin: "https://agentmon.lan"
trust_forwarded_proto: true
data_dir: "/data"
servers:
  - id: server-a
    name: server-a
    url: "http://10.0.0.5:8377"
    token_ref: "env:SRVA_TOKEN"
    signing_key_ref: "env:SRVA_KEY"
`), 0o600)

	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Servers) != 1 {
		t.Fatalf("servers: %+v", cfg.Servers)
	}
	s := cfg.Servers[0]
	if s.Token != "tok-a" || s.SigningKey != "key-a" {
		t.Fatalf("secret refs not resolved: %+v", s)
	}
	if !cfg.TrustForwardedProto || cfg.ExternalOrigin != "https://agentmon.lan" {
		t.Fatalf("bad cfg: %+v", cfg)
	}
}

func TestLoadEmptyTokenRefErrors(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(`
listen: "127.0.0.1:8080"
external_origin: "https://agentmon.lan"
data_dir: "/data"
servers:
  - id: server-a
    name: server-a
    url: "http://10.0.0.5:8377"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for server with no token_ref/signing_key_ref, got nil")
	}
}

func TestLoadUnsetEnvRefErrors(t *testing.T) {
	// Even when the env var is set, a bare-literal signing_key_ref must be rejected.
	t.Setenv("SRVA_TOKEN", "tok-a")
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(`
listen: "127.0.0.1:8080"
external_origin: "https://agentmon.lan"
data_dir: "/data"
servers:
  - id: server-a
    name: server-a
    url: "http://10.0.0.5:8377"
    token_ref: "env:SRVA_TOKEN"
    signing_key_ref: "literal-key"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(p)
	if err == nil {
		t.Fatal("bare literal signing_key_ref must be rejected even when env is set")
	}
}
