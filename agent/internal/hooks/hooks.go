// Package hooks implements the agent's local Claude Code hook intake.
package hooks

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"agentmon/agent/internal/config"
	"agentmon/agent/internal/state"
	"agentmon/agent/internal/tmux"
)

// RequireHookAuth gates POST /hook with the hook token, using a constant-time,
// length-safe SHA-256 compare (mirrors api.RequireBearer). token must be non-empty
// (the route is only mounted when cfg.HookToken != "").
func RequireHookAuth(token string, next http.Handler) http.Handler {
	want := sha256.Sum256([]byte(token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		got := sha256.Sum256([]byte(presented))
		if !ok || subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hookBody is the tolerant subset read from Claude's hook event JSON. Unknown and
// extra fields are ignored (forward-compat, design §18-Q3).
type hookBody struct {
	HookEventName    string `json:"hook_event_name"`
	NotificationType string `json:"notification_type"`
	SessionID        string `json:"session_id"`
}

// HookHandler applies a correlated hook to the state machine. It returns 204 on the
// happy path AND on every soft failure (unknown socket, bad pane, missing $TMUX,
// unparseable body) so a hook never breaks or stalls Claude. now defaults to
// time.Now when nil. (Token auth and loopback are handled before/here respectively.)
func HookHandler(cfg config.Config, m *state.Machine, now func() time.Time) http.HandlerFunc {
	if now == nil {
		now = time.Now
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if !isLoopback(r.RemoteAddr) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		pane := r.Header.Get("X-AgentMon-Pane")
		socket := socketFromTmux(r.Header.Get("X-AgentMon-Tmux"))
		target, matched := matchTarget(cfg, socket)
		if !tmux.ValidatePaneID(pane) || !matched {
			log.Printf("hook: soft drop (pane=%q socket=%q matched=%v)", pane, socket, matched)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var body hookBody
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		if err := dec.Decode(&body); err != nil || body.HookEventName == "" {
			log.Printf("hook: soft drop (bad body / no event name): %v", err)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		m.Apply(state.Event{
			Target:           target,
			Pane:             pane,
			Name:             body.HookEventName,
			NotificationKind: body.NotificationType,
			ClaudeSessionID:  body.SessionID,
			At:               now(),
		})
		w.WriteHeader(http.StatusNoContent)
	}
}

// socketFromTmux extracts the socket name from $TMUX ("<path>,<pid>,<idx>"): the
// basename of the path before the first comma. "" when $TMUX is empty/malformed.
func socketFromTmux(tmuxEnv string) string {
	if tmuxEnv == "" {
		return ""
	}
	path := tmuxEnv
	if i := strings.IndexByte(tmuxEnv, ','); i >= 0 {
		path = tmuxEnv[:i]
	}
	if path == "" {
		return ""
	}
	return filepath.Base(path)
}

// matchTarget maps a tmux socket name to a configured target label. The default
// socket is named "default" on disk but configured as SocketName "".
func matchTarget(cfg config.Config, socket string) (string, bool) {
	if socket == "" {
		return "", false
	}
	for _, t := range cfg.Targets {
		if t.SocketName == socket || (socket == "default" && t.SocketName == "") {
			return t.Label, true
		}
	}
	return "", false
}

func isLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
