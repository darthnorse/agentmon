package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"agentmon/agent/internal/config"
	"agentmon/agent/internal/directive"
	"agentmon/agent/internal/tmux"
	"agentmon/shared"
)

// Whole-milestone M2 verification against REAL tmux (spec §8 M2 done-when): a WS
// client through the agent drives a real pane (a probe reaches the shell), and
// expired / replayed / forged / resource-mismatched directives are all rejected,
// and mode=ro drops input. Skips when tmux is absent (CI); runs on the dev box.

const (
	itKey    = "m2-integration-signing-key-aaaaaaaa"
	itToken  = "m2-integration-bearer-token"
	itServer = "server-a"
	itSocket = "agentmon-m2-ws"
)

func itRequireTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed; skipping WS integration test")
	}
}

// startRealSession creates a shell session on a dedicated tmux socket and returns
// its first pane id.
func startRealSession(t *testing.T) string {
	t.Helper()
	_ = exec.Command("tmux", "-L", itSocket, "kill-server").Run()
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", itSocket, "kill-server").Run() })
	if out, err := exec.Command("tmux", "-L", itSocket, "new-session", "-d", "-s", "s", "-x", "100", "-y", "30").CombinedOutput(); err != nil {
		t.Fatalf("new-session: %v: %s", err, out)
	}
	time.Sleep(300 * time.Millisecond) // let the shell start and draw its prompt
	out, err := exec.Command("tmux", "-L", itSocket, "list-panes", "-F", "#{pane_id}").Output()
	if err != nil {
		t.Fatalf("list-panes: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// realPaneIO wires a PaneIO bound to real tmux on itSocket, behind RequireBearer.
func realPaneIO() http.Handler {
	cfg := config.Config{
		ServerID:        itServer,
		HubToken:        itToken,
		DirectiveKey:    itKey,
		ScrollbackLines: 200,
		Targets:         []config.Target{{Label: "default", SocketName: itSocket}},
	}
	h := &PaneIO{
		Cfg:       cfg,
		Verifier:  directive.NewVerifier(itServer, []byte(itKey), nil),
		Run:       tmux.ExecRunner,
		Capture:   tmux.CapturePane,
		NewClient: func(ctx context.Context, socket, session, pane string) (PaneConn, error) {
			return tmux.NewControlClient(ctx, socket, session, pane)
		},
		Tune: tmux.TuneSession,
	}
	mux := http.NewServeMux()
	mux.Handle("GET /panes/{paneId}/io", RequireBearer(itToken, h.Handler()))
	return mux
}

// mint signs a directive for the given pane/mode with the supplied exp and nonce.
func mint(t *testing.T, paneID, mode string, exp time.Time, nonce string) string {
	t.Helper()
	d := shared.Directive{
		ServerID: itServer, Target: "default",
		Resource: shared.PaneID(itServer, "default", paneID), Mode: mode,
		PrincipalID: "u", Action: "terminal.write",
		Exp: exp.Format(time.RFC3339), Nonce: nonce, RequestID: "r",
	}
	hdr, err := directive.Sign([]byte(itKey), d)
	if err != nil {
		t.Fatal(err)
	}
	return hdr
}

func wsURL(srv *httptest.Server, paneID, mode string) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http") + panePath(paneID) + "?target=default&mode=" + mode
}

func dialReal(t *testing.T, srv *httptest.Server, paneID, mode, header string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	h := http.Header{}
	h.Set("Authorization", "Bearer "+itToken)
	if header != "" {
		h.Set("X-AgentMon-Directive", header)
	}
	return websocket.DefaultDialer.Dial(wsURL(srv, paneID, mode), h)
}

// readsToken reports whether any frame received within timeout contains token.
// One overall read deadline bounds the window: gorilla treats a mid-stream read
// timeout as a fatal connection error, so we must not read past the first error.
func readsToken(t *testing.T, conn *websocket.Conn, token string, timeout time.Duration) bool {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	var acc strings.Builder
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return false // timeout (window elapsed) or close
		}
		acc.Write(data)
		if strings.Contains(acc.String(), token) {
			return true
		}
	}
}

