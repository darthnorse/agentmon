package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"github.com/gorilla/websocket"

	"agentmon/hubd/internal/authn"
	"agentmon/hubd/internal/authz"
	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/state"
	"agentmon/shared"
)

// hubPaneIDRe guards the pane id at the hub; the agent re-validates authoritatively
// before any send-keys. Same shape as the agent's tmux.ValidatePaneID.
var hubPaneIDRe = regexp.MustCompile(`^%[0-9]+$`)

const (
	relayWriteWait   = 10 * time.Second
	relayDialTimeout = 10 * time.Second
	// relayBrowserReadLimit caps untrusted browser→hub frames (keystrokes/control);
	// 1 MiB is generous and matches the agent's own inbound read limit.
	relayBrowserReadLimit = 1 << 20
	// relayAgentReadLimit caps trusted agent→hub frames. The agent sends the whole
	// scrollback snapshot as one binary message (up to ScrollbackLines, color-escaped),
	// which can exceed 1 MiB — so this is much larger, but still bounded against a
	// runaway agent rather than unlimited.
	relayAgentReadLimit    = 32 << 20
	defaultRelayPongWait   = 60 * time.Second
	defaultRelayPingPeriod = 20 * time.Second // must stay < defaultRelayPongWait
)

// wsStateFrame is the JSON payload for hub-originated {t:"state"} control frames
// sent to the browser over the terminal WebSocket.
type wsStateFrame struct {
	T       string       `json:"t"`
	State   shared.State `json:"state"`
	Session string       `json:"session"`
}

// relayStateInfo carries per-connection metadata for injecting {t:"state"} frames
// into the browser-writer goroutine. A nil pointer disables state-frame injection
// (Bcast nil, or pane not found in any session, or Sessions call failed).
//
// Seen-projection is snapshotted once at connect time; a POST /seen during an
// active relay is not reflected until the browser reconnects.
type relayStateInfo struct {
	serverID    string
	target      string // canonical Target from the found session
	sessionName string
	seen        db.PrincipalSeen // snapshotted at connect time
	seenOK      bool
	stateCh     <-chan state.Change // broadcaster subscription channel
}

// relayMsg carries a single agent→browser frame from the agent-reader goroutine
// to the single browser-writer goroutine.
type relayMsg struct {
	mt   int
	data []byte
}

// PaneRelayHandler serves GET /api/v1/servers/{id}/panes/{paneId}/io. RequireAuth
// has already stamped the principal. It authorizes terminal.write, checks Origin,
// mints a directive with the per-server signing key, dials the agent's WS carrying
// it, upgrades the browser, audits terminal.open, and relays frames transparently.
//
// When Deps.Bcast is non-nil the handler also resolves which session owns paneID
// (one HTTP call to d.Agent.Sessions), subscribes to the broadcaster, and enables
// the injection of {t:"state"} JSON text frames interleaved with binary terminal
// frames.  If the session lookup fails or the pane is not found, terminal bytes are
// still relayed normally without state frames.
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

		// Phase 5: cap concurrent relays per principal (reject-newest). Acquire
		// before the agent dial so a rejected relay does no wasted work; the
		// deferred Release runs on EVERY exit path (dial failure, upgrade
		// failure, or normal relayPanes return), so a slot is never leaked.
		if d.RelayCap != nil {
			if !d.RelayCap.Acquire(p.ID) {
				writeJSONError(w, http.StatusTooManyRequests, "too many terminal sessions")
				return
			}
			defer d.RelayCap.Release(p.ID)
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

		// ── Optional: resolve pane→session and subscribe to broadcaster ──────────
		// Only when Bcast is wired up; test setups that don't set Bcast get a plain
		// M4-style relay with no state-frame injection.
		var si *relayStateInfo
		if d.Bcast != nil {
			sessions, sessErr := d.Agent.Sessions(r.Context(), srv, target)
			if sessErr == nil {
				sessName, sessTarget, sessFound := findSessionForPane(sessions, paneID)
				if sessFound {
					var ps db.PrincipalSeen
					var psOK bool
					if d.Seen != nil {
						ps, psOK, _ = d.Seen.GetSeen(r.Context(), p.ID, id, sessTarget, sessName)
					}
					_, stateCh, cancel := d.Bcast.Subscribe()
					defer cancel() // runs after relayPanes returns; all goroutines are done by then
					si = &relayStateInfo{
						serverID:    id,
						target:      sessTarget,
						sessionName: sessName,
						seen:        ps,
						seenOK:      psOK,
						stateCh:     stateCh,
					}
				}
			}
		}
		// ─────────────────────────────────────────────────────────────────────────

		pongWait := d.RelayPongWait
		if pongWait <= 0 {
			pongWait = defaultRelayPongWait
		}
		pingPeriod := d.RelayPingPeriod
		if pingPeriod <= 0 {
			pingPeriod = defaultRelayPingPeriod
		}
		relayPanes(browser, agentConn, pongWait, pingPeriod, si)
	}
}

