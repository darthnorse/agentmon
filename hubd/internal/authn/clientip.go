package authn

import (
	"net"
	"net/http"
	"strings"
)

// ClientIP returns the client IP for audit/rate-limit use. Behind a single
// trusted proxy (Caddy), X-Forwarded-For is "spoofed?, ..., realclient" — Caddy
// appends the real peer as the LAST hop — so when forwarded headers are trusted
// we take the last comma-separated value; otherwise the direct peer (host only).
func ClientIP(r *http.Request, trustForwardedProto bool) string {
	if trustForwardedProto {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
