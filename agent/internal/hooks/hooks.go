// Package hooks implements the agent's local coding-agent hook intake.
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

// RequireLoopback rejects any request that did not originate from a loopback
// address, returning 403. This runs before token auth so the token is never
// inspected for non-loopback callers (prevents a remote token-validity oracle).
func RequireLoopback(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopback(r.RemoteAddr) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hookBody is the tolerant subset read from coding-agent hook event JSON.
// Unknown/extra fields are ignored so newer hook versions don't break older agents.
type hookBody struct {
	HookEventName    string `json:"hook_event_name"`
	NotificationType string `json:"notification_type"`
	SessionID        string `json:"session_id"`
}

// HookHandler applies a correlated hook to the state machine. It returns 204 on the
// happy path AND on every soft failure (unknown socket, bad pane, missing $TMUX,
// unparseable body) so a hook never breaks or stalls the coding agent. now defaults to
// time.Now when nil. Token auth and loopback are handled by middleware before this.
func HookHandler(cfg config.Config, m *state.Machine, now func() time.Time) http.HandlerFunc {
	if now == nil {
		now = time.Now
	}
	return func(w http.ResponseWriter, r *http.Request) {
		pane := r.Header.Get("X-AgentMon-Pane")
		tmuxEnv := r.Header.Get("X-AgentMon-Tmux")
		socket := SocketFromTmux(tmuxEnv)
		t, matched := MatchTarget(cfg, socket)
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
			Target:           t.Label,
			Pane:             pane,
			Name:             body.HookEventName,
			NotificationKind: body.NotificationType,
			ClaudeSessionID:  body.SessionID,
			Epoch:            epochFromTmux(tmuxEnv),
			At:               now(),
		})
		w.WriteHeader(http.StatusNoContent)
	}
}

// epochFromTmux extracts the tmux server pid (field 2 of $TMUX
// "<path>,<pid>,<idx>"). "" when absent/malformed — epoch is best-effort.
func epochFromTmux(tmuxEnv string) string {
	parts := strings.SplitN(tmuxEnv, ",", 3)
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

// SocketFromTmux extracts the socket name from $TMUX ("<path>,<pid>,<idx>"): the
// basename of the path before the first comma. "" when $TMUX is empty/malformed.
func SocketFromTmux(tmuxEnv string) string {
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

// MatchTarget maps a tmux socket name to its configured target. The default
// socket is named "default" on disk but configured as SocketName "". Exported
// for the report intake, which needs the target's SocketName (tmux calls) as
// well as its Label (store key).
func MatchTarget(cfg config.Config, socket string) (config.Target, bool) {
	if socket == "" {
		return config.Target{}, false
	}
	for _, t := range cfg.Targets {
		if t.SocketName == socket || (socket == "default" && t.SocketName == "") {
			return t, true
		}
	}
	return config.Target{}, false
}

func isLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