// findSessionForPane scans the session list and returns the session name and
// canonical Target for the first session whose windows contain paneID.
// Returns ("", "", false) when the pane is not found.
func findSessionForPane(sessions []shared.Session, paneID string) (sessName, sessTarget string, found bool) {
	for _, s := range sessions {
		for _, w := range s.Windows {
			for _, pn := range w.Panes {
				if pn.ID == paneID {
					return s.Name, s.Target, true
				}
			}
		}
	}
	return "", "", false
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

// relayPanes copies WS frames in both directions until either side closes or
// errors, then tears down both conns so the peer's blocked ReadMessage unblocks
// (no leaked goroutine, no orphaned agent connection → no orphaned tmux control
// subprocess). Read deadlines are kept alive by the ping/pong exchange; writes
// use per-message deadlines. Caller must pass pingPeriod < pongWait.
//
// # Single-writer invariant
//
// gorilla/websocket requires at most one concurrent WriteMessage caller per
// connection.  The browser conn has exactly one: G3 (browser-writer).  The agent
// conn has exactly one: G1 (browser-reader goroutine that writes agent).  pingLoop
// uses WriteControl on both, which is documented safe to call concurrently with
// one outstanding WriteMessage.
//
// # Goroutine structure
//
//   - G1 (browser→agent): reads browser.ReadMessage, writes agent.WriteMessage.
//   - G2 (agent-reader): reads agent.ReadMessage, pushes relayMsg onto agentFrames.
//   - G3 (browser-writer): the SOLE goroutine calling browser.WriteMessage; selects
//     over agentFrames (agent frames → write verbatim), si.stateCh (filtered state
//     changes → write {t:"state"} JSON text frame), and teardown.
//   - pingLoop: sends WriteControl pings to both conns.
//
// # Teardown
//
// done has capacity 3 so no goroutine ever blocks sending to it.  When any
// goroutine exits, the main path closes teardown (unblocks G2's agentFrames send
// and G3's select), closes stopPing, and closes both WS conns so any goroutine
// blocked in ReadMessage or WriteMessage gets an immediate error.  G2 also closes
// agentFrames so G3's select wakes up and returns if it was blocked there.  The
// main path then drains the remaining two done sends before returning.
func relayPanes(browser, agent *websocket.Conn, pongWait, pingPeriod time.Duration, si *relayStateInfo) {
	browser.SetReadLimit(relayBrowserReadLimit)
	agent.SetReadLimit(relayAgentReadLimit)
	armLiveness(browser, pongWait)
	armLiveness(agent, pongWait)

	stopPing := make(chan struct{})
	go pingLoop(stopPing, pingPeriod, browser, agent)

	// agentFrames queues frames from G2 (agent-reader) to G3 (browser-writer).
	// Buffered so G2 doesn't stall on every frame; capacity of 8 is enough for
	// a burst while G3 is writing the previous frame.
	agentFrames := make(chan relayMsg, 8)

	// teardown is closed once by the main path to broadcast a stop signal to G2
	// (unblocks its agentFrames send) and G3 (unblocks its select).
	teardown := make(chan struct{})

	// done collects exit signals from all three goroutines. Capacity 3 guarantees
	// no goroutine ever blocks on the send.
	done := make(chan struct{}, 3)

	// G1: browser → agent.  Sole writer of the agent conn.  Unchanged from M4.
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			mt, data, err := browser.ReadMessage()
			if err != nil {
				return
			}
			_ = agent.SetWriteDeadline(time.Now().Add(relayWriteWait))
			if err := agent.WriteMessage(mt, data); err != nil {
				return
			}
		}
	}()

	// G2: agent reader.  Reads agent frames and hands them to G3 via agentFrames.
	// On exit, closes agentFrames (G3 sees the closed channel) then signals done.
	// The select on teardown prevents G2 from blocking on a full agentFrames buffer
	// if G3 has already exited due to a browser write error.
	go func() {
		defer func() {
			close(agentFrames)
			done <- struct{}{}
		}()
		for {
			mt, data, err := agent.ReadMessage()
			if err != nil {
				return
			}
			select {
			case agentFrames <- relayMsg{mt, data}:
			case <-teardown:
				return
			}
		}
	}()

	// G3: browser-writer.  The SOLE goroutine calling browser.WriteMessage.
	// Selects on:
	//   (a) agentFrames: write verbatim (binary terminal data).
	//   (b) stateCh: filter to this session, apply seen-projection, write
	//       {t:"state"} JSON text frame.  A nil stateCh is never selected.
	//   (c) teardown: exit cleanly.
	var stateCh <-chan state.Change // nil when si is nil; nil channel is never selected
	if si != nil {
		stateCh = si.stateCh
	}
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			select {
			case f, ok := <-agentFrames:
				if !ok {
					return // G2 closed agentFrames — agent side is gone
				}
				_ = browser.SetWriteDeadline(time.Now().Add(relayWriteWait))
				if err := browser.WriteMessage(f.mt, f.data); err != nil {
					return
				}
			case c, ok := <-stateCh:
				if !ok {
					// stateCh is closed only by defer cancel() after relayPanes returns; the !ok branch is defensive.
					return
				}
				// Filter: only inject frames for this relay's session.
				if c.ServerID != si.serverID || c.Target != si.target || c.Session != si.sessionName {
					continue
				}
				projected := state.SeenProject(c.Global, c.LatestReceivedAt, si.seen, si.seenOK)
				b, _ := json.Marshal(wsStateFrame{T: "state", State: projected, Session: si.sessionName})
				_ = browser.SetWriteDeadline(time.Now().Add(relayWriteWait))
				if err := browser.WriteMessage(websocket.TextMessage, b); err != nil {
					return
				}
			case <-teardown:
				return
			}
		}
	}()

	// Block until the first goroutine exits, then signal teardown and close both
	// conns so the remaining goroutines' blocked reads/writes return errors.
	<-done
	close(teardown)
	close(stopPing)
	_ = browser.Close()
	_ = agent.Close()
	<-done
	<-done
}

// armLiveness sets the initial read deadline and bumps it on every pong AND on
// every ping the peer sends (the agent pings the hub; a browser may too). The ping
// handler still sends the pong, preserving default behavior.
func armLiveness(c *websocket.Conn, pongWait time.Duration) {
	_ = c.SetReadDeadline(time.Now().Add(pongWait))
	c.SetPongHandler(func(string) error {
		return c.SetReadDeadline(time.Now().Add(pongWait))
	})
	c.SetPingHandler(func(msg string) error {
		_ = c.SetReadDeadline(time.Now().Add(pongWait))
		err := c.WriteControl(websocket.PongMessage, []byte(msg), time.Now().Add(relayWriteWait))
		if err == websocket.ErrCloseSent {
			return nil
		}
		return err
	})
}

// pingLoop sends periodic pings to both conns. WriteControl is documented safe to
// call concurrently with the single-writer WriteMessage in each copy goroutine.
func pingLoop(stop <-chan struct{}, pingPeriod time.Duration, conns ...*websocket.Conn) {
	t := time.NewTicker(pingPeriod)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			for _, c := range conns {
				_ = c.WriteControl(websocket.PingMessage, nil, time.Now().Add(relayWriteWait))
			}
		}
	}
}
