package authn

import "net/http"

// CheckOrigin guards credentialed cross-origin requests (login + cookie-authed
// mutations). A present Origin must equal externalOrigin; an absent Origin
// (same-origin navigation or a non-browser client) is allowed. SameSite=Lax on
// the session cookie is the companion control.
func CheckOrigin(r *http.Request, externalOrigin string) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	return o == externalOrigin
}
