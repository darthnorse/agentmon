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
	"agentmon/hubd/internal/state"
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
	// closed is initialized eagerly (before any goroutine) so the test goroutine's
	// read and the fake-agent goroutine's send never race on the field itself.
	rec := &dialRecord{closed: make(chan struct{}, 8)}
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

func TestRelayStaysOpenWhileActiveBeyondPongWait(t *testing.T) {
	rec := &dialRecord{}
	agent := fakeAgentWS(t, rec)
	defer agent.Close()
	d := relayDeps(agent.URL, "b", "k", &recSink{})
	d.RelayPongWait = 200 * time.Millisecond
	d.RelayPingPeriod = 40 * time.Millisecond
	hub := relayServer(d, authz.Principal{ID: "u1"})
	defer hub.Close()

	c, _, err := dialBrowser(t, hub, "/api/v1/servers/aigallery/panes/%253/io?target=default", testOrigin)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// A gorilla client auto-replies to pings with pongs during ReadMessage. Keep
	// reading in the background so pongs flow and the hub's read deadline is bumped.
	go func() {
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}()
	// Wait well past pongWait, then prove the relay is still alive by round-tripping.
	time.Sleep(500 * time.Millisecond)
	if err := c.WriteMessage(websocket.BinaryMessage, []byte("ping-alive")); err != nil {
		t.Fatalf("relay died before deadline refresh kept it open: %v", err)
	}
	// Bounded poll: wait up to 2s in 10ms steps for "ping-alive" to reach the agent.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rec.mu.Lock()
		for _, g := range rec.got {
			if string(g) == "ping-alive" {
				rec.mu.Unlock()
				return // success
			}
		}
		rec.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("late message did not reach the agent — relay was not kept alive")
}

func TestRelayRelaysLargeAgentSnapshot(t *testing.T) {
	const size = 2 << 20 // 2 MiB, above the old 1<<20 browser-side limit
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /panes/{paneId}/io", func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		big := make([]byte, size)
		for i := range big {
			big[i] = byte('a' + i%26)
		}
		_ = c.WriteMessage(websocket.BinaryMessage, big)
		// keep the conn open briefly so the relay can forward before teardown
		_, _, _ = c.ReadMessage()
	})
	agent := httptest.NewServer(mux)
	defer agent.Close()

	d := relayDeps(agent.URL, "b", "k", &recSink{})
	hub := relayServer(d, authz.Principal{ID: "u1"})
	defer hub.Close()

	c, _, err := dialBrowser(t, hub, "/api/v1/servers/aigallery/panes/%253/io?target=default", testOrigin)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	c.SetReadLimit(size + 4096) // the test browser client must accept the big frame too
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, data, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("read large snapshot: %v", err)
	}
	if len(data) != size {
		t.Fatalf("relayed snapshot truncated: got %d want %d", len(data), size)
	}
}

