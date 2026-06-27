# AgentMon Phase 1 / M2 — Agent terminal WS + HMAC directive — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. TDD every task.

**Goal:** Give `agentmon-agent` a live terminal WebSocket (`WS /panes/{paneId}/io`) that bridges a tmux pane's bytes both directions, gated by a hub-minted HMAC directive, with mechanical `ro`/`rw` enforcement.

**Architecture:** A per-server agent verifies the hub's `X-AgentMon-Directive` (HMAC-SHA256 over the canonical JSON payload) on top of the existing bearer token, resolves the requested pane to its tmux session, attaches a control-mode `ControlClient` (ported in M1), sends a bounded scrollback snapshot as the first binary frame, then pumps live `%output` bytes to the client and client bytes back into the pane via `send-keys -H`. The agent does only mechanical verification + `ro`/`rw`; it never derives user authorization (that is the hub's job, M4).

**Tech Stack:** Go 1.23, `CGO_ENABLED=0`, stdlib `crypto/hmac`+`crypto/sha256`+`encoding/base64`, `github.com/gorilla/websocket v1.5.3` (already in module cache; matches the Phase 0.5 spike), tmux 3.5a control mode.

## Global Constraints

- Go modules build/test per-module only: `./shared/... ./agent/... ./hubd/...` — **never** `go build ./...` at the workspace root (root is not a module).
- All Go binaries build `CGO_ENABLED=0` (static). New deps must be pure-Go.
- Secrets resolve via `env:`/`file:` refs; `hub_token` AND `directive_key` are required at agent startup (fatal if empty).
- tmux integration tests call `requireTmux(t)` and `t.Skip` when tmux is absent (CI has no tmux); pure unit tests stay green in CI.
- The agent enforces **only** mechanical `ro`/`rw` and directive validity — never user authz (spec §13.4). Raw keystrokes are never logged (spec §13.5).
- New deps added with `go get` land as direct requires — strip any stray `// indirect`; never `go mod tidy` (it would drop the intentional `require agentmon/shared`).
- Pane ids interpolated into the control-mode command stream MUST be validated `^%[0-9]+$` (already enforced by `NewControlClient`); the WS handler validates again before use.

**Directive wire format (spec §6.3), authoritative for every task:**
```
X-AgentMon-Directive: <base64url(payload)>.<base64url(HMAC-SHA256(payload, directive_key))>
```
`payload` = `shared.Directive.CanonicalJSON()` bytes. base64url = `base64.RawURLEncoding` (no padding), both parts. The agent HMACs the **received payload bytes verbatim** (it does not re-marshal), then unmarshals for field checks. Directive payload fields (already defined in `shared/directive.go`): `ServerID, Target, Resource, Mode, PrincipalID, Action, Exp(RFC3339), Nonce, RequestID`.

---

## File structure

| File | Responsibility |
|---|---|
| `agent/internal/directive/directive.go` | `Sign` (HMAC mint — used by tests now, hub in M4) + `Verifier` (parse, HMAC-verify, expiry, resource/target/server match, nonce replay cache). Pure; no tmux/WS. |
| `agent/internal/directive/directive_test.go` | Unit tests: round-trip verify, forged sig, expired, resource/target/server mismatch, replay, bad mode, malformed header, far-future exp. |
| `agent/internal/tmux/control.go` (modify) | Add `socket` to `ControlClient`; thread `-L <socket>` into the attach command; add `OutputChan()`/`DoneChan()` accessors so it satisfies `api.PaneConn`. |
| `agent/internal/tmux/pane.go` | `ResolvePaneSession` (authoritative pane→session lookup via `list-panes -a`), `CapturePane` (scrollback snapshot, ported from spike), `TuneSession` (window-size latest / aggressive-resize off). |
| `agent/internal/tmux/pane_test.go` | Unit tests (Runner seam): resolve hit/miss, socket flag threading, capture arg building. |
| `agent/internal/tmux/pane_integration_test.go` | Real-tmux: resolve a real pane's session; capture returns CRLF bytes. |
| `agent/internal/api/ws.go` | `PaneIO` handler: bearer (middleware) + directive gate + pane resolve + WS upgrade + scrollback + bidirectional pump + `ro`/`rw`. Injectable seams for unit tests. |
| `agent/internal/api/ws_test.go` | Unit tests with a fake `PaneConn` + httptest WS: scrollback-first, input forwarded (rw) / dropped (ro), resize parsed, directive failures rejected pre-upgrade. |
| `agent/cmd/agentmon-agent/main.go` (modify) | Require `directive_key`; build `Verifier` + `PaneIO`; register `GET /panes/{paneId}/io`. |
| `agent/go.mod` / `agent/go.sum` (modify) | Add `github.com/gorilla/websocket v1.5.3`. |

