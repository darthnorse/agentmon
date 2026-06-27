package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"regexp"
	"time"

	"github.com/gorilla/websocket"

	"agentmon/agent/internal/config"
	"agentmon/agent/internal/directive"
	"agentmon/agent/internal/tmux"
	"agentmon/shared"
)

var wsPaneIDRe = regexp.MustCompile(`^%[0-9]+$`)

// PaneConn is the slice of *tmux.ControlClient the WS handler needs; injected so
// the handler is unit-testable without a real tmux.
type PaneConn interface {
	OutputChan() <-chan []byte
	DoneChan() <-chan struct{}
	SendInput([]byte) error
	Resize(cols, rows int) error
	Close()
}

// PaneIO serves WS /panes/{paneId}/io. The tmux-facing operations are seams so the
// framing/mode logic can be tested with fakes (production binds the tmux package).
type PaneIO struct {
	Cfg       config.Config
	Verifier  *directive.Verifier
	Run       tmux.Runner
	Capture   func(ctx context.Context, socket, pane string, lines int) ([]byte, error)
	NewClient func(ctx context.Context, socket, session, pane string) (PaneConn, error)
	Tune      func(ctx context.Context, socket, session string)
}

func (h *PaneIO) upgrader() websocket.Upgrader {
	// The agent is LAN-only and already requires a bearer token AND a hub-signed
	// directive that a browser cannot forge, so an Origin check here would add
	// nothing; the browser-facing Origin check lives at the hub (spec §13.4).
	return websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
}

func (h *PaneIO) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		paneID := r.PathValue("paneId")
		if !wsPaneIDRe.MatchString(paneID) {
			writeJSONError(w, http.StatusBadRequest, "invalid pane id")
			return
		}
		target, ok := h.Cfg.ResolveTarget(r.URL.Query().Get("target"))
		if !ok {
			writeJSONError(w, http.StatusNotFound, "unknown target")
			return
		}
		mode := r.URL.Query().Get("mode")
		if mode != "ro" && mode != "rw" {
			writeJSONError(w, http.StatusBadRequest, "mode must be ro|rw")
			return
		}

		wantResource := shared.PaneID(h.Cfg.ServerID, target.Label, paneID)
		dir, err := h.Verifier.Verify(r.Header.Get("X-AgentMon-Directive"), wantResource, target.Label)
		if err != nil {
			log.Printf("ws: directive rejected (pane=%s target=%s): %v", paneID, target.Label, err)
			writeJSONError(w, http.StatusForbidden, "directive rejected")
			return
		}
		// The signed directive's mode is authoritative; the URL mode must agree.
		if dir.Mode != mode {
			log.Printf("ws: url mode %q != directive mode %q", mode, dir.Mode)
			writeJSONError(w, http.StatusForbidden, "mode mismatch")
			return
		}

		sessionID, ok, err := tmux.ResolvePaneSession(r.Context(), h.Run, target.SocketName, paneID)
		if err != nil {
			log.Printf("ws: resolve pane %s: %v", paneID, err)
			writeJSONError(w, http.StatusInternalServerError, "resolve failed")
			return
		}
		if !ok {
			writeJSONError(w, http.StatusNotFound, "pane not found")
			return
		}

		upgrader := h.upgrader()
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return // Upgrade already wrote the error
		}
		defer conn.Close()

		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		if h.Tune != nil {
			h.Tune(ctx, target.SocketName, sessionID)
		}
		cc, err := h.NewClient(ctx, target.SocketName, sessionID, paneID)
		if err != nil {
			log.Printf("ws: control client: %v", err)
			return
		}
		defer cc.Close()

		// 1) scrollback bootstrap before any live output. Deadline so a stalled
		// client cannot block the handler on this pre-pump write.
		if snap, err := h.Capture(ctx, target.SocketName, paneID, h.Cfg.ScrollbackLines); err == nil && len(snap) > 0 {
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			_ = conn.WriteMessage(websocket.BinaryMessage, snap)
		}

		// 2) sole writer: live output + keepalive pings.
		go writePump(ctx, cancel, conn, cc)

		// 3) reader: binary input (rw only) + JSON control (resize).
		readPump(cancel, conn, cc, dir.Mode)
	}
}

func writePump(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, cc PaneConn) {
	// A writer-side exit (tmux gone, or an output/ping write error) must force
	// readPump's blocked ReadMessage to return so the handler's deferred
	// cc.Close()/conn.Close() actually run — a hung client would otherwise leak
	// the handler goroutine AND the live tmux control-mode subprocess. Setting a
	// past read deadline unblocks ReadMessage with an error; on the normal path
	// (writePump returns via <-ctx.Done() after readPump already exited) it is a
	// harmless no-op.
	defer conn.SetReadDeadline(time.Now())
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-cc.DoneChan():
			_ = conn.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseGoingAway, "tmux gone"),
				time.Now().Add(2*time.Second))
			cancel()
			return
		case b, ok := <-cc.OutputChan():
			if !ok {
				return
			}
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.BinaryMessage, b); err != nil {
				cancel()
				return
			}
		case <-ping.C:
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
				cancel()
				return
			}
		}
	}
}

func readPump(cancel context.CancelFunc, conn *websocket.Conn, cc PaneConn, mode string) {
	conn.SetReadLimit(1 << 20)
	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			cancel()
			return
		}
		switch mt {
		case websocket.BinaryMessage:
			if mode != "rw" {
				continue // mechanical read-only: drop input
			}
			if err := cc.SendInput(data); err != nil {
				cancel()
				return
			}
		case websocket.TextMessage:
			var f shared.ResizeFrame
			if json.Unmarshal(data, &f) == nil && f.Type == shared.FrameResize {
				_ = cc.Resize(f.Cols, f.Rows)
			}
		}
	}
}