func TestRelayRaceUnderConcurrentTraffic(t *testing.T) {
	rec := &dialRecord{}
	agent := fakeAgentWS(t, rec)
	defer agent.Close()
	d := relayDeps(agent.URL, "b", "k", &recSink{})
	d.RelayPongWait = 2 * time.Second
	d.RelayPingPeriod = 5 * time.Millisecond // pings interleave with writes
	hub := relayServer(d, authz.Principal{ID: "u1"})
	defer hub.Close()

	c, _, err := dialBrowser(t, hub, "/api/v1/servers/aigallery/panes/%253/io?target=default", testOrigin)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // drain echoes + snapshot
		defer wg.Done()
		for i := 0; i < 51; i++ {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}()
	for i := 0; i < 50; i++ {
		if err := c.WriteMessage(websocket.BinaryMessage, []byte("msg")); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	wg.Wait()
}

// TestRelayStateFrameDeliveredAndInterleaved verifies the M7 state-frame injection:
//  1. A {t:"state"} JSON text frame is pushed to the browser when the broadcaster
//     publishes a Change for the relay's session (TDD: RED before the refactor, GREEN after).
//  2. Binary terminal frames still pass through after a state frame.
//  3. Changes for other servers/sessions are filtered and do not reach the browser.
//
// The fake agent serves both the WS pane endpoint and GET /sessions so that the
// handler can resolve pane %3 → session "test-session" and subscribe to the broadcaster.
func TestRelayStateFrameDeliveredAndInterleaved(t *testing.T) {
	upFake := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	mux := http.NewServeMux()
	rec := &dialRecord{}

	// WS relay endpoint — same behaviour as fakeAgentWS.
	mux.HandleFunc("GET /panes/{paneId}/io", func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.paneID = r.PathValue("paneId")
		rec.mu.Unlock()
		c, err := upFake.Upgrade(w, r, nil)
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
	})

	// Sessions endpoint — returns session "test-session" containing pane %3.
	mux.HandleFunc("GET /sessions", func(w http.ResponseWriter, r *http.Request) {
		sess := shared.SessionList{Sessions: []shared.Session{{
			Name:   "test-session",
			Target: "default",
			Windows: []shared.Window{{
				ID: "1", Index: "0", Name: "win",
				Panes: []shared.Pane{{ID: "%3"}},
			}},
		}}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sess)
	})

	agent := httptest.NewServer(mux)
	defer agent.Close()

	bcast := state.NewBroadcaster()
	d := relayDeps(agent.URL, "bearer-xyz", "sk-123", &recSink{})
	d.Bcast = bcast
	// d.Seen is nil → no seen row → SeenProject returns global state unchanged.

	hub := relayServer(d, authz.Principal{ID: "u1"})
	defer hub.Close()

	c, _, err := dialBrowser(t, hub, "/api/v1/servers/aigallery/panes/%253/io?target=default", testOrigin)
	if err != nil {
		t.Fatalf("browser dial: %v", err)
	}
	defer c.Close()
	c.SetReadLimit(1 << 20)

	// 1) Read SNAP — confirms the relay is fully up and the browser-writer goroutine
	//    (G3) has completed one iteration of its loop (SNAP: agent→G2→agentFrames→G3→browser).
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	mt, data, err := c.ReadMessage()
	if err != nil || mt != websocket.BinaryMessage || string(data) != "SNAP" {
		t.Fatalf("snapshot: mt=%d data=%q err=%v", mt, data, err)
	}

	// 2) Publish a state change for the relay's session.  The subscription channel
	//    is buffered (cap 64), so this is safe even if G3 is momentarily between
	//    loop iterations.
	bcast.Publish(state.Change{
		ServerID:         "aigallery",
		Target:           "default",
		Session:          "test-session",
		Global:           shared.StateDone,
		LatestReceivedAt: "2024-01-01 00:00:00.000",
	})

	// 3) G3 must deliver a JSON text frame for the state change.
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	mt, data, err = c.ReadMessage()
	if err != nil {
		t.Fatalf("read state frame: %v", err)
	}
	if mt != websocket.TextMessage {
		t.Fatalf("state frame: want TextMessage (type 1), got %d; data=%q", mt, data)
	}
	var sf wsStateFrame
	if err := json.Unmarshal(data, &sf); err != nil {
		t.Fatalf("unmarshal state frame %q: %v", data, err)
	}
	// d.Seen is nil → no seen row → SeenProject passes StateDone through unchanged.
	if sf.T != "state" || sf.State != shared.StateDone || sf.Session != "test-session" {
		t.Fatalf("state frame mismatch: got %+v, want {t:state state:done session:test-session}", sf)
	}

	// 4) Binary terminal data still passes through after a state frame.
	if err := c.WriteMessage(websocket.BinaryMessage, []byte("after-state")); err != nil {
		t.Fatalf("write after-state: %v", err)
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	mt, data, err = c.ReadMessage()
	if err != nil || mt != websocket.BinaryMessage || string(data) != "after-state" {
		t.Fatalf("binary echo after state frame: mt=%d data=%q err=%v", mt, data, err)
	}

	// 5) Changes for other sessions/servers must be filtered — not forwarded.
	//    Publish two noise changes, then confirm the relay is still alive with
	//    a round-trip binary frame (which would appear between noise changes if
	//    any leaked through).
	bcast.Publish(state.Change{ServerID: "other", Target: "default", Session: "other-session", Global: shared.StateWorking})
	bcast.Publish(state.Change{ServerID: "aigallery", Target: "default", Session: "other-session", Global: shared.StateIdle})

	if err := c.WriteMessage(websocket.BinaryMessage, []byte("still-alive")); err != nil {
		t.Fatalf("write still-alive: %v", err)
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	mt, data, err = c.ReadMessage()
	if err != nil || mt != websocket.BinaryMessage || string(data) != "still-alive" {
		t.Fatalf("still-alive echo: mt=%d data=%q err=%v", mt, data, err)
	}
}
