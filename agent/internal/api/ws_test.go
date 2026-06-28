package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"agentmon/agent/internal/config"
	"agentmon/agent/internal/directive"
	"agentmon/shared"
)

const wsKey = "ws-test-signing-key-aaaaaaaaaaaaaaaa"
const wsToken = "ws-test-bearer-token"

// fakePane is an injected PaneConn that records input and lets the test push output.
type fakePane struct {
	out      chan []byte
	done     chan struct{}
	inputs   chan []byte
	resizes  chan [2]int
	closed   chan struct{}
}

func newFakePane() *fakePane {
	return &fakePane{
		out: make(chan []byte, 8), done: make(chan struct{}),
		inputs: make(chan []byte, 8), resizes: make(chan [2]int, 8),
		closed: make(chan struct{}),
	}
}
func (f *fakePane) OutputChan() <-chan []byte   { return f.out }
func (f *fakePane) DoneChan() <-chan struct{}   { return f.done }
func (f *fakePane) SendInput(b []byte) error    { f.inputs <- append([]byte(nil), b...); return nil }
func (f *fakePane) Resize(c, r int) error       { f.resizes <- [2]int{c, r}; return nil }
func (f *fakePane) Close()                      { close(f.closed) }

func testTarget() config.Config {
	return config.Config{
		ServerID:        "server-a",
		HubToken:        wsToken,
		ScrollbackLines: 100,
		Targets:         []config.Target{{Label: "default", SocketName: ""}},
	}
}

// buildHandler wires a PaneIO whose tmux seams are fakes, returning the fake pane
// so the test can drive output.
func buildHandler(t *testing.T, fake *fakePane) http.Handler {
	t.Helper()
	cfg := testTarget()
	h := &PaneIO{
		Cfg:      cfg,
		Verifier: directive.NewVerifier("server-a", []byte(wsKey), nil),
		Run: func(ctx context.Context, args ...string) ([]byte, error) {
			// list-panes -a: pane %3 lives in session $1.
			// ResolvePaneSession splits on the literal \037 token (delimToken),
			// not the raw 0x1f byte — mimic real tmux output here.
			return []byte("%3\\037$1\n"), nil
		},
		Capture: func(ctx context.Context, socket, pane string, lines int) ([]byte, error) {
			return []byte("SCROLLBACK"), nil
		},
		NewClient: func(ctx context.Context, socket, session, pane string) (PaneConn, error) {
			return fake, nil
		},
		Tune: func(ctx context.Context, socket, session string) {},
	}
	// A ServeMux is required so {paneId} path values are populated; the handler
	// reads r.PathValue("paneId"). A bare handler would see an empty pane id.
	mux := http.NewServeMux()
	mux.Handle("GET /panes/{paneId}/io", RequireBearer(wsToken, h.Handler()))
	return mux
}

// panePath percent-escapes the pane id for the URL path: a pane id is "%3", and a
// literal "%" is invalid in a URL path segment, so it must be sent as "%253"
// (PathValue decodes it back to "%3"). The hub does the same when dialing in M4.
func panePath(paneID string) string { return "/panes/" + url.PathEscape(paneID) + "/io" }

func signedHeader(t *testing.T, mode string) string {
	t.Helper()
	d := shared.Directive{
		ServerID: "server-a", Target: "default",
		Resource: shared.PaneID("server-a", "default", "%3"), Mode: mode,
		PrincipalID: "u", Action: "terminal." + map[string]string{"ro": "read", "rw": "write"}[mode],
		Exp:   time.Now().Add(60 * time.Second).Format(time.RFC3339),
		Nonce: "nonce-" + mode + "-" + time.Now().Format(time.RFC3339Nano), RequestID: "r",
	}
	hdr, err := directive.Sign([]byte(wsKey), d)
	if err != nil {
		t.Fatal(err)
	}
	return hdr
}

// dial upgrades to the pane WS with bearer + directive headers set.
func dial(t *testing.T, srv *httptest.Server, mode string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + panePath("%3") + "?target=default&mode=" + mode
	h := http.Header{}
	h.Set("Authorization", "Bearer "+wsToken)
	h.Set("X-AgentMon-Directive", signedHeader(t, mode))
	return websocket.DefaultDialer.Dial(u, h)
}

func TestPaneWSSendsScrollbackFirstThenOutput(t *testing.T) {
	fake := newFakePane()
	srv := httptest.NewServer(buildHandler(t, fake))
	defer srv.Close()
	conn, _, err := dial(t, srv, "rw")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	mt, data, err := conn.ReadMessage()
	if err != nil || mt != websocket.BinaryMessage || string(data) != "SCROLLBACK" {
		t.Fatalf("first frame = (%d,%q,%v), want binary SCROLLBACK", mt, data, err)
	}
	fake.out <- []byte("LIVE")
	_, data, err = conn.ReadMessage()
	if err != nil || string(data) != "LIVE" {
		t.Fatalf("second frame = (%q,%v), want LIVE", data, err)
	}
}

