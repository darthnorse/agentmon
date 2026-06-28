package api

import (
	"context"
	"crypto/hmac"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"agentmon/hubd/internal/audit"
	"agentmon/hubd/internal/authn"
	"agentmon/hubd/internal/authz"
	"agentmon/hubd/internal/db"
	hubdirective "agentmon/hubd/internal/directive"
	"agentmon/hubd/internal/registry"
	"agentmon/shared"
)

const testOrigin = "http://hub.test"

// recSink captures audit rows for assertions.
type recSink struct {
	mu   sync.Mutex
	rows []db.AuditEntry
}

func (s *recSink) Append(_ context.Context, e db.AuditEntry) error {
	s.mu.Lock()
	s.rows = append(s.rows, e)
	s.mu.Unlock()
	return nil
}
func (s *recSink) actions() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, e := range s.rows {
		out = append(out, e.Action)
	}
	return out
}

// dialRecord captures what the fake agent saw on the WS upgrade.
type dialRecord struct {
	mu                     sync.Mutex
	auth, directive, reqID string
	paneID, target, mode   string
	got                    [][]byte
	closed                 chan struct{}
}

func (r *dialRecord) append(b []byte) {
	r.mu.Lock()
	r.got = append(r.got, append([]byte(nil), b...))
	r.mu.Unlock()
}

// fakeAgentWS upgrades, records the request, sends a binary "SNAP", then echoes input.
func fakeAgentWS(t *testing.T, rec *dialRecord) *httptest.Server {
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /panes/{paneId}/io", func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.auth = r.Header.Get("Authorization")
		rec.directive = r.Header.Get("X-AgentMon-Directive")
		rec.reqID = r.Header.Get("X-AgentMon-Request-Id")
		rec.paneID = r.PathValue("paneId")
		rec.target = r.URL.Query().Get("target")
		rec.mode = r.URL.Query().Get("mode")
		if rec.closed == nil {
			rec.closed = make(chan struct{}, 8)
		}
		rec.mu.Unlock()
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.WriteMessage(websocket.BinaryMessage, []byte("SNAP"))
		for {
			mt, data, err := c.ReadMessage()
			if err != nil {
				break
			}
			rec.append(data)
			_ = c.WriteMessage(mt, data) // echo
		}
		// Signal that this agent connection's read loop has exited (relay tore us down).
		// Non-blocking send into a buffered channel: safe even if called multiple times
		// on the same rec (e.g. the teardown test dials twice).
		select {
		case rec.closed <- struct{}{}:
		default:
		}
	})
	return httptest.NewServer(mux)
}

func verifyMinted(t *testing.T, header string, key []byte) shared.Directive {
	t.Helper()
	p, sig, ok := strings.Cut(header, ".")
	if !ok {
		t.Fatalf("bad directive header %q", header)
	}
	payload, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		t.Fatal(err)
	}
	sigB, _ := base64.RawURLEncoding.DecodeString(sig)
	if !hmac.Equal(sigB, shared.DirectiveMAC(key, payload)) {
		t.Fatal("minted directive does not verify with the server signing key")
	}
	var d shared.Directive
	_ = json.Unmarshal(payload, &d)
	return d
}

// relayDeps builds Deps wired to a fake registry pointing at agentURL.
func relayDeps(agentURL, bearer, signingKey string, sink audit.Sink) Deps {
	srv := db.Server{ID: "aigallery", Name: "AG", URL: agentURL, Bearer: bearer,
		SigningKey: signingKey, Status: "active"}
	d := testDeps(registry.New(fakeStore{servers: map[string]db.Server{srv.ID: srv}}))
	d.Audit = audit.NewRecorder(sink)
	d.Minter = hubdirective.Minter{}
	d.ExternalOrigin = testOrigin
	return d
}

// relayServer mounts the handler under the real route pattern, injecting principal p.
func relayServer(d Deps, p authz.Principal) *httptest.Server {
	mux := http.NewServeMux()
	h := d.PaneRelayHandler()
	mux.Handle("GET /api/v1/servers/{id}/panes/{paneId}/io",
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h(w, r.WithContext(authn.ContextWithPrincipal(r.Context(), p)))
		}))
	return httptest.NewServer(mux)
}

func wsURL(httpURL, path string) string { return "ws" + strings.TrimPrefix(httpURL, "http") + path }

func dialBrowser(t *testing.T, hub *httptest.Server, path, origin string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	hdr := http.Header{}
	if origin != "" {
		hdr.Set("Origin", origin)
	}
	return websocket.DefaultDialer.Dial(wsURL(hub.URL, path), hdr)
}