---

## Task A: HMAC directive Sign + Verifier (agent side)

**Files:**
- Create: `agent/internal/directive/directive.go`
- Test: `agent/internal/directive/directive_test.go`

**Interfaces:**
- Consumes: `shared.Directive` + `shared.Directive.CanonicalJSON()` (exists); `shared.PaneID(server,target,pane)` (exists).
- Produces:
  - `func Sign(key []byte, d shared.Directive) (string, error)` — returns the `X-AgentMon-Directive` header value.
  - `type Verifier struct{ ... }`
  - `func NewVerifier(serverID string, key []byte, now func() time.Time) *Verifier` (`now` nil → `time.Now`).
  - `func (v *Verifier) Verify(header, wantResource, wantTarget string) (shared.Directive, error)` — validates everything and records the nonce; returns the validated directive (`.Mode` is authoritative).
  - Sentinel errors: `ErrMalformed, ErrBadSignature, ErrExpired, ErrServerMismatch, ErrResourceMismatch, ErrTargetMismatch, ErrBadMode, ErrReplay`.

- [ ] **Step 1: Write the failing tests**

```go
package directive

import (
	"testing"
	"time"

	"agentmon/shared"
)

func testKey() []byte { return []byte("super-secret-signing-key-0123456789") }

// fixedNow returns a clock pinned to t for deterministic expiry/replay tests.
func fixedNow(t time.Time) func() time.Time { return func() time.Time { return t } }

func baseDirective(now time.Time) shared.Directive {
	return shared.Directive{
		ServerID:    "server-a",
		Target:      "default",
		Resource:    shared.PaneID("server-a", "default", "%3"),
		Mode:        "rw",
		PrincipalID: "user_1",
		Action:      "terminal.write",
		Exp:         now.Add(60 * time.Second).Format(time.RFC3339),
		Nonce:       "nonce-1",
		RequestID:   "req-1",
	}
}

func TestVerifyRoundTrip(t *testing.T) {
	now := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)
	d := baseDirective(now)
	hdr, err := Sign(testKey(), d)
	if err != nil {
		t.Fatal(err)
	}
	v := NewVerifier("server-a", testKey(), fixedNow(now))
	got, err := v.Verify(hdr, d.Resource, "default")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.Mode != "rw" || got.Nonce != "nonce-1" {
		t.Fatalf("verified directive wrong: %+v", got)
	}
}

func TestVerifyRejectsForgedSignature(t *testing.T) {
	now := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)
	d := baseDirective(now)
	hdr, _ := Sign([]byte("the-WRONG-key-aaaaaaaaaaaaaaaaaaaa"), d)
	v := NewVerifier("server-a", testKey(), fixedNow(now))
	if _, err := v.Verify(hdr, d.Resource, "default"); err == nil {
		t.Fatal("want error for forged signature")
	}
}

func TestVerifyRejectsExpired(t *testing.T) {
	now := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)
	d := baseDirective(now)
	hdr, _ := Sign(testKey(), d)
	v := NewVerifier("server-a", testKey(), fixedNow(now.Add(61*time.Second)))
	if _, err := v.Verify(hdr, d.Resource, "default"); err == nil {
		t.Fatal("want error for expired directive")
	}
}

func TestVerifyRejectsResourceMismatch(t *testing.T) {
	now := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)
	d := baseDirective(now)
	hdr, _ := Sign(testKey(), d)
	v := NewVerifier("server-a", testKey(), fixedNow(now))
	other := shared.PaneID("server-a", "default", "%9") // different pane
	if _, err := v.Verify(hdr, other, "default"); err == nil {
		t.Fatal("want error when resource does not match the requested pane")
	}
}

func TestVerifyRejectsTargetMismatch(t *testing.T) {
	now := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)
	d := baseDirective(now)
	hdr, _ := Sign(testKey(), d)
	v := NewVerifier("server-a", testKey(), fixedNow(now))
	if _, err := v.Verify(hdr, d.Resource, "other-target"); err == nil {
		t.Fatal("want error when target does not match")
	}
}

func TestVerifyRejectsServerMismatch(t *testing.T) {
	now := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)
	d := baseDirective(now)
	hdr, _ := Sign(testKey(), d)
	v := NewVerifier("server-B", testKey(), fixedNow(now)) // agent is a different server
	if _, err := v.Verify(hdr, d.Resource, "default"); err == nil {
		t.Fatal("want error when directive serverId is not this agent's")
	}
}

func TestVerifyRejectsReplayedNonce(t *testing.T) {
	now := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)
	d := baseDirective(now)
	hdr, _ := Sign(testKey(), d)
	v := NewVerifier("server-a", testKey(), fixedNow(now))
	if _, err := v.Verify(hdr, d.Resource, "default"); err != nil {
		t.Fatalf("first use should pass: %v", err)
	}
	if _, err := v.Verify(hdr, d.Resource, "default"); err == nil {
		t.Fatal("want error on replay of the same nonce")
	}
}

func TestVerifyRejectsBadMode(t *testing.T) {
	now := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)
	d := baseDirective(now)
	d.Mode = "admin" // not ro|rw
	hdr, _ := Sign(testKey(), d)
	v := NewVerifier("server-a", testKey(), fixedNow(now))
	if _, err := v.Verify(hdr, d.Resource, "default"); err == nil {
		t.Fatal("want error for a mode that is not ro|rw")
	}
}

func TestVerifyRejectsMalformedHeader(t *testing.T) {
	v := NewVerifier("server-a", testKey(), fixedNow(time.Now()))
	for _, h := range []string{"", "no-dot", "a.b.c", "!!!.@@@"} {
		if _, err := v.Verify(h, "pane:server-a/default/%3", "default"); err == nil {
			t.Fatalf("want error for malformed header %q", h)
		}
	}
}

func TestVerifyRejectsFarFutureExp(t *testing.T) {
	now := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)
	d := baseDirective(now)
	d.Exp = now.Add(2 * time.Hour).Format(time.RFC3339) // beyond the sanity cap
	hdr, _ := Sign(testKey(), d)
	v := NewVerifier("server-a", testKey(), fixedNow(now))
	if _, err := v.Verify(hdr, d.Resource, "default"); err == nil {
		t.Fatal("want error for an exp further out than the max lifetime cap")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd agent && go test ./internal/directive/ -v`
