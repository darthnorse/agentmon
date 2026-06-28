# AgentMon — M4: WS terminal relay + HMAC directive minting + audit `terminal.open`

*Design spec. **M4** of [Phase 1](2026-06-27-agentmon-phase-1-design.md) — the second half of the hub,
built on top of [M3 (auth + registry + REST)](2026-06-27-agentmon-m3-hub-auth-registry-rest.md) and the
[Agent Onboarding](2026-06-28-agentmon-agent-onboarding-design.md) milestone. The **agent** already
serves the terminal WebSocket and verifies directives ([M2](2026-06-27-agentmon-phase-1-m2-agent-ws-directive.md));
this milestone is **entirely hub-side**: the hub now MINTS the directive and RELAYS the browser↔agent
WebSocket.*

Date: 2026-06-28

---

## 1. Purpose & scope

Complete the product spine: a browser opens a live terminal pane on any registered server **through the
hub**. The hub authenticates the browser (M3 cookie), authorizes the action, **mints a short-lived
HMAC-signed access directive** with the per-server signing key, dials the agent's terminal WebSocket
carrying that directive, and **relays frames transparently** in both directions. Every accepted
connection is audited as `terminal.open`.

```text
browser ──WS──▶ hub ──WS(Bearer + X-AgentMon-Directive)──▶ agent ──▶ tmux pane
        ◀──────     ◀──────────────────────────────────────     ◀──────
```

After M4, Phase-1 deliverable "browser opens a pane from either server via hub relay" and "audit records
terminal open" are met (spec §17 Phase 1).

### 1.1 In scope

- **`shared.SignDirective`** — promote the HMAC sign primitive into `shared` so the hub can mint without
  importing an agent-internal package. The agent keeps its stateful `Verifier`.
- **Hub directive `Minter`** — builds + signs a `shared.Directive` (mode `rw`, ~60s `Exp`, unique nonce)
  using the per-server `db.Server.SigningKey` read live from the registry.
- **WS terminal relay** — `GET /api/v1/servers/{id}/panes/{paneId}/io`: authenticate, authorize, mint,
  dial the agent, upgrade the browser, pump frames both directions with proactive liveness.
- **Audit `terminal.open`** on every accepted relay (deny path already audited by the chokepoint).
- Two **carried M3 minors** that M4 touches: generalize `SessionDetailHandler`'s hardcoded target; add an
  `/api/` 404 guard so unknown API paths don't fall through to the SPA.

### 1.2 Out of scope (deferred, with target)

| Deferred item | Target | Notes |
|---|---|---|
| Per-principal / per-server **read-only policy** (mint `ro`) | later | P1 authz allows everything and **always mints `rw`** (locked decision §2). The agent already enforces `ro` mechanically, so the lock is a one-line policy change when real authz lands. |
| Browser terminal UI (xterm.js, key bar, mobile view) | **M5** | The web app is all stubs today; M4 defines the wire contract the SPA will build to (transparent binary + JSON-resize). |
| Backpressure / slow-client disconnect policy, PTY fallback, resume robustness | **Phase 5** | M4 uses per-message write deadlines (a stuck write tears the connection down); richer policy is hardening. |
| `{t:"state"}` / `{t:"reconnect"}` hub→client frames | **Phase 3 / 5** | State comes from hooks (Phase 3); M4 relays terminal I/O only. |
| `POST /sessions` (create session) | later | Not part of the relay. |

---

## 2. Locked decisions (settled in brainstorming)

1. **Always mint `rw`.** The public route operates read-write; the hub dials the agent with `mode=rw` and
   a directive whose `Mode` is `rw`. There is no per-principal RW policy in P1 (authz allows all), so a
   requested `ro` mode is **not** honored yet. *Consequence for this host:* the relay's only live mode
   drives `send-keys`, so all live verification is pinned to the dedicated `agentmon` socket demo panes
   (never the default socket — see §8 safety).
