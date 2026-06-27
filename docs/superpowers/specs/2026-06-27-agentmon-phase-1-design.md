# AgentMon Phase 1 — Multi-server mobile-capable terminal spine

*Design spec. Scope: Phase 1 of the [AgentMon design](../../../agentmon-design.md) (§17).
Builds on the validated Phase 0.5 spike (`spike-0.5/`), which is throwaway reference code, not a
dependency.*

Date: 2026-06-27

---

## 1. Purpose & scope

Phase 1 proves the **product spine** as one end-to-end vertical slice:

> hub registry → ≥2 agents → live tmux session lists from both → open a pane from either through
> the hub relay → usable on a phone → every hop through `authorize()`, with audit on terminal-open
> and login.

The value is the *whole* spine working on real hosts. It is built in dependency order
(agent → hub → web) but the deliverable is the integrated path, not the layers.

This spec **folds the Phase 0 scaffold (§17 Phase 0) into Phase 1** (the repo has no scaffold yet),
and — per the decisions in §2 — **pulls the full browser-auth bundle forward from Phase 2**, since a
cookie-authenticated remote-shell product cannot ship login without CSRF + WS origin checks as one
unit.

### 1.1 In scope

- Monorepo scaffold, two Go modules (`agent`, `hubd`), Vite/React SPA, shared contracts, config
  formats, SQLite migrations, CI/build incl. the multi-stage hub image with `//go:embed`.
- `agentmon-agent` on **two real LAN servers**: tmux session discovery, terminal WS, scrollback
  bootstrap, bearer-token + HMAC-directive verification, mechanical `ro|rw`.
- `agentmon-hubd`: config-driven server registry; full username/password auth bundle; `authorize()`
  chokepoint; `/api/v1` REST for servers/sessions; WS relay with HMAC-directive minting; audit;
  embedded SPA.
- Web SPA: login, project-labelled session list grouped by server, mobile full-screen terminal
  (ported from the spike's xterm.js page), key bar, scrollback, reconnect-after-sleep.
- TLS terminated by **Caddy** (real cert) in front of `hubd`.

### 1.2 Out of scope (deferred, with target phase)

| Deferred item | Target phase | Notes |
|---|---|---|
| Hook-based agent state (`blocked`/`done`/…) + attention-sorted inbox + cross-session alerts | Phase 3 | Phase 1 mobile view is a plain session list, no state sorting. |
| User-facing write-`Lock` toggle + default-read-only posture | Phase 2 | `mode=ro|rw` *plumbing* is built in Phase 1 (hub always mints `rw`); the UX/decision is Phase 2. |
| `POST /sessions` (create session from AgentMon) | Phase 2+ | Phase 1 discovers + attaches; create sessions via SSH+tmux for now. |
| `POST /seen`, `GET /events` (SSE) | Phase 3 | Tied to state/seen. |
| API keys, roles, OIDC, admin UI, PTY fallback, backpressure hardening | Phase 5/6 | Per parent design. |

### 1.3 Acceptance criteria covered

Maps to parent design §2 acceptance criteria. Phase 1 satisfies **#1, #2, #3, #5, #6, #7, #8, #10,
#11**. Deferred: **#4** (blocked floats → Phase 3), **#9 user-facing** (locked terminal UX → Phase 2;
the *mechanical* `ro` enforcement is built and tested in M2), **#12** (cross-session blocked alert →
Phase 3).

---

## 2. Locked decisions (Phase 1)

These were settled during brainstorming and are not re-litigated below.

1. **Auth:** full username/password bundle in Phase 1 — argon2id password hash, hub-issued
   `HttpOnly`/`Secure`/`SameSite` session cookie, CSRF protection for cookie-authed mutating REST, WS
   upgrade origin check, login rate-limiting. (Phase 2 then narrows to write-lock + audit depth.)
2. **TLS:** Caddy terminates TLS with a real cert and reverse-proxies to `hubd` (plain HTTP on the
   LAN). `hubd` trusts `X-Forwarded-Proto` (so it sets `Secure` on cookies) and origin-checks against
   the configured external host. Caddy is **TLS-only**; AgentMon owns its own login and cookie.
3. **hub→agent trust:** per-agent **bearer token** + **HMAC-SHA256 signed short-lived directive**
   (§6.3). Agent verifies signature, expiry, resource match, and nonce-unseen, and enforces `ro|rw`
   mechanically.