Expected: build failure — `undefined: Sign`, `undefined: NewVerifier`.

- [ ] **Step 3: Write the minimal implementation**

```go
// Package directive signs and verifies the hub→agent HMAC access directive
// (spec §6.3). Signing (Sign) is the crypto primitive the hub uses to mint a
// directive (wired in M4) and tests use to exercise the verifier. The agent only
// ever Verifies: it checks the HMAC, expiry, server/resource/target match, and
// nonce replay — it never derives user authorization from a directive.
package directive

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"agentmon/shared"
)

var (
	ErrMalformed        = errors.New("directive: malformed header")
	ErrBadSignature     = errors.New("directive: signature mismatch")
	ErrExpired          = errors.New("directive: expired")
	ErrServerMismatch   = errors.New("directive: server mismatch")
	ErrResourceMismatch = errors.New("directive: resource mismatch")
	ErrTargetMismatch   = errors.New("directive: target mismatch")
	ErrBadMode          = errors.New("directive: mode not ro|rw")
	ErrReplay           = errors.New("directive: nonce replay")
)

// maxLifetime caps how far in the future a directive's exp may be. The hub mints
// ~60s expiries; rejecting anything beyond this bounds the nonce cache retention
// and catches a bogus far-future directive. A clock-skew allowance is folded in.
const maxLifetime = 5 * time.Minute

func mac(key, payload []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(payload)
	return h.Sum(nil)
}

// Sign returns the X-AgentMon-Directive header value for d.
func Sign(key []byte, d shared.Directive) (string, error) {
	payload, err := d.CanonicalJSON()
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	return enc.EncodeToString(payload) + "." + enc.EncodeToString(mac(key, payload)), nil
}

// Verifier verifies directives for one agent (server). Safe for concurrent use.
type Verifier struct {
	serverID string
	key      []byte
	now      func() time.Time

	mu   sync.Mutex
	seen map[string]time.Time // nonce -> directive exp (for eviction)
}

func NewVerifier(serverID string, key []byte, now func() time.Time) *Verifier {
	if now == nil {
		now = time.Now
	}
	return &Verifier{serverID: serverID, key: key, now: now, seen: map[string]time.Time{}}
}

// Verify checks the header's signature and fields against the expected resource
// and target, then records the nonce to block replays. The returned directive's
// Mode is authoritative for ro/rw.
func (v *Verifier) Verify(header, wantResource, wantTarget string) (shared.Directive, error) {
	var zero shared.Directive
	p, sigPart, ok := strings.Cut(header, ".")
	if !ok || p == "" || sigPart == "" {
		return zero, ErrMalformed
	}
	enc := base64.RawURLEncoding
	payload, err := enc.DecodeString(p)
	if err != nil {
		return zero, ErrMalformed
	}
	sig, err := enc.DecodeString(sigPart)
	if err != nil {
		return zero, ErrMalformed
	}
	if !hmac.Equal(sig, mac(v.key, payload)) {
		return zero, ErrBadSignature
	}
	var d shared.Directive
	if err := json.Unmarshal(payload, &d); err != nil {
		return zero, ErrMalformed
	}
	if d.ServerID != v.serverID {
		return zero, ErrServerMismatch
	}
	if d.Resource != wantResource {
		return zero, ErrResourceMismatch
	}
	if d.Target != wantTarget {
		return zero, ErrTargetMismatch
	}
	if d.Mode != "ro" && d.Mode != "rw" {
		return zero, ErrBadMode
	}
	exp, err := time.Parse(time.RFC3339, d.Exp)
	if err != nil {
		return zero, ErrMalformed
	}
	now := v.now()
	if !now.Before(exp) { // now >= exp
		return zero, ErrExpired
	}
	if exp.After(now.Add(maxLifetime)) {
		return zero, ErrExpired // far-future exp is treated as invalid
	}
	if err := v.recordNonce(d.Nonce, exp, now); err != nil {
		return zero, err
	}
	return d, nil
}

// recordNonce evicts expired nonces, then rejects a nonce already seen within its
// validity window and otherwise records it until its directive's exp.
func (v *Verifier) recordNonce(nonce string, exp, now time.Time) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	for n, e := range v.seen {
		if !now.Before(e) {
			delete(v.seen, n)
		}
	}
	if _, dup := v.seen[nonce]; dup {
		return ErrReplay
	}
	v.seen[nonce] = exp
	return nil
}

var _ = fmt.Sprintf // keep fmt import available for future error context
```

