package api

import (
	"net/http"

	"agentmon/hubd/internal/authn"
)

// onboardRateLimit caps the rate of the open onboarding endpoints per client IP.
// Unlike the login limiter (which counts failures), this records EVERY request,
// so the sliding window bounds total onboarding traffic from one IP.
func onboardRateLimit(l *authn.Limiter, trustForwardedProto bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := authn.ClientIP(r, trustForwardedProto)
		if !l.Allowed(ip) {
			writeJSONError(w, http.StatusTooManyRequests, "too many attempts")
			return
		}
		l.Fail(ip) // record this request in the window
		next.ServeHTTP(w, r)
	})
}
