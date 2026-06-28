# AgentMon M4 — WS terminal relay + HMAC directive minting + audit `terminal.open` — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The hub mints a short-lived HMAC access directive with the per-server signing key, dials the agent's terminal WebSocket carrying it, and relays frames transparently between the browser and the agent; every accepted connection is audited as `terminal.open`.

**Architecture:** Entirely hub-side — the agent already serves `GET /panes/{paneId}/io` and verifies directives. We (1) promote the HMAC sign primitive to `shared` so the hub can mint without importing an agent-internal package, (2) add a hub `Minter`, (3) add a `PaneRelayHandler` that authenticates → authorizes → mints → dials the agent → upgrades the browser → relays frames with proactive ping/pong liveness, (4) audit `terminal.open`, and fold in two carried M3 minors.

**Tech Stack:** Go 1.26.4, `github.com/gorilla/websocket`, `modernc.org/sqlite`, Go 1.22+ `net/http.ServeMux` pattern routing, `crypto/hmac`+`crypto/sha256` for directives.

## Global Constraints

- **Go 1.26.4**; `CGO_ENABLED=0` for all builds and tests. Module set is a `go.work` over `./agent ./hubd ./shared`.
- **Always use the latest version of every dependency; verify "latest" from a live source, never from model memory** (standing user rule). When adding `gorilla/websocket` to `hubd`, resolve the latest tag live and align `agent` + `hubd` to the same version.
- **Directive `Exp` MUST be formatted `time.RFC3339`, NOT `RFC3339Nano`** — the agent verifier parses strict RFC3339 and returns `ErrMalformed` otherwise.
- **The hub always mints `Mode: "rw"`** in P1 (locked decision). The agent requires the dial's `?mode=` to equal the directive's mode → dial the agent with `mode=rw`.
- **Mint short expiries (~60s) with a unique, non-empty `Nonce`** per directive (agent caps `Exp` at 5m and rejects empty nonces).
- **URL-escape the pane id** (`url.PathEscape`) when building the agent dial URL: pane `%3` → path segment `%253`.
- **No global `WriteTimeout`** on the hub `http.Server` (already absent — keep it that way). Use **per-message write deadlines** on the WS relay instead.
- **Proactive, pong-based client liveness** on the relay (read deadlines bumped by pong/ping handlers; ping period < pong wait).
- **Transparent passthrough wire protocol** — the hub copies WS frames through untouched (binary in/out + JSON `{"type":"resize",...}`); no translation layer.
- **Never log raw keystrokes**; the `terminal.open` audit row carries principal/resource/mode/ip/ua only.
- The per-server signing key is read **live from the registry** (`db.Server.SigningKey`), never from config.
- TDD throughout; commit after every green task. Follow existing patterns in `hubd/internal/api/*_test.go` (`testDeps`, `withPrincipal`, `fakeStore`, gorilla end-to-end via `httptest.NewServer`).

Spec: `docs/superpowers/specs/2026-06-28-agentmon-m4-ws-relay-directive-minting-design.md`.

---

## File structure

| File | Create/Modify | Responsibility |
|---|---|---|
| `shared/directive.go` | Modify | Add `DirectiveMAC` + `SignDirective` (canonical HMAC sign primitive). |
| `shared/directive_test.go` | Modify | Format + mac tests for the new primitives. |
| `agent/internal/directive/directive.go` | Modify | `Sign` delegates to `shared.SignDirective`; `Verify` uses `shared.DirectiveMAC`. |
| `agent/internal/directive/directive_test.go` | Modify | Round-trip: shared-signed directive verifies in the agent. |
| `hubd/internal/directive/mint.go` | Create | `Minter` — build + sign a `shared.Directive` for (server, principal, pane, target). |
| `hubd/internal/directive/mint_test.go` | Create | Lock every minting gotcha (RFC3339, 60s, mode rw, resource, nonce, signs verifiably). |
| `hubd/internal/audit/audit.go` | Modify | Add `Recorder.TerminalOpen`. |
| `hubd/internal/audit/audit_test.go` | Modify | Assert the `terminal.open` row shape. |
| `hubd/internal/api/ws.go` | Create | `PaneRelayHandler` + agent dial URL builder + transparent relay pump + liveness. |
| `hubd/internal/api/ws_test.go` | Create | End-to-end relay (gorilla browser ↔ hub ↔ fake agent WS) + error paths + race. |
| `hubd/internal/api/servers.go` | Modify | Add `Minter` + `ExternalOrigin` fields to `Deps`. |
| `hubd/internal/api/router.go` | Modify | Register the relay route; add the `/api/` 404 guard. |
| `hubd/internal/api/router_test.go` | Modify | Relay route is auth-wrapped; unknown `/api/` → JSON 404. |
| `hubd/internal/api/sessions.go` | Modify | `SessionDetailHandler` honors `?target=` (was hardcoded `"default"`). |
| `hubd/internal/api/sessions_test.go` | Modify | `?target=` reflected into the agent query + authz resource. |
| `hubd/cmd/agentmon-hubd/main.go` | Modify | Wire `Minter{}` + `ExternalOrigin` into `api.Deps`. |
| `hubd/go.mod`, `hubd/go.sum` | Modify | Add `github.com/gorilla/websocket` (latest, aligned with agent). |

---

## Task 1: Promote the HMAC sign primitive to `shared`

**Files:**
- Modify: `shared/directive.go`
- Modify: `shared/directive_test.go`
- Modify: `agent/internal/directive/directive.go`
- Modify: `agent/internal/directive/directive_test.go`

**Interfaces:**
- Produces: `shared.DirectiveMAC(key, payload []byte) []byte`; `shared.SignDirective(key []byte, d Directive) (string, error)` — wire form `base64.RawURLEncoding(payload) + "." + base64.RawURLEncoding(mac)`.
- Consumes: existing `shared.Directive` + `Directive.CanonicalJSON()`; existing `agent/internal/directive.Verifier`.

