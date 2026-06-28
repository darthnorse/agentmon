# M4 → M5 carry-over (WS relay + HMAC directive minting + audit terminal.open)

M4 (the second half of the hub: the browser↔hub↔agent WebSocket terminal relay,
hub-side HMAC directive **minting**, and the `terminal.open` audit) is complete,
reviewed, **live-accepted on this host**, and merged. This captures the
consciously-deferred items and the M5 wiring reminders.

Branch: `phase-1-m4-ws-relay`, 14 commits off `main@3629adf`. Spec:
`docs/superpowers/specs/2026-06-28-agentmon-m4-ws-relay-directive-minting-design.md`.
Plan: `docs/superpowers/plans/2026-06-28-agentmon-m4-ws-relay-directive-minting.md`.

## Locked decisions (brainstorming)
- **Always mint `rw`** in P1 (authz allows everything; the agent already enforces `ro` mechanically, so
  the read-only lock is a one-line policy change when real authz lands — no per-principal RW policy yet).
- **Promote `Sign` to `shared`** (`shared.SignDirective` + `shared.DirectiveMAC`): one canonical HMAC
  sign/mac, so hub-mint and agent-verify cannot diverge. The hub imports only `shared` to mint; the
  agent keeps its stateful `Verifier`.
- **Transparent passthrough** wire protocol — the hub copies WS frames through untouched (binary in/out
  + JSON `{type:"resize"}` + the binary scrollback snapshot + close); no translation layer.

## What shipped (8 SDD tasks, TDD throughout)
1. **`shared.SignDirective`/`DirectiveMAC`** — agent `Sign` delegates, `Verify` uses the shared mac; pure
   internals swap, locked by a cross-module sign→verify round-trip test.
2. **`hubd/internal/directive.Minter`** — rw, `Exp` RFC3339 (not Nano) ~60s ≤ the agent's 5m cap, unique
   CSPRNG nonce, `Resource = PaneID`, signed with the per-server `db.Server.SigningKey` read **live** from
   the registry. Every gotcha test-locked.
3. **`audit.Recorder.TerminalOpen`** — `terminal.open` (principal/resource/mode/ip/ua; no keystrokes).
4. **`PaneRelayHandler`** (`hubd/internal/api/ws.go`) — `GET /api/v1/servers/{id}/panes/{paneId}/io`:
   RequireAuth → authorize `terminal.write` → Origin check (pre-dial + in the upgrader) → registry lookup
   → mint → **dial the agent first** (Bearer + `X-AgentMon-Directive` + `X-AgentMon-Request-Id`, pane
   `url.PathEscape`'d, `mode=rw`, http→ws/https→wss) → upgrade browser → audit + `TouchLastSeen` →
   transparent bidirectional frame relay. Dial failure → clean 502.
5. **Relay liveness** — pong-based read deadlines (bumped by pong AND ping handlers), a ping ticker
   (`WriteControl`, safe concurrent with the single per-conn `WriteMessage`), per-message write deadlines,
   no global `WriteTimeout`. Timing is injected via `Deps.RelayPongWait`/`RelayPingPeriod` (default
   60s/20s) — a review-driven DI refactor that removed a production test-timing mutex.
6. **Route + `/api/` 404 guard** — relay route registered (auth-wrapped); unknown `/api/...` → JSON 404
   instead of SPA HTML. (ServeMux specificity keeps real routes winning; the catch-all also turns a
   wrong-method-on-known-route into 404 rather than the auto-405 — benign, lost `Allow` header.)
7. **`SessionDetailHandler` honors `?target=`** (was hardcoded `"default"`) — threaded into both the
   authz resource and the agent query.
8. **main.go wiring** — `Minter{}` + `ExternalOrigin` into `api.Deps`.

`github.com/gorilla/websocket v1.5.3` (latest) added to hubd, aligned with the agent.

## Review path
SDD per-task (spec + quality) reviews on all 8; **opus** review on the relay keystone (Task 4) and the
liveness/DI refactor (Task 5). Then an **opus whole-branch review** ("Ready to merge with fixes") and the
full **`/multi-review --codex`** 4-lens gate (feature-dev:code-reviewer[specialist fallback] +
code-simplifier + deep-scan + Codex gpt-5.5). The security spine was independently confirmed by deep-scan
and Codex (directive never reaches the browser; CSPRNG nonces unique; RFC3339 60s exp ≤ cap; signing key
live-from-registry + never logged; Origin fail-closed pre-dial; pane-id validated + escaped; authz before
mint/dial; clean teardown no leak; minted fields line up with the agent verifier). No Critical/High.

### Fixes applied pre-merge (each test-locked unless noted)
- **Important (whole-branch): relay read limit on the trusted agent direction** (`ws.go`) — the agent
  sends the whole scrollback snapshot (up to 5000 color-escaped lines) as ONE binary message; the shared
  `1<<20` read limit on the agent conn would hit gorilla's per-message `ErrReadLimit` and collapse the
  relay (terminal fails to open) — a regression vs. the pre-M4 direct path. Fix: asymmetric limits —
  `relayBrowserReadLimit = 1<<20` (untrusted keystrokes), `relayAgentReadLimit = 32<<20` (trusted output,
  bounded vs. a runaway). +`TestRelayRelaysLargeAgentSnapshot` (2 MiB relays intact; fails under the old cap).
- **Cross-model (codex + specialist + deep-scan): nonce CSPRNG error swallowed** (`mint.go`) — `nonce()`
  ignored `crypto/rand.Read`'s error → an all-zero nonce on CSPRNG failure weakens the replay primitive.
  Fix: `nonce()` propagates the error; `Mint` fails (→500) rather than minting a degraded nonce. (The
  failure path isn't unit-testable without swapping the global `rand.Reader` — decorative test skipped per
  policy; happy-path Mint tests still cover it.) Bundled a latent test-helper bug (`fixedMinter` produced
  non-digit nonces for n≥10 → `fmt.Sprintf`).
- Review-driven test/design fixes folded into their tasks: the teardown test now blocks on a real
  agent-side close signal (race-hardened), and relay timing moved to DI (dropped the production mutex).

## LIVE acceptance — DONE this session (2026-06-28, on this host)
Verified end-to-end against the live `aigallery` agent, pinned to the **`agentmon`** socket (NEVER the
default socket — see memory [[dev-host-runs-hub-and-claude]]). Built the M4 hub, ran it on a loopback
test port against a **copy** of the live SQLite (so the running container + its DB were untouched), logged
in as `patrik`, and drove a small gorilla WS probe:
- login 200; `GET /api/v1/servers` lists `aigallery`;
- relay upgrade **101** browser↔hub↔agent to demo pane `%0` (demo-web);
- scrollback **snapshot** relayed (~100 KB);
- **rw `send-keys` path live**: injected `echo AGENTMON_M4_RELAY_OK\r` → the demo pane's shell ran it and
  the output relayed back through the full chain;
- `terminal.open` present in `GET /api/v1/audit`.
Post-test safety confirmed: Claude's default-socket session `0` still alive and attached; demo panes
intact; test hub + DB copy + throwaway probe removed; production container still up.

**Not yet live:** the browser UI itself (the web app is **all M5 stubs** — there is no xterm.js terminal,
key bar, or login form yet). The relay's wire contract is proven by the httptest end-to-end suite (real
gorilla browser ↔ hub ↔ fake agent WS) plus this live run; M5 builds the SPA against it.