func TestRelayHappyPathBidirectionalAndHeaders(t *testing.T) {
	rec := &dialRecord{}
	agent := fakeAgentWS(t, rec)
	defer agent.Close()
	sink := &recSink{}
	d := relayDeps(agent.URL, "bearer-xyz", "sk-123", sink)
	hub := relayServer(d, authz.Principal{ID: "u1"})
	defer hub.Close()

	c, _, err := dialBrowser(t, hub, "/api/v1/servers/aigallery/panes/%253/io?target=default", testOrigin)
	if err != nil {
		t.Fatalf("browser dial: %v", err)
	}
	defer c.Close()

	// 1) snapshot relayed agent→browser
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	mt, data, err := c.ReadMessage()
	if err != nil || mt != websocket.BinaryMessage || string(data) != "SNAP" {
		t.Fatalf("snapshot: mt=%d data=%q err=%v", mt, data, err)
	}

	// 2) input relayed browser→agent and echoed back
	if err := c.WriteMessage(websocket.BinaryMessage, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, echo, err := c.ReadMessage()
	if err != nil || string(echo) != "hello" {
		t.Fatalf("echo: %q err=%v", echo, err)
	}

	// 3) resize JSON passes through and round-trips correctly
	_ = c.WriteJSON(shared.ResizeFrame{Type: shared.FrameResize, Cols: 88, Rows: 26})
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, rz, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("resize echo: %v", err)
	}
	var gotRz shared.ResizeFrame
	if json.Unmarshal(rz, &gotRz) != nil || gotRz.Cols != 88 || gotRz.Rows != 26 {
		t.Fatalf("resize did not round-trip: %q", rz)
	}

	// headers the hub sent to the agent
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.auth != "Bearer bearer-xyz" {
		t.Fatalf("agent Authorization %q", rec.auth)
	}
	if rec.paneID != "%3" {
		t.Fatalf("agent paneId %q, want %%3", rec.paneID)
	}
	if rec.target != "default" || rec.mode != "rw" {
		t.Fatalf("agent query target=%q mode=%q", rec.target, rec.mode)
	}
	if rec.reqID == "" {
		t.Fatal("missing X-AgentMon-Request-Id")
	}
	dir := verifyMinted(t, rec.directive, []byte("sk-123"))
	if dir.Mode != "rw" || dir.Resource != shared.PaneID("aigallery", "default", "%3") {
		t.Fatalf("minted directive %+v", dir)
	}

	// terminal.open audited
	if !contains(sink.actions(), "terminal.open") {
		t.Fatalf("terminal.open not audited; saw %v", sink.actions())
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func TestRelayBadOriginRejected(t *testing.T) {
	rec := &dialRecord{}
	agent := fakeAgentWS(t, rec)
	defer agent.Close()
	d := relayDeps(agent.URL, "b", "k", &recSink{})
	hub := relayServer(d, authz.Principal{ID: "u1"})
	defer hub.Close()

	_, resp, err := dialBrowser(t, hub, "/api/v1/servers/aigallery/panes/%253/io?target=default", "http://evil.example")
	if err == nil {
		t.Fatal("expected handshake failure on bad origin")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("want 403, got %v", resp)
	}
}

func TestRelayDenyEmptyPrincipalAudited(t *testing.T) {
	agent := fakeAgentWS(t, &dialRecord{})
	defer agent.Close()
	sink := &recSink{}
	d := relayDeps(agent.URL, "b", "k", sink)
	hub := relayServer(d, authz.Principal{}) // empty principal → authz denies
	defer hub.Close()

	_, resp, err := dialBrowser(t, hub, "/api/v1/servers/aigallery/panes/%253/io?target=default", testOrigin)
	if err == nil {
		t.Fatal("expected handshake failure on authz deny")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("want 403, got %v", resp)
	}
	if !contains(sink.actions(), "terminal.write") {
		t.Fatalf("deny not audited; saw %v", sink.actions())
	}
}

func TestRelayUnknownServerIs404(t *testing.T) {
	d := relayDeps("http://unused", "b", "k", &recSink{})
	hub := relayServer(d, authz.Principal{ID: "u1"})
	defer hub.Close()
	_, resp, err := dialBrowser(t, hub, "/api/v1/servers/nope/panes/%253/io?target=default", testOrigin)
	if err == nil {
		t.Fatal("expected failure")
	}
	if resp == nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %v", resp)
	}
}

func TestRelayBadPaneIDIs400(t *testing.T) {
	d := relayDeps("http://unused", "b", "k", &recSink{})
	hub := relayServer(d, authz.Principal{ID: "u1"})
	defer hub.Close()
	_, resp, err := dialBrowser(t, hub, "/api/v1/servers/aigallery/panes/notapane/io?target=default", testOrigin)
	if err == nil {
		t.Fatal("expected failure")
	}
	if resp == nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %v", resp)
	}
}

func TestRelayAgentDialFailureIs502(t *testing.T) {
	// registry points at a closed port → dial fails
	d := relayDeps("http://127.0.0.1:1", "b", "k", &recSink{})
	hub := relayServer(d, authz.Principal{ID: "u1"})
	defer hub.Close()
	_, resp, err := dialBrowser(t, hub, "/api/v1/servers/aigallery/panes/%253/io?target=default", testOrigin)
	if err == nil {
		t.Fatal("expected failure")
	}
	if resp == nil || resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("want 502, got %v", resp)
	}
}

func TestRelayClosingBrowserTearsDownAgent(t *testing.T) {
	rec := &dialRecord{}
	agent := fakeAgentWS(t, rec)
	defer agent.Close()
	d := relayDeps(agent.URL, "b", "k", &recSink{})
	hub := relayServer(d, authz.Principal{ID: "u1"})
	defer hub.Close()

	c, _, err := dialBrowser(t, hub, "/api/v1/servers/aigallery/panes/%253/io?target=default", testOrigin)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, _ = c.ReadMessage() // drain SNAP
	c.Close()                 // browser goes away

	// Assert that the relay actually propagated the close to the agent side.
	// rec.closed receives a token when the fake agent's read loop exits.
	select {
	case <-rec.closed:
		// teardown confirmed
	case <-time.After(2 * time.Second):
		t.Fatal("agent connection was not torn down after the browser closed")
	}

	// Additional no-wedge assertion: a fresh dial must still succeed.
	c2, _, err := dialBrowser(t, hub, "/api/v1/servers/aigallery/panes/%253/io?target=default", testOrigin)
	if err != nil {
		t.Fatalf("second dial after teardown: %v", err)
	}
	c2.Close()
}