- [ ] **Step 1: Write the failing test (shared format + agent round-trip)**

Add to `shared/directive_test.go`:

```go
func TestSignDirectiveFormatAndMAC(t *testing.T) {
	key := []byte("server-key")
	d := Directive{ServerID: "s", Target: "default", Resource: "pane:s/default/%3",
		Mode: "rw", PrincipalID: "u1", Action: "terminal.write",
		Exp: "2026-06-28T12:01:00Z", Nonce: "n1", RequestID: "r1"}
	h, err := SignDirective(key, d)
	if err != nil {
		t.Fatal(err)
	}
	p, sig, ok := strings.Cut(h, ".")
	if !ok || p == "" || sig == "" {
		t.Fatalf("want payload.sig, got %q", h)
	}
	payload, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		t.Fatalf("payload not base64url: %v", err)
	}
	sigB, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		t.Fatalf("sig not base64url: %v", err)
	}
	if !hmac.Equal(sigB, DirectiveMAC(key, payload)) {
		t.Fatal("sig is not DirectiveMAC(key, payload)")
	}
	want, _ := d.CanonicalJSON()
	if string(payload) != string(want) {
		t.Fatalf("payload != CanonicalJSON: %q vs %q", payload, want)
	}
}
```

Add the imports `"crypto/hmac"`, `"encoding/base64"`, `"strings"` to `shared/directive_test.go`.

Add to `agent/internal/directive/directive_test.go`:

```go
func TestSharedSignedDirectiveVerifies(t *testing.T) {
	key := []byte("k")
	now := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
	d := shared.Directive{ServerID: "server-a", Target: "default",
		Resource: shared.PaneID("server-a", "default", "%3"),
		Mode: "rw", PrincipalID: "u1", Action: "terminal.write",
		Exp:   now.Add(60 * time.Second).Format(time.RFC3339),
		Nonce: "n1", RequestID: "r1"}
	header, err := shared.SignDirective(key, d)
	if err != nil {
		t.Fatal(err)
	}
	v := NewVerifier("server-a", key, func() time.Time { return now })
	got, err := v.Verify(header, d.Resource, d.Target)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.Mode != "rw" || got.Nonce != "n1" {
		t.Fatalf("got %+v", got)
	}
}

func TestSharedSignWrongKeyRejected(t *testing.T) {
	now := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
	d := shared.Directive{ServerID: "server-a", Target: "default",
		Resource: shared.PaneID("server-a", "default", "%3"), Mode: "rw",
		Exp: now.Add(60 * time.Second).Format(time.RFC3339), Nonce: "n2"}
	header, _ := shared.SignDirective([]byte("KEY-A"), d)
	v := NewVerifier("server-a", []byte("KEY-B"), func() time.Time { return now })
	if _, err := v.Verify(header, d.Resource, d.Target); err != ErrBadSignature {
		t.Fatalf("want ErrBadSignature, got %v", err)
	}
}
```

Ensure `agent/internal/directive/directive_test.go` imports `"agentmon/shared"` and `"time"`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./shared/ ./agent/internal/directive/ 2>&1 | head -30`
Expected: FAIL — `undefined: SignDirective` / `undefined: DirectiveMAC`.

- [ ] **Step 3: Add the primitives to `shared/directive.go`**

Replace the imports and add the functions:

```go
package shared

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
)

// Directive is the short-lived hub→agent access grant (HMAC-signed by the hub,
// verified by the agent). See Phase 1 spec §6.3. Mode is "ro" or "rw".
type Directive struct {
	ServerID    string `json:"serverId"`
	Target      string `json:"target"`
	Resource    string `json:"resource"` // e.g. pane:server-a/default/%3
	Mode        string `json:"mode"`
	PrincipalID string `json:"principalId"`
	Action      string `json:"action"`
	Exp         string `json:"exp"`   // RFC3339
	Nonce       string `json:"nonce"`
	RequestID   string `json:"requestId"`
}

// CanonicalJSON is the exact byte sequence that gets HMAC'd. Field order is fixed
// by the struct definition; both hub (sign) and agent (verify) must use this.
func (d Directive) CanonicalJSON() ([]byte, error) { return json.Marshal(d) }

// DirectiveMAC is the single canonical HMAC-SHA256 over a directive's payload.
// Both the hub (SignDirective) and the agent (Verify) compute the tag with this
// so the mint and verify sides cannot silently diverge.
func DirectiveMAC(key, payload []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(payload)
	return h.Sum(nil)
}

