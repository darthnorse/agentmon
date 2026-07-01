# AgentMon Phase 5 (Hardening) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bound the two places where a misbehaving client can spawn unbounded resources: a per-principal concurrency cap on terminal-WS relays, and HTTP/tmux timeouts on the agent.

**Architecture:** A new `authn.Gauge` live-slot counter caps concurrent relays per principal (reject-newest → 429), wired nil-guarded into `PaneRelayHandler`. The agent gains `http.Server` Read/Header/Idle timeouts (mirroring the hub) plus a per-request `context.WithTimeout` around its tmux-shelling handlers. No behavior change on the happy path.

**Tech Stack:** Go 1.26, `net/http`, `gorilla/websocket`, standard `sync`/`context`. Go workspace `agentmon/{shared,agent,hubd}`. Tests are `go test` in-package.

## Global Constraints

- Threat model: **single LAN operator, no adversary.** These are robustness rails against the operator's own runaway client, NOT anti-attacker defenses. Keep ceilings generous.
- **CGO_ENABLED=0**, Go 1.26, `modernc.org/sqlite` (unchanged — no DB work here).
- Relay cap = **32 per principal**; reject-newest with HTTP **429**; rejections are **NOT audited**.
- Agent server timeouts: **ReadHeaderTimeout 10s, ReadTimeout 30s, IdleTimeout 120s, NO WriteTimeout** (WS-safe — identical to the hub's, verified in prod).
- Agent per-request tmux timeout = **10s**, applied to `SessionsHandler` / `CreateSessionHandler` / `RenameSessionHandler` only (NOT `StateHandler`, NOT the WS pane-IO handler).
- Every new `Deps`/config field is **nil-guarded** (nil ⇒ unlimited / default) so existing tests are unaffected; the live values are wired only in `main`.
- SAFE acceptance only: scratch hub on a loopback port + fresh/copy DB + a **throwaway tmux socket** (`tmux -L <scratch>`). NEVER the default socket (session 0 = operator + Claude), NEVER the live `agentmon` socket.
- Commit after each task. Do NOT push or deploy without owner confirmation.

---

## File Structure

- **Create** `hubd/internal/authn/gauge.go` — the `Gauge` primitive (Acquire/Release/InUse).
- **Create** `hubd/internal/authn/gauge_test.go` — unit + race tests for `Gauge`.
- **Modify** `hubd/internal/api/servers.go` — add `RelayCap *authn.Gauge` field to `Deps`.
- **Modify** `hubd/internal/api/ws.go` — acquire/release the slot in `PaneRelayHandler`.
- **Modify** `hubd/internal/api/ws_test.go` — cap-at-N+1 (429), release-on-exit, `hubPaneIDRe` confirm-test.
- **Modify** `hubd/cmd/agentmon-hubd/main.go` — construct `authn.NewGauge(32)` → `Deps.RelayCap`.
- **Modify** `agent/cmd/agentmon-agent/main.go` — `newAgentServer` helper with timeouts.
- **Create** `agent/cmd/agentmon-agent/main_test.go` — assert the server's timeout fields.
- **Modify** `agent/internal/api/sessions.go` — `agentTmuxTimeout` var + wrap the three handlers.
- **Modify** `agent/internal/api/sessions_test.go` — the timeout tests.

---

## Task 1: `authn.Gauge` — per-key live concurrency counter

**Files:**
- Create: `hubd/internal/authn/gauge.go`
- Test: `hubd/internal/authn/gauge_test.go`

**Interfaces:**
- Consumes: nothing (stdlib `sync` only).
- Produces:
  - `func NewGauge(max int) *Gauge`
  - `func (g *Gauge) Acquire(key string) bool` — true if it took a slot; false (no increment) at the cap.
  - `func (g *Gauge) Release(key string)` — frees a slot; deletes the key at zero; no-op if none held.
  - `func (g *Gauge) InUse(key string) int` — slots currently held for key.

- [ ] **Step 1: Write the failing test**

Create `hubd/internal/authn/gauge_test.go`:

```go
package authn

import (
	"sync"
	"testing"
)

func TestGaugeAcquireUpToCapThenReject(t *testing.T) {
	g := NewGauge(2)
	if !g.Acquire("u1") || !g.Acquire("u1") {
		t.Fatal("first two acquires should succeed")
	}
	if g.Acquire("u1") {
		t.Fatal("third acquire should be rejected at cap")
	}
	if got := g.InUse("u1"); got != 2 {
		t.Fatalf("InUse = %d, want 2 (rejected acquire must not increment)", got)
	}
	// A different key has its own budget.
	if !g.Acquire("u2") {
		t.Fatal("distinct key should have its own budget")
	}
}

func TestGaugeReleaseFreesSlotAndDeletesAtZero(t *testing.T) {
	g := NewGauge(1)
	if !g.Acquire("u1") {
		t.Fatal("acquire should succeed")
	}
	if g.Acquire("u1") {
		t.Fatal("at cap")
	}
	g.Release("u1")
	if got := g.InUse("u1"); got != 0 {
		t.Fatalf("InUse after release = %d, want 0", got)
	}
	if len(g.inuse) != 0 {
		t.Fatalf("map should evict the zeroed key, len = %d", len(g.inuse))
	}
	if !g.Acquire("u1") {
		t.Fatal("acquire should succeed again after release")
	}
}

func TestGaugeReleaseUnheldIsNoop(t *testing.T) {
	g := NewGauge(1)
	g.Release("nobody") // must not panic or go negative
	if got := g.InUse("nobody"); got != 0 {
		t.Fatalf("InUse = %d, want 0", got)
	}
}

func TestGaugeConcurrentAcquireRelease(t *testing.T) {
	g := NewGauge(1000)
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if g.Acquire("u1") {
					g.Release("u1")
				}
			}
		}()
	}
	wg.Wait()
	if got := g.InUse("u1"); got != 0 {
		t.Fatalf("InUse after balanced acquire/release = %d, want 0", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/agentmon/hubd && go test ./internal/authn/ -run TestGauge -v`
Expected: FAIL — `undefined: NewGauge` (compile error).

- [ ] **Step 3: Write minimal implementation**

Create `hubd/internal/authn/gauge.go`:

```go
package authn

import "sync"

// Gauge is a per-key live concurrency counter. Acquire takes a slot when the
// key is below the cap; Release frees one. Unlike Limiter (a sliding-window
// RATE counter), Gauge tracks slots currently held, so it bounds concurrent
// long-lived resources — e.g. terminal-WS relays, each of which makes the agent
// spawn a tmux control-mode subprocess. Reject-newest: at the cap Acquire
// returns false WITHOUT incrementing, so an existing holder is never evicted.
type Gauge struct {
	mu    sync.Mutex
	max   int
	inuse map[string]int
}

// NewGauge returns a Gauge that allows at most max concurrent slots per key.
func NewGauge(max int) *Gauge {
	return &Gauge{max: max, inuse: make(map[string]int)}
}

// Acquire reserves a slot for key and reports success. At the cap it returns
// false without incrementing.
func (g *Gauge) Acquire(key string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.inuse[key] >= g.max {
		return false
	}
	g.inuse[key]++
	return true
}

// Release frees a slot for key. The key is deleted at zero so a churn of
// distinct keys cannot grow the map unbounded. Releasing an unheld key is a no-op.
func (g *Gauge) Release(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.inuse[key] <= 1 {
		delete(g.inuse, key)
		return
	}
	g.inuse[key]--
}

// InUse returns the number of slots currently held for key (0 if none).
func (g *Gauge) InUse(key string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.inuse[key]
}
```

- [ ] **Step 4: Run tests (with the race detector) to verify they pass**

Run: `cd /root/agentmon/hubd && go test ./internal/authn/ -run TestGauge -race -v`
Expected: PASS (all four).

- [ ] **Step 5: Commit**

```bash
cd /root/agentmon
git add hubd/internal/authn/gauge.go hubd/internal/authn/gauge_test.go
git commit -m "feat(hubd/authn): add Gauge — per-key live concurrency counter"
```

---

## Task 2: Wire the relay concurrency cap into `PaneRelayHandler`

**Files:**
- Modify: `hubd/internal/api/servers.go` (add `RelayCap` field to `Deps`, ~line 58)
- Modify: `hubd/internal/api/ws.go` (`PaneRelayHandler`, after the Origin re-check ~line 107)
- Modify: `hubd/cmd/agentmon-hubd/main.go` (construct the gauge, ~line 127)
- Test: `hubd/internal/api/ws_test.go`

**Interfaces:**
- Consumes: `authn.NewGauge`, `(*authn.Gauge).Acquire/Release/InUse` (Task 1); existing `Deps`, `authorizeOr403`, `authn.CheckOrigin`, test helpers `relayDeps` / `relayServer` / `dialBrowser` / `fakeAgentWS`.
- Produces: `Deps.RelayCap *authn.Gauge` (nil ⇒ unlimited).

- [ ] **Step 1: Write the failing tests**

Append to `hubd/internal/api/ws_test.go`:

```go
func TestRelayConcurrencyCapRejectsOverCap(t *testing.T) {
	d := relayDeps("http://unused", "b", "k", &recSink{})
	d.RelayCap = authn.NewGauge(1)
	d.RelayCap.Acquire("u1") // pre-fill the single slot for this principal
	hub := relayServer(d, authz.Principal{ID: "u1"})
	defer hub.Close()

	_, resp, err := dialBrowser(t, hub, "/api/v1/servers/aigallery/panes/%253/io?target=default", testOrigin)
	if err == nil {
		t.Fatal("expected handshake failure when over the relay cap")
	}
	if resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("want 429, got %v", resp)
	}
}

func TestRelayConcurrencyCapReleasesOnEarlyReturn(t *testing.T) {
	// Unknown server → the handler 404s AFTER acquiring the slot; the deferred
	// Release must return the slot so the principal is not permanently charged.
	d := relayDeps("http://unused", "b", "k", &recSink{})
	d.RelayCap = authn.NewGauge(1)
	hub := relayServer(d, authz.Principal{ID: "u1"})
	defer hub.Close()

	_, resp, err := dialBrowser(t, hub, "/api/v1/servers/nope/panes/%253/io?target=default", testOrigin)
	if err == nil || resp == nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got resp=%v err=%v", resp, err)
	}
	// The Release runs in the handler goroutine after the response is written, so poll.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if d.RelayCap.InUse("u1") == 0 {
			return // slot released — success
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("slot not released after early-return 404; InUse=%d", d.RelayCap.InUse("u1"))
}

func TestRelayConcurrencyCapReleasesAfterTeardown(t *testing.T) {
	rec := &dialRecord{closed: make(chan struct{}, 8)}
	agent := fakeAgentWS(t, rec)
	defer agent.Close()
	d := relayDeps(agent.URL, "b", "k", &recSink{})
	d.RelayCap = authn.NewGauge(1) // cap 1: the second dial proves release only if the first freed its slot
	hub := relayServer(d, authz.Principal{ID: "u1"})
	defer hub.Close()

	c, _, err := dialBrowser(t, hub, "/api/v1/servers/aigallery/panes/%253/io?target=default", testOrigin)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, _ = c.ReadMessage() // drain SNAP
	c.Close()                 // browser goes away → relay tears down
	select {
	case <-rec.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("agent side not torn down")
	}
	// Poll for the slot release, then a fresh dial (cap 1) must succeed.
	deadline := time.Now().Add(2 * time.Second)
	for d.RelayCap.InUse("u1") != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if d.RelayCap.InUse("u1") != 0 {
		t.Fatalf("slot not released after teardown; InUse=%d", d.RelayCap.InUse("u1"))
	}
}

func TestHubPaneIDReRejectsInjection(t *testing.T) {
	// Confirm-test: locks the hub's pane-id guard on the file we are editing.
	good := []string{"%0", "%37", "%1234"}
	bad := []string{"", "0", "%", "%0\ninject", "%0;x", "%0 %1", "% 0", "%0a", "abc"}
	for _, s := range good {
		if !hubPaneIDRe.MatchString(s) {
			t.Errorf("hubPaneIDRe should accept %q", s)
		}
	}
	for _, s := range bad {
		if hubPaneIDRe.MatchString(s) {
			t.Errorf("hubPaneIDRe should reject %q", s)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /root/agentmon/hubd && go test ./internal/api/ -run 'TestRelayConcurrencyCap|TestHubPaneIDRe' -v`
Expected: the three `TestRelayConcurrencyCap*` FAIL — `d.RelayCap` is an unknown field (compile error). (`TestHubPaneIDReRejectsInjection` would pass once it compiles, but the package won't compile until Step 3 adds the field.)

- [ ] **Step 3a: Add the nil-guarded field to `Deps`**

In `hubd/internal/api/servers.go`, inside `type Deps struct` (after the `Presence` field, ~line 58), add:

```go
	RelayCap            *authn.Gauge       // Phase 5: per-principal cap on concurrent terminal relays (nil → unlimited)
```

(`authn` is already imported in `servers.go`.)

- [ ] **Step 3b: Acquire/Release in `PaneRelayHandler`**

In `hubd/internal/api/ws.go`, in the handler returned by `PaneRelayHandler`, immediately AFTER the Origin re-check block (the `if !authn.CheckOrigin(...) { ... }` ending ~line 107) and BEFORE `srv, found, err := d.Reg.Get(...)`, insert:

```go
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
```

(`p` is the principal returned by `authorizeOr403` earlier in the handler.)

- [ ] **Step 3c: Wire the live gauge in `main`**

In `hubd/cmd/agentmon-hubd/main.go`, just before the `API: api.Deps{` literal (~line 127), add:

```go
	relayCap := authn.NewGauge(32) // Phase 5: ≤32 concurrent terminal relays per principal
```

and inside the `api.Deps{...}` literal, after `Presence: presence,`, add:

```go
			RelayCap:            relayCap,
```

(`authn` is already imported in `main.go` for `LoginDeps`/`PasswordDeps`.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /root/agentmon/hubd && go test ./internal/api/ -run 'TestRelayConcurrencyCap|TestHubPaneIDRe' -race -v`
Expected: PASS (4 tests). Then the full relay suite to prove no regression:
Run: `cd /root/agentmon/hubd && go test ./internal/api/ -run TestRelay -race`
Expected: PASS.

- [ ] **Step 5: Verify the hub still builds**

Run: `cd /root/agentmon/hubd && go build ./...`
Expected: no output (success).

- [ ] **Step 6: Commit**

```bash
cd /root/agentmon
git add hubd/internal/authn/gauge.go hubd/internal/api/servers.go hubd/internal/api/ws.go hubd/internal/api/ws_test.go hubd/cmd/agentmon-hubd/main.go
git commit -m "feat(hubd): cap concurrent terminal relays per principal (429 over 32)"
```

---

## Task 3: Agent HTTP server timeouts

**Files:**
- Modify: `agent/cmd/agentmon-agent/main.go` (extract `newAgentServer`, replace `ListenAndServe`)
- Test: `agent/cmd/agentmon-agent/main_test.go` (create)

**Interfaces:**
- Consumes: stdlib `net/http`, `time`.
- Produces: `func newAgentServer(addr string, h http.Handler) *http.Server`.

- [ ] **Step 1: Write the failing test**

Create `agent/cmd/agentmon-agent/main_test.go`:

```go
package main

import (
	"net/http"
	"testing"
	"time"
)

func TestNewAgentServerTimeouts(t *testing.T) {
	s := newAgentServer(":0", http.NewServeMux())
	if s.ReadHeaderTimeout != 10*time.Second {
		t.Errorf("ReadHeaderTimeout = %v, want 10s", s.ReadHeaderTimeout)
	}
	if s.ReadTimeout != 30*time.Second {
		t.Errorf("ReadTimeout = %v, want 30s", s.ReadTimeout)
	}
	if s.IdleTimeout != 120*time.Second {
		t.Errorf("IdleTimeout = %v, want 120s", s.IdleTimeout)
	}
	if s.WriteTimeout != 0 {
		t.Errorf("WriteTimeout = %v, want 0 (the long-lived pane-IO WS must not be killed)", s.WriteTimeout)
	}
	if s.Addr != ":0" {
		t.Errorf("Addr = %q, want :0", s.Addr)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/agentmon/agent && go test ./cmd/agentmon-agent/ -run TestNewAgentServerTimeouts -v`
Expected: FAIL — `undefined: newAgentServer`.

- [ ] **Step 3: Implement**

In `agent/cmd/agentmon-agent/main.go`:

1. Add `"time"` to the import block.
2. Add the helper (e.g. just above `func main()`):

```go
// newAgentServer builds the agent's HTTP server with Slowloris/hygiene timeouts.
// These mirror the hub's (hubd/cmd/agentmon-hubd/main.go) and are verified
// WS-safe there: after the pane-IO Upgrade the conn is hijacked, so ReadTimeout
// no longer applies. There is deliberately NO WriteTimeout — a global write
// deadline would kill the long-lived terminal WS mid-stream.
func newAgentServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}
```

3. Replace the final line
   `log.Fatal(http.ListenAndServe(cfg.Listen, mux))`
   with:

```go
	srv := newAgentServer(cfg.Listen, mux)
	log.Fatal(srv.ListenAndServe())
```

(Keep the existing `log.Printf("agentmon-agent %s listening ...")` line directly above.)

- [ ] **Step 4: Run test + build to verify pass**

Run: `cd /root/agentmon/agent && go test ./cmd/agentmon-agent/ -run TestNewAgentServerTimeouts -v && go build ./...`
Expected: PASS, then a clean build.

- [ ] **Step 5: Commit**

```bash
cd /root/agentmon
git add agent/cmd/agentmon-agent/main.go agent/cmd/agentmon-agent/main_test.go
git commit -m "feat(agent): add HTTP server Read/Header/Idle timeouts (mirror the hub)"
```

---

## Task 4: Agent per-request tmux timeout

**Files:**
- Modify: `agent/internal/api/sessions.go` (add `agentTmuxTimeout` var; wrap the 3 handlers)
- Test: `agent/internal/api/sessions_test.go` (append)

**Interfaces:**
- Consumes: existing `SessionsHandler` / `CreateSessionHandler` / `RenameSessionHandler` signatures, `testCfg()`, `config.Config`, `tmux.DiscoverOpts`.
- Produces: package var `agentTmuxTimeout time.Duration` (default 10s, overridable in tests).

- [ ] **Step 1: Write the failing test**

Append to `agent/internal/api/sessions_test.go`:

```go
func TestSessionsHandlerTimesOutOnHungTmux(t *testing.T) {
	old := agentTmuxTimeout
	agentTmuxTimeout = 30 * time.Millisecond
	defer func() { agentTmuxTimeout = old }()

	// A discoverer that blocks until the request context is cancelled — i.e. a hung tmux.
	slow := func(ctx context.Context, _ tmux.DiscoverOpts) ([]shared.Session, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	h := SessionsHandler(testCfg(), slow, nil)
	rr := httptest.NewRecorder()
	start := time.Now()
	h(rr, httptest.NewRequest(http.MethodGet, "/sessions?target=default", nil))
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("handler did not bound the hung discoverer: took %v", elapsed)
	}
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("want 500 on discovery timeout, got %d", rr.Code)
	}
}

func TestCreateSessionHandlerTimesOutOnHungTmux(t *testing.T) {
	old := agentTmuxTimeout
	agentTmuxTimeout = 30 * time.Millisecond
	defer func() { agentTmuxTimeout = old }()

	slow := func(ctx context.Context, _, _, _ string) error {
		<-ctx.Done()
		return ctx.Err()
	}
	h := CreateSessionHandler(testCfg(), slow)
	rr := httptest.NewRecorder()
	body := strings.NewReader(`{"name":"ok"}`)
	start := time.Now()
	h(rr, httptest.NewRequest(http.MethodPost, "/sessions?target=default", body))
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("create did not bound the hung creator: took %v", elapsed)
	}
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("want 500 on create timeout, got %d", rr.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/agentmon/agent && go test ./internal/api/ -run 'TimesOutOnHungTmux' -v`
Expected: FAIL — `undefined: agentTmuxTimeout` (compile error).

- [ ] **Step 3: Implement**

In `agent/internal/api/sessions.go`:

1. Add `"time"` to the import block (it currently imports `context`, `encoding/json`, `errors`, `log`, `net/http`, `os`; add `time`).
2. Add the package var (near the top, after the imports):

```go
// agentTmuxTimeout bounds each tmux-shelling handler so a hung `tmux` invocation
// cannot pin the request goroutine — the http.Server ReadTimeout only covers
// reading the request, not the handler's shell-out. A var (not const) so tests
// can shorten it.
var agentTmuxTimeout = 10 * time.Second
```

3. In `SessionsHandler`, replace:

```go
		sessions, err := discover(r.Context(), tmux.DiscoverOpts{
```

with:

```go
		ctx, cancel := context.WithTimeout(r.Context(), agentTmuxTimeout)
		defer cancel()
		sessions, err := discover(ctx, tmux.DiscoverOpts{
```

4. In `CreateSessionHandler`, replace:

```go
		if err := create(r.Context(), t.SocketName, req.Name, cwd); err != nil {
```

with:

```go
		ctx, cancel := context.WithTimeout(r.Context(), agentTmuxTimeout)
		defer cancel()
		if err := create(ctx, t.SocketName, req.Name, cwd); err != nil {
```

5. In `RenameSessionHandler`, replace:

```go
		if err := rename(r.Context(), t.SocketName, req.From, req.To); err != nil {
```

with:

```go
		ctx, cancel := context.WithTimeout(r.Context(), agentTmuxTimeout)
		defer cancel()
		if err := rename(ctx, t.SocketName, req.From, req.To); err != nil {
```

- [ ] **Step 4: Run tests + full agent api suite to verify pass**

Run: `cd /root/agentmon/agent && go test ./internal/api/ -run 'TimesOutOnHungTmux' -v`
Expected: PASS (2 tests).
Run: `cd /root/agentmon/agent && go test ./... -race`
Expected: PASS (no regression across the agent module).

- [ ] **Step 5: Commit**

```bash
cd /root/agentmon
git add agent/internal/api/sessions.go agent/internal/api/sessions_test.go
git commit -m "feat(agent): bound tmux-shelling handlers with a 10s per-request timeout"
```

---

## Task 5: Full verification, review, and acceptance (no new code)

**Files:** none (verification + process).

- [ ] **Step 1: Full workspace test + vet**

Run:
```bash
cd /root/agentmon
( cd hubd && go test ./... -race ) && ( cd agent && go test ./... -race ) && ( cd shared && go test ./... )
( cd hubd && go vet ./... ) && ( cd agent && go vet ./... )
```
Expected: all PASS; vet clean.

- [ ] **Step 2: Web build unaffected (sanity — no web changes this milestone)**

Run: `cd /root/agentmon/web && npm run build`
Expected: builds (only the pre-existing xterm chunk-size warning). If it fails for a reason unrelated to this milestone, note it and move on — no web files were touched.

- [ ] **Step 3: `/multi-review --codex` on the branch diff**

Invoke the `multi-review` skill with `--codex` on the `phase-5-hardening` branch diff (the four code changes). Apply any real findings (add regression tests first); defer the rest with written rationale appended to the carryover.

- [ ] **Step 4: SAFE live acceptance of the relay cap**

On a **throwaway** tmux socket only (heed the safety constraints):
```bash
tmux -L p5scratch new -d -s demo1   # throwaway socket, NOT default, NOT agentmon
```
- Build a scratch hub, run it on a loopback port against a **fresh/copy** DB.
- Set the cap low for the probe (either build a scratch binary with `NewGauge(2)` or drive N past 32) and open relays past the cap; observe the **429** on the over-cap dial and confirm existing relays keep working (reject-newest).
- Confirm the default-socket session 0 is untouched afterward (`tmux -L p5scratch kill-server` to clean up; never touch the default or `agentmon` sockets).

Document the acceptance result inline in the carryover.

- [ ] **Step 5: Write the carryover + update memory**

- Create `docs/superpowers/phase5-carryover.md`: what shipped (Tasks 1–4), the review outcome, the SAFE-acceptance result, and the **Deferred (resolve if multi-user lands)** list copied from the spec §3 with rationale.
- Update the memory index + `agent-onboarding-status.md` / `live-deployment.md` to note Phase 5 is built-on-branch (and whether merged/deployed — only after owner confirms).

- [ ] **Step 6: Finish the branch**

Invoke the `superpowers:finishing-a-development-branch` skill to present merge/PR options. **Deploy only after owner confirmation** — the relay cap is a **hub** redeploy (`docker compose up -d --build` on the dedicated box); the agent timeouts require an **agent** rebuild + restart too.

---

## Self-Review (completed against the spec)

- **Spec coverage:** §2A relay cap → Tasks 1–2; §2B agent timeouts → Task 3; §2B per-request tmux timeout → Task 4; §3 deferrals → Task 5 Step 5 (carryover); §4 confirm-test (pane-id) → Task 2 Step 1; §5 testing/acceptance → Tasks 1–5; §5 deploy note → Task 5 Step 6. No gaps.
- **Placeholder scan:** no TBD/TODO; every code step shows complete code and exact run commands.
- **Type consistency:** `NewGauge`/`Acquire`/`Release`/`InUse` and `Deps.RelayCap *authn.Gauge` match across Tasks 1–2; `newAgentServer(string, http.Handler) *http.Server` and `agentTmuxTimeout time.Duration` are used exactly as defined. Handler seam signatures (`SessionCreator`, `SessionRenamer`, `Discoverer`) match the existing definitions in `sessions.go`.
- **Scope:** two changes, five tasks (four code + one verification), single implementation plan — correctly sized.
