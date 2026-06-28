package authn

import (
	"crypto/subtle"
	"net/http"
)

// CheckCSRF reports whether the request carries the session's CSRF token in the
// X-CSRF-Token header. The session cookie is SameSite=Lax; this synchronizer
// token is defense-in-depth for cookie-authed mutations.
func CheckCSRF(r *http.Request, sess Session) bool {
	got := r.Header.Get("X-CSRF-Token")
	if got == "" || sess.CSRFToken == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(sess.CSRFToken)) == 1
}