// SignDirective returns the X-AgentMon-Directive header value for d:
// base64url(canonicalPayload) + "." + base64url(mac). This is the hub's mint
// primitive; the agent's Verifier parses exactly this form.
func SignDirective(key []byte, d Directive) (string, error) {
	payload, err := d.CanonicalJSON()
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	return enc.EncodeToString(payload) + "." + enc.EncodeToString(DirectiveMAC(key, payload)), nil
}
```

- [ ] **Step 4: Repoint the agent directive package to the shared primitive**

In `agent/internal/directive/directive.go`: remove the local `mac` function, make `Sign` delegate, and have `Verify` use `shared.DirectiveMAC`.

Replace the `mac` + `Sign` block:

```go
// Sign returns the X-AgentMon-Directive header value for d. It delegates to the
// shared mint primitive so the agent and hub share one canonical implementation.
func Sign(key []byte, d shared.Directive) (string, error) {
	return shared.SignDirective(key, d)
}
```

Delete the old `func mac(key, payload []byte) []byte { ... }`.

In `Verify`, change the signature-check line from:

```go
	if !hmac.Equal(sig, mac(v.key, payload)) {
```

to:

```go
	if !hmac.Equal(sig, shared.DirectiveMAC(v.key, payload)) {
```

Remove the now-unused imports `"crypto/sha256"` from `directive.go` (keep `"crypto/hmac"` — still used by `hmac.Equal`). Run `gofmt`/`goimports` discipline: the file should still import `crypto/hmac`, `encoding/base64`, `encoding/json`, `errors`, `strings`, `sync`, `time`, `agentmon/shared`.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./shared/ ./agent/internal/directive/ 2>&1 | tail -20`
Expected: PASS (including the pre-existing directive tests, unchanged behavior).

- [ ] **Step 6: Commit**

```bash
gofmt -w shared/directive.go shared/directive_test.go agent/internal/directive/directive.go agent/internal/directive/directive_test.go
git add shared/directive.go shared/directive_test.go agent/internal/directive/directive.go agent/internal/directive/directive_test.go
git commit -m "feat(directive): promote HMAC sign primitive to shared (hub mint reuse)

Adds shared.DirectiveMAC + shared.SignDirective; agent Sign delegates and
Verify uses the shared mac so mint/verify share one canonical implementation."
```

---

## Task 2: Hub directive `Minter`

**Files:**
- Create: `hubd/internal/directive/mint.go`
- Create: `hubd/internal/directive/mint_test.go`

**Interfaces:**
- Consumes: `shared.SignDirective`, `shared.PaneID`, `shared.Directive`, `db.Server` (for `ID`+`SigningKey`).
- Produces: `directive.Minter{Now func() time.Time; NewNonce func() string; NewRequestID func() string}` with `func (m Minter) Mint(srv db.Server, principalID, paneID, target string) (header, requestID string, err error)`; package const `Expiry = 60 * time.Second`.

- [ ] **Step 1: Write the failing test**

Create `hubd/internal/directive/mint_test.go`:

```go
package directive

import (
	"crypto/hmac"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agentmon/hubd/internal/db"
	"agentmon/shared"
)

func fixedMinter(now time.Time) Minter {
	n := 0
	return Minter{
		Now:          func() time.Time { return now },
		NewNonce:     func() string { n++; return "nonce-" + string(rune('0'+n)) },
		NewRequestID: func() string { return "req-1" },
	}
}

func decode(t *testing.T, header string, key []byte) shared.Directive {
	t.Helper()
	p, sig, ok := strings.Cut(header, ".")
	if !ok {
		t.Fatalf("bad header %q", header)
	}
	payload, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		t.Fatal(err)
	}
	sigB, _ := base64.RawURLEncoding.DecodeString(sig)
	if !hmac.Equal(sigB, shared.DirectiveMAC(key, payload)) {
		t.Fatal("signature does not verify with the server key")
	}
	var d shared.Directive
	if err := json.Unmarshal(payload, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestMintProducesVerifiableRWDirective(t *testing.T) {
	now := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
	srv := db.Server{ID: "aigallery", SigningKey: "sk-123"}
	header, reqID, err := fixedMinter(now).Mint(srv, "u1", "%3", "default")
	if err != nil {
		t.Fatal(err)
	}
	if reqID != "req-1" {
		t.Fatalf("reqID %q", reqID)
	}
	d := decode(t, header, []byte("sk-123"))
	if d.ServerID != "aigallery" || d.PrincipalID != "u1" {
		t.Fatalf("ids %+v", d)
	}
	if d.Mode != "rw" {
		t.Fatalf("mode %q, want rw", d.Mode)
	}
	if d.Resource != shared.PaneID("aigallery", "default", "%3") {
		t.Fatalf("resource %q", d.Resource)
	}
	if d.Target != "default" {
		t.Fatalf("target %q", d.Target)
	}
	if d.Nonce == "" {
		t.Fatal("empty nonce")
	}
	if d.RequestID != "req-1" {
		t.Fatalf("requestId %q", d.RequestID)
	}
}

func TestMintExpIsRFC3339NotNanoAnd60s(t *testing.T) {
	now := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
	header, _, _ := fixedMinter(now).Mint(db.Server{ID: "s", SigningKey: "k"}, "u1", "%0", "default")
	d := decode(t, header, []byte("k"))
	// RFC3339 (seconds precision) parses; the string must NOT carry sub-second digits.
	exp, err := time.Parse(time.RFC3339, d.Exp)
	if err != nil {
		t.Fatalf("Exp not RFC3339: %q (%v)", d.Exp, err)
	}
	if strings.Contains(d.Exp, ".") {
		t.Fatalf("Exp has sub-second precision (RFC3339Nano?): %q", d.Exp)
	}
	if got := exp.Sub(now); got != Expiry {
		t.Fatalf("Exp delta %v, want %v", got, Expiry)
	}
}

func TestMintNonceUniquePerCall() {} // placeholder name guard; real test below

func TestMintNonceIsUniquePerCall(t *testing.T) {
	now := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
	m := fixedMinter(now)
	srv := db.Server{ID: "s", SigningKey: "k"}
	h1, _, _ := m.Mint(srv, "u1", "%0", "default")
	h2, _, _ := m.Mint(srv, "u1", "%0", "default")
	if decode(t, h1, []byte("k")).Nonce == decode(t, h2, []byte("k")).Nonce {
		t.Fatal("nonce repeated across calls")
	}
}

func TestMintDefaultsGenerateNonEmptyNonceAndID(t *testing.T) {
	header, reqID, err := (Minter{}).Mint(db.Server{ID: "s", SigningKey: "k"}, "u1", "%1", "default")
	if err != nil {
		t.Fatal(err)
	}
	if reqID == "" {
		t.Fatal("default requestID empty")
	}
	// decode with the real key to confirm a default-signed directive verifies + has a nonce
	p, _, _ := strings.Cut(header, ".")
	payload, _ := base64.RawURLEncoding.DecodeString(p)
	var d shared.Directive
	_ = json.Unmarshal(payload, &d)
	if d.Nonce == "" {
		t.Fatal("default nonce empty")
	}
}
```

Delete the `TestMintNonceUniquePerCall` placeholder stub before committing — it is only here to remind you the real test is `TestMintNonceIsUniquePerCall`; do not keep an empty test.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./hubd/internal/directive/ 2>&1 | head`
Expected: FAIL — package/`Minter` undefined.

- [ ] **Step 3: Implement `hubd/internal/directive/mint.go`**

```go
// Package directive is the hub's mint side of the access directive. The hub MINTS
// (signs) a short-lived rw grant with the per-server signing key; the agent only
// verifies. The crypto + canonical form live in shared.SignDirective.
package directive

import (
	"crypto/rand"
	"encoding/base64"
	"time"

	"github.com/google/uuid"

	"agentmon/hubd/internal/db"
	"agentmon/shared"
)

// Expiry is how far ahead a minted directive's Exp sits. Short (well under the
// agent's 5m cap) because it only needs to cover connection establishment.
const Expiry = 60 * time.Second

// Minter builds and signs directives. The seams (Now/NewNonce/NewRequestID) are
// injectable so tests can pin the timestamp and nonce; production uses the wall
// clock, a CSPRNG nonce, and a uuid request id.
type Minter struct {
	Now          func() time.Time
	NewNonce     func() string
	NewRequestID func() string
}

func (m Minter) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m Minter) nonce() string {
	if m.NewNonce != nil {
		return m.NewNonce()
	}
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (m Minter) requestID() string {
	if m.NewRequestID != nil {
		return m.NewRequestID()
	}
	return uuid.NewString()
}

// Mint returns the X-AgentMon-Directive header value and the request id for an
// rw terminal grant on srv's pane. The directive is signed with srv.SigningKey.
func (m Minter) Mint(srv db.Server, principalID, paneID, target string) (header, requestID string, err error) {
	requestID = m.requestID()
	d := shared.Directive{
		ServerID:    srv.ID,
		Target:      target,
		Resource:    shared.PaneID(srv.ID, target, paneID),
		Mode:        "rw",
		PrincipalID: principalID,
		Action:      "terminal.write",
		Exp:         m.now().Add(Expiry).UTC().Format(time.RFC3339),
		Nonce:       m.nonce(),
		RequestID:   requestID,
	}
	header, err = shared.SignDirective([]byte(srv.SigningKey), d)
	return header, requestID, err
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./hubd/internal/directive/ -v 2>&1 | tail -20`
Expected: PASS for all `TestMint*`.

- [ ] **Step 5: Commit**

```bash
gofmt -w hubd/internal/directive/
git add hubd/internal/directive/
git commit -m "feat(hub): directive Minter — rw, RFC3339 60s exp, unique nonce, per-server key"
```

---

## Task 3: Audit `terminal.open`

**Files:**
- Modify: `hubd/internal/audit/audit.go`
- Modify: `hubd/internal/audit/audit_test.go`

**Interfaces:**
- Produces: `func (r *Recorder) TerminalOpen(ctx context.Context, principalID, resource, mode, ip, ua string)` — writes action `terminal.open`, result `allow`, `Meta = mode`.

- [ ] **Step 1: Write the failing test**

Add to `hubd/internal/audit/audit_test.go` (match the file's existing sink/test style; if it defines a capture sink, reuse it — otherwise add this one):

```go
func TestTerminalOpenRecorded(t *testing.T) {
	var got db.AuditEntry
	r := NewRecorder(sinkFunc(func(_ context.Context, e db.AuditEntry) error { got = e; return nil }))
	r.TerminalOpen(context.Background(), "u1", "pane:aigallery/default/%3", "rw", "10.0.0.2", "curl/8")
	if got.Action != "terminal.open" || got.Result != "allow" {
		t.Fatalf("action/result: %+v", got)
	}
	if got.PrincipalID != "u1" || got.Resource != "pane:aigallery/default/%3" {
		t.Fatalf("principal/resource: %+v", got)
	}
	if got.Meta != "rw" || got.IP != "10.0.0.2" || got.UserAgent != "curl/8" {
		t.Fatalf("meta/ip/ua: %+v", got)
	}
	if got.ID == "" {
		t.Fatal("audit id not stamped")
	}
}
```

If `hubd/internal/audit/audit_test.go` does not already define a `sinkFunc` adapter, add it once near the top of the file:

```go
type sinkFunc func(context.Context, db.AuditEntry) error

func (f sinkFunc) Append(ctx context.Context, e db.AuditEntry) error { return f(ctx, e) }
```

(Reuse whatever capture sink already exists in that file instead, to avoid a duplicate — check first.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./hubd/internal/audit/ 2>&1 | head`
Expected: FAIL — `r.TerminalOpen undefined`.

- [ ] **Step 3: Implement `TerminalOpen`**

Add to `hubd/internal/audit/audit.go` after `Deny`:

```go
func (r *Recorder) TerminalOpen(ctx context.Context, principalID, resource, mode, ip, ua string) {
	r.write(ctx, db.AuditEntry{PrincipalID: principalID, Action: "terminal.open",
		Resource: resource, Result: "allow", IP: ip, UserAgent: ua, Meta: mode})
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./hubd/internal/audit/ 2>&1 | tail`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w hubd/internal/audit/
git add hubd/internal/audit/
git commit -m "feat(audit): TerminalOpen — terminal.open (principal/resource/mode/ip/ua)"
```

---

## Task 4: WS terminal relay handler + transparent pump

**Files:**
- Modify: `hubd/go.mod`, `hubd/go.sum` (add `gorilla/websocket`)
- Modify: `hubd/internal/api/servers.go` (extend `Deps`)
- Create: `hubd/internal/api/ws.go`
- Create: `hubd/internal/api/ws_test.go`

**Interfaces:**
- Consumes: `directive.Minter` (Task 2), `Recorder.TerminalOpen` (Task 3), `registry.Registry.Get`/`TouchLastSeen`, `authn.CheckOrigin`, `authn.ClientIP`, `authn.PrincipalFrom`, `Deps.authorizeOr403`, `authz.TerminalWrite`, `shared.PaneID`.
- Produces: `func (d Deps) PaneRelayHandler() http.HandlerFunc`; `Deps` gains `Minter directive.Minter` and `ExternalOrigin string`; helper `agentWSURL(rawURL, paneID, target string) (string, error)`; `relayPanes(browser, agent *websocket.Conn)`.

- [ ] **Step 1: Add the gorilla/websocket dependency (latest, aligned with the agent)**

Resolve the latest version live and add it to `hubd`:

```bash
cd /root/agentmon/hubd
go get github.com/gorilla/websocket@latest
go mod tidy
cd /root/agentmon
```

Then check the resolved version and align the agent to the same tag if they differ:

```bash
grep gorilla/websocket hubd/go.mod agent/go.mod
# If hubd resolved a newer tag than agent, bump the agent:
#   cd agent && go get github.com/gorilla/websocket@<that-version> && go mod tidy && cd ..
```

Expected: both `go.mod` files pin the same `github.com/gorilla/websocket` version (the latest tag).

- [ ] **Step 2: Extend `Deps` (compile prerequisite for the handler)**

In `hubd/internal/api/servers.go`, add the import `"agentmon/hubd/internal/directive"` and two fields to `Deps`:

```go
// Deps holds the shared dependencies for all API handlers.
type Deps struct {
	Reg                 *registry.Registry
	Agent               *registry.Client
	Audit               *audit.Recorder
	AuditRepo           AuditReader
	HealthTimeout       time.Duration
	TrustForwardedProto bool
	Minter              directive.Minter // M4: mints hub→agent WS access directives
	ExternalOrigin      string           // M4: WS upgrade Origin check
}
```

- [ ] **Step 3: Write the failing end-to-end relay test**

Create `hubd/internal/api/ws_test.go`:

```go
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
	mu                            sync.Mutex
	auth, directive, reqID        string
	paneID, target, mode          string
	got                           [][]byte
}

func (r *dialRecord) append(b []byte) { r.mu.Lock(); r.got = append(r.got, append([]byte(nil), b...)); r.mu.Unlock() }

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
				return
			}
			rec.append(data)
			_ = c.WriteMessage(mt, data) // echo
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

	// 3) resize JSON passes through
	_ = c.WriteJSON(shared.ResizeFrame{Type: shared.FrameResize, Cols: 88, Rows: 26})
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, _ = c.ReadMessage() // echoed resize

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
	_, _, _ = c.ReadMessage() // drain SNAP
	c.Close()                 // browser goes away
	// The fake agent's ReadMessage must return (teardown propagated). Give it a moment.
	time.Sleep(200 * time.Millisecond)
	// If teardown did not propagate, the agent goroutine would still be blocked; we
	// assert indirectly by confirming a fresh dial still works (no fd/goroutine wedge).
	c2, _, err := dialBrowser(t, hub, "/api/v1/servers/aigallery/panes/%253/io?target=default", testOrigin)
	if err != nil {
		t.Fatalf("second dial after teardown: %v", err)
	}
	c2.Close()
}
```

- [ ] **Step 4: Run the test to verify it fails**

Run: `go test ./hubd/internal/api/ -run TestRelay 2>&1 | head -20`
Expected: FAIL — `d.PaneRelayHandler undefined`.

- [ ] **Step 5: Implement `hubd/internal/api/ws.go`**

```go
package api

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"github.com/gorilla/websocket"

	"agentmon/hubd/internal/authn"
	"agentmon/hubd/internal/authz"
	"agentmon/shared"
)