> Note: drop the `fmt` import + the `var _` line if `fmt` ends up unused; it is listed only so a partial edit does not break the build. Prefer removing both.

- [ ] **Step 4: Run to verify it passes**

Run: `cd agent && go test ./internal/directive/ -v`
Expected: PASS (all cases).

- [ ] **Step 5: Commit**

```bash
git add agent/internal/directive/
git commit -m "feat(m2): agent-side HMAC directive Sign + Verifier (expiry, resource/target/server match, nonce replay)"
```

---

## Task B: tmux socket support + pane→session resolution + scrollback capture

**Files:**
- Modify: `agent/internal/tmux/control.go`
- Modify: `agent/internal/tmux/control_lifecycle_test.go` (signature update only)
- Create: `agent/internal/tmux/pane.go`
- Test: `agent/internal/tmux/pane_test.go`, `agent/internal/tmux/pane_integration_test.go`

**Interfaces:**
- Consumes: `Runner`, `splitFields` (from escape.go), `socketArgs`, `with`, `nonEmptyLines`, `fieldSep`, `trimNL` (all in package `tmux`); `paneIDRe` (control.go).
- Produces:
  - `func NewControlClient(ctx context.Context, socket, session, pane string) (*ControlClient, error)` — **signature gains `socket`** (first after ctx). `socket==""` → default tmux socket.
  - `func (c *ControlClient) OutputChan() <-chan []byte` and `func (c *ControlClient) DoneChan() <-chan struct{}` — accessors so `*ControlClient` satisfies `api.PaneConn`.
  - `func ResolvePaneSession(ctx context.Context, run Runner, socket, paneID string) (sessionID string, ok bool, err error)`
  - `func CapturePane(ctx context.Context, socket, pane string, lines int) ([]byte, error)`
  - `func TuneSession(ctx context.Context, socket, sessionID string)` — best-effort window-size/aggressive-resize tuning.

