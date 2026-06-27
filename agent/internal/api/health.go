package api

import (
	"encoding/json"
	"net/http"
)

// HealthHandler reports liveness. tmuxAvailable is resolved once at startup
// (passed in) rather than per request. Healthz is intentionally unauthenticated.
func HealthHandler(serverID, version string, tmuxAvailable bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":            true,
			"version":       version,
			"serverId":      serverID,
			"tmuxAvailable": tmuxAvailable,
		})
	}
}
