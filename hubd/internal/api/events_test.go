package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentmon/hubd/internal/audit"
	"agentmon/hubd/internal/authz"
	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/state"
	"agentmon/shared"
)

// noFlushWriter is a minimal http.ResponseWriter that intentionally does NOT
// implement http.Flusher, to exercise the 500 "stream unsupported" path.
type noFlushWriter struct {
	header http.Header
	code   int
	body   bytes.Buffer
}

func (w *noFlushWriter) Header() http.Header         { return w.header }
func (w *noFlushWriter) WriteHeader(code int)        { w.code = code }
func (w *noFlushWriter) Write(b []byte) (int, error) { return w.body.Write(b) }

// pipeResponseWriter wraps an io.PipeWriter as an http.ResponseWriter +
// http.Flusher. Flush() is a no-op because io.Pipe is already streaming —
// each Write blocks until the reader consumes the bytes, so data is
// immediately visible to the scanner without explicit flush semantics.
type pipeResponseWriter struct {
	pw     *io.PipeWriter
	header http.Header
	code   int
}

func (w *pipeResponseWriter) Header() http.Header         { return w.header }
func (w *pipeResponseWriter) WriteHeader(code int)        { w.code = code }
func (w *pipeResponseWriter) Write(b []byte) (int, error) { return w.pw.Write(b) }
func (w *pipeResponseWriter) Flush()                      {} // pipe writes are already streaming

// TestEventsHandler_DeniesUnauthedPrincipal asserts that EventsHandler returns 403
// when the request context has no authenticated principal (empty-ID principal), and
// that the handler returns without writing any SSE stream content.
// This exercises the authorizeOr403 chokepoint that FIX 1 adds.
func TestEventsHandler_DeniesUnauthedPrincipal(t *testing.T) {
	d := Deps{
		Proj:         state.NewProjection(),
		Bcast:        state.NewBroadcaster(),
		Seen:         &fakeSeenStore{},
		SSEHeartbeat: 50 * time.Millisecond, // fast heartbeat so test exits quickly if authz gate is missing
		Audit:        audit.NewRecorder(nopSink{}),
	}
	// withPrincipal with empty ID — authorizeOr403 denies when principal.ID == "".
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/events", nil).WithContext(ctx), authz.Principal{})
	w := httptest.NewRecorder()
	d.EventsHandler()(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403 for unauthenticated principal, got %d (body=%s)", w.Code, w.Body)
	}
	// Ensure no SSE content was written (no "event:" lines).
	if strings.Contains(w.Body.String(), "event:") {
		t.Fatalf("SSE stream must not be started for unauthorized request; body=%s", w.Body)
	}
}

