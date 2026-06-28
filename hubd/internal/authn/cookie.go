package authn

import (
	"net/http"
	"time"
)

func SetSessionCookie(w http.ResponseWriter, name, token string, secure bool, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: token, Path: "/",
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
		MaxAge: int(ttl.Seconds()),
	})
}

func ClearSessionCookie(w http.ResponseWriter, name string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: "", Path: "/",
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
		MaxAge: -1,
	})
}

// SecureFromRequest reports whether the cookie should carry the Secure flag.
// Behind Caddy the LAN hop is plain HTTP, so we trust X-Forwarded-Proto only
// when configured to (trust_forwarded_proto); otherwise fall back to r.TLS.
func SecureFromRequest(r *http.Request, trustForwardedProto bool) bool {
	if trustForwardedProto && r.Header.Get("X-Forwarded-Proto") == "https" {
		return true
	}
	return r.TLS != nil
}