2. **Promote `Sign` to `shared`.** `shared.SignDirective(key, d)` + `shared.DirectiveMAC(key, payload)`
   become the single canonical sign/HMAC implementation (the canonical byte form `CanonicalJSON` already
   lives in `shared`). The agent's `directive.Sign` delegates to it and `Verify` uses `shared.DirectiveMAC`;
   no behavior change, locked by a cross-module round-trip test. The hub imports only `shared` to mint.
3. **Transparent passthrough wire protocol.** The browser speaks the agent's existing protocol — **binary
   frames** for terminal output/input, a **JSON `{type:"resize",cols,rows}`** control frame, plus the
   binary scrollback snapshot and WS close frames. The hub copies frames through **untouched** (no
   translation layer). The spec §8.3 `{t:…}` envelope was explicitly "suggested"; the codebase committed to
   binary at the agent in M2, and the relay stays a dumb pipe.

---

## 3. Architecture

### 3.1 Where the code lives

| Unit | Location | Responsibility |
|---|---|---|
| `SignDirective`, `DirectiveMAC` | `shared/directive.go` (extend) | Canonical HMAC sign + mac, shared by hub mint and agent verify. |
| `Sign` (wrapper), `Verify` (uses shared mac) | `agent/internal/directive/directive.go` (edit) | Agent keeps the stateful `Verifier` + nonce cache; no behavior change. |
| `Minter` | `hubd/internal/directive/mint.go` (new) | Build + sign a `shared.Directive` for a (server, principal, pane, target). Clock/nonce/id seams for tests. |
| `PaneRelayHandler` + relay pump | `hubd/internal/api/ws.go` (new) | Authn/authz/mint/dial/upgrade/relay; origin check; liveness. |
| `TerminalOpen` | `hubd/internal/audit/audit.go` (extend) | `terminal.open` audit row. |
| Route + `/api/` 404 guard | `hubd/internal/api/router.go` (edit) | Register the relay route; JSON 404 for unknown API paths. |
| `SessionDetailHandler` target | `hubd/internal/api/sessions.go` (edit) | Read `?target=` instead of hardcoding `"default"`. |
| Deps wiring | `hubd/cmd/agentmon-hubd/main.go` (edit) | Inject `Minter`, `ExternalOrigin`, seams into `api.Deps`. |
| Dependency | `hubd/go.mod` (edit) | Add `github.com/gorilla/websocket` (latest, aligned with the agent). |

### 3.2 `shared.SignDirective`

```go
// shared/directive.go
func DirectiveMAC(key, payload []byte) []byte            // hmac-sha256(key, payload)
func SignDirective(key []byte, d Directive) (string, error) // b64url(payload) + "." + b64url(mac)
```

- Wire form is **`base64.RawURLEncoding(payload) + "." + base64.RawURLEncoding(mac)`** — exactly what the
  agent's `Verifier.Verify` already parses.
- `agent/internal/directive`: `Sign` → `return shared.SignDirective(key, d)`; `Verify` replaces its local
  `mac(...)` with `shared.DirectiveMAC(...)`. The `Verifier` struct, nonce cache, `maxLifetime` cap, and
  all error semantics are unchanged.

### 3.3 `Minter`

```go
// hubd/internal/directive/mint.go
type Minter struct {
    Now          func() time.Time // default time.Now
    NewNonce     func() string    // default 32-byte CSPRNG, base64url
    NewRequestID func() string    // default CSPRNG (or uuid)
}

// Mint returns the X-AgentMon-Directive header value and the request id.
func (m Minter) Mint(srv db.Server, p authz.Principal, paneID, target string) (header, requestID string, err error)
```

The minted `shared.Directive`:

| Field | Value |
|---|---|
| `ServerID` | `srv.ID` |
| `Target` | `target` (resolved, default `"default"`) |
| `Resource` | `shared.PaneID(srv.ID, target, paneID)` → e.g. `pane:aigallery/default/%3` |
| `Mode` | `"rw"` (always, P1) |
| `PrincipalID` | `p.ID` |
| `Action` | `"terminal.write"` |
| `Exp` | `Now().Add(60s).UTC().Format(time.RFC3339)` — **RFC3339, not RFC3339Nano** |
| `Nonce` | unique, non-empty CSPRNG |
| `RequestID` | unique CSPRNG |

Signed with `[]byte(srv.SigningKey)` via `shared.SignDirective`. The 60s expiry sits well under the
agent's 5-minute `maxLifetime` cap.

### 3.4 `PaneRelayHandler`

Route: **`GET /api/v1/servers/{id}/panes/{paneId}/io?target=<t>`**, wrapped in `Auth.RequireAuth`
(cookie → principal in context; no CSRF-token check fires on a GET upgrade — see §6 origin).

Request flow:

1. **Validate pane id** against `^%[0-9]+$` (the agent re-validates authoritatively). 400 on bad id.
2. **Resolve target** — `?target=` or default `"default"`.
3. **Authorize** — `d.authorizeOr403(w, r, authz.TerminalWrite, shared.PaneID(id, target, paneID))`.
   Deny is audited + 403 by the existing chokepoint. (We mint `rw`, so authorize the write action.)
4. **Origin check** — `authn.CheckOrigin(r, externalOrigin)` is enforced **before** any mint/dial so a
   cross-origin attempt returns a clean **403** without dialing the agent (a wasted upstream connection).
   The upgrader's own `CheckOrigin` (§6) enforces the same predicate at upgrade time as belt-and-suspenders.
5. **Look up the server** — `Reg.Get(ctx, id)`; 404 if missing/inactive. Yields URL + Bearer + SigningKey
   read live from the DB-backed registry.
6. **Mint** the directive (`Minter.Mint`).
7. **Dial the agent FIRST** (before upgrading the browser, so a failure is a clean HTTP error, not an
   upgrade-then-immediately-close): build `ws(s)://<host>/panes/<url.PathEscape(paneID)>/io?target=…&mode=rw`
   (scheme `http→ws`, `https→wss` from `srv.URL`), with headers `Authorization: Bearer <bearer>`,
   `X-AgentMon-Directive: <minted>`, `X-AgentMon-Request-Id: <reqID>`. Dial failure → **502**
   `agent unavailable` (mirrors the REST path), logged server-side with the id + cause.
8. **Upgrade the browser** connection (gorilla `Upgrader.Upgrade`). On failure, close the agent conn.
9. **Audit `terminal.open`** (connection accepted) and `Reg.TouchLastSeen(ctx, id)`.
10. **Relay pump** (§3.5).

### 3.5 Transparent relay pump

Two goroutines copy WebSocket messages, **type-preserved**, sharing one `context.CancelFunc`:

- **browser → agent:** `ReadMessage` from the browser, `WriteMessage` to the agent (binary input + JSON
  resize pass through; rw → all forwarded).
- **agent → browser:** `ReadMessage` from the agent, `WriteMessage` to the browser (binary output, the
  binary scrollback snapshot, JSON, and close frames pass through).

Teardown mirrors the agent's `writePump`/`readPump` discipline: the first side to error/close calls
`cancel()` and the deferred `Close()` on both conns unblocks the other goroutine's blocked `ReadMessage`
— no leaked goroutine and no orphaned agent connection (which would orphan the agent's tmux control
subprocess).

**Liveness & deadlines (proactive, pong-based):**

- **Browser conn:** `SetReadDeadline(pongWait)`, a `SetPongHandler` that pushes the deadline forward, and
  a ping ticker with `pingPeriod < pongWait`. A phone that sleeps without a close stops ponging → the
  deadline fires → teardown.
- **Agent conn:** the agent already pings the hub (~20s); the hub sets a read deadline on the agent conn
  and bumps it in a ping handler (which also sends the pong). Optionally the hub pings the agent too.