// hubPaneIDRe guards the pane id at the hub; the agent re-validates authoritatively
// before any send-keys. Same shape as the agent's tmux.ValidatePaneID.
var hubPaneIDRe = regexp.MustCompile(`^%[0-9]+$`)

// Relay tunables. pongWait/pingPeriod are vars so tests can shrink them. There is
// deliberately NO global server WriteTimeout for the WS route; writes use per-
// message deadlines (relayWriteWait) instead.
var (
	relayPongWait   = 60 * time.Second
	relayPingPeriod = 20 * time.Second
)

const (
	relayWriteWait   = 10 * time.Second
	relayDialTimeout = 10 * time.Second
	relayReadLimit   = 1 << 20
)

// PaneRelayHandler serves GET /api/v1/servers/{id}/panes/{paneId}/io. RequireAuth
// has already stamped the principal. It authorizes terminal.write, checks Origin,
// mints a directive with the per-server signing key, dials the agent's WS carrying
// it, upgrades the browser, audits terminal.open, and relays frames transparently.
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

		relayPanes(browser, agentConn)
	}
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

// relayPanes copies WS frames transparently in both directions until either side
// closes/errors, then tears down both conns so the peer's blocked ReadMessage
// unblocks (no leaked goroutine, no orphaned agent connection → no orphaned tmux
// control subprocess). Liveness (ping/pong + read deadlines) is added in the next
// task; this is the byte-faithful core.
func relayPanes(browser, agent *websocket.Conn) {
	browser.SetReadLimit(relayReadLimit)
	agent.SetReadLimit(relayReadLimit)

	done := make(chan struct{}, 2)
	copyFrames := func(dst, src *websocket.Conn) {
		defer func() { done <- struct{}{} }()
		for {
			mt, data, err := src.ReadMessage()
			if err != nil {
				return
			}
			_ = dst.SetWriteDeadline(time.Now().Add(relayWriteWait))
			if err := dst.WriteMessage(mt, data); err != nil {
				return
			}
		}
	}
	go copyFrames(agent, browser) // browser → agent
	go copyFrames(browser, agent) // agent → browser

	<-done            // first side finished
	_ = browser.Close() // unblock the other copy's ReadMessage
	_ = agent.Close()
	<-done // wait for the second
}
```

Note: `ws.go` imports for this task are `fmt log net/http net/url regexp time` + `github.com/gorilla/websocket` + `agentmon/hubd/internal/authn`, `agentmon/hubd/internal/authz`, `agentmon/shared`. Run `gofmt`/`go vet` to confirm no unused imports remain.

- [ ] **Step 6: Run the test to verify it passes**

Run: `go test ./hubd/internal/api/ -run TestRelay 2>&1 | tail -30`
Expected: PASS for all `TestRelay*`.

- [ ] **Step 7: Run the whole api package to confirm no regressions**

Run: `go test ./hubd/internal/api/ 2>&1 | tail`
Expected: PASS (existing handler tests unaffected by the new `Deps` fields).

- [ ] **Step 8: Commit**

```bash
gofmt -w hubd/internal/api/ servers.go 2>/dev/null; gofmt -w hubd/internal/api/
git add hubd/go.mod hubd/go.sum agent/go.mod agent/go.sum hubd/internal/api/ws.go hubd/internal/api/ws_test.go hubd/internal/api/servers.go
git commit -m "feat(hub): WS terminal relay — mint, dial agent, transparent frame relay + terminal.open audit"
```

---

## Task 5: Proactive ping/pong liveness + per-message deadlines + race test

**Files:**
- Modify: `hubd/internal/api/ws.go` (enhance `relayPanes`)
- Modify: `hubd/internal/api/ws_test.go` (liveness + race tests)

**Interfaces:**
- Consumes: `relayPanes` (Task 4), `relayPongWait`/`relayPingPeriod` vars.
- Produces: same `relayPanes` signature, now with read deadlines, pong/ping handlers, and a ping ticker.

- [ ] **Step 1: Write the failing liveness + race tests**

Add to `hubd/internal/api/ws_test.go`:

```go
// setRelayTiming shrinks the ping/pong windows for fast liveness tests and returns
// a restore func. Tests using it must NOT call t.Parallel (package-level vars).
func setRelayTiming(t *testing.T, pong, ping time.Duration) {
	t.Helper()
	op, opi := relayPongWait, relayPingPeriod
	relayPongWait, relayPingPeriod = pong, ping
	t.Cleanup(func() { relayPongWait, relayPingPeriod = op, opi })
}

