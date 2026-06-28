package authn

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSetSessionCookieAttributes(t *testing.T) {
	w := httptest.NewRecorder()
	SetSessionCookie(w, "agentmon_session", "tok", true, time.Hour)
	c := w.Result().Cookies()[0]
	if c.Name != "agentmon_session" || c.Value != "tok" || !c.HttpOnly || !c.Secure ||
		c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
		t.Fatalf("cookie attrs: %+v", c)
	}
}

func TestSecureFromRequest(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	if !SecureFromRequest(r, true) {
		t.Fatal("trusted https forward must be secure")
	}
	if SecureFromRequest(r, false) {
		t.Fatal("untrusted forward must not be secure")
	}
}
