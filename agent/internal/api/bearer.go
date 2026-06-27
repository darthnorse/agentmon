package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

// RequireBearer rejects any request whose Authorization header is not exactly
// "Bearer <token>". The token check compares fixed-size SHA-256 digests with a
// constant-time compare, so neither the token bytes NOR its length leak through
// timing (a raw ConstantTimeCompare returns early on a length mismatch). token
// must be non-empty (enforced at startup in main).
func RequireBearer(token string, next http.Handler) http.Handler {
	want := sha256.Sum256([]byte(token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "Bearer "
		presented, ok := strings.CutPrefix(r.Header.Get("Authorization"), prefix)
		got := sha256.Sum256([]byte(presented))
		if !ok || subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}