func TestWSDrivesRealPaneRW(t *testing.T) {
	itRequireTmux(t)
	pane := startRealSession(t)
	srv := httptest.NewServer(realPaneIO())
	defer srv.Close()

	conn, _, err := dialReal(t, srv, pane, "rw", mint(t, pane, "rw", time.Now().Add(60*time.Second), "rw-ok-1"))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// First frame must be the scrollback snapshot.
	_, snap, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("scrollback read: %v", err)
	}
	if len(snap) == 0 {
		t.Fatal("expected a non-empty scrollback snapshot as the first frame")
	}

	// Drive the real shell: the probe must come back in live output.
	const probe = "SMOKEPROBE_RW_42"
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte("echo "+probe+"\r")); err != nil {
		t.Fatalf("write input: %v", err)
	}
	if !readsToken(t, conn, probe, 5*time.Second) {
		t.Fatalf("probe %q never reached the shell / streamed back", probe)
	}
}

func TestWSReadOnlyDropsInputRealPane(t *testing.T) {
	itRequireTmux(t)
	pane := startRealSession(t)
	srv := httptest.NewServer(realPaneIO())
	defer srv.Close()

	conn, _, err := dialReal(t, srv, pane, "ro", mint(t, pane, "ro", time.Now().Add(60*time.Second), "ro-ok-1"))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, _, err := conn.ReadMessage(); err != nil { // drain scrollback
		t.Fatalf("scrollback read: %v", err)
	}

	const probe = "SMOKEPROBE_RO_99"
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte("echo "+probe+"\r")); err != nil {
		t.Fatalf("write input: %v", err)
	}
	// Input must be dropped: the probe must NOT appear in output.
	if readsToken(t, conn, probe, 2*time.Second) {
		t.Fatalf("read-only mode forwarded input: probe %q reached the shell", probe)
	}
}

func TestWSRejectsBadDirectivesRealPane(t *testing.T) {
	itRequireTmux(t)
	pane := startRealSession(t)
	srv := httptest.NewServer(realPaneIO())
	defer srv.Close()

	expect403 := func(t *testing.T, name, header string) {
		t.Helper()
		conn, resp, err := dialReal(t, srv, pane, "rw", header)
		if conn != nil {
			conn.Close()
		}
		if err == nil {
			t.Fatalf("%s: expected upgrade failure", name)
		}
		if resp == nil || resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s: status = %v, want 403", name, resp)
		}
	}

	// expired
	expect403(t, "expired", mint(t, pane, "rw", time.Now().Add(-1*time.Second), "expired-1"))
	// forged signature (valid-looking but wrong key)
	forged, _ := directive.Sign([]byte("the-WRONG-signing-key-bbbbbbbbbbbb"),
		shared.Directive{ServerID: itServer, Target: "default",
			Resource: shared.PaneID(itServer, "default", pane), Mode: "rw",
			Exp: time.Now().Add(60 * time.Second).Format(time.RFC3339), Nonce: "forged-1", RequestID: "r"})
	expect403(t, "forged", forged)
	// resource mismatch: directive names a different pane than the URL/handler.
	expect403(t, "resource-mismatch", mint(t, "%999999", "rw", time.Now().Add(60*time.Second), "mism-1"))

	// replay: a valid directive succeeds once, then its nonce is rejected.
	hdr := mint(t, pane, "rw", time.Now().Add(60*time.Second), "replay-1")
	conn, _, err := dialReal(t, srv, pane, "rw", hdr)
	if err != nil {
		t.Fatalf("replay first use should succeed: %v", err)
	}
	conn.Close()
	expect403(t, "replayed-nonce", hdr)
}