func TestRelayStaysOpenWhileActiveBeyondPongWait(t *testing.T) {
	setRelayTiming(t, 200*time.Millisecond, 40*time.Millisecond)
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
	rec.mu.Lock()
	defer rec.mu.Unlock()
	var sawAlive bool
	for _, g := range rec.got {
		if string(g) == "ping-alive" {
			sawAlive = true
		}
	}
	if !sawAlive {
		t.Fatal("late message did not reach the agent — relay was not kept alive")
	}
}

func TestRelayRaceUnderConcurrentTraffic(t *testing.T) {
	setRelayTiming(t, 2*time.Second, 5*time.Millisecond) // pings interleave with writes
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
```

- [ ] **Step 2: Run to verify the liveness test fails (no deadline refresh yet)**

Run: `go test ./hubd/internal/api/ -run 'TestRelayStaysOpen|TestRelayRace' 2>&1 | tail -20`
Expected: `TestRelayStaysOpenWhileActiveBeyondPongWait` FAILS — without read-deadline handling the relay either never sets a deadline (test is weak) OR, once the deadline code is added but pongs aren't wired, the late write fails. (If the test passes trivially because no deadline is set yet, that's acceptable as a starting point — the implementation below makes the behavior real and the race test meaningful.)

- [ ] **Step 3: Add liveness to `relayPanes`**

Replace `relayPanes` in `hubd/internal/api/ws.go` with:

```go
func relayPanes(browser, agent *websocket.Conn) {
	browser.SetReadLimit(relayReadLimit)
	agent.SetReadLimit(relayReadLimit)
	armLiveness(browser)
	armLiveness(agent)

	stopPing := make(chan struct{})
	go pingLoop(stopPing, browser, agent)

	done := make(chan struct{}, 2)
	copyFrames := func(dst, src *websocket.Conn) {
		defer func() { done <- struct{}{} }()
		for {
			mt, data, err := src.ReadMessage()
			if err != nil {
				return
			}
			_ = dst.SetWriteDeadline(time.Now().Add(relayWriteWait))
			if err := dst.WriteMessage(mt, data); err != nil {
				return
			}
		}
	}
	go copyFrames(agent, browser) // browser → agent
	go copyFrames(browser, agent) // agent → browser

	<-done
	close(stopPing)
	_ = browser.Close()
	_ = agent.Close()
	<-done
}

// armLiveness sets the initial read deadline and bumps it on every pong AND on
// every ping the peer sends (the agent pings the hub; a browser may too). The ping
// handler still sends the pong, preserving default behavior.
func armLiveness(c *websocket.Conn) {
	_ = c.SetReadDeadline(time.Now().Add(relayPongWait))
	c.SetPongHandler(func(string) error {
		return c.SetReadDeadline(time.Now().Add(relayPongWait))
	})
	c.SetPingHandler(func(msg string) error {
		_ = c.SetReadDeadline(time.Now().Add(relayPongWait))
		err := c.WriteControl(websocket.PongMessage, []byte(msg), time.Now().Add(relayWriteWait))
		if err == websocket.ErrCloseSent {
			return nil
		}
		return err
	})
}

// pingLoop sends periodic pings to both conns. WriteControl is documented safe to
// call concurrently with the single-writer WriteMessage in each copy goroutine.
func pingLoop(stop <-chan struct{}, conns ...*websocket.Conn) {
	t := time.NewTicker(relayPingPeriod)
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
```

Add `"sync"` to the imports only if used elsewhere; `relayPanes` itself does not need it (the test file does). Ensure `ws.go` imports: `log net/http net/url regexp time` + `github.com/gorilla/websocket` + the three internal packages + `agentmon/shared`. Remove the temporary `var _ = sync.Mutex{}` guard line from Task 4 if it is still present.

- [ ] **Step 4: Run the liveness + race tests**

Run: `go test ./hubd/internal/api/ -run 'TestRelayStaysOpen|TestRelayRace' -race 2>&1 | tail -20`
Expected: PASS, `-race` clean.

- [ ] **Step 5: Run the whole api package with race**

Run: `go test ./hubd/internal/api/ -race 2>&1 | tail`
Expected: PASS, no data races.

- [ ] **Step 6: Commit**

```bash
gofmt -w hubd/internal/api/ws.go hubd/internal/api/ws_test.go
git add hubd/internal/api/ws.go hubd/internal/api/ws_test.go
git commit -m "feat(hub): relay liveness — pong-based read deadlines + ping ticker, per-message write deadlines"
```

---

## Task 6: Register the relay route + `/api/` 404 guard

**Files:**
- Modify: `hubd/internal/api/router.go`
- Modify: `hubd/internal/api/router_test.go`

**Interfaces:**
- Consumes: `Deps.PaneRelayHandler` (Task 4), `Auth.RequireAuth`.
- Produces: route `GET /api/v1/servers/{id}/panes/{paneId}/io`; subtree handler `/api/` → JSON 404.

- [ ] **Step 1: Write the failing tests**

Add to `hubd/internal/api/router_test.go` (mirror its existing `NewRouter` construction; reuse whatever minimal `RouterDeps` builder the file already has):

```go
func TestRouterUnknownAPIPathIsJSON404(t *testing.T) {
	h := newTestRouter(t) // existing helper in router_test.go
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/does-not-exist", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("want JSON, got %q", ct)
	}
	if !strings.Contains(w.Body.String(), `"error"`) {
		t.Fatalf("want JSON error envelope, got %s", w.Body)
	}
}

func TestRouterRelayRouteRequiresAuth(t *testing.T) {
	h := newTestRouter(t)
	w := httptest.NewRecorder()
	// A plain GET (no Upgrade, no cookie) must be rejected by RequireAuth before
	// any upgrade is attempted.
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/servers/aigallery/panes/%253/io", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("relay route must require auth, got %d", w.Code)
	}
}
```

If `router_test.go` has no `newTestRouter` helper, add a minimal one that builds `NewRouter(RouterDeps{...})` with a real `Authenticator` (empty store) and a `Deps` from `testDeps(registry.New(fakeStore{}))` plus `WebUI: http.HandlerFunc(func(w,_){w.WriteHeader(200)})`. Ensure `strings` and `net/http/httptest` are imported.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./hubd/internal/api/ -run TestRouter 2>&1 | head -20`
Expected: FAIL — unknown `/api/v1/...` returns 200 HTML (SPA catch-all); relay route not registered.