func TestPaneWSForwardsInputInRW(t *testing.T) {
	fake := newFakePane()
	srv := httptest.NewServer(buildHandler(t, fake))
	defer srv.Close()
	conn, _, err := dial(t, srv, "rw")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_, _, _ = conn.ReadMessage() // drain scrollback
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte("ls\r")); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-fake.inputs:
		if string(got) != "ls\r" {
			t.Fatalf("input = %q, want ls\\r", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("input never reached the pane")
	}
}

func TestPaneWSDropsInputInRO(t *testing.T) {
	fake := newFakePane()
	srv := httptest.NewServer(buildHandler(t, fake))
	defer srv.Close()
	conn, _, err := dial(t, srv, "ro")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_, _, _ = conn.ReadMessage() // drain scrollback
	_ = conn.WriteMessage(websocket.BinaryMessage, []byte("rm -rf /\r"))
	select {
	case got := <-fake.inputs:
		t.Fatalf("ro mode forwarded input %q; must drop", got)
	case <-time.After(300 * time.Millisecond):
		// expected: no input forwarded
	}
}

func TestPaneWSResizeReachesPane(t *testing.T) {
	fake := newFakePane()
	srv := httptest.NewServer(buildHandler(t, fake))
	defer srv.Close()
	conn, _, err := dial(t, srv, "rw")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_, _, _ = conn.ReadMessage() // drain scrollback
	msg, _ := json.Marshal(shared.ResizeFrame{Type: "resize", Cols: 120, Rows: 40})
	_ = conn.WriteMessage(websocket.TextMessage, msg)
	select {
	case got := <-fake.resizes:
		if got != [2]int{120, 40} {
			t.Fatalf("resize = %v, want [120 40]", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("resize never reached the pane")
	}
}

func TestPaneWSRejectsForgedDirective(t *testing.T) {
	fake := newFakePane()
	srv := httptest.NewServer(buildHandler(t, fake))
	defer srv.Close()
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + panePath("%3") + "?target=default&mode=rw"
	h := http.Header{}
	h.Set("Authorization", "Bearer "+wsToken)
	h.Set("X-AgentMon-Directive", "forged.signature")
	_, resp, err := websocket.DefaultDialer.Dial(u, h)
	if err == nil {
		t.Fatal("want upgrade failure for a forged directive")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %v, want 403", resp)
	}
}

func TestPaneWSRejectsMissingBearer(t *testing.T) {
	fake := newFakePane()
	srv := httptest.NewServer(buildHandler(t, fake))
	defer srv.Close()
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + panePath("%3") + "?target=default&mode=rw"
	_, resp, err := websocket.DefaultDialer.Dial(u, nil)
	if err == nil {
		t.Fatal("want failure with no bearer")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %v, want 401", resp)
	}
}

// TestPaneWSRejectsModeMismatch proves the URL mode must agree with the signed
// directive's authoritative mode: a directive minted for ro cannot drive a rw URL.
func TestPaneWSRejectsModeMismatch(t *testing.T) {
	fake := newFakePane()
	srv := httptest.NewServer(buildHandler(t, fake))
	defer srv.Close()
	// URL asks for rw, but the directive is SIGNED with Mode:"ro".
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + panePath("%3") + "?target=default&mode=rw"
	h := http.Header{}
	h.Set("Authorization", "Bearer "+wsToken)
	h.Set("X-AgentMon-Directive", signedHeader(t, "ro"))
	_, resp, err := websocket.DefaultDialer.Dial(u, h)
	if err == nil {
		t.Fatal("want upgrade failure when URL mode != directive mode")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %v, want 403", resp)
	}
}

// TestPaneWSDoneChanTearsDownAndClosesPane exercises the writer-initiated teardown
// path: when the control client's DoneChan closes ("tmux gone"), the handler must
// (1) end the client's read with a close/error and (2) run its deferred cc.Close()
// even though readPump is blocked in ReadMessage. The defer'd read-deadline in
// writePump is what unblocks readPump here.
func TestPaneWSDoneChanTearsDownAndClosesPane(t *testing.T) {
	fake := newFakePane()
	srv := httptest.NewServer(buildHandler(t, fake))
	defer srv.Close()
	conn, _, err := dial(t, srv, "rw")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_, _, _ = conn.ReadMessage() // drain scrollback

	close(fake.done) // simulate the tmux control client exiting

	// The client's next read must return (close frame or error) promptly.
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("want read close/error after DoneChan teardown")
	}
	// The handler's deferred cc.Close() must have run.
	select {
	case <-fake.closed:
		// expected: pane closed
	case <-time.After(2 * time.Second):
		t.Fatal("pane was never Close()d after teardown")
	}
}