- **Writes:** `SetWriteDeadline` per `WriteMessage` on **both** conns. **No global `WriteTimeout`** (the
  hub server already omits it — a long-lived terminal WS must not be killed by a global write deadline).

Tunables (named constants, documented): `pongWait` ≈ 60s, `pingPeriod` ≈ 20s (< pongWait), per-message
write deadline ≈ 10s, dial handshake timeout ≈ 10s, read size limit ≈ 1 MiB (matches the agent).

### 3.6 Audit

```go
func (r *Recorder) TerminalOpen(ctx, principalID, resource, mode, ip, ua string)
// action "terminal.open", result "allow", resource = pane:…, meta = mode (+ requestId)
```

Recorded once per accepted relay. Never logs keystrokes (spec §13.5). The deny path (`terminal.write`
denied) is already covered by `authorizeOr403` → `Audit.Deny`.

---

## 4. Carried M3 minors folded in

- **`SessionDetailHandler` target** (`sessions.go`): replace the hardcoded `shared.SessionID(id,"default",name)`
  and `Agent.Sessions(ctx,srv,"")` with the request's `?target=` (default `"default"`), threaded into both
  the authz resource and the agent query, so sessions on non-default targets are addressable.
- **`/api/` 404 guard** (`router.go`): `mux.Handle("/api/", jsonNotFound())` returns `{"error":"not found"}`
  with 404 for any unmatched `/api/...` path/method instead of serving SPA HTML. Go 1.22+ `ServeMux`
  longest-prefix matching keeps registered `/api/v1/...` routes (and its automatic 405s) winning; only
  genuinely unknown API paths fall to the guard. The SPA catch-all `mux.Handle("/", WebUI)` is unchanged.

---

## 5. API surface (added/changed)

```text
GET /api/v1/servers/{id}/panes/{paneId}/io?target=<t>     [auth] WS upgrade → terminal relay
GET /api/v1/servers/{id}/sessions/{name}?target=<t>       [auth] now honors ?target= (was "default")
ANY /api/v1/<unknown>                                      → 404 JSON (was SPA HTML)
```

Hub → agent (unchanged contract, now exercised by the hub): `GET /panes/{paneId}/io?target=&mode=rw` with
`Authorization: Bearer`, `X-AgentMon-Directive`, `X-AgentMon-Request-Id`.

Browser ↔ hub WS frames (transparent passthrough, = agent protocol):

```jsonc
// browser → hub          binary frame            = keystrokes (raw bytes)
// browser → hub          {"type":"resize","cols":88,"rows":26}   (text/JSON)
// hub → browser          binary frame            = scrollback snapshot, then live output
// hub → browser          close frame             = tmux gone / teardown
```

---

## 6. Security

- **Authn:** the WS upgrade is wrapped in `RequireAuth`; an unauthenticated upgrade → 401 before any agent
  dial. Browsers send the session cookie automatically on same-origin WS.
- **WS CSRF defense = Origin check.** A GET upgrade does not carry the `X-CSRF-Token` header (the
  synchronizer token defends mutating REST verbs), so cross-origin WS is blocked by enforcing
  `authn.CheckOrigin(r, externalOrigin)`. It is checked **explicitly before mint/dial** (clean 403, no
  wasted agent dial) and again inside the upgrader's `CheckOrigin` at upgrade time. Browsers always send
  `Origin` on WS handshakes; a present `Origin ≠ externalOrigin` fails. (`SameSite=Lax` on the cookie is
  the companion control.) `externalOrigin` is threaded into the relay deps.
- **Authz chokepoint:** `authorizeOr403` runs before mint/dial; denies are audited.
- **Directive secrecy:** the browser never receives the agent bearer or the directive. The directive is
  minted server-side, lives only on the hub→agent hop, expires in ~60s, and carries a unique nonce
  (agent rejects replays). The signing key is read from the DB per connection and never logged.