- [ ] **Step 3: Implement the route + guard in `router.go`**

In `NewRouter`, add the relay route alongside the other `/api/v1/servers/...` routes:

```go
	mux.Handle("GET /api/v1/servers/{id}/panes/{paneId}/io", rd.Auth.RequireAuth(rd.API.PaneRelayHandler()))
```

And add the `/api/` subtree guard immediately BEFORE the SPA catch-all `mux.Handle("/", rd.WebUI)`:

```go
	// Unknown /api/ paths (and wrong methods on known ones) must not fall through to
	// the SPA. Registered /api/v1/... patterns are more specific and still win; this
	// only catches genuinely unknown API paths.
	mux.Handle("/api/", apiNotFound())

	mux.Handle("/", rd.WebUI)
```

Add the helper at the bottom of `router.go`:

```go
func apiNotFound() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSONError(w, http.StatusNotFound, "not found")
	})
}
```

(`writeJSONError` already exists in the `api` package — `servers.go`.)

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./hubd/internal/api/ -run TestRouter 2>&1 | tail`
Expected: PASS.

- [ ] **Step 5: Run the full api package**

Run: `go test ./hubd/internal/api/ 2>&1 | tail`
Expected: PASS (the existing end-to-end tests still pass; the `/api/` guard does not shadow registered routes).

- [ ] **Step 6: Commit**

```bash
gofmt -w hubd/internal/api/router.go hubd/internal/api/router_test.go
git add hubd/internal/api/router.go hubd/internal/api/router_test.go
git commit -m "feat(hub): register WS relay route + /api/ 404 guard (no SPA fallthrough)"
```

---

## Task 7: Generalize `SessionDetailHandler` target

**Files:**
- Modify: `hubd/internal/api/sessions.go`
- Modify: `hubd/internal/api/sessions_test.go`

**Interfaces:**
- Consumes: `shared.SessionID`, `Deps.Agent.Sessions(ctx, srv, target)`.
- Produces: `SessionDetailHandler` reads `?target=` (default `"default"`) into both the authz resource and the agent query.

- [ ] **Step 1: Write the failing test**

Add to `hubd/internal/api/sessions_test.go` a fake agent that records the `target` query, and assert the handler forwards `?target=`:

```go
func TestSessionDetailHonorsTargetQuery(t *testing.T) {
	var gotTarget string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-a" {
			w.WriteHeader(401)
			return
		}
		gotTarget = r.URL.Query().Get("target")
		w.Write([]byte(`{"sessions":[{"name":"proj","server":"x","target":"work","cwd":"/p","command":"claude","windows":[]}]}`))
	}))
	defer ts.Close()
	d := depsWith(db.Server{ID: "server-a", URL: ts.URL, Bearer: "tok-a", Status: "active"})

	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/servers/server-a/sessions/proj?target=work", nil), authz.Principal{ID: "u1"})
	r.SetPathValue("id", "server-a")
	r.SetPathValue("name", "proj")
	w := httptest.NewRecorder()
	d.SessionDetailHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("code %d body %s", w.Code, w.Body)
	}
	if gotTarget != "work" {
		t.Fatalf("agent saw target %q, want work", gotTarget)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./hubd/internal/api/ -run TestSessionDetailHonorsTarget 2>&1 | head`
Expected: FAIL — `gotTarget` is `""` (handler hardcodes `"default"` for the resource and queries with `""`).

- [ ] **Step 3: Implement the generalization**

In `hubd/internal/api/sessions.go`, change `SessionDetailHandler`'s opening to resolve the target and use it for both the resource and the agent query:

```go
func (d Deps) SessionDetailHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		name := r.PathValue("name")
		target := r.URL.Query().Get("target")
		if target == "" {
			target = "default"
		}
		if _, ok := d.authorizeOr403(w, r, authz.SessionView, shared.SessionID(id, target, name)); !ok {
			return
		}
		srv, ok, err := d.Reg.Get(r.Context(), id)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if !ok {
			writeJSONError(w, http.StatusNotFound, "unknown server")
			return
		}
		sessions, err := d.Agent.Sessions(r.Context(), srv, target)
		if err != nil {
			log.Printf("sessions: agent %s: %v", id, err)
			writeJSONError(w, http.StatusBadGateway, "agent unavailable")
			return
		}
		for _, s := range sessions {
			if s.Name == name {
				writeJSON(w, http.StatusOK, s)
				return
			}
		}
		writeJSONError(w, http.StatusNotFound, "unknown session")
	}
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./hubd/internal/api/ -run TestSessionDetail 2>&1 | tail`
Expected: PASS (including the pre-existing `TestSessionDetailFoundAndNotFound` and `...UnknownServerIs404`, which pass no `?target=` and so default to `"default"`).

- [ ] **Step 5: Commit**

```bash
gofmt -w hubd/internal/api/sessions.go hubd/internal/api/sessions_test.go
git add hubd/internal/api/sessions.go hubd/internal/api/sessions_test.go
git commit -m "fix(hub): SessionDetailHandler honors ?target= (was hardcoded default)"
```

---

## Task 8: Wire `Minter` + `ExternalOrigin` into main, whole-milestone verification

**Files:**
- Modify: `hubd/cmd/agentmon-hubd/main.go`

**Interfaces:**
- Consumes: `directive.Minter`, `api.Deps{Minter, ExternalOrigin}` (Task 4).

- [ ] **Step 1: Wire the new Deps fields in `main.go`**

Add the import `"agentmon/hubd/internal/directive"` to `hubd/cmd/agentmon-hubd/main.go` and extend the `API: api.Deps{...}` literal:

```go
		API: api.Deps{
			Reg:                 reg,
			Agent:               registry.NewClient(10 * time.Second),
			Audit:               rec,
			AuditRepo:           database,
			HealthTimeout:       3 * time.Second,
			TrustForwardedProto: cfg.TrustForwardedProto,
			Minter:              directive.Minter{}, // defaults: time.Now, CSPRNG nonce, uuid requestId
			ExternalOrigin:      cfg.ExternalOrigin,
		},
