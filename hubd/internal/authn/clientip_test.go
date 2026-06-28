package authn

import (
	"net/http/httptest"
	"testing"
)

func TestClientIPTrustedXFFLastHop(t *testing.T) {
	// trusted + XFF "1.1.1.1, 2.2.2.2" → "2.2.2.2" (last hop, ignores spoofed prefix)
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Forwarded-For", "1.1.1.1, 2.2.2.2")
	r.RemoteAddr = "10.0.0.1:1234" // proxy's addr, ignored when XFF is present
	got := ClientIP(r, true)
	if got != "2.2.2.2" {
		t.Fatalf("trusted+XFF: want 2.2.2.2, got %q", got)
	}
}

func TestClientIPUntrustedIgnoresXFF(t *testing.T) {
	// untrusted proxy → ignore XFF, fall back to RemoteAddr host
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Forwarded-For", "1.1.1.1, 2.2.2.2")
	r.RemoteAddr = "3.3.3.3:5555"
	got := ClientIP(r, false)
	if got != "3.3.3.3" {
		t.Fatalf("untrusted+XFF: want 3.3.3.3 (from RemoteAddr), got %q", got)
	}
}

func TestClientIPNoXFF(t *testing.T) {
	// no XFF header → host from RemoteAddr
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "4.4.4.4:9090"
	got := ClientIP(r, true)
	if got != "4.4.4.4" {
		t.Fatalf("no XFF: want 4.4.4.4, got %q", got)
	}
}