- [ ] **Step 1: Write the failing tests (`pane_test.go`)**

```go
package tmux

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestResolvePaneSessionFindsOwningSession(t *testing.T) {
	run := func(ctx context.Context, args ...string) ([]byte, error) {
		if !contains(args, "list-panes") || !contains(args, "-a") {
			t.Fatalf("expected list-panes -a, got %v", args)
		}
		// pane_id <delim> session_id, faithful tmux form (token delimiter).
		return []byte(p("%0", "$0") + "\n" + p("%3", "$1") + "\n" + p("%4", "$1") + "\n"), nil
	}
	sid, ok, err := ResolvePaneSession(context.Background(), run, "", "%3")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if sid != "$1" {
		t.Fatalf("session = %q, want $1", sid)
	}
}

func TestResolvePaneSessionMissIsNotFound(t *testing.T) {
	run := func(ctx context.Context, args ...string) ([]byte, error) {
		return []byte(p("%0", "$0") + "\n"), nil
	}
	_, ok, err := ResolvePaneSession(context.Background(), run, "", "%9")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("want ok=false for an unknown pane")
	}
}

func TestResolvePaneSessionThreadsSocket(t *testing.T) {
	var sawSocket bool
	run := func(ctx context.Context, args ...string) ([]byte, error) {
		if contains(args, "-L") && argAfter(args, "-L") == "devsock" {
			sawSocket = true
		}
		return []byte(p("%1", "$0") + "\n"), nil
	}
	_, _, _ = ResolvePaneSession(context.Background(), run, "devsock", "%1")
	if !sawSocket {
		t.Fatal("expected -L devsock in tmux args")
	}
}

func TestResolvePaneSessionPropagatesRunnerError(t *testing.T) {
	run := func(ctx context.Context, args ...string) ([]byte, error) {
		return nil, errors.New("tmux exploded")
	}
	if _, _, err := ResolvePaneSession(context.Background(), run, "", "%1"); err == nil {
		t.Fatal("want error when the runner fails")
	}
}

func TestCaptureArgsConvertLFtoCRLF(t *testing.T) {
	// captureToCRLF is the pure post-processing helper CapturePane uses.
	got := captureToCRLF([]byte("a\nb\n"))
	if string(got) != "a\r\nb\r\n" {
		t.Fatalf("got %q, want CRLF-translated", got)
	}
	if strings.Contains(strings.ReplaceAll(string(got), "\r\n", ""), "\n") {
		t.Fatal("a bare LF survived translation")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd agent && go test ./internal/tmux/ -run 'ResolvePaneSession|Capture'`
Expected: build failure — `undefined: ResolvePaneSession`, `undefined: captureToCRLF`.

- [ ] **Step 3a: Create `pane.go`**

