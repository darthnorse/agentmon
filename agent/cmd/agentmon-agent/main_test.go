package main

import (
	"net/http"
	"testing"
	"time"
)

func TestNewAgentServerTimeouts(t *testing.T) {
	s := newAgentServer(":0", http.NewServeMux())
	if s.ReadHeaderTimeout != 10*time.Second {
		t.Errorf("ReadHeaderTimeout = %v, want 10s", s.ReadHeaderTimeout)
	}
	if s.ReadTimeout != 30*time.Second {
		t.Errorf("ReadTimeout = %v, want 30s", s.ReadTimeout)
	}
	if s.IdleTimeout != 120*time.Second {
		t.Errorf("IdleTimeout = %v, want 120s", s.IdleTimeout)
	}
	if s.WriteTimeout != 0 {
		t.Errorf("WriteTimeout = %v, want 0 (the long-lived pane-IO WS must not be killed)", s.WriteTimeout)
	}
	if s.Addr != ":0" {
		t.Errorf("Addr = %q, want :0", s.Addr)
	}
}
