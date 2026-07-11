package report

import (
	"encoding/json"
	"net/http"
	"strconv"

	"agentmon/agent/internal/config"
)

// DrainHandler serves GET /orchestrator/reports?target=&instance=&ack= —
// hub-bearer-authed (mounted behind api.RequireBearer; NOT loopback — the hub
// dials it). Ack-on-next-drain: instance+ack acknowledge (and delete) the
// batch the hub received on its PREVIOUS poll; the response carries everything
// still buffered for the target (design doc §4). GET-with-deletion is
// deliberate and matches the protocol: the deletion is the ack of already-
// delivered data, never of the data being returned.
func DrainHandler(cfg config.Config, st *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t, ok := cfg.ResolveTarget(r.URL.Query().Get("target"))
		if !ok {
			writeError(w, http.StatusNotFound, "unknown target")
			return
		}
		var ack uint64
		if s := r.URL.Query().Get("ack"); s != "" {
			v, err := strconv.ParseUint(s, 10, 64)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid ack cursor")
				return
			}
			ack = v
		}
		batch := st.Drain(t.Label, r.URL.Query().Get("instance"), ack)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(batch)
	}
}