```go
package tmux

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

// ResolvePaneSession returns the tmux session id that owns paneID on the given
// socket, or ok=false if no such pane exists. It is authoritative: it lists every
// pane and looks the id up, because `display-message -t <bogus>` returns an empty
// string with exit 0 (it falls back to a stray session) rather than failing.
func ResolvePaneSession(ctx context.Context, run Runner, socket, paneID string) (string, bool, error) {
	base := socketArgs(socket)
	out, err := run(ctx, with(base, "list-panes", "-a", "-F", "#{pane_id}"+fieldSep+"#{session_id}")...)
	if err != nil {
		return "", false, err
	}
	for _, line := range nonEmptyLines(out) {
		f, err := splitFields(line, 2)
		if err != nil {
			return "", false, err
		}
		if f[0] == paneID {
			return f[1], true, nil
		}
	}
	return "", false, nil
}

// CapturePane returns the pane's scrollback as a snapshot to bootstrap a new
// viewer: -e keeps colour escapes, -S -<lines> reaches back, and bare LFs become
// CRLF for xterm. socket "" uses the default tmux socket. (Ported from the spike.)
func CapturePane(ctx context.Context, socket, pane string, lines int) ([]byte, error) {
	if lines <= 0 {
		lines = 5000
	}
	base := socketArgs(socket)
	args := with(base, "capture-pane", "-p", "-e", "-t", pane, "-S", fmt.Sprintf("-%d", lines))
	out, err := exec.CommandContext(ctx, "tmux", args...).Output()
	if err != nil {
		return nil, err
	}
	return captureToCRLF(out), nil
}

func captureToCRLF(b []byte) []byte {
	return bytes.ReplaceAll(b, []byte("\n"), []byte("\r\n"))
}

// TuneSession makes the passive control client adopt the viewer's size on resize:
// window-size latest + aggressive-resize off, scoped to the session. Best-effort;
// errors are ignored (a tuning failure must not block a terminal). escape-time is
// NOT touched — send-keys -H is byte-exact regardless, so we avoid mutating a
// shared server's global option.
func TuneSession(ctx context.Context, socket, sessionID string) {
	base := socketArgs(socket)
	run := func(extra ...string) {
		_ = exec.CommandContext(ctx, "tmux", with(base, extra...)...).Run()
	}
	run("set-option", "-t", sessionID, "window-size", "latest")
	run("set-option", "-t", sessionID, "aggressive-resize", "off")
}
```

- [ ] **Step 3b: Modify `control.go`** — add the `socket` field, thread it, add accessors.

Add `socket string` to the `ControlClient` struct (next to `session`/`pane`). Change the constructor:

```go
func NewControlClient(ctx context.Context, socket, session, pane string) (*ControlClient, error) {
	if !paneIDRe.MatchString(pane) {
		return nil, fmt.Errorf("invalid pane id %q", pane)
	}
	args := with(socketArgs(socket), "-C", "attach-session", "-t", session)
	cmd := exec.CommandContext(ctx, "tmux", args...)
	stdin, err := cmd.StdinPipe()
	// ... (rest unchanged: stdout pipe, Stderr=io.Discard, Start, struct init incl. socket: socket, go c.readLoop)
}
```

Add accessors at the end of control.go:

```go
// OutputChan / DoneChan expose the client's channels behind read-only types so it
// satisfies api.PaneConn without the api package importing concrete fields.
func (c *ControlClient) OutputChan() <-chan []byte  { return c.Output }
func (c *ControlClient) DoneChan() <-chan struct{}  { return c.Done }
```

- [ ] **Step 3c: Fix the one existing caller** in `control_lifecycle_test.go:66`:

```go
if _, err := NewControlClient(context.Background(), "", "sess", p); err == nil {
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd agent && go test ./internal/tmux/`
Expected: PASS (unit tests; integration tests run on the dev box, skip in CI).

- [ ] **Step 4b: Add the integration test (`pane_integration_test.go`)**

```go
package tmux

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func TestResolvePaneSessionRealTmux(t *testing.T) {
	requireTmux(t)
	const sock = "agentmon-m2-pane"
	_ = exec.Command("tmux", "-L", sock, "kill-server").Run()
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", sock, "kill-server").Run() })
	if out, err := exec.Command("tmux", "-L", sock, "new-session", "-d", "-s", "s", "-x", "80", "-y", "24").CombinedOutput(); err != nil {
		t.Fatalf("new-session: %v: %s", err, out)
	}
	paneOut, _ := exec.Command("tmux", "-L", sock, "list-panes", "-F", "#{pane_id}").Output()
	pane := strings.TrimSpace(string(paneOut))

	sid, ok, err := ResolvePaneSession(context.Background(), ExecRunner, sock, pane)
	if err != nil || !ok {
		t.Fatalf("resolve: ok=%v err=%v", ok, err)
	}
	if !strings.HasPrefix(sid, "$") {
		t.Fatalf("session id = %q, want $N", sid)
	}

	snap, err := CapturePane(context.Background(), sock, pane, 100)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if strings.Contains(strings.ReplaceAll(string(snap), "\r\n", ""), "\n") {
		t.Fatal("capture output has a bare LF; want CRLF")
	}
}
```

