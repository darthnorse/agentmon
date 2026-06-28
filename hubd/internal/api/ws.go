package api

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"github.com/gorilla/websocket"

	"agentmon/hubd/internal/authn"
	"agentmon/hubd/internal/authz"
	"agentmon/shared"
)

// hubPaneIDRe guards the pane id at the hub; the agent re-validates authoritatively
// before any send-keys. Same shape as the agent's tmux.ValidatePaneID.
var hubPaneIDRe = regexp.MustCompile(`^%[0-9]+$`)

// Relay tunables. pongWait/pingPeriod are vars so tests can shrink them. There is
// deliberately NO global server WriteTimeout for the WS route; writes use per-
// message deadlines (relayWriteWait) instead.
var (
	relayPongWait   = 60 * time.Second
	relayPingPeriod = 20 * time.Second
)

const (
	relayWriteWait   = 10 * time.Second
	relayDialTimeout = 10 * time.Second
	relayReadLimit   = 1 << 20
)

// PaneRelayHandler serves GET /api/v1/servers/{id}/panes/{paneId}/io. RequireAuth
// has already stamped the principal. It authorizes terminal.write, checks Origin,
// mints a directive with the per-server signing key, dials the agent's WS carrying
// it, upgrades the browser, audits terminal.open, and relays frames transparently.
func (d Deps) PaneRelayHandler() http.HandlerFunc {
	up := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return authn.CheckOrigin(r, d.ExternalOrigin) },
	}
	dialer := &websocket.Dialer{HandshakeTimeout: relayDialTimeout}
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		paneID := r.PathValue("paneId")
		if !hubPaneIDRe.MatchString(paneID) {
			writeJSONError(w, http.StatusBadRequest, "invalid pane id")
			return
		}
		target := r.URL.Query().Get("target")
		if target == "" {
			target = "default"
		}
		resource := shared.PaneID(id, target, paneID)

		p, ok := d.authorizeOr403(w, r, authz.TerminalWrite, resource)
		if !ok {
			return // deny audited + 403 by the chokepoint
		}
		// WS CSRF defense: a GET upgrade carries no X-CSRF-Token, so enforce the
		// Origin check explicitly before any agent dial (clean 403, no wasted dial).
		if !authn.CheckOrigin(r, d.ExternalOrigin) {
			writeJSONError(w, http.StatusForbidden, "bad origin")
			return
		}

		srv, found, err := d.Reg.Get(r.Context(), id)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if !found {
			writeJSONError(w, http.StatusNotFound, "unknown server")
			return
		}

		header, reqID, err := d.Minter.Mint(srv, p.ID, paneID, target)
		if err != nil {
			log.Printf("relay: mint (server=%s): %v", id, err)
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}

		agentURL, err := agentWSURL(srv.URL, paneID, target)
		if err != nil {
			log.Printf("relay: agent url (server=%s): %v", id, err)
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		hdr := http.Header{}
		hdr.Set("Authorization", "Bearer "+srv.Bearer)
		hdr.Set("X-AgentMon-Directive", header)
		hdr.Set("X-AgentMon-Request-Id", reqID)
		agentConn, resp, err := dialer.DialContext(r.Context(), agentURL, hdr)
		if err != nil {
			if resp != nil {
				log.Printf("relay: dial agent %s: %v (status %d)", id, err, resp.StatusCode)
			} else {
				log.Printf("relay: dial agent %s: %v", id, err)
			}
			writeJSONError(w, http.StatusBadGateway, "agent unavailable")
			return
		}
		defer agentConn.Close()

		browser, err := up.Upgrade(w, r, nil)
		if err != nil {
			return // Upgrade wrote the response; agentConn closed via defer
		}
		defer browser.Close()

		d.Audit.TerminalOpen(r.Context(), p.ID, resource, "rw",
			authn.ClientIP(r, d.TrustForwardedProto), r.UserAgent())
		_ = d.Reg.TouchLastSeen(r.Context(), id)

		relayPanes(browser, agentConn)
	}
}

// agentWSURL builds the agent dial URL: scheme http→ws / https→wss, the pane id
// URL-escaped into the path (%3 → %253), mode pinned to rw.
func agentWSURL(rawURL, paneID, target string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	if u.Host == "" {
		return "", fmt.Errorf("server url has no host: %q", rawURL)
	}
	scheme := "ws"
	if u.Scheme == "https" {
		scheme = "wss"
	}
	return scheme + "://" + u.Host + "/panes/" + url.PathEscape(paneID) +
		"/io?target=" + url.QueryEscape(target) + "&mode=rw", nil
}

// relayPanes copies WS frames transparently in both directions until either side
// closes/errors, then tears down both conns so the peer's blocked ReadMessage
// unblocks (no leaked goroutine, no orphaned agent connection → no orphaned tmux
// control subprocess). Liveness (ping/pong + read deadlines) is added in the next
// task; this is the byte-faithful core.
func relayPanes(browser, agent *websocket.Conn) {
	browser.SetReadLimit(relayReadLimit)
	agent.SetReadLimit(relayReadLimit)

	done := make(chan struct{}, 2)
	copyFrames := func(dst, src *websocket.Conn) {
		defer func() { done <- struct{}{} }()
		for {
			mt, data, err := src.ReadMessage()
			if err != nil {
				return
			}
			_ = dst.SetWriteDeadline(time.Now().Add(relayWriteWait))
			if err := dst.WriteMessage(mt, data); err != nil {
				return
			}
		}
	}
	go copyFrames(agent, browser) // browser → agent
	go copyFrames(browser, agent) // agent → browser

	<-done              // first side finished
	_ = browser.Close() // unblock the other copy's ReadMessage
	_ = agent.Close()
	<-done // wait for the second
}
