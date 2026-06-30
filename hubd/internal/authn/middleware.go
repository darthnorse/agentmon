package authn

import (
	"context"
	"encoding/json"
	"net/http"

	"agentmon/hubd/internal/authz"
)

type ctxKey int

const (
	principalKey ctxKey = iota
	csrfKey
)

// PrincipalFrom retrieves the authenticated principal from the request context.
// Returns the zero Principal and false if none has been stamped.
func PrincipalFrom(ctx context.Context) (authz.Principal, bool) {
	p, ok := ctx.Value(principalKey).(authz.Principal)
	return p, ok
}

// ContextWithPrincipal stores p into ctx under the same key that PrincipalFrom
// reads. Handler tests (Task 11/12) use this to inject a principal directly
// without going through the session cookie machinery.
func ContextWithPrincipal(ctx context.Context, p authz.Principal) context.Context {
	return context.WithValue(ctx, principalKey, p)
}

// contextWithCSRF stores the CSRF token into ctx so that MeHandler can read it
// without a second Store.Get (RequireAuth already holds the session).
func contextWithCSRF(ctx context.Context, csrf string) context.Context {
	return context.WithValue(ctx, csrfKey, csrf)
}

// csrfFrom retrieves the CSRF token stashed by RequireAuth.
func csrfFrom(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(csrfKey).(string)
	return v, ok
}

// Authenticator is the edge middleware that resolves a session cookie to an
// authz.Principal and stamps it into the request context.
type Authenticator struct {
	Store      *Store
	CookieName string
}

// RequireAuth is an http.Handler middleware. It reads the session cookie,
// looks it up in the store, enforces CSRF on mutating methods, and stamps an
// authz.Principal into the request context before calling next.
func (a *Authenticator) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			if !CheckCSRF(r, sess) {
				writeErr(w, http.StatusForbidden, "csrf")
				return
			}
		}
		p := authz.Principal{
			ID:                 sess.PrincipalID,
			Username:           sess.Username,
			DisplayName:        sess.DisplayName,
			MustChangePassword: sess.MustChangePassword,
		}
		ctx := ContextWithPrincipal(r.Context(), p)
		ctx = contextWithCSRF(ctx, sess.CSRFToken)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
