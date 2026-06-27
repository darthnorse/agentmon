package api

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
)

// RequireBearer rejects any request whose Authorization header is not exactly
// "Bearer <token>". The comparison is constant-time. token must be non-empty
// (enforced at startup in main).
func RequireBearer(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "Bearer "
		h := r.Header.Get("Authorization")
		presented, ok := strings.CutPrefix(h, prefix)
		if !ok || subtle.ConstantTimeCompare([]byte(presented), []byte(token)) != 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}