- [ ] **Step 5: Commit**

```bash
git add agent/internal/tmux/
git commit -m "feat(m2): tmux socket support + pane->session resolution + scrollback capture"
```

---

## Task C: agent terminal WS handler + main wiring

**Files:**
- Modify: `agent/go.mod`, `agent/go.sum` (add gorilla/websocket)
- Create: `agent/internal/api/ws.go`
- Test: `agent/internal/api/ws_test.go`
- Modify: `agent/cmd/agentmon-agent/main.go`

**Interfaces:**
- Consumes: `config.Config` + `config.ResolveTarget` (exists); `directive.Verifier` + `directive.Sign` (Task A); `tmux.Runner`, `tmux.ResolvePaneSession`, `tmux.CapturePane`, `tmux.TuneSession`, `tmux.NewControlClient` (Task B); `shared.PaneID`, `shared.ResizeFrame` (exist); `RequireBearer`, `writeJSONError` (exist).
- Produces:
  - `type PaneConn interface { OutputChan() <-chan []byte; DoneChan() <-chan struct{}; SendInput([]byte) error; Resize(cols, rows int) error; Close() }`
  - `type PaneIO struct { Cfg config.Config; Verifier *directive.Verifier; Run tmux.Runner; Capture func(ctx context.Context, socket, pane string, lines int) ([]byte, error); NewClient func(ctx context.Context, socket, session, pane string) (PaneConn, error); Tune func(ctx context.Context, socket, session string) }`
  - `func (h *PaneIO) Handler() http.HandlerFunc`

- [ ] **Step 1: Add the dependency**

Run: `cd agent && go get github.com/gorilla/websocket@v1.5.3`
Then open `agent/go.mod` and ensure the require reads `github.com/gorilla/websocket v1.5.3` (no `// indirect`). Do NOT run `go mod tidy`.

- [ ] **Step 2: Write the failing tests (`ws_test.go`)**

```go
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
func buildHandler(t *testing.T, fake *fakePane, mode string) http.Handler {
	t.Helper()
	cfg := testTarget()
	h := &PaneIO{
		Cfg:      cfg,
		Verifier: directive.NewVerifier("server-a", []byte(wsKey), nil),
		Run: func(ctx context.Context, args ...string) ([]byte, error) {
			// list-panes -a: pane %3 lives in session $1.
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
	srv := httptest.NewServer(buildHandler(t, fake, "rw"))
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
	srv := httptest.NewServer(buildHandler(t, fake, "rw"))
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
	srv := httptest.NewServer(buildHandler(t, fake, "ro"))
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
	srv := httptest.NewServer(buildHandler(t, fake, "rw"))
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
	srv := httptest.NewServer(buildHandler(t, fake, "rw"))
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
	srv := httptest.NewServer(buildHandler(t, fake, "rw"))
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
```

- [ ] **Step 3: Run to verify it fails**

Run: `cd agent && go test ./internal/api/ -run 'PaneWS'`
Expected: build failure — `undefined: PaneIO`, `undefined: PaneConn`.

- [ ] **Step 4: Implement `ws.go`**

```go
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

		conn, err := h.upgrader().Upgrade(w, r, nil)
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

		// 1) scrollback bootstrap before any live output.
		if snap, err := h.Capture(ctx, target.SocketName, paneID, h.Cfg.ScrollbackLines); err == nil && len(snap) > 0 {
			_ = conn.WriteMessage(websocket.BinaryMessage, snap)
		}

		// 2) sole writer: live output + keepalive pings.
		go writePump(ctx, cancel, conn, cc)

		// 3) reader: binary input (rw only) + JSON control (resize).
		readPump(cancel, conn, cc, dir.Mode)
	}
}

func writePump(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, cc PaneConn) {
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
```

