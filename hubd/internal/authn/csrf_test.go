package authn

import (
	"net/http/httptest"
	"testing"
)

func TestCheckCSRF(t *testing.T) {
	sess := Session{CSRFToken: "tok123"}
	r := httptest.NewRequest("POST", "/x", nil)
	if CheckCSRF(r, sess) {
		t.Fatal("missing header must fail")
	}
	r.Header.Set("X-CSRF-Token", "tok123")
	if !CheckCSRF(r, sess) {
		t.Fatal("matching header must pass")
	}
	r.Header.Set("X-CSRF-Token", "wrong")
	if CheckCSRF(r, sess) {
		t.Fatal("mismatched header must fail")
	}
}
