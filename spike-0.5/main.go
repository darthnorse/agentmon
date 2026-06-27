// Command spike-0.5 is a THROWAWAY Phase 0.5 input-fidelity spike for AgentMon.
// One binary: serves a single xterm.js page over HTTPS and relays it to one
// hard-coded tmux pane via control mode. No auth beyond a static token, no DB,
// no multi-server. The point is to delete it after the on-device go/no-go.
package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

//go:embed static
var staticFS embed.FS

type server struct {
	session    string
	pane       string
	token      string
	scrollback int
}

var upgrader = websocket.Upgrader{
	// Spike only: bound to the LAN, single static token. Do NOT copy this into
	// Phase 1 — the real hub enforces same-origin per §13.3.
	CheckOrigin: func(*http.Request) bool { return true },
}

func main() {
	addr := flag.String("addr", ":8443", "HTTPS listen address")
	httpAddr := flag.String("http", "", "optional plain-HTTP address (ws://): tests core input fidelity without cert trust; no clipboard/PWA (not a secure context)")
	session := flag.String("session", "agentmon-spike", "target tmux session")
	token := flag.String("token", "", "shared static token (required)")
	certFile := flag.String("cert", "cert.pem", "TLS cert")
	keyFile := flag.String("key", "key.pem", "TLS key")
	scrollback := flag.Int("scrollback", 5000, "scrollback lines to bootstrap")
	flag.Parse()

	if *token == "" {
		log.Fatal("a -token is required")
	}

	pane, err := resolvePane(*session)
	if err != nil {
		log.Fatalf("resolve pane for session %q: %v (is it running?)", *session, err)
	}
	tuneTmux(*session)
	log.Printf("target: session=%s pane=%s scrollback=%d", *session, pane, *scrollback)

	s := &server{session: *session, pane: pane, token: *token, scrollback: *scrollback}

	sub, _ := fs.Sub(staticFS, "static")
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(sub)))
	mux.HandleFunc("/ws", s.wsHandler)

	if *httpAddr != "" {
		go func() {
			log.Printf("serving plain HTTP on %s (ws:// — core input test, no clipboard/PWA)", *httpAddr)
			if err := http.ListenAndServe(*httpAddr, mux); err != nil {
				log.Printf("http listener stopped: %v", err)
			}
		}()
		log.Printf("phone — core input test (no cert):  http://192.168.1.51%s/?token=%s", normHostPort(*httpAddr), *token)
	}

	log.Printf("serving HTTPS on %s", *addr)
	log.Printf("phone — full test incl. clipboard/PWA (needs trusted cert):  https://192.168.1.51%s/?token=%s", normHostPort(*addr), *token)
	if err := http.ListenAndServeTLS(*addr, *certFile, *keyFile, mux); err != nil {
		log.Fatal(err)
	}
}

func (s *server) wsHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("token") != s.token {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()
	log.Printf("ws connected: %s", r.RemoteAddr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cc, err := NewControlClient(ctx, s.session, s.pane)
	if err != nil {
		log.Printf("control client: %v", err)
		return
	}
	defer cc.Close()

	// 1) scrollback bootstrap, sent before any live output.
	if snap, err := capturePane(s.pane, s.scrollback); err == nil && len(snap) > 0 {
		_ = ws.WriteMessage(websocket.BinaryMessage, snap)
	}

	// 2) sole-writer goroutine: live pty output + keepalive pings.
	go func() {
		ping := time.NewTicker(20 * time.Second)
		defer ping.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-cc.Done:
				_ = ws.WriteControl(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseGoingAway, "tmux gone"),
					time.Now().Add(2*time.Second))
				cancel()
				return
			case b, ok := <-cc.Output:
				if !ok {
					return
				}
				_ = ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := ws.WriteMessage(websocket.BinaryMessage, b); err != nil {
					cancel()
					return
				}
			case <-ping.C:
				_ = ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
					cancel()
					return
				}
			}
		}
	}()

	// 3) reader loop: binary = raw input bytes; text = JSON control (resize).
	ws.SetReadLimit(1 << 20)
	for {
		mt, data, err := ws.ReadMessage()
		if err != nil {
			cancel()
			return
		}
		switch mt {
		case websocket.BinaryMessage:
			if err := cc.SendInput(data); err != nil {
				cancel()
				return
			}
		case websocket.TextMessage:
			var msg struct {
				Type string `json:"type"`
				Cols int    `json:"cols"`
				Rows int    `json:"rows"`
			}
			if json.Unmarshal(data, &msg) == nil && msg.Type == "resize" {
				_ = cc.Resize(msg.Cols, msg.Rows)
			}
		}
	}
}

// capturePane bootstraps scrollback. -e keeps colour escapes; -S -N reaches back
// N lines. xterm wants CRLF, so translate the bare LFs capture-pane emits.
func capturePane(pane string, lines int) ([]byte, error) {
	out, err := exec.Command("tmux", "capture-pane", "-p", "-e",
		"-t", pane, "-S", fmt.Sprintf("-%d", lines)).Output()
	if err != nil {
		return nil, err
	}
	return bytes.ReplaceAll(out, []byte("\n"), []byte("\r\n")), nil
}

func resolvePane(session string) (string, error) {
	out, err := exec.Command("tmux", "list-panes", "-t", session,
		"-F", "#{pane_id}").Output()
	if err != nil {
		return "", err
	}
	pane := strings.TrimSpace(string(out))
	if i := strings.IndexByte(pane, '\n'); i >= 0 {
		pane = pane[:i]
	}
	if pane == "" {
		return "", fmt.Errorf("no panes")
	}
	return pane, nil
}

// tuneTmux applies the spike's two sizing/latency requirements (§11.7, §18.1).
func tuneTmux(session string) {
	run := func(args ...string) { _ = exec.Command("tmux", args...).Run() }
	run("set-option", "-g", "escape-time", "0") // no ESC batching
	run("set-option", "-t", session, "window-size", "latest")
	run("set-option", "-t", session, "aggressive-resize", "off")
}

func normHostPort(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return addr
	}
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[i:]
	}
	return ""
}