- [ ] **Step 5: Run to verify it passes**

Run: `cd agent && go test ./internal/api/ -run 'PaneWS'`
Expected: PASS (all WS cases).

- [ ] **Step 6: Wire `main.go`**

Add the directive_key requirement after the hub_token check:

```go
	if cfg.DirectiveKey == "" {
		log.Fatal("config: directive_key is required")
	}
```

Build the handler deps and register the route (alongside the existing `/sessions` line):

```go
	paneIO := &api.PaneIO{
		Cfg:      cfg,
		Verifier: directive.NewVerifier(cfg.ServerID, []byte(cfg.DirectiveKey), nil),
		Run:      tmux.ExecRunner,
		Capture:  tmux.CapturePane,
		NewClient: func(ctx context.Context, socket, session, pane string) (api.PaneConn, error) {
			return tmux.NewControlClient(ctx, socket, session, pane)
		},
		Tune: tmux.TuneSession,
	}
	mux.Handle("GET /panes/{paneId}/io", api.RequireBearer(cfg.HubToken, paneIO.Handler()))
```

Add the `directive` import: `"agentmon/agent/internal/directive"`.

- [ ] **Step 7: Build, vet, full agent test**

Run: `cd agent && go vet ./... && go test ./... && CGO_ENABLED=0 go build ./...`
Expected: all green; static build ok.

- [ ] **Step 8: Commit**

```bash
git add agent/
git commit -m "feat(m2): agent terminal WS /panes/{id}/io — directive gate, scrollback, binary bridge, ro/rw"
```

---

## Whole-milestone verification (after Task C)

Real-tmux end-to-end smoke (dev box; the agent's done-when, spec §8 M2). A small smoke client (Go, under a `//go:build ignore` file or a temporary `_test.go` driver) that:
1. Starts an agent against a `tmux -L` socket session running a shell, with a known `hub_token` + `directive_key`.
2. Mints a valid `rw` directive with `directive.Sign`, dials `WS /panes/{paneId}/io?target=default&mode=rw` with bearer+directive.
3. Asserts the first frame is the scrollback snapshot; sends `printf PROBE_$$\r`; asserts a later `%output` frame contains the probe string (input reached the shell).
4. Repeats with: expired directive → 403; replayed nonce → 403; forged signature → 403; resource-mismatched (wrong pane) → 403; `mode=ro` → input dropped (shell never sees the probe).

Record the smoke result + commands in `.superpowers/sdd/progress.md`. CI runs only the pure unit suite (directive, ws framing, tmux decode); tmux/WS integration `t.Skip` without tmux.

## Review gate (after verification)

1. Opus whole-branch review → fix Critical/Important, record Minors in the ledger.
2. `superpowers:requesting-code-review` then `/multi-review --codex` (code-simplifier + deep-scan + feature-dev:code-reviewer + Codex) on the M2 code → fix all but nitpicks.
3. Finish: merge `phase-2-m2` → main, push. Update `agentmon.md` memory, write `docs/superpowers/m2-carryover.md` for M3, leave the MORNING REPORT.

---

## Self-review notes (coverage check vs spec §6.2/§6.3/§8/§10)

- §6.2 binary both directions + scrollback-first + JSON resize control: Task C (scrollback frame, binary pump, resize). Keys-as-raw-bytes: inherent to the binary path.
- §6.3 directive (b64url payload.HMAC), short expiry, resource match, replayed nonce, mechanical-mode-only: Task A (verify) + Task C (resource = `pane:<server>/<target>/<paneId>`, mode from directive).
- §8 M2 done-when (probe reaches pane; expired/replayed/forged/resource-mismatch rejected; ro drops): whole-milestone verification.
- §10 security: bearer still required (middleware), directive second factor (Task A), agent enforces only mechanical ro/rw (Task C), no keystroke logging (only metadata logged), pane id validated.
- Pre-task (robust tmux -F decode): DONE before this plan (commit c7a367d).
```
