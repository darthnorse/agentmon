package api

import (
	"encoding/json"
	"net/http"
	"os/exec"
)

func HealthHandler(serverID, version string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, tmuxErr := exec.LookPath("tmux")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":            true,
			"version":       version,
			"serverId":      serverID,
			"tmuxAvailable": tmuxErr == nil,
		})
	}
}