// TestEventsHandler_SnapshotAndDelta asserts:
//  1. An initial event:snapshot containing all projection sessions
//     seen-projected for the principal.
//  2. After a Publish, an event:state delta with the seen-projected change.
//  3. Clean handler return on context cancel — no goroutine leak
//     (test ends without hanging on handlerDone).
func TestEventsHandler_SnapshotAndDelta(t *testing.T) {
	proj := state.NewProjection()
	proj.Set(state.SessionView{
		ServerID: "srv", Target: "", Session: "api",
		Global: shared.StateWorking, LatestReceivedAt: "ts0",
	})

	bcast := state.NewBroadcaster()

	d := Deps{
		Proj:         proj,
		Bcast:        bcast,
		Seen:         &fakeSeenStore{},
		SSEHeartbeat: time.Hour, // prevent heartbeat ticks during test
	}

	pr, pw := io.Pipe()
	pw2 := &pipeResponseWriter{pw: pw, header: make(http.Header)}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := httptest.NewRequest("GET", "/api/v1/events", nil).WithContext(ctx)
	req = withPrincipal(req, authz.Principal{ID: "u1"})

	handlerDone := make(chan struct{})
	go func() {
		defer close(handlerDone)
		defer pw.Close() //nolint:errcheck
		d.EventsHandler()(pw2, req)
	}()

	sc := bufio.NewScanner(pr)

	// ── (1) Snapshot ───────────────────────────────────────────────────────────
	var snapData string
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "event: snapshot") {
			if !sc.Scan() {
				t.Fatal("expected data line after event: snapshot")
			}
			snapData = strings.TrimPrefix(sc.Text(), "data: ")
			sc.Scan() // consume empty separator line
			break
		}
	}
	if snapData == "" {
		t.Fatal("no event:snapshot received")
	}
	var snap []stateEvent
	if err := json.Unmarshal([]byte(snapData), &snap); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	if len(snap) != 1 || snap[0].Server != "srv" || snap[0].Session != "api" || snap[0].State != shared.StateWorking {
		t.Fatalf("snapshot content: got %+v", snap)
	}

	// After reading the snapshot through the pipe, the handler goroutine's
	// io.Pipe Write has returned (pipe Write blocks until the reader has
	// consumed all bytes). The handler then called Flush() (no-op) and
	// Subscribe(). A brief yield ensures the scheduler runs the handler
	// goroutine past Subscribe() before we Publish.
	time.Sleep(time.Millisecond)

	// ── (2) Delta ──────────────────────────────────────────────────────────────
	bcast.Publish(state.Change{
		ServerID: "srv", Target: "", Session: "api",
		Global: shared.StateDone, LatestReceivedAt: "ts1",
	})

	var deltaData string
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "event: state") {
			if !sc.Scan() {
				t.Fatal("expected data line after event: state")
			}
			deltaData = strings.TrimPrefix(sc.Text(), "data: ")
			sc.Scan() // consume empty separator line
			break
		}
	}
	if deltaData == "" {
		t.Fatal("no event:state delta received")
	}
	var delta stateEvent
	if err := json.Unmarshal([]byte(deltaData), &delta); err != nil {
		t.Fatalf("unmarshal delta: %v", err)
	}
	if delta.Server != "srv" || delta.Session != "api" || delta.State != shared.StateDone {
		t.Fatalf("delta content: got %+v", delta)
	}

	// ── (3) Clean teardown ─────────────────────────────────────────────────────
	cancel()

	// Drain any remaining lines; the pipe close (from handler goroutine) will
	// cause the scanner to see EOF and exit.
	for sc.Scan() {
	}

	select {
	case <-handlerDone:
		// handler returned cleanly — no goroutine leak
	case <-time.After(2 * time.Second):
		t.Fatal("handler goroutine did not exit after context cancel — goroutine leak")
	}
}

// TestEventsHandler_PublishInRaceWindow is the regression test for FIX 1
// (subscribe-after-snapshot race).  A Change published immediately after the
// snapshot pipe-write unblocks — WITHOUT any sleep — must not be lost.
//
// io.Pipe Write blocks until the reader has fully consumed the bytes. Once the
// scanner drains the snapshot, the handler goroutine's Write returns.  On
// GOMAXPROCS=1 the test goroutine keeps the CPU and calls Publish before the
// handler goroutine reaches Subscribe() (old code), deterministically hitting
// the race.  With the fix (Subscribe before snapshot) the channel is open
// before any Write can occur, so the buffered Change is always delivered.
func TestEventsHandler_PublishInRaceWindow(t *testing.T) {
	// Empty projection — the snapshot is "[]".  Any missed Change produces zero
	// event:state lines and the test times out, proving the race.
	proj := state.NewProjection()
	bcast := state.NewBroadcaster()
	d := Deps{
		Proj:         proj,
		Bcast:        bcast,
		Seen:         &fakeSeenStore{},
		SSEHeartbeat: time.Hour,
	}

	pr, pw := io.Pipe()
	pw2 := &pipeResponseWriter{pw: pw, header: make(http.Header)}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := httptest.NewRequest("GET", "/api/v1/events", nil).WithContext(ctx)
	req = withPrincipal(req, authz.Principal{ID: "u1"})

	handlerDone := make(chan struct{})
	go func() {
		defer close(handlerDone)
		defer pw.Close() //nolint:errcheck
		d.EventsHandler()(pw2, req)
	}()

	sc := bufio.NewScanner(pr)

	// Drain the snapshot lines.  After sc.Scan() consumes the last byte of the
	// snapshot frame the handler goroutine's pw.Write has returned; with OLD
	// code Subscribe() has NOT been called yet.
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "event: snapshot") {
			sc.Scan() // data: [...]
			sc.Scan() // blank separator
			break
		}
	}

	// Publish with NO sleep — on GOMAXPROCS=1 this arrives before the handler
	// goroutine runs again (old: Subscribe not yet called → lost).
	// With fix: Subscribe was first → buffered → delivered.
	bcast.Publish(state.Change{
		ServerID: "s", Target: "", Session: "race-session",
		Global: shared.StateDone, LatestReceivedAt: "2026-06-29 10:00:00.000",
	})

	// Read SSE lines in a goroutine so we can use a select timeout.
	type scanResult struct{ data string }
	found := make(chan scanResult, 1)
	go func() {
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "event: state") {
				sc.Scan() // data line
				found <- scanResult{sc.Text()}
				return
			}
		}
	}()

	select {
	case r := <-found:
		var delta stateEvent
		data := strings.TrimPrefix(r.data, "data: ")
		if err := json.Unmarshal([]byte(data), &delta); err != nil {
			t.Fatalf("unmarshal delta: %v", err)
		}
		if delta.Server != "s" || delta.Session != "race-session" || delta.State != shared.StateDone {
			t.Fatalf("unexpected delta content: %+v", delta)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("no event:state delta received — Change was lost (subscribe-after-snapshot race)")
	}

	cancel()
	for sc.Scan() {} // drain until handler closes pipe
	select {
	case <-handlerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("handler goroutine did not exit after context cancel")
	}
}

