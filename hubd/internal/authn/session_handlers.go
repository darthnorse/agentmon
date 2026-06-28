package authn

import (
	"encoding/json"
	"net/http"
)

func (a *Authenticator) LogoutHandler(trustForwardedProto bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(a.CookieName); err == nil {
			a.Store.Delete(c.Value)
		}
		ClearSessionCookie(w, a.CookieName, SecureFromRequest(r, trustForwardedProto))
		w.WriteHeader(http.StatusNoContent)
	}
}

// MeHandler returns the current principal plus the session CSRF token (the SPA
// needs it to send X-CSRF-Token on mutations; the HttpOnly cookie is unreadable
// to JS). It re-reads the cookie to surface the CSRF token. Mount behind
// RequireAuth so an absent/expired session is already 401.
func (a *Authenticator) MeHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(a.CookieName)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		sess, ok := a.Store.Get(c.Value)
		if !ok {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"principalId": sess.PrincipalID, "username": sess.Username,
			"displayName": sess.DisplayName, "csrfToken": sess.CSRFToken,
		})
	}
}