- **Pane id injection:** validated at the hub (`^%[0-9]+$`) and again at the agent before it reaches
  `send-keys`; `url.PathEscape` keeps a `%`-prefixed id intact across the hub→agent path.
- **No keystroke logging** (spec §13.5): `terminal.open` records principal/resource/mode/ip/ua only.

---

## 7. Testing strategy (TDD)

- **`shared` round-trip:** `SignDirective` in shared → `agent/directive.Verifier.Verify` accepts; a
  tampered payload or wrong key → `ErrBadSignature`. Proves the promoted primitive agrees with the agent.
- **`Minter` (each carryover gotcha test-locked):** `Exp` is RFC3339 (rejects RFC3339Nano), `Exp` ≈
  now+60s and ≤ the agent cap, `Mode == "rw"`, `Resource == PaneID(...)`, non-empty + unique nonce and
  requestID across calls, signs verifiably with the per-server key (a different key fails verify).
- **Relay (httptest end-to-end):** a real gorilla "browser" client ↔ the hub relay (httptest) ↔ a **fake
  agent WS** (httptest) that verifies the minted directive with the shared signing key. Asserts:
  - bytes relay both directions; binary input/output, JSON resize, and the binary snapshot pass through;
  - the hub dials the agent with the right headers (Bearer, a directive that verifies, `X-AgentMon-Request-Id`,
    `url.PathEscape`'d pane, `mode=rw`, `target`);
  - **401** unauth (no cookie), **404** unknown/inactive server, **502** agent dial failure (URL → closed
    port), **403** Origin mismatch;
  - `terminal.open` audited on accept; `terminal.write` deny audited on authz fail;
  - closing either side tears down the other (fake agent observes the close; no goroutine leak).
- **router:** unknown `/api/v1/nope` → JSON 404 (not SPA HTML); the relay route is registered.
- **`SessionDetailHandler`:** `?target=foo` reflected in the agent query and the authz resource.
- **Race:** `-race` on the relay package (two pump goroutines + cancel).

The end-to-end style mirrors the existing `hubd/internal/api/integration_test.go` fake-agent pattern, so
the real upgrade + real dial + real pump are exercised, with small unit tests for `Minter`/origin/error
paths.

---

## 8. Live verification & safety

`make test` (all modules, `CGO_ENABLED=0`), `go vet`, `-race`, `gofmt`, static build, and `docker build`
must stay green. Then the live confirmation:

> **CRITICAL — dev-host hazard.** This host runs the hub **and** Claude Code's own tmux session on the
> **default** socket. The relay always mints `rw`, which drives `send-keys`. A live relay test MUST target
> the dedicated **`agentmon`** socket demo panes (`demo-web`/`demo-db` on the live `aigallery` agent,
> systemd `agentmon-agent`, :8377) — **never** the default socket. A prior session tore down its own tmux
> server by injecting into the default socket. Read-only discovery is safe anywhere; only the `send-keys`
> path is dangerous.

Live steps (authenticated as hub user `patrik`, against `aigallery`):

1. Open `GET /api/v1/servers/aigallery/panes/<paneId>/io` (a `%`-pane from a demo session) as an
   authenticated browser-equivalent WS client; confirm the scrollback snapshot arrives and live output
   streams.
2. Send a benign keystroke (e.g. into `demo-web`) and confirm it lands in that pane (`agentmon` socket
   only).
3. Confirm `terminal.open` appears in `GET /api/v1/audit`; confirm an unauthenticated upgrade → 401 and a
   bad pane → 4xx.
4. Cleanup is unchanged from the onboarding milestone (the `aigallery` agent stays installed for M4).

---

## 9. Review path

TDD per task via subagent-driven-development with review checkpoints (spec + quality reviews per task;
opus review on the relay keystone). Then the full **`/multi-review --codex`** 4-lens gate — the Phase-1
hub gate lands here per the M3→M4 cadence (M3 was reviewed but the combined hub gate was deferred to M4).
Fix every Critical/Important finding (and anything that is not a pure nitpick) before merge.