## Deferred / carried (non-blocking; reviewers + I triaged as carry)
- **No per-principal relay concurrency cap** (`ws.go`) — each relay spawns a tmux control subprocess on
  the agent; an authenticated user could open many. Bounded by the Phase-1 single-user trust model; the
  spec defers backpressure/slow-client policy to **Phase 5**. Add a per-principal/per-server semaphore
  (429 when exceeded) when hardening.
- **Hub substitutes literal `"default"` target** for an empty `?target=` while the agent's
  `ResolveTarget("")` returns its *first configured* target — brittle hub/agent coupling. Benign today
  because the `aigallery` agent's only target IS labeled `default` (verified live). When multiple targets
  are modeled, either send `""` and mint from the agent-confirmed label, or enforce the `default` label.
- **Origin/CSRF rejects are not audited** — a blocked cross-origin upgrade returns 403 with no audit row
  (the deny audit only fires on authz deny). Attack is blocked; observability gap only.
- **`agentWSURL` uses only `u.Host`**, dropping any path prefix of `srv.URL` (the REST client keeps it).
  Harmless for the `scheme://host:port` LAN-agent convention; latent if an agent is ever behind a path
  prefix.
- **Close codes not relayed** to the browser (gorilla surfaces a peer close as a `*CloseError`, not a
  forwardable message) — the hub just closes the browser conn. Inherent; terminal UX is fine.
- **Pure nitpicks** (deliberately not applied, per "fix all but nitpicks"): double `authn.CheckOrigin`
  per request (kept as defense-in-depth); a `shared.ParseDirectiveHeader` to dedupe the test
  `verifyMinted`≈`decode` helpers; a `queryTarget` DRY helper; `slices.Contains` over the local `contains`;
  `pingLoop` variadic→fixed-arity; `apiNotFound` inlining; the now-thin agent `Sign` wrapper (a deliberate
  compat shim); `// M4:` comment prefixes on the new `Deps` fields.

## M5 reminders (the web SPA — the actual next milestone)
- The web app (`web/src/routes/{index,login}.tsx`) is **all M5 stubs**. M5 builds the real browser
  experience against M4's wire contract: a **login form** (POST `/api/v1/auth/login`, store the
  `csrfToken` from the body for mutating calls), a **server/session list** (`GET /api/v1/servers`,
  `/servers/{id}/sessions`), and the **xterm.js terminal** that opens the relay WS
  `GET /api/v1/servers/{id}/panes/{paneId}/io?target=` and speaks the transparent protocol: **binary**
  frames for output/input, a JSON `{type:"resize",cols,rows}` control frame; the first binary frame is the
  scrollback snapshot. The browser sends the session cookie automatically and must send an `Origin` equal
  to `external_origin` (the relay's CSRF defense).
- The **read-only lock** (spec §6.3/§11.8) lands when the SPA + real authz arrive: today the hub always
  mints `rw`; honoring a browser-requested `ro` (and authorizing `terminal.read` for it) is the small
  change that makes the mobile "read-only until unlock" default real end-to-end.
- Fold the carried minors above into M5/Phase-5 where the SPA work touches them.

## Verification at merge
Full per-module suite green (16 pkgs, `CGO_ENABLED=0`); static build OK; `go vet` clean; `-race` clean
(directive + api, `CGO_ENABLED=1`); `gofmt` clean on all M4-touched files (10 pre-existing drift files
left untouched — out of scope, no CI gofmt gate); hub binary builds; gorilla pinned identically in hubd +
agent. **Live two-pane rw relay accepted on this host** (above).