```

- [ ] **Step 2: Build the hub and agent binaries**

Run: `CGO_ENABLED=0 go build ./... 2>&1 | tail`
Expected: no output (clean build across all three modules).

- [ ] **Step 3: Vet + gofmt check**

Run:
```bash
go vet ./... 2>&1 | tail
gofmt -l agent hubd shared
```
Expected: `go vet` clean; `gofmt -l` prints nothing (no unformatted files).

- [ ] **Step 4: Full module test suites, with race on the touched packages**

Run:
```bash
go test ./... 2>&1 | tail -40
go test -race ./hubd/internal/api/ ./hubd/internal/directive/ ./agent/internal/directive/ ./shared/ 2>&1 | tail
```
Expected: all packages PASS; `-race` clean.

- [ ] **Step 5: Confirm the Makefile build target still succeeds (embeds agents)**

Run: `make build-hub 2>&1 | tail -15` (if it requires network/embeds, ensure it completes; otherwise `CGO_ENABLED=0 go build ./hubd/...`).
Expected: hub builds.

- [ ] **Step 6: Commit**

```bash
git add hubd/cmd/agentmon-hubd/main.go
git commit -m "feat(hub): wire directive Minter + ExternalOrigin into the relay deps"
```

- [ ] **Step 7: Final milestone tag-up commit (optional, if any docs need updating)**

Update `docs/superpowers/specs/...m4...` only if an interface drifted during implementation. Otherwise proceed to multi-review.

---

## Self-Review (run after writing the plan; fix inline)

**Spec coverage:**
- §3.2 promote Sign → **Task 1**. §3.3 Minter → **Task 2**. §3.6 audit → **Task 3**. §3.4 handler (validate/authorize/origin/lookup/mint/dial/upgrade) → **Task 4**. §3.5 pump + liveness → **Tasks 4+5**. §4 carried minors → **Task 6** (`/api/` guard) + **Task 7** (target). §5 route → **Task 6**. §6 security (origin/authz/secrecy/pane-id) → **Tasks 4+6**. §7 tests → folded per task. §8 wiring → **Task 8**. ✓ No gaps.

**Placeholder scan:** The only intentional placeholder is the `TestMintNonceUniquePerCall` stub in Task 2, explicitly flagged for deletion before commit. All other steps carry real code/commands. ✓

**Type consistency:** `Minter.Mint(srv db.Server, principalID, paneID, target string) (header, requestID string, err error)` is used identically in Tasks 2, 4. `relayPanes(browser, agent *websocket.Conn)` signature is stable across Tasks 4→5. `Deps{Minter, ExternalOrigin}` introduced in Task 4, consumed in Task 8. `shared.SignDirective`/`shared.DirectiveMAC` defined in Task 1, used in Tasks 2 (mint) and the Task 4 test (`verifyMinted`). ✓

**Known gotchas covered:** RFC3339-not-Nano (Task 2 test), 60s exp (Task 2), unique non-empty nonce (Task 2), mode rw (Tasks 2/4), url.PathEscape pane id (Task 4 `agentWSURL` + the `%3`/`%253` assertions), no global WriteTimeout / per-message deadlines (Task 5 + already absent in main), pong-based liveness (Task 5), per-server signing key from registry (Tasks 2/4), dev-host send-keys safety (verification §8 — live tests pinned to the `agentmon` socket). ✓