4. **`mode` plumbing:** carried end-to-end in the directive; hub always mints `mode=rw` in Phase 1.
5. **Topology:** dev inner-loop against a local socket-separated agent (`tmux -L`); integration
   (M3/M4) and on-device acceptance (M5) against **both real LAN servers**.
6. **Build:** milestones M0–M5 (§8); **3 multi-review gates** (incl. Codex) after the agent (M2), the
   hub (M4), and the web (M5); **TDD on every milestone**; fix all findings but nitpicks.
7. **Terminal is read-write in Phase 1** (no Lock UX yet).

Inherited-and-locked from the spike/parent design (not re-decided): Go `hubd`+`agent`; Vite/React/TS
SPA; `//go:embed`; `modernc.org/sqlite` with `CGO_ENABLED=0`; **binary WS frames for terminal data +
JSON text frames for control**; input via `send-keys -t <pane> -H <hex>` on a held-open control-client
stdin (lone ESC clean; LF=soft-newline, CR=submit); bracketed paste owned solely by xterm.js;
`window-size latest` + `aggressive-resize off` + passive client `refresh-client -C`; `%output`
un-escape (`\`+3 octal → byte, else literal).

---

## 3. Architecture (Phase 1 view)

```text
 phone / desktop browser
        │  HTTPS / WSS  (real cert)
        ▼
   ┌─────────┐   HTTP / WS (LAN)        ┌──────────────────────────┐
   │  Caddy  │ ───────────────────────▶ │      agentmon-hubd        │
   │ TLS only│                          │  (one process/container)  │
   └─────────┘                          │  • auth + cookie + CSRF   │
                                        │  • authorize() chokepoint │
                                        │  • config registry        │
                                        │  • /api/v1 REST           │
                                        │  • WS relay + directive   │
                                        │  • audit (SQLite)         │
                                        │  • //go:embed SPA         │
                                        └──────────┬───────────────┘
                       bearer token + HMAC directive (LAN, per server)
                ┌──────────────────────────┴───────────────────────────┐
                ▼                                                        ▼
   ┌────────────────────────┐                            ┌────────────────────────┐
   │ agentmon-agent @ srv-A │                            │ agentmon-agent @ srv-B │
   │  • tmux control mode   │                            │  • tmux control mode   │
   │  • discovery / WS / IO │                            │  • discovery / WS / IO │
   │  • verify bearer+dir.  │                            │  • verify bearer+dir.  │
   └───────────┬────────────┘                            └───────────┬────────────┘
               ▼ tmux -C                                             ▼ tmux -C
            tmux sessions                                         tmux sessions
```

Rules (from parent design §4, §19): browsers reach **only** the hub; agents are LAN-internal and
never browser-reachable; browsers never receive an agent token or directive; the hub is both PEP and
PDP; the agent makes no user-authorization decisions.

---

## 4. Components & responsibilities (Phase 1)

### 4.1 `agentmon-agent` (Go, per server)

- **tmux control mode** — port `spike-0.5/control.go` + `control_test.go` into
  `agent/internal/tmux`. One control-mode client per managed socket/target; parse `%output`,
  `%begin`/`%end`/`%error`, `%exit` (plus `%window-*`/`%session-changed` tolerated/ignored in P1);
  `SendInput` via `send-keys -H`; `Resize` via `refresh-client -C`; held-open stdin.
- **Discovery** — `tmux list-sessions/list-windows/list-panes` (§11.4) → session tree with
  `name`, `cwd` (`#{pane_current_path}`), active `command`, windows, panes. No `state` field yet
  (or always `unknown`).
- **REST** (`agent/internal/api`): `GET /healthz`, `GET /sessions?target=<t>`. (`POST /sessions` and
  `POST /hook` deferred; hook listener present in config but unwired.)
- **WS** (`agent/internal/io`): `WS /panes/{paneId}/io?target=<t>&mode=ro|rw` — scrollback snapshot
  (binary) then live `%output` (binary); accepts binary input + JSON `resize`; honors `mode`.
- **Mechanical security** (§6.3): verify `Authorization: Bearer <agent-token>`; verify
  `X-AgentMon-Directive` (HMAC, expiry, resource match, nonce); drop input frames when `mode=ro`;
  validate pane/target refs. **Never** decides principal authz.
- **Config**: `agent.toml` (§7.2). Runs under systemd as a non-root `dev` account
  (`deploy/agentmon-agent.service`).
- **Single default target in Phase 1** (one `os_user`/socket); contracts carry `targetId` so
  multi-target is additive.

### 4.2 `agentmon-hubd` (Go, one process)

- **authn** (`internal/authn`): `POST /auth/login` (argon2id verify, rate-limited), `POST
  /auth/logout`, `GET /me`; issues hub session cookie (`HttpOnly`, `Secure` when
  `X-Forwarded-Proto=https`, `SameSite=Lax`); CSRF token for cookie-authed mutations; resolves every
  request to a `principalID` **at the edge** (§7.1) — downstream sees only the resolved principal.
- **authz** (`internal/authz`): single `authorize(ctx, principal, action, resource) (Decision,
  error)` chokepoint (§5). v1 body: allow for the authenticated single principal across all actions;
  called at **every** REST handler and WS upgrade.
- **registry** (`internal/registry`): server list from `config.yaml` (static/config-managed, §2),
  loaded at boot; holds per-server `url`, bearer token, and HMAC signing key (secrets server-side
  only).
- **api/v1** (`internal/api/v1`): the Phase 1 REST surface (§6.1) + the WS relay endpoint.
- **relay** (`internal/relay`): on terminal attach — `authorize(terminal.read)`, WS origin check,
  mint HMAC directive (`internal/directive`), dial the agent WS with bearer+directive, pump frames
  both ways (binary passthrough, JSON control forwarded/normalized), audit `terminal.open`.
- **directive** (`internal/directive`): mint + (shared verify lib used by the agent) HMAC-SHA256
  directives (§6.3).
- **audit** (`internal/audit`): append-only audit writes (§6.5).
- **db** (`internal/db`): `modernc.org/sqlite` repos + migrations (§7.3), `CGO_ENABLED=0`.
- **webui** (`internal/webui`): `//go:embed dist` + static serve with SPA fallback (unknown
  non-`/api` path → `index.html`; verify deep-link refresh per parent §20).

### 4.3 Web SPA (Vite + React + TypeScript)

- **Login page** → `POST /auth/login`, then cookie-authed.
- **Session list** (`AgentInbox` precursor — plain list, no state sorting yet): servers → sessions,
  each row's **primary label = session name (project)**, with `cwd`/basename as secondary cue
  (§9.5). Data via TanStack Query against `/api/v1`.
- **Mobile full-screen terminal** — port `spike-0.5/static/index.html` into React components
  (`Terminal.tsx`, `MobileTerminal.tsx`, `MobileKeyBar.tsx`): xterm.js (`@xterm/xterm` +
  `addon-fit`), binary WS client (`lib/ws-client.ts`), scrollback render, resize-follows-active
  (report `cols`/`rows` via JSON `resize`), reconnect banner after sleep/network change.
- **Key bar** (§6.2): `[Esc][Ctrl][Tab][⇧Tab][↑][↓][←][→][⏎ newline][Paste][Enter]`. Sticky `Ctrl`;
  `⏎ newline` sends LF, `Enter` sends CR. **No `Lock` button in Phase 1** (terminal is rw; Lock is
  Phase 2).
- Stack per parent §5.3: shadcn/ui, TanStack Router + Query, Zustand, xterm.js.
- Pure API client — no terminal relay logic in the browser (§19).

---

## 5. Authorization model (Phase 1)

```go
type Decision struct{ Allow bool; Reason string }
func authorize(ctx context.Context, principal Principal, action Action, resource ResourceID) (Decision, error)
```

- **Actions used in Phase 1** (from §7.4): `server.view`, `session.view`, `terminal.read`,
  `terminal.write`, `audit.read`. (`session.create`, `session.kill`, `state.read` unused yet.)
- **Resource IDs** (§7.3): `server:<id>`, `session:<serverId>/<targetId>/<name>`,
  `pane:<serverId>/<targetId>/<paneId>`, `user:<id>`.
- **v1 body**: authenticated single principal ⇒ allow for all actions. WS upgrade authorizes
  `terminal.read`; input frames are gated by `terminal.write` (always granted in P1 — the only future
  real decision is the write-lock). Every protected entry point calls it; denies are audited.
- **Principal stamping**: `principal_id` (constant single-user value) on every audited row.

---

## 6. Contracts

### 6.1 Public API v1 (Phase 1 subset of parent §8.2)

```text
POST /api/v1/auth/login        body {username,password} → set-cookie; {user}
POST /api/v1/auth/logout       → clears cookie
GET  /api/v1/me                → {principalId, username, displayName}

GET  /api/v1/servers           → [{id,name,labels,enabled}]
GET  /api/v1/servers/{id}      → {id,name,...,healthy}
GET  /api/v1/servers/{id}/sessions
       → [{name, server, target, cwd, command, windows:[{id,index,name,panes:[{id,command,cwd}]}]}]
GET  /api/v1/servers/{id}/sessions/{name}   → single session detail (same shape)

WS   /api/v1/servers/{id}/panes/{paneId}/io     # terminal relay (§6.2)

GET  /api/v1/audit             → recent audit rows (read; minimal, for verifying #10/#11)
```

Deferred to later phases: `POST /servers/{id}/sessions`, `POST /seen`, `GET /events`,
`/users`, `/roles`, `/api-keys`. Session payloads carry name/server/target/cwd/command so any client
renders a project-identifiable list without extra calls (§8.2). No `state` field is populated in
Phase 1.

### 6.2 WebSocket terminal protocol (resolved encoding, supersedes the illustrative JSON-input in §8.3)

- **Binary frame** = raw terminal bytes, both directions (client→hub input = xterm.js `onData` bytes;
  hub→client output = un-escaped `%output` bytes; first hub→client binary frame is the scrollback
  **snapshot**).
- **JSON text frame** = control:
  - client→hub: `{"type":"resize","cols":88,"rows":26}` (and optionally `{"type":"focus",...}`).
  - hub→client: `{"type":"reconnect","status":"resumed"}`,
    `{"type":"error","code":"read_only","message":"..."}`. (`{"type":"state",...}` reserved for
    Phase 3.)
- **Keys** are sent as their raw byte sequences from the key bar (e.g. Esc = `0x1b`, Ctrl-C = `0x03`,
  arrows = `ESC [ A/B/C/D`), i.e. through the same binary path — no separate `{t:"key"}` frame.

Rules (§8.3): WS upgrade requires `terminal.read` + passes the origin check; input frames require
`terminal.write`; every accepted WS connection is audited as `terminal.open`; **raw keystrokes are
not logged**.

### 6.3 Agent ↔ hub internal API & HMAC directive (parent §12)

Every agent request carries:

```text
Authorization: Bearer <agent-token>
X-AgentMon-Directive: <b64url(payload)>.<b64url(HMAC-SHA256(payload, signing_key))>
X-AgentMon-Request-Id: <request-id>
```

Directive payload (§12.1): `{serverId, target, resource, mode, principalId, action, exp, nonce,
requestId}`. **HMAC-SHA256** with the per-server `signing_key` (distinct from the bearer token;
symmetric — no PKI to stand up). Hub mints; agent verifies. Rules:

- short expiry (~60s for connection establishment); agent rejects expired.
- agent rejects signature mismatch, resource/path mismatch (`resource` must match the requested
  `pane:.../<paneId>` + target), and **replayed nonce** (TTL nonce-cache = expiry window).
- the directive only conveys hub *intent* + mechanical mode; the agent never derives user authz from
  it.

Agent endpoints (§12.2/§12.3): `GET /healthz`, `GET /sessions?target=`,
`WS /panes/{paneId}/io?target=&mode=ro|rw`. (`POST /sessions`, `POST /hook` deferred.)

### 6.4 Shared types

A shared contracts source (Go types in a small internal package + TS mirror, or codegen — chosen in
M0) covers: session/window/pane DTOs, the WS control-frame JSON shapes, the directive payload, and
resource-ID helpers. Keep one source of truth so hub, agent, and SPA agree.

---

## 7. Config, schema, deployment

### 7.1 Hub `config.yaml` (sketch)

```yaml
listen: "127.0.0.1:8080"                 # plain HTTP behind Caddy
external_origin: "https://agentmon.example.lan"   # cookie Secure + WS origin check
trust_forwarded_proto: true
data_dir: "/data"                        # sqlite db + config volume
session_cookie: { name: "agentmon_session", ttl: "168h" }
login_rate_limit: { max_attempts: 5, window: "15m" }
servers:
  - { id: "server-a", name: "server-a", url: "http://10.0.0.5:8377",
      token_ref: "env:AGENTMON_SRVA_TOKEN", signing_key_ref: "env:AGENTMON_SRVA_SIGNKEY" }
  - { id: "server-b", name: "server-b", url: "http://10.0.0.6:8377",
      token_ref: "env:AGENTMON_SRVB_TOKEN", signing_key_ref: "env:AGENTMON_SRVB_SIGNKEY" }
```

The single user's password is set out-of-band (CLI subcommand `agentmon-hubd user set-password
--username <u>` writing an argon2id hash to the DB, or a first-run env). Secrets resolve via
`*_ref` (env/file) so they never sit in plaintext config in the image.

### 7.2 Agent `agent.toml` (sketch)

```toml
listen      = "10.0.0.5:8377"     # LAN interface only
server_id   = "server-a"
hub_token   = "env:AGENTMON_AGENT_TOKEN"     # bearer the hub must present
directive_key = "env:AGENTMON_AGENT_SIGNKEY" # HMAC key to verify hub directives
scrollback_lines = 5000
[[targets]]
  os_user = "dev"
  socket_name = ""        # default socket
  label = "default"
# [hook] listener is Phase 3 — not wired in Phase 1
```

### 7.3 SQLite schema (Phase 1)

M0 creates the **full** schema from parent §7.2 via migrations (forward-compat). Phase 1 **actively
uses** `users` and `audit_log`. The server registry is **config-driven** (loaded at boot);
`servers`, `tmux_targets`, `session_state_events`, `principal_seen` tables exist but are populated in
later phases. `audit_log` is append-only; raw keystrokes are never written.

### 7.4 Deployment

- **Hub:** multi-stage image (parent §16.1) — Node build of the SPA → embed into `hubd` →
  distroless static. `/data` volume holds `config.yaml` + sqlite. **Caddy** in front
  (`deploy/caddy.example.conf`) terminates TLS, proxies `/`, `/api`, and WS upgrades to `hubd`,
  forwards `X-Forwarded-*`.
- **Agent:** systemd unit + `install-agent.sh` (parent §16.2), non-root `dev` account, LAN-bound.

---

## 8. Milestones (M0–M5)

Dependency order; TDD each; review gates after M2, M4, M5.

| # | Milestone | Key work | Done when (demonstrable) |
|---|-----------|----------|--------------------------|
| **M0** | Contracts & scaffold | Monorepo (§15), 2 Go modules + Vite app, shared contracts (§6.4), config formats, full SQLite migrations + repo interfaces, config registry loader, Vite→hubd dev proxy, CI (build + unit tests + multi-stage image + `//go:embed` + `CGO_ENABLED=0` check), SPA-fallback static serve. | Skeleton builds green in CI; `hubd` serves a placeholder SPA over the embed and answers `/healthz`; agent answers `/healthz`; migrations run on a fresh volume; deep-link refresh returns `index.html`. |
| **M1** | Agent: discovery + REST | Port `control.go`/`control_test.go`; discovery → session tree (name/cwd/command/windows/panes); `GET /healthz`, `GET /sessions`; bearer-token verify. | `curl -H 'Authorization: Bearer …'` an agent on a **real server** → live session tree; bad/no token → 401; control-mode unit tests green. |
| **M2** | Agent: terminal WS + directive | `WS /panes/{id}/io?mode=`; scrollback snapshot; binary I/O bridge (port spike `main.go` WS logic); HMAC directive verify + nonce cache + expiry + resource match; mechanical `ro`/`rw`. | Ported smoke client drives a **real** pane (a probe string reaches `claude`/shell); expired/replayed/forged/resource-mismatched directive rejected; `mode=ro` drops input. **→ Review gate 1 (agent).** |
| **M3** | Hub: auth + registry + REST | SQLite repos + migrations live; login (argon2id) + cookie + CSRF + rate-limit + `/me`/`logout`; `authorize()` chokepoint; config registry; `GET /servers`, `/servers/{id}`, `/servers/{id}/sessions`, `/sessions/{name}` (hub→agent bearer); audit login + denies. | Log in over HTTPS-behind-Caddy; `GET /servers/{id}/sessions` returns project-labelled sessions from **both real servers**; unauth → 401; CSRF/origin enforced; login success/failure audited. |
| **M4** | Hub: relay + directive minting | WS relay: `authorize(terminal.read)` + WS origin check + HMAC directive minting + dial agent (bearer+directive) + bidirectional frame pump (binary passthrough, JSON control); audit `terminal.open`. | A WS client through the **hub** drives a real pane on **either** server end-to-end; DevTools/network shows the browser never receives an agent token/directive (#8); `terminal.open` audited (#10); every REST+WS path passes `authorize()` (#11). **→ Review gate 2 (hub).** |
| **M5** | Web: login + list + mobile terminal | React login→cookie; project-labelled session list grouped by server (§9.5); mobile full-screen terminal (ported xterm.js page → components); binary WS client; scrollback; resize-follows-active; key bar; reconnect-after-sleep. | The **on-device acceptance run** (§9) passes on iPhone Safari + Android Chrome against both real servers. **→ Review gate 3 (web) + on-device gate.** |

---

## 9. Testing strategy

- **TDD throughout** (per workflow rule). Write the failing test first at each milestone.
- **Unit (CI, pure Go / TS):** control-mode `parseOutput`/`unescapeOutput`/`encodeSendKeys` (ported);
  HMAC directive sign/verify/expiry/nonce/resource-match; argon2id + cookie + CSRF token + rate-limit;
  resource-ID helpers; SPA-fallback routing.
- **Integration (dev box / test tmux socket):** agent discovery + WS against a real `tmux -L` socket;
  hub→agent REST/relay against a real agent and against an `httptest` fake agent for error paths
  (timeouts, bad directive). tmux/Claude-dependent tests do **not** run in CI (no tmux there) — they
  run on the dev box; CI runs the pure unit suite.
- **On-device acceptance (manual, M5 gate)** — iPhone Safari + Android Chrome, against both real
  servers, driving a **real Claude Code session** (a shell hides the hard cases):
  - [ ] log in over HTTPS (real cert via Caddy); cookie set
  - [ ] see sessions from **both** servers, labelled by project name (cwd secondary)
  - [ ] open a terminal from **either** server
  - [ ] readable text; touch **swipe-scroll** scrollback without selecting the page
  - [ ] `Enter` submits; `⏎ newline` inserts a newline without submitting
  - [ ] `Esc` cancels a Claude prompt; `Ctrl-C` interrupts a tool; `Tab`/`⇧Tab` work; arrows navigate
  - [ ] paste works; multi-line paste intact
  - [ ] rotate portrait ↔ landscape reflows
  - [ ] phone sleep / app-switch **reconnects** without killing the tmux session
  - [ ] DevTools/network confirms the browser never receives an agent token/directive (#8)
  - [ ] audit shows the `terminal.open` (#10); spot-check every REST+WS went through `authorize()`
        (#11)

  (Lock/read-only UX, blocked-sort, and cross-session alerts are **not** tested here — Phase 2/3.)

---

## 10. Security checklist (Phase 1)

- Hub listener bound to LAN/localhost; agents LAN-only; no public exposure (§13.1).
- Agent tokens + signing keys server-side only; never in the SPA bundle (§13.2, #8).
- Password stored argon2id; never plaintext (§13.2).
- Cookie `HttpOnly`/`Secure`(via `X-Forwarded-Proto`)/`SameSite`; CSRF on cookie-authed mutations;
  **WS origin check** against `external_origin`; login rate-limiting (§13.3).
- `authorize()` at every REST handler + WS upgrade; agent enforces only mechanical `ro|rw` (§13.4).
- Audit: login success/failure, `terminal.open`, authorization denies. Session-name in `meta`,
  durable identity on server/target IDs. **No raw keystroke logging** (§13.5).
- Directive: short expiry, nonce replay-protection, resource match (§6.3).

---

## 11. Implementation checkpoints to verify (from parent §20)

- Confirm `//go:embed` + SPA-fallback serves deep links (nested-route refresh → `index.html`).
- Confirm `modernc.org/sqlite` builds `CGO_ENABLED=0` for the target arch; WAL/locking behaves on the
  mounted volume.
- Confirm Caddy forwards WS upgrades + `X-Forwarded-*`; `hubd` derives `Secure` and origin correctly
  behind it.
- Confirm tmux control-mode behavior on **both real servers'** tmux versions (the spike validated
  3.5a; check each host).
- Re-confirm mobile xterm.js touch-scroll vs page-scroll and keyboard-viewport resize on current iOS
  Safari / Android Chrome.

---

## 12. Open questions for Phase 1

1. **Shared contracts mechanism (decide in M0):** hand-written Go types + a TS mirror, or codegen
   (e.g. from Go structs / an OpenAPI doc)? Lean: hand-written + a small TS mirror for the few P1
   DTOs; revisit if drift hurts.
2. **Audit read endpoint scope:** `GET /api/v1/audit` is included minimally to verify #10/#11. Keep
   it (cheap) or verify via DB only? Lean: keep, behind `audit.read`.
3. **`session.view` auditing:** audit every session-list view (noisy) or only `terminal.open` + login
   + denies? Lean: the latter; sample/skip routine list views.
