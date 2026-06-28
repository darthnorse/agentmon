package authn

import (
	"net/http/httptest"
	"testing"
)

func TestCheckOrigin(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/v1/auth/login", nil)
	if !CheckOrigin(r, "https://agentmon.lan") {
		t.Fatal("absent Origin should pass")
	}
	r.Header.Set("Origin", "https://agentmon.lan")
	if !CheckOrigin(r, "https://agentmon.lan") {
		t.Fatal("matching Origin should pass")
	}
	r.Header.Set("Origin", "https://evil.example")
	if CheckOrigin(r, "https://agentmon.lan") {
		t.Fatal("mismatched Origin must fail")
	}
}