// TestEventsHandler_NilBcastIs503 is the regression test for FIX 2
// (nil-Bcast guard): a request against a Deps with no Broadcaster must
// return 503 without panicking.
func TestEventsHandler_NilBcastIs503(t *testing.T) {
	d := Deps{
		Proj:         state.NewProjection(),
		Bcast:        nil, // intentionally nil
		Seen:         &fakeSeenStore{},
		SSEHeartbeat: time.Hour,
	}
	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/events", nil), authz.Principal{ID: "u1"})
	w := httptest.NewRecorder()
	d.EventsHandler()(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil Bcast: want 503, got %d (body=%s)", w.Code, w.Body)
	}
}

// TestEventsHandler_NoFlusherIs500 asserts that a ResponseWriter not
// implementing http.Flusher causes the handler to return 500 immediately.
func TestEventsHandler_NoFlusherIs500(t *testing.T) {
	d := Deps{
		Proj:         state.NewProjection(),
		Bcast:        state.NewBroadcaster(),
		Seen:         &fakeSeenStore{},
		SSEHeartbeat: time.Hour,
	}
	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/events", nil), authz.Principal{ID: "u1"})
	w := &noFlushWriter{header: make(http.Header)}
	d.EventsHandler()(w, r)
	if w.code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", w.code)
	}
}

// TestEventsHandler_SeenProjection asserts that a principal with a seen row
// for a done session gets state=idle in the snapshot (seen projection applied).
func TestEventsHandler_SeenProjection(t *testing.T) {
	proj := state.NewProjection()
	proj.Set(state.SessionView{
		ServerID: "s", Target: "", Session: "work",
		Global:           shared.StateDone,
		LatestReceivedAt: "2026-01-01T10:00:00.000",
	})

	seen := &fakeSeenStore{}
	_ = seen.UpsertSeen(context.Background(), db.PrincipalSeen{
		PrincipalID:   "u1",
		ServerID:      "s",
		TargetID:      "",
		Session:       "work",
		LastFocusedAt: "2026-01-01T10:00:01.000", // after LatestReceivedAt → masks done→idle
	})

	bcast := state.NewBroadcaster()
	d := Deps{
		Proj:         proj,
		Bcast:        bcast,
		Seen:         seen,
		SSEHeartbeat: time.Hour,
	}

	pr, pw := io.Pipe()
	pw2 := &pipeResponseWriter{pw: pw, header: make(http.Header)}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := httptest.NewRequest("GET", "/api/v1/events", nil).WithContext(ctx)
	req = withPrincipal(req, authz.Principal{ID: "u1"})

	handlerDone := make(chan struct{})
	go func() {
		defer close(handlerDone)
		defer pw.Close() //nolint:errcheck
		d.EventsHandler()(pw2, req)
	}()

	sc := bufio.NewScanner(pr)

	var snapData string
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "event: snapshot") {
			if !sc.Scan() {
				t.Fatal("expected data line")
			}
			snapData = strings.TrimPrefix(sc.Text(), "data: ")
			sc.Scan()
			break
		}
	}
	if snapData == "" {
		t.Fatal("no snapshot received")
	}
	var snap []stateEvent
	if err := json.Unmarshal([]byte(snapData), &snap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(snap) != 1 || snap[0].State != shared.StateIdle {
		t.Fatalf("seen projection: want state=idle, got %+v", snap)
	}

	cancel()
	for sc.Scan() {
	}
	select {
	case <-handlerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("handler goroutine leak")
	}
}
