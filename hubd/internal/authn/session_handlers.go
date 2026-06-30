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
// to JS). Must be mounted behind RequireAuth, which stamps both the principal
// and the CSRF token into the request context — no second Store.Get needed.
func (a *Authenticator) MeHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := PrincipalFrom(r.Context())
		if !ok {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		csrf, _ := csrfFrom(r.Context())
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"principalId": p.ID, "username": p.Username,
			"displayName": p.DisplayName, "csrfToken": csrf,
			"mustChangePassword": p.MustChangePassword,
		})
	}
}
