# AgentMon — Design Spec

*Multi-server, mobile-first terminal dashboard for supervising AI coding agents.*

AgentMon is a self-hosted, browser-based "single pane of glass" for supervising AI coding agents
such as Claude Code, Codex, and similar terminal-based coding assistants running inside `tmux`
across multiple development servers.

The product is not "a browser terminal." The product is:

> **A mobile-friendly, multi-server supervision console that tells you which AI coding session
> needs attention and lets you jump into it safely from anywhere.**

AgentMon follows the same naming/convention family as DockMon, DNSMon, and StreamMon. Users reach
only a central hub on the private network; the hub talks to a small Go agent on each development
server over the same internal LAN. The dashboard streams live terminals, preserves real scrollback,
and surfaces which sessions are blocked, done, working, idle, or unknown.

The architecture is **multi-server, mobile-first, and API-first from day one**, and it is a
**single-user operator console** — not a multi-tenant web app. AgentMon is closer to `k9s` or
`lazygit` (a personal cockpit you run against your own machines) than to a shared dashboard like
Grafana. "Multi-user" for a team is achieved by each operator running their own AgentMon instance
against their own servers, not by RBAC inside one hub.

v1 does **not** build roles, scoped permissions, an identity-provider abstraction, or OIDC. It does
lay down three cheap seams so that multi-user or SSO would later be *additive*, never a painful
refactor:

1. **Principal stamping** — every audited action and every user-specific row carries a
   `principal_id` (a single constant value in v1).
2. **One authorization chokepoint** — `authorize(principal, action, resource)` is called at every
   protected entry point; its v1 body is effectively "allow", with the sole real decision being the
   terminal write-lock (§7.5).
3. **Identity at the edge** — business logic only ever sees a resolved `principalID`; authentication
   is a thin edge layer that produces it, so a future reverse-proxy header or OIDC login slots in
   without touching downstream code.

Plus the durable seams that are cheap regardless: stable typed resource IDs, a multi-server
registry, a versioned API used by the UI itself, an append-only audit log, and per-principal seen
state.

v1 ships with trivial defaults: local username/password login (one account, schema allows more —
all equal, no roles), static/config-managed servers, no admin UI, and no OIDC.

---

## 0. Architecture decisions baked into this spec

This spec promotes the real constraints to first-class design requirements.

1. **Mobile is a hard MVP requirement, not polish.**
   AgentMon is not v1-complete unless it is usable from a phone. The mobile flow is not a squeezed
   desktop grid; it is an attention-first agent inbox plus a full-screen terminal.

2. **Multi-server is part of the first useful spine.**
   A single-session prototype can validate terminal transport, but it is not an AgentMon MVP.
   The first real spine must prove: hub registry → two agents → sessions from both → open a pane
   from either → all through the same authz/audit path.

3. **The hub is a Go backend plus a static SPA, shipped as one binary.**
   The hub is `agentmon-hubd`, a Go control plane that owns long-lived terminal WebSockets, fan-out
   event streams, backpressure, authz, audit, and agent relays. The UI is a **Vite + React
   single-page app** built to static assets and embedded into the `hubd` binary via `//go:embed`.
   The UI is just another API client of `/api/v1`. There is no Node server in production. See §5.3
   for the rationale and §16 for packaging.

4. **State is event-sourced internally.**
   The visible state is still simple (`blocked`, `done`, `working`, `idle`, `unknown`), but the hub
   stores state events so bad state transitions can be debugged.

5. **Claude Code hooks are treated as version-sensitive.**
   Hook events are the primary signal, but exact event names, matchers, and payload shape must be
   verified against the installed Claude Code version during implementation.

6. **Security is tightened for a remote-shell product.**
   The design includes signed short-lived hub→agent directives, CSRF/origin checks for
   browser-authenticated routes and WebSockets, input locking, paste confirmation, and a rule to
   avoid logging raw terminal keystrokes by default.

---

## 1. Product thesis

AI coding agents are increasingly long-running, autonomous, and distributed across multiple
servers. `tmux` solves persistence, but mobile interaction with raw SSH/tmux is poor: scrolling,
copy/paste, terminal resizing, keyboard control keys, and quickly finding the session that needs
attention are all painful.

AgentMon solves the supervision layer:

- show all relevant sessions across all development servers;
- sort urgent sessions first;
- make blocked/done/working state visible at a glance;
- provide reliable terminal access from desktop and mobile;
- keep `tmux` as the source of persistence and emergency fallback;
- expose one secure hub surface instead of exposing agents directly;
- keep cheap seams — principal stamping, one authz chokepoint, identity resolved at the edge — so
  multi-user or SSO stays additive, never a refactor.

---

## 2. MVP definition

The MVP is not "all future auth features." The MVP is the smallest version that proves AgentMon's
core product value without painting the architecture into a corner.

### v1 must have

- Central hub reachable by browser on the private network.
- At least two registered servers.
- One `agentmon-agent` running per server.
- Hub-only browser/API access; agents are never browser-reachable.
- Static/config-managed server registry.
- One local user authenticated by username/password (the schema allows more equal accounts, but
  there are no roles).
- SQLite persistence for the local user, audit, state events, and seen state.
- `authorize(principal, action, resource)` called on every REST endpoint and WebSocket upgrade
  (trivial in v1; the only real decision is the terminal write-lock).
- Versioned `/api/v1` API used by the UI.
- Live discovery of `tmux` sessions/windows/panes per server.
- Human-readable session identity: each session is identified by its `tmux` session name (the
  project name) and that name is displayed everywhere a session appears — inbox rows, desktop
  sidebar, terminal headers, audit, and notifications. The working directory (`cwd`) is shown as a
  secondary identifier and used as the fallback label when a session is unnamed. See §9.5.
- Browser terminal attach through the hub relay.
- Mobile-friendly terminal view from day one.
- Basic desktop session list/grid.
- Hook-based state tracking for Claude Code sessions.
- State rollup by pane → session → server.
- Blocked sessions surfaced first.
- Cross-session attention alerts: while focused on one session, another going `blocked` raises an
  in-app alert (toast + sound/vibrate) so the user knows to switch. With one terminal open at a time,
  this is the core supervision loop — not polish.
- Installable as a PWA (web-app manifest, standalone display) so the mobile terminal gets the full
  viewport and can later receive push notifications.
- Per-user `done` → `idle` seen transition.
- Append-only audit log for security-sensitive actions.

### v1 explicitly defers

- Multi-user RBAC (roles, scoped permissions, identity-provider abstraction).
- OIDC / SSO (reachable later via a reverse-proxy identity header or an OIDC edge module).
- Admin UI for users/roles.
- Policy engine such as OPA/Cedar.
- Per-OS-user `tmux` isolation (a deployment topology — one agent per OS user — not a hub feature).
- Full generic prompt scraping for every terminal app.
- Fancy dashboards and layout sharing.
- Terminal recording/playback.
- Public internet exposure.
- Cloud dependency.

### v1 acceptance criteria

AgentMon v1 is not complete unless the following work:

1. Open the hub from desktop and phone.
2. See sessions from at least two servers.
3. Tell which project each session belongs to at a glance from its session name (with `cwd` shown
   as a secondary cue), without opening the terminal.
4. See blocked sessions float above non-blocked sessions.
5. Open a terminal from either server.
6. Use the terminal on mobile with readable text, touch scrollback, Enter, Esc, Tab, Ctrl-C,
   arrows, paste, and orientation changes.
7. Put the phone to sleep or switch apps, then reconnect without killing the underlying session.
8. Confirm that a browser never receives an agent token.
9. ~~Confirm that a locked (read-only) terminal can read output but cannot send input.~~
   **[DESCOPED — Phase-4 owner decision, 2026-06-29: the read/write input lock is dropped from v1;
   terminals are always `rw`. See §6.3/§7.5/§11.8 amendments.]**
10. Confirm that every terminal open/session create action is audited.
11. Confirm that all REST and WebSocket access passes through the same `authorize()` function.
12. While full-screen in one session on mobile, be alerted (visibly/audibly) when a *different*
    session becomes `blocked`.

---

## 3. Goals & non-goals

### Goals

- One hub shows terminal sessions from many servers.
- Mobile-friendly terminal supervision from day one.
- Persistent sessions: work keeps running when no browser client is attached.
- Real scrollback and touch scrolling.
- Live state: `blocked`, `done`, `working`, `idle`, `unknown`.
- Blocked sessions are the primary attention signal.
- Every session is identifiable by project at a glance.
- Hub is the only user-facing surface.
- Agents are internal-only and never exposed directly to browsers or API clients.
- Single-user by design; cheap seams keep future multi-user/SSO additive (§7.1).
- UI and programmatic clients use the same versioned API and authorization path.
- `tmux` remains the source of truth for terminal persistence and SSH fallback.

### Non-goals for v1

- No public internet exposure.
- No cloud dependency.
- No replacement for `tmux`.
- No OIDC implementation yet.
- No full admin UI for identity/RBAC yet.
- No per-OS-user privilege isolation yet.
- No attempt to perfectly classify arbitrary shell/TUI state without hooks.

---

## 4. Topology

```text
        (private network access)                  (same internal LAN)

  Users ─────────────────────▶  AgentMon HUB  ─────────────┬──▶ agentmon-agent @ server-A ──▶ tmux
 browsers   HTTPS + WSS         single container           ├──▶ agentmon-agent @ server-B ──▶ tmux
 mobile                         hubd + embedded UI          └──▶ agentmon-agent @ server-C ──▶ tmux
                                  PEP/PDP + relay
  API clients ─────────────────▶  /api/v1  (API keys)
```

The hub container and all agents live on the same internal LAN. How a user reaches that LAN
(directly on-site, or remotely via their own VPN) is a network concern outside AgentMon's scope;
AgentMon only assumes the hub is reachable by the browser and the agents are reachable by the hub.

### Client → hub

- HTTPS and WSS only.
- Session cookie for browser users.
- Bearer API key for programmatic clients.
- Full authentication and authorization at the hub.
- The hub is both the policy enforcement point (PEP) and policy decision point (PDP).

### Hub → agent

- Server-to-server traffic over the internal LAN.
- Per-agent bearer token.
- Short-lived signed access directive per request/connection.
- Directive includes `target`, resource, access mode, expiry, nonce/request ID.
- Browser/API clients never receive agent tokens or directives.

### Agent → tmux

- Local process interaction on the same host.
- Agent drives `tmux` through control mode.
- Agent makes no user authorization decisions.
- Agent mechanically enforces `ro` vs `rw` and rejects stale/malformed directives.

---

## 5. Component architecture

The system is two deployable artifacts: the **hub** (one container, anywhere on the LAN) and the
**agent** (one Go binary per development server, on systemd).

### 5.1 `agentmon-agent` — Go, one per server

A single static Go binary installed as a systemd service and bound only to the internal LAN
interface, for example:

```toml
listen = "10.0.0.5:8377"
hub_name = "agentmon-prod"
server_id = "server-a"
```

Responsibilities:

1. **Session discovery**
   - Enumerate `tmux` sessions, windows, and panes for a target OS user / tmux socket.
   - Capture each session's name, current path, and active command so the hub can present a
     project-identifiable session tree.
   - Return a live session tree to the hub.

2. **Terminal I/O**
   - Maintain one control-mode client per managed tmux server/socket.
   - Bridge pane output to hub WebSockets.
   - Accept input/resize/control messages from hub WebSockets.
   - Honor `mode=ro|rw`.

3. **Scrollback bootstrap**
   - On attach, capture current pane scrollback and send it before live output.
   - Keep this bounded and configurable.

4. **State tracking**
   - Accept local Claude Code hook events.
   - Maintain current state per pane/session.
   - Push state updates to hub.
   - Use lightweight fallback heuristics only when hooks are absent/stale.

5. **Hook intake**
   - Local-only `/hook` endpoint bound to `127.0.0.1` or a Unix socket.
   - Separate local hook token.
   - Minimal JSON payload.

6. **Mechanical security enforcement**
   - Verify hub bearer token.
   - Verify signed access directive.
   - Enforce `ro` vs `rw`.
   - Validate target/pane/session references.
   - Never decide whether a principal is authorized; the hub already did that.

Suggested Go packages:

- standard `net/http` or `chi`;
- `nhooyr.io/websocket` or `gorilla/websocket`;
- `creack/pty` only for fallback attach mode;
- internal parser for tmux control mode.

### 5.2 `agentmon-hubd` — central backend control plane

The hub backend owns all stateful server-side behavior:

- local username/password auth and session-cookie issuance;
- principal resolution (identity resolved at the edge, §7.1);
- `authorize(principal, action, resource)`;
- server registry;
- agent token storage;
- WebSocket relay;
- state aggregation;
- audit logging;
- API versioning;
- CSRF/origin checks;
- signed hub→agent directives;
- SSE/WS event stream to browsers/API clients;
- serving the embedded static web UI.

Recommended implementation: **Go**.

Why Go is a strong fit:

- terminal streaming and backpressure are easier to reason about;
- the agent is already Go, so protocol/types are shared;
- a single static binary (with the UI embedded) is operationally simple;
- long-lived WebSockets and fan-out streams are first-class server concerns;
- pure-Go SQLite (`modernc.org/sqlite`, `CGO_ENABLED=0`) keeps the build static and the container
  tiny.

`hubd` is the only process in the hub container. It serves both the API/WS data plane and the
static UI assets from the same origin, which simplifies the cookie/CSRF story.

### 5.3 Web UI — Vite + React SPA

The UI is a **Vite + React single-page app** written in TypeScript. It is the browser UI, not the
terminal data plane.

Responsibilities:

- render login page;
- render desktop dashboard;
- render mobile agent inbox and full-screen terminal;
- call `/api/v1` on `hubd`;
- open WebSocket/SSE connections to `hubd`;
- use `xterm.js` for terminal rendering;
- persist user preferences through the API.

Why Vite + React rather than Next.js:

- The UI is a fully client-rendered, authenticated dashboard. There are no public pages and no SEO,
  so Next's server-side features — SSR, React Server Components, route handlers, server actions,
  middleware, ISR — buy nothing here.
- The data plane (terminal WebSockets with backpressure and fan-out) must live in Go regardless. It
  was never going to live in a Next route handler.
- A static SPA embeds cleanly into the `hubd` binary via `//go:embed`, giving a single-process,
  single-container deploy. Next's `output: export` would technically work but adds a heavier, more
  opinionated toolchain and quietly disables features for no benefit here.
- Vite gives fast HMR in dev and emits a plain `dist/` folder for embedding.

Recommended frontend stack:

- **Vite + React + TypeScript** — build/runtime base.
- **shadcn/ui** (Radix + Tailwind) — copy-in, own-the-source component library; strong on mobile.
- **TanStack Router** — typed client-side routing.
- **TanStack Query** — data fetching/caching against `/api/v1`; handles the inbox's
  polling/refetch/stale states.
- **Zustand** — light global state (session list, selected pane, connection status).
- **xterm.js** (+ fit / web-links / webgl addons) — terminal rendering; see §14.3.

Dev/prod split:

- **Dev:** run the Vite dev server with an API/WS proxy to a locally running `hubd`.
- **Prod:** `vite build` → `web/dist/` → embedded into `hubd` via `//go:embed` → served at `/`.
  All API/WS traffic is same-origin under `/api/v1`.

If AgentMon itself ever needs to support third-party UI plugins/extensions, that is an
extension-point concern in `hubd` plus a registration API — not a frontend-framework concern; the
SPA stack above does not need to change for it.

---

## 6. Mobile-first UX

Mobile is a hard requirement because a primary pain point is that raw `tmux` over mobile SSH is not
good enough. AgentMon v1 must be usable on mobile Safari and mobile Chrome.

### 6.1 Mobile product model

Desktop is a dashboard. Mobile is an attention queue.

Mobile primary flow:

```text
Open AgentMon
  → see blocked/done sessions first
  → tap a session
  → full-screen terminal
  → respond / approve / cancel / paste
  → move to next blocked session
```

### 6.2 Mobile views

#### Agent inbox

- Top section: `Needs attention`.
- Then: `Done / unread`.
- Then: `Working`.
- Then: `Idle / unknown`.
- Each row's primary label is the session name (the project); the row also shows server,
  cwd/project path when known, last event time, and state.
- Search/filter by server, project, session name, state.
- Blocked sessions always float to the top.

#### Full-screen terminal

- One terminal per screen on phone.
- Header: `server / session / window / pane`, with the session name shown prominently.
- State dot and last event in header.
- Back button returns to inbox without closing session.
- Input lock visible and obvious.
- Terminal uses available viewport height after mobile keyboard appears.
- Orientation changes trigger resize logic.
- Reconnect banner appears after sleep/network change.

#### Mobile control bar

A mobile terminal needs explicit keys that iOS/Android soft keyboards simply do not have — Esc,
Ctrl, Tab, arrows. This is the same accessory row Termius and Blink ship, and it is **required, not
optional**: there is no other reliable way to send these keys from a phone. Scope the bar around the
keys actually used to supervise Claude Code, not a generic terminal.

Day-one key bar:

```text
[Esc] [Ctrl] [Tab] [⇧Tab] [↑] [↓] [←] [→] [⏎ newline] [Paste] [Enter] [Lock]
```

Behavior:

- `Ctrl` is sticky for one keypress (tap `Ctrl`, then `C` → `0x03`); this makes `Ctrl-C`, `Ctrl-D`,
  `Ctrl-L`, `Ctrl-R` reachable.
- `Esc` sends a clean lone `0x1b` (Claude uses it to cancel/clear); verify the transport does not
  batch or delay it (the "Esc delay" trap, §18.1).
- `↑ ↓` move through Claude's prompt options; `← →` edit. `⇧Tab` covers Claude's mode cycle;
  `Tab` for completion.
- **`⏎ newline`** inserts a newline *without submitting* — the phone equivalent of Shift+Enter — so a
  multi-line prompt no longer needs the `\` line-continuation trick. Send whatever the installed
  Claude Code build expects for a soft newline (Shift+Enter / `\`+Enter / a literal mid-line `\n`);
  confirm per §20. Plain `Enter` still submits.
- `Paste` pastes from the OS clipboard (paste *into* the terminal is reliable); confirm if the text
  is multiline or large.
- `Lock` toggles whether touch input is forwarded — this is the read/write mode of §7.5.

### 6.3 Mobile safety defaults

**[Phase-4 amendment, owner decision 2026-06-29: the read/write input lock is DESCOPED from v1 —
terminals are always `rw`. The read-only-until-unlock posture, the `Lock` key-bar button, and
auto-lock-on-leave below no longer apply. Multiline-paste confirmation is unaffected and stays.]**

Remote shell access from a phone is risky. Mobile must default to safe interaction:

- Terminal opens read-only visually until the user taps an explicit input area or unlock button.
- Pasting more than one line requires confirmation.
- Pasting text containing suspicious shell patterns can show a stronger confirmation.
- Leaving the terminal view can auto-lock input.
- The write-lock (§7.5) defaults to engaged; write controls appear only after explicit unlock.

Touch gestures (resolve in the Phase 0.5 spike):

- **Swipe = scroll the scrollback** by default. This is the #1 mobile pain AgentMon fixes: xterm.js
  holds scrollback client-side, so scrolling up is a swipe — no tmux copy-mode dance. (Caveat:
  scrollback only covers the *normal* buffer; a full-screen alt-screen TUI has none — verify how the
  installed Claude Code build uses the buffer, §20.)
- **Long-press = select text**, then copy. Note: xterm.js draws to a canvas, so iOS's *native*
  long-press selection handles do not apply — selection is xterm's own and rougher on touch than
  Termius's native handles, so a dedicated "select/copy" affordance may be needed. Paste *in* is
  unaffected.

### 6.4 Mobile terminal acceptance tests

Before calling v1 complete, test these on iOS Safari and Android Chrome, **driving a real Claude Code
session** (not just a shell — a shell hides every hard case):

- touch scrollback works without selecting the page;
- native page scroll does not fight terminal scroll;
- long-press selects text and it can be copied out (canvas-selection caveat, §6.3);
- keyboard does not permanently cover the prompt;
- `Enter` submits; the `⏎ newline` button inserts a newline *without* submitting (no `\` trick);
- Esc cancels a Claude prompt;
- Tab and Shift+Tab work;
- Ctrl-C interrupts a running tool;
- arrow keys navigate Claude's prompt options through the control bar;
- paste works; multiline paste confirmation works;
- rotate portrait ↔ landscape works;
- phone sleep/app switch reconnects; reconnect does not kill the `tmux` session;
- while full-screen in one session, another session going `blocked` raises a visible/audible alert.

### 6.5 Desktop vs mobile layout

Desktop:

- tiled grid;
- sidebar tree;
- multiple terminals visible;
- NOC-style overview.

Mobile:

- state-sorted list;
- one full-screen terminal;
- explicit key bar;
- strong input lock;
- next-blocked navigation.

Tablet:

- optional split view: session list + terminal;
- grid only if space allows.

---

## 7. Identity, authentication & authorization

### 7.1 Principle: one enforcement point

Every request resolves to a principal and passes through the same authorization function:

```text
authorize(principal, action, resource) -> allow | deny
```

This is called for:

- browser routes that expose protected data;
- REST API calls;
- WebSocket upgrades;
- terminal input frames when the write-lock is open;
- session creation;
- audit access.

In v1 there is one user and no roles, so `authorize()` effectively returns `allow` — the only real
decision is the terminal write-lock (§7.5). It exists as a hard seam anyway, so adding policy later
edits one function instead of every handler.

Three habits keep multi-user/SSO additive rather than a refactor:

1. **Principal stamping** — `principal_id` on every audited action and user-specific row (a constant
   in v1).
2. **One chokepoint** — this function, called everywhere, trivial body now.
3. **Identity at the edge** — downstream code sees only a resolved `principalID`; authentication
   (password now; proxy header or OIDC later) stays at the edge.

Agents never authorize users. Agents only verify hub trust and enforce the already-authorized
mechanical mode.

### 7.2 Data model

SQLite is enough for v1, behind a repository interface that can later target Postgres. Use the
pure-Go driver `modernc.org/sqlite` so the hub builds with `CGO_ENABLED=0`.

```sql
users (
  id TEXT PRIMARY KEY,
  username TEXT UNIQUE NOT NULL,
  display_name TEXT NOT NULL,
  password_hash TEXT NOT NULL,         -- argon2id/bcrypt; the only credential type in v1
  status TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

servers (
  id TEXT PRIMARY KEY,
  name TEXT UNIQUE NOT NULL,
  url TEXT NOT NULL,
  token_ref TEXT NOT NULL,
  labels TEXT,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

tmux_targets (
  id TEXT PRIMARY KEY,
  server_id TEXT NOT NULL REFERENCES servers(id),
  os_user TEXT NOT NULL,
  socket_name TEXT,
  label TEXT,
  enabled INTEGER NOT NULL DEFAULT 1,
  UNIQUE(server_id, os_user, socket_name)
);

session_state_events (
  id TEXT PRIMARY KEY,
  server_id TEXT NOT NULL REFERENCES servers(id),
  target_id TEXT,
  tmux_session_name TEXT NOT NULL,
  tmux_pane_id TEXT,
  source TEXT NOT NULL,                 -- hook, heuristic, hub, agent
  raw_event TEXT NOT NULL,
  derived_state TEXT NOT NULL,
  payload TEXT,
  event_ts TEXT NOT NULL,
  received_at TEXT NOT NULL
);

principal_seen (
  principal_id TEXT NOT NULL,
  server_id TEXT NOT NULL,
  target_id TEXT NOT NULL DEFAULT '',   -- empty sentinel, never NULL (composite-PK safe)
  tmux_session_name TEXT NOT NULL,
  last_seen_event_id TEXT,
  last_focused_at TEXT NOT NULL,
  PRIMARY KEY(principal_id, server_id, target_id, tmux_session_name)
);

audit_log (
  id TEXT PRIMARY KEY,
  principal_id TEXT,
  action TEXT NOT NULL,
  resource TEXT NOT NULL,
  result TEXT NOT NULL,
  request_id TEXT,
  ip TEXT,
  user_agent TEXT,
  meta TEXT,
  ts TEXT NOT NULL
);
```

Notes:

- One local credential type (password). No `identities` / provider abstraction in v1 — identity is
  resolved at the edge (§7.1), so a proxy-header or OIDC login can be added later without touching
  these tables.
- `session_state_events` is append-only and stores **transitions** (write a row only when
  `derived_state` changes), not periodic heuristic samples — keep a retention/compaction policy in
  mind.
- `principal_seen` makes `done` → `idle` personal; `target_id` uses an empty-string sentinel so the
  composite primary key behaves (SQLite treats NULLs as distinct).
- `servers` may start seeded/config-managed but still exists in the model.
- **Deferred (not v1):** `api_keys` for headless automation — purely additive (another way to
  authenticate as the single user).

### 7.3 Resource names

Use stable, typed resource addresses in authorization and audit.

```text
server:<serverId>
target:<serverId>/<targetId>
session:<serverId>/<targetId>/<sessionName>
pane:<serverId>/<targetId>/<paneId>
user:<userId>
role:<roleId>
api-key:<keyId>
```

Avoid using display names as the only durable identity. `tmux` session names are human-friendly
(and are the project label users see), but audit and seen-state should include server and target
IDs so renaming a session does not lose history.

### 7.4 Actions

Initial action namespace:

```text
server.view
session.view
session.create
session.kill
terminal.read
terminal.write
state.read
audit.read
```

In v1 a single user holds all of these; the namespace exists so a future role system has stable
action names to bind to.

### 7.5 Read/write is the input lock, not a role

**[Phase-4 amendment, owner decision 2026-06-29: the input lock is DESCOPED from v1 — terminals are
always `rw` and the hub always mints `rw` (its current behavior). The mechanism below remains
accurately described and is the seam a future `viewer` role would reuse, but no `ro` path ships in
v1. The agent's mechanical `ro|rw` enforcement is retained (defense in depth, already built).]**

There are no roles in v1. The only read-vs-write distinction is the **input lock** (§6) — a
per-session safety mode the single user toggles, not a per-principal permission. The mechanism is
the same one a future `viewer` role would reuse, so it's worth building cleanly:

- A terminal can be attached read-only (lock engaged) or read-write (lock open).
- `terminal.read` gates the WS upgrade (always allowed in v1).
- When the lock is engaged, the hub drops input frames and passes `mode=ro` to the agent; when open,
  it passes `mode=rw`.
- The agent enforces `ro|rw` mechanically — defense in depth for the lock today, and exactly the
  hook a role system would drive later.

### 7.6 Authentication: local now; proxy/OIDC later

v1 has exactly one authentication method: **local username/password**, verified by the hub, which
then issues its own session cookie. No provider abstraction, no `identities` table.

The seam that keeps SSO cheap is structural, not a framework: downstream code receives a resolved
`principalID` and never knows how it was proven (§7.1). So later you can add — without touching
authorization or the agent path — either of:

- **Reverse-proxy identity** (the usual self-hosted route): front AgentMon with Authelia /
  oauth2-proxy / Tailscale / Cloudflare Access, and trust a signed `X-Forwarded-User` header. This
  is roughly a ten-line edge handler and gives you SSO without building OIDC.
- **A native OIDC edge module** (Authorization Code + PKCE, issuer discovery) if you ever want the
  hub to speak OIDC directly.

Either one just produces a `principalID`; everything downstream is unchanged. Rules either way: the
hub issues its own session cookie, and external tokens never go downstream to agents.

### 7.7 Future multi-user (additive, not v1)

If a team ever needs one shared hub instead of per-operator instances, the additive path is: add a
`roles` / `role_bindings` model behind the existing `authorize()` chokepoint, and (optionally) feed
role mappings from OIDC claims. Because principals, the chokepoint, and edge-resolved identity
already exist, none of that is a rewrite. Until then, "multi-user" means **each operator runs their
own AgentMon** (§7.1, §0).

---

## 8. API surface

The UI consumes the public API. API clients use the same endpoints and same authorization path.

### 8.1 Browser authentication

v1 supports one method:

```text
session cookie       browser (local username/password login)
```

It resolves to the single principal. (Bearer API keys for scripts/automation are deferred — see the
§7.3 notes; when added they resolve to the same principal.)

### 8.2 Public API v1

```text
POST /api/v1/auth/login
POST /api/v1/auth/logout
GET  /api/v1/me

GET  /api/v1/servers
GET  /api/v1/servers/{serverId}
GET  /api/v1/servers/{serverId}/sessions
POST /api/v1/servers/{serverId}/sessions
GET  /api/v1/servers/{serverId}/sessions/{sessionName}

WS   /api/v1/servers/{serverId}/panes/{paneId}/io
GET  /api/v1/events                         # SSE initially; WS optional later
POST /api/v1/seen                           # mark session focused/seen

GET  /api/v1/audit
```

Deferred (additive, not v1): `/api/v1/users`, `/api/v1/roles`, `/api/v1/role-bindings` (need a role
model) and `/api/v1/api-keys` (need the deferred `api_keys` table).

Session list/detail payloads include the session name, server, target, `cwd`, active command, and
current state so any client can render a project-identifiable list without extra calls.

### 8.3 WebSocket terminal protocol

Use binary frames for terminal output/input where possible, and JSON control frames for metadata.
If implementation simplicity wins, all-JSON with base64 is acceptable for v1, but pick one early
and keep it uniform.

Suggested mixed protocol:

Client → hub:

```jsonc
{ "t": "input", "data": "ls -la\n" }
{ "t": "key", "key": "Ctrl-C" }
{ "t": "resize", "cols": 88, "rows": 26 }
{ "t": "focus", "focused": true }
```

Hub → client:

```jsonc
{ "t": "snapshot", "data": "...initial scrollback..." }
{ "t": "output", "data": "..." }
{ "t": "state", "state": "blocked", "session": "api-refactor" }
{ "t": "reconnect", "status": "resumed" }
{ "t": "error", "code": "read_only", "message": "terminal.write is required" }
```

Rules:

- WS upgrade requires `terminal.read`.
- Input frames require `terminal.write`.
- Every accepted WS connection is audited as `terminal.open`.
- First accepted input can be audited as `terminal.write.enabled` or `terminal.input` metadata,
  but raw keystrokes are not logged by default.

---

## 9. State model

### 9.1 Visible states

| State | Dot | Meaning |
|---|---|---|
| `blocked` | 🔴 | Agent needs input/approval; highest-priority signal |
| `done` | 🔵 | Agent finished a turn and this principal has not seen it yet |
| `working` | 🟡 | Agent is actively processing/running tools |
| `idle` | 🟢 | Calm shell, or done and already seen by this principal |
| `unknown` | ⚪ | Plain shell or undetected state |

### 9.2 Rollup priority

Roll up pane → session → server using:

```text
blocked > done(unseen) > working > idle > unknown
```

### 9.3 Seen transition

`done` becomes `idle` per principal when that principal focuses the session terminal.

The underlying global session may still be `done`; the user-specific presentation becomes `idle`
only for principals whose `principal_seen` record is newer than the relevant state event.

### 9.4 State event storage

Do not only store the latest mutable state. Store state events and derive current state.

Benefits:

- debug incorrect state transitions;
- compare hook vs heuristic behavior;
- preserve useful history in audit/support scenarios;
- avoid losing context when the agent restarts.

### 9.5 Session identity (project labelling)

Each session must be identifiable by project without opening its terminal. AgentMon does not invent
a separate "project" entity in v1; it treats the `tmux` session name as the project label, because
that matches how the user already organizes work (e.g. `dockmon`, `dnsmon`, `streammon-api`).

Rules:

- The session name is the primary, human-readable label shown in every surface: inbox rows, desktop
  sidebar, terminal header, notifications, and audit `meta`.
- The session's working directory (from `#{pane_current_path}`) is captured and shown as a
  secondary cue, and is used as the displayed label when a session has only the default tmux numeric
  name. A short form (e.g. the directory basename) is acceptable for compact rows.
- When AgentMon creates a session via `POST /sessions`, a non-empty `name` is required and is used
  verbatim as the session name (after sanitization per §13.6). The UI's "new session" flow should
  prompt for a project name and may suggest the target directory basename as a default.
- Identity for audit/seen-state keys on `serverId` + `targetId` + durable references, not on the
  display name alone, so a user can rename a tmux session without losing history (see §7.3).
- Recommended (not enforced) convention for the user: name each tmux session after its project so
  the dashboard reads naturally across servers. Document this in the README.

---

## 10. Detecting agent state

### 10.1 Claude Code hooks are primary

Claude Code hooks are the primary state signal for Claude Code sessions. The implementation must
verify exact event names, matchers, and payload fields against the installed Claude Code version.

Design-intent mapping:

| Hook signal | Derived state |
|---|---|
| `UserPromptSubmit` | `working` |
| `PreToolUse` | `working` |
| `PostToolUse` | `working` or preserve active state |
| `SubagentStart` | `working` |
| `SubagentStop` | usually preserve parent state |
| `PermissionRequest` | `blocked` |
| `Notification` with permission/input matcher | `blocked` |
| `Notification` with idle/done matcher | `done` |
| `Stop` | `done` |
| `StopFailure` | `blocked` or `unknown` with error metadata |
| `SessionEnd` | `idle` / `unknown` |

Important: do not blindly map every `Notification` to `blocked`. Matchers/payload details should
be used when available.

### 10.2 Hook payload

Recommended local payload to the agent:

```json
{
  "event": "PermissionRequest",
  "session": "api-refactor",
  "pane": "%3",
  "cwd": "/home/dev/projects/api",
  "matcher": "permission_prompt",
  "ts": "2026-06-27T10:31:00Z"
}
```

Fields:

- `event`: hook event name.
- `session`: tmux session name.
- `pane`: tmux pane ID when available.
- `cwd`: current working directory when available.
- `matcher`: hook matcher or subtype when available.
- `ts`: event timestamp.

### 10.3 Hook installation

The agent installer should provide a `claude-hooks.json` template or snippet that posts to the
local agent:

```bash
curl -fsS \
  -H "Authorization: Bearer $AGENTMON_HOOK_TOKEN" \
  -H "Content-Type: application/json" \
  -d "$AGENTMON_HOOK_PAYLOAD" \
  http://127.0.0.1:8377/hook
```

The exact command will depend on Claude Code's hook invocation format and available environment.
The installer should also provide a test command:

```bash
agentmon-agent hook-test --session api-refactor --event PermissionRequest
```

### 10.4 Fallback heuristics

Fallback heuristics are secondary. They should not be the main product truth.

Use fallbacks for:

- panes with no recent hook signal;
- open/visible panes;
- processes that look like known agents;
- periodic sanity checks.

Avoid high-frequency `capture-pane` polling across every pane on every server.

Suggested fallback cadence:

- process/session discovery: every 3–10 seconds;
- capture-pane heuristic: only visible/watched/stale panes, every 2–5 seconds;
- hooks always win if recent.

---

## 11. Terminal backend

### 11.1 Decision: tmux control mode

`tmux` is the default backbone because persistence is the non-negotiable property. Running agents
must survive:

- browser disconnects;
- phone sleep;
- hub restart;
- agent restart;
- SSH drops;
- agent binary upgrades.

`tmux` also preserves the emergency workflow: SSH into the server and attach manually.

### 11.2 Backend abstraction

Keep the terminal backend behind an interface:

```go
type TerminalBackend interface {
    ListSessions(ctx context.Context, target Target) ([]Session, error)
    AttachPane(ctx context.Context, req AttachRequest) (PaneStream, error)
    CreateSession(ctx context.Context, req CreateSessionRequest) error
    Resize(ctx context.Context, pane PaneRef, size TerminalSize) error
    SendInput(ctx context.Context, pane PaneRef, input TerminalInput) error
}
```

`Session` carries at least: name, server/target refs, windows/panes, `cwd`, active command, and
last-known state — enough for the hub to present a project-identifiable tree.

v1 implementation: `tmux` control mode.

Fallbacks later:

- `tmux attach` via PTY for hard cases;
- `dtach`/`abduco` for simpler detached-process support;
- agent-SDK/headless mode for richer non-terminal integrations.

### 11.3 Control-mode model

The agent maintains one control-mode client per managed tmux server/socket.

It must parse at least:

- `%output`;
- `%begin` / `%end` / `%error`;
- `%window-add`;
- `%window-close`;
- `%layout-change`;
- `%session-changed`;
- `%sessions-changed`;
- `%window-renamed`;
- `%client-detached`;
- `%exit`.

`%window-renamed` and session-rename events must update the displayed project label live.

### 11.4 Session listing

Example tmux commands:

```bash
tmux list-sessions -F '#{session_id} #{session_name}'
tmux list-windows  -t <session> -F '#{window_id} #{window_index}:#{window_name}'
tmux list-panes    -t <session> -F '#{pane_id} #{pane_current_command} #{pane_pid} #{pane_current_path}'
```

The session name and `pane_current_path` feed the project label per §9.5.

### 11.5 Scrollback bootstrap

On terminal attach:

1. authorize read access;
2. hub opens agent stream;
3. agent captures bounded scrollback;
4. client receives `snapshot`;
5. live output begins;
6. resize is applied for this viewer.

The snapshot must be bounded, for example:

```toml
terminal_snapshot_lines = 5000
terminal_snapshot_max_bytes = "2MiB"
```

### 11.6 Input fidelity

Terminal input is the hardest area. Validate early:

- literal text;
- Enter;
- Backspace;
- Ctrl-C;
- Ctrl-D;
- Ctrl-L;
- Ctrl-R;
- Esc;
- Tab;
- arrows;
- paste;
- multiline paste;
- bracketed paste behavior;
- mobile key bar;
- TUI programs if needed.

Use a combination of:

- `send-keys -l` for literal text;
- named `send-keys` for control/special keys;
- paste-buffer strategies for large/multiline input;
- PTY fallback if control mode cannot satisfy a specific pane.

### 11.7 Sizing — size follows the active client

A tmux pane is a single character grid shared by all clients; there is no per-viewer rendering, so
true independent sizing of the *same* pane is not achievable (configuring `window-size` only changes
*which one* size wins). The good news: the real workflow is **sequential, one device at a time**
(work on desktop, detach, attach from the phone) — which is exactly the easy case `tmux attach`
already handles. So AgentMon does not need independent sizing; it needs `tmux attach` semantics.

Rules:

- **The active client sets the size.** Whoever currently holds the write-lock reports its
  `cols`/`rows`; the window resizes to it (`window-size latest` / `resize-window`), exactly like
  attaching a terminal.
- **The agent's bookkeeping client is passive.** The agent keeps a control-mode client open
  continuously (for state/output even when no one is watching); it must never impose its own size on
  the active human viewer. This is the one concurrency the manual `tmux attach` flow doesn't have,
  and the only sizing care-point.
- A read-only viewer does not drive size — render the pane's actual grid and scale-to-fit (font
  size) rather than forcing a resize.
- Simultaneous active viewing of the *same* pane at different sizes is out of scope (it isn't the
  workflow); if it ever happens, the latest active client wins and the other reflows.

### 11.8 Read-only enforcement

**[Phase-4 amendment, owner decision 2026-06-29: no `ro` path ships in v1 (always `rw`). Layers 1–2
(hub) are not exercised since the hub always authorizes/mints `rw`; layers 3–4 (agent mechanical
`ro|rw`) remain implemented as built. Kept as the documented seam for a future role system.]**

Read-only is enforced at multiple layers:

1. Hub refuses input frames without `terminal.write`.
2. Hub passes `mode=ro` to agent when principal lacks write permission.
3. Agent drops input frames for `mode=ro`.
4. Where possible, tmux attaches/clients are read-only mechanically.

---

## 12. Agent ↔ hub internal API

All agent endpoints require:

```text
Authorization: Bearer <agent-token>
X-AgentMon-Directive: <signed-directive>
X-AgentMon-Request-Id: <request-id>
```

### 12.1 Signed directive

Directive payload:

```json
{
  "serverId": "server-a",
  "target": "dev",
  "resource": "pane:server-a/dev/%3",
  "mode": "rw",
  "principalId": "user_123",
  "action": "terminal.write",
  "exp": "2026-06-27T10:32:00Z",
  "nonce": "...",
  "requestId": "req_..."
}
```

Rules:

- Short expiry, e.g. 30–120 seconds for connection establishment.
- Nonce/request ID logged.
- Agent rejects expired directives.
- Agent rejects directive/resource mismatch.
- Agent does not use directive to decide user authz; it only verifies hub intent and mechanical
  constraints.

### 12.2 Agent REST

```text
GET /healthz
  → { ok, version, serverId, host, tmuxAvailable }

GET /sessions?target=<target>
  → { sessions: [ { name, cwd, command, windows, panes, state } ] }

POST /sessions?target=<target>
  body: { name, cwd?, command? }       # name required; used as the tmux session name
  → { name }

POST /hook
  localhost/local-token only
  body: { event, session, pane?, cwd?, matcher?, ts, payload? }
  → 204
```

### 12.3 Agent WebSocket

```text
WS /panes/{paneId}/io?target=<target>&mode=ro|rw
```

Message types mirror the public terminal protocol, but the hub may translate/normalize them.

---

## 13. Security

AgentMon is remote shell access by design. Treat it like SSH.

### 13.1 Exposure

- Hub listens only on the private LAN interface or behind a TLS proxy reachable only on that LAN.
- Agents listen only on the internal LAN interface.
- Hook endpoint listens only on localhost or Unix socket.
- No public internet exposure in v1.

### 13.2 Secrets

- Agent tokens are long random per-server secrets.
- Agent tokens live only in hub server-side config/secret store.
- Browser bundles never contain agent tokens.
- The local password is stored as an argon2id/bcrypt hash, never plaintext.
- (Deferred) when API keys land, they are hashed at rest, revocable, and write-capable only by
  explicit opt-in.

### 13.3 Browser security

- Session cookies are HttpOnly, Secure, SameSite where appropriate.
- Same-origin UI + API (the UI is served by `hubd`) keeps the cookie/CSRF model simple.
- CSRF protection for cookie-authenticated mutating REST calls.
- Origin checks for WebSocket upgrades.
- No terminal write WS from cross-origin pages.
- Login rate limiting.

### 13.4 Authorization

- Centralized at hub.
- Every endpoint calls `authorize()` (trivial body in v1; the seam is what matters).
- WS upgrade authorizes read.
- Input frames are gated by the write-lock (§7.5), not a role.
- Agent enforces only mechanical `ro|rw`.

### 13.5 Audit

Audit:

- login success/failure;
- API key created/revoked;
- server/session viewed;
- terminal opened;
- terminal write mode enabled;
- session created/killed;
- authorization denies.

Include the session name in audit `meta` for human-readable trails, but key durable identity on
server/target IDs (§7.3). Do **not** log raw terminal keystrokes by default — they may contain
secrets.

### 13.6 Session creation safety

- Sanitize session names (the user-supplied project name must be constrained to a safe character
  set before it becomes a tmux session name).
- Restrict allowed working directories.
- Avoid shell interpolation.
- Use argument arrays when invoking commands.
- Run agent as non-root.
- Start v1 with a shared non-root service account unless per-user isolation is intentionally built.

---

## 14. Frontend UX

### 14.1 Desktop

- Sidebar tree: servers → sessions → windows/panes, with the session name as each node's label.
- Server/session dots roll up state.
- Blocked sessions sort first.
- Tiled terminal grid; each tile is titled with its session name (project).
- Open/focus terminal from sidebar.
- "Focus next blocked" command.
- Toast/sound when a hidden session becomes blocked (the toast names the project).
- Tab-aware suppression: no alert for the tile currently focused.
- Layout saved per user.

### 14.2 Mobile

- Agent inbox instead of full grid.
- Full-screen terminal.
- Mobile control key bar.
- Input lock.
- Swipe or button to next blocked session.
- Cross-session alert while in a terminal: toast + sound/vibrate when another session goes blocked.
- Installable PWA; push notification when AgentMon is backgrounded or the phone is asleep.
- Large paste confirmation.
- Reconnect handling.
- Safe default read-only posture until explicitly unlocked.

### 14.3 xterm.js

Use:

- `xterm.js` (the `@xterm/xterm` package);
- `@xterm/addon-fit`;
- `@xterm/addon-web-links`;
- `@xterm/addon-webgl` if stable in target browsers, with fallback.

Implementation notes:

- Test mobile WebGL behavior early.
- Ensure touch scroll is terminal scroll, not page scroll.
- Ensure font sizing is configurable.
- Provide minimum contrast themes.

---

## 15. Repo layout

```text
agentmon/
├── agent/                         # Go module: agentmon-agent
│   ├── cmd/agentmon-agent/main.go
│   ├── internal/api/              # REST + WS handlers, token/directive checks
│   ├── internal/hooks/            # local /hook intake
│   ├── internal/io/               # terminal backend interface + WS bridge
│   ├── internal/state/            # state machine + hook mapping
│   ├── internal/tmux/             # control-mode client/parser/list/capture
│   └── internal/config/
│
├── hubd/                          # Go module: central backend/control plane
│   ├── cmd/agentmon-hubd/main.go
│   ├── internal/api/v1/           # public REST + WS/SSE
│   ├── internal/authn/            # sessions, API keys, auth providers
│   ├── internal/authz/            # authorize() chokepoint
│   ├── internal/audit/
│   ├── internal/db/               # SQLite repos/migrations (modernc.org/sqlite)
│   ├── internal/registry/         # servers + agent token refs
│   ├── internal/relay/            # hub ↔ agent WS relay
│   ├── internal/state/            # aggregation + per-principal seen
│   ├── internal/directive/        # signed hub→agent directives
│   └── internal/webui/            # //go:embed of web/dist + static file server
│
├── web/                           # Vite + React SPA (TypeScript)
│   ├── index.html
│   ├── vite.config.ts
│   ├── package.json
│   ├── src/
│   │   ├── main.tsx
│   │   ├── routes/                # TanStack Router routes
│   │   ├── components/
│   │   │   ├── Terminal.tsx
│   │   │   ├── MobileTerminal.tsx
│   │   │   ├── MobileKeyBar.tsx
│   │   │   ├── Sidebar.tsx
│   │   │   └── AgentInbox.tsx
│   │   ├── lib/api-client.ts      # /api/v1 client (TanStack Query)
│   │   ├── lib/ws-client.ts       # terminal WS/SSE client
│   │   └── store/                 # Zustand stores
│   └── dist/                      # build output, embedded into hubd
│
├── docker-compose.yml             # hub service + volume (repo root → `docker compose up` needs no -f)
├── deploy/
│   ├── Dockerfile                 # all-in-one hub image (see §16)
│   ├── agentmon-agent.service     # systemd unit for the per-server agent
│   ├── install-agent.sh
│   ├── claude-hooks.json
│   ├── caddy.example.conf          # optional TLS proxy in front of hub
│   └── seed.yaml
│
├── migrations/
├── docs/
└── README.md
```

---

## 16. Deployment & packaging

AgentMon has exactly two deploy artifacts: the **hub** (one Docker container, runs anywhere on the
LAN) and the **agent** (one Go binary per dev server, runs under systemd). They share the same
internal LAN. The hub never runs on the dev servers, and the agents never run in the hub container.

### 16.1 Hub: all-in-one container

The hub ships as a single small image. The Vite SPA is built to static assets and embedded into the
`hubd` binary with `//go:embed`, so the running container is one process (`hubd`) serving both the
API/WS data plane and the UI from the same origin. There is no Node runtime in production.

Properties:

- one process, one port, one binary;
- pure-Go SQLite (`modernc.org/sqlite`) → `CGO_ENABLED=0` → static binary → distroless/`scratch`
  base;
- the SQLite database file lives on a mounted volume so data survives container upgrades;
- same-origin UI + API simplifies cookies/CSRF.

#### Multi-stage Dockerfile (sketch)

```dockerfile
# ---- Stage 1: build the SPA ----
FROM node:22-alpine AS web
WORKDIR /web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build          # emits /web/dist

# ---- Stage 2: build hubd with the SPA embedded ----
FROM golang:1.23-alpine AS hubd
WORKDIR /src
COPY . /src
# place built assets where //go:embed expects them
COPY --from=web /web/dist /src/hubd/internal/webui/dist
WORKDIR /src/hubd
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/agentmon-hubd ./cmd/agentmon-hubd

# ---- Stage 3: minimal runtime ----
FROM gcr.io/distroless/static-debian12
COPY --from=hubd /out/agentmon-hubd /agentmon-hubd
VOLUME ["/data"]
EXPOSE 8443
USER 65532:65532
ENTRYPOINT ["/agentmon-hubd"]
CMD ["--config", "/data/config.yaml"]
```

The embed lives in `hubd/internal/webui`:

```go
package webui

import "embed"

//go:embed dist
var Assets embed.FS
```

`hubd` serves `Assets` at `/` with SPA fallback (any unknown non-`/api` path returns `index.html`)
and mounts the API under `/api/v1`.

#### docker-compose (sketch)

```yaml
services:
  agentmon-hub:
    build:
      context: .
      dockerfile: deploy/Dockerfile
    image: agentmon-hubd:latest
    restart: unless-stopped
    ports:
      - "8443:8443"            # bind to the LAN interface in real deployments
    volumes:
      - agentmon-data:/data     # holds config.yaml + sqlite db
    environment:
      - AGENTMON_CONFIG=/data/config.yaml

volumes:
  agentmon-data:
```

TLS can be terminated by `hubd` directly or by a thin reverse proxy (Caddy/nginx) in front of it;
`deploy/caddy.example.conf` covers the proxy case. Either way the listener is bound to the private
LAN interface only.

### 16.2 Agent: systemd, not a container

The agent must see the host's real `tmux`, process tree, and user sessions, so it runs directly on
each dev server as a static binary under systemd — not in a container.
`deploy/agentmon-agent.service` plus `deploy/install-agent.sh` handle install;
`deploy/claude-hooks.json` wires Claude Code's hooks to the agent's local `/hook` endpoint.

```ini
# deploy/agentmon-agent.service
[Unit]
Description=AgentMon agent
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/agentmon-agent --config /etc/agentmon/agent.toml
User=dev
Restart=on-failure
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
```

### 16.3 Why the hub isn't two processes

An alternative all-in-one image would run Node (`next start` or a Node API) plus `hubd` behind a
supervisor. That is heavier (two runtimes, two crash/restart paths) and reintroduces a "which
backend owns auth" question. Because the UI has no need for a server runtime, the single static
binary is the simpler and more reliable choice for a self-hosted box. The `TerminalBackend` and
repository interfaces remain the real seams if anything ever needs to change.

### 16.4 Upgrades

- **Hub:** pull a new image, recreate the container; the `/data` volume (config + SQLite) persists.
  Run DB migrations on startup.
- **Agent:** replace the binary and `systemctl restart agentmon-agent`. Because persistence lives in
  `tmux`, restarting the agent does not kill running coding sessions.

---

## 17. Build phases

### Phase 0 — Repo, config, and contracts

Purpose: set up the architecture without building much behavior.

Deliverables:

- monorepo layout;
- shared API/protocol types;
- config format;
- SQLite migration scaffold;
- seed admin user/roles;
- static server registry;
- local dev: Vite dev server proxying to a locally run `hubd`;
- basic CI/build (including the multi-stage hub image).

### Phase 0.5 — Mobile input fidelity spike (mandatory go/no-go gate)

Purpose: prove the single make-or-break risk before building anything on top of it — that a *browser*
terminal on a *phone* can drive Claude Code's interactive TUI at least as well as Termius does today.
Scrollback and multi-server are the easy wins; input fidelity is the bet. If it fails, transport and
encoding decisions must change now, not after the inbox, auth, and relay are built on top.

A deliberately throwaway slice: one hard-coded server, one agent, one pane, a static token, no RBAC,
no inbox. But it **must** run on a real iPhone (Safari) and Android (Chrome), driving a real Claude
Code session. Go/no-go checklist (mirrors §6.4):

- Esc cancels a Claude prompt; Ctrl-C interrupts a tool; arrows navigate prompt options;
- the `⏎ newline` button inserts a newline without submitting; Tab / Shift+Tab work;
- paste a multi-line block intact (bracketed paste owned by exactly one layer);
- select text and copy it out (canvas-selection caveat, §6.3);
- swipe-scroll the scrollback without triggering selection;
- `window-size latest` / passive-agent sizing (§11.7) holds when the agent client is also attached.

Pass → build Phase 1 on this foundation and throw the slice away. Fail → fix the input path first.

### Phase 1 — Multi-server mobile-capable terminal spine

Purpose: prove the actual product spine.

Deliverables:

- agent runs on at least two servers;
- hub registry lists both servers;
- `/api/v1/servers` returns authorized servers;
- `/api/v1/servers/{id}/sessions` returns live tmux sessions with names + cwd;
- sessions are listed by project name (and cwd) so they are identifiable without attaching;
- browser opens a pane from either server via hub relay;
- mobile full-screen terminal view works;
- basic key bar works;
- scrollback snapshot works;
- reconnect after phone sleep works;
- `authorize()` is called on every endpoint/WS;
- audit records terminal open.

This phase can use one seeded admin user and simple config auth, but it must keep the real authz
shape.

### Phase 2 — Local auth, audit, and the read/write lock

Deliverables:

- `users` (single local account, password hash) and `audit_log` tables;
- local username/password login;
- hub-issued session cookies (HttpOnly/Secure/SameSite), login rate limiting;
- `authorize()` chokepoint wired at every entry point (trivial body, real seam);
- principal stamping on audited actions and seen-state;
- read-only / write-lock enforced through hub and agent (`mode=ro|rw`);
- audit deny/success coverage;
- CSRF/origin checks.

### Phase 3 — Claude Code state via hooks

Deliverables:

- agent local `/hook` intake;
- hook token;
- hook installer/template;
- state event storage;
- state aggregation at hub;
- server/session rollup dots;
- blocked/done/working/idle/unknown UI;
- per-principal seen state;
- mobile inbox sorts blocked first;
- desktop sidebar sorts blocked first.

### Phase 4 — UX: desktop grid + mobile agent inbox

Deliverables:

- desktop tiled grid;
- sidebar tree;
- focus next blocked;
- mobile inbox polish;
- mobile full-screen terminal polish;
- per-user layout/prefs;
- terminal theme/font settings;
- toast/sound alerts.

### Phase 5 — Terminal hardening

Deliverables:

- robust control-mode parser tests;
- backpressure handling;
- slow-client disconnect policy;
- better resize behavior;
- larger paste handling;
- PTY fallback for problematic panes;
- bounded scrollback config;
- reconnect/resume robustness;
- agent restart behavior.

### Phase 6 — Future additions, no refactor required

- OIDC provider.
- Admin UI.
- Dynamic role editor.
- Per-OS-user tmux model.
- Policy engine behind `authorize()`.
- Richer agent SDK/headless integrations.
- Public exposure option with stronger auth/mTLS, if ever desired.

---

## 18. Open questions

1. **tmux control-mode input fidelity**
   - Validate literal input, special keys, Ctrl keys, paste, and TUI apps early.
   - Decide exact encoding strategy after testing.
   - **[RESOLVED in Phase 0.5, 2026-06-27 — see `spike-0.5/`]:** forward xterm.js
     `onData` bytes verbatim via `send-keys -t <pane> -H <hex>` written to the
     control client's stdin (keep that stdin open or the client exits with
     `%exit`). Byte-exact incl. a lone ESC (bypasses tmux's escape-time).
     Soft-newline = **LF `0x0a`** (inserts newline without submitting; CR `0x0d`
     submits). Bracketed paste owned solely by xterm.js (`term.paste()`), never
     tmux `paste-buffer`. `%output` un-escape rule: `\`+3 octal digits → byte,
     else literal. Verified on tmux 3.5a + Claude Code v2.1.195 (normal buffer,
     `alternate_on=0`, so scrollback applies).

2. **Per-viewer sizing**
   - Confirm best tmux strategy for desktop + mobile viewers on the same pane
     (`window-size`, `aggressive-resize`, grouped sessions).
   - Document any unavoidable limitations.
   - **[RESOLVED in Phase 0.5]:** `window-size latest` + `aggressive-resize off`;
     the passive control client adopts the active viewer's size via
     `refresh-client -C <cols>x<rows>`. Verified on tmux 3.5a.

3. **Claude Code hook payloads**
   - Verify exact installed hook event names, matchers, and payload fields.
   - Build hook parsing to tolerate extra/unknown fields.

4. **Shared service account vs per-OS-user tmux**
   - v1 recommendation: shared non-root `dev` service account.
   - Future: per-OS-user tmux target model already exists.

5. **Frame encoding**
   - Binary frames for terminal data + JSON control frames, or all-JSON/base64.
   - Pick once before UI/backend integration gets broad.
   - **[RESOLVED in Phase 0.5]:** binary WS frames for terminal data (both
     directions), JSON text frames for control (e.g. `{type:"resize",cols,rows}`).
     Worked cleanly in the spike; adopt for Phase 1.

6. **Session creation policy**
   - Which users can create sessions?
   - Which directories are allowed?
   - Which commands/templates are allowed?
   - **[RESOLVED in M10, 2026-06-29]:** the single v1 user creates (authz
     `session.create`, trivially allowed); the name is required + constrained to
     `^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$` (tmux-safe, validated at hub + agent);
     `cwd` is optional and restricted to an agent `session_dirs` allow-list
     (default `$HOME`), symlink-resolved + traversal-blocked; **custom commands
     are not exposed** (a non-empty `command` is rejected) — sessions start the
     default shell. See `docs/superpowers/specs/2026-06-29-agentmon-m10-new-session-design.md`.

7. **API key write access**
   - Should terminal write via API key be disabled globally by default?
   - Recommendation: yes.

8. **Mobile paste safety**
   - What threshold triggers confirmation?
   - Recommendation: multiline or >200 chars.

9. **Attention alerts (core loop, not polish)**
   - Because the user keeps one terminal open, status *is* navigation: a *different* session going
     `blocked` must reach them even when they are not looking at the inbox.
   - In-app: toast + sound/vibrate while AgentMon is foreground (desktop and mobile).
   - Out-of-app: Web Push when backgrounded/asleep — requires AgentMon installed as a PWA (esp. iOS).
   - Notify `blocked` by default; `done` optional.

10. **TLS termination**
    - Terminate TLS in `hubd` directly, or front it with Caddy/nginx?
    - Either is fine; keep the listener bound to the LAN interface only.

11. **Project labelling beyond session name**
    - v1 uses the tmux session name as the project label (§9.5).
    - If session names ever prove insufficient (e.g. multiple sessions per project), consider an
      explicit project entity or a label derived from `cwd`/git remote later — no v1 schema change
      needed because state/seen already key on durable IDs.

12. **Desktop layout: grid vs single-terminal-plus-inbox**
    - The real workflow is one terminal open at a time, navigated by status. Confirm whether the
      desktop tiled grid (§6.5, §14.1) is actually wanted, or whether desktop should mirror the
      mobile model (inbox/sidebar + one terminal). Dropping the grid removes the last place
      concurrent same-pane viewing — and sizing conflict — could arise.

---

## 19. Implementation rules of thumb

- Do not skip multi-server in the real MVP.
- Do not defer mobile until polish.
- Do not let browsers talk to agents.
- Do not put agent tokens in the client bundle.
- Do not bypass `authorize()` anywhere, including WebSockets.
- Do not log raw terminal keystrokes by default.
- Do not build OIDC/RBAC; keep multi-user/SSO additive via the three seams (§7.1).
- Stay single-user: a team gets multiple AgentMon instances, not roles inside one hub.
- Do not over-trust heuristic screen scraping.
- Do not let a phone resize ruin every desktop viewer.
- Do not show a session without a human-readable project label (session name, or cwd fallback).
- Keep the UI a pure API client; never push terminal relay logic toward the browser.
- Keep SSH + manual `tmux attach` as the emergency fallback.

---

## 20. External implementation notes to verify

These are not product requirements, but implementation checkpoints:

- Verify the current Claude Code hook event list and payload schema against the installed version.
- Verify `tmux` control-mode behavior on the target server versions.
- Verify mobile browser behavior for `xterm.js`, WebGL renderer, keyboard viewport resizing, and
  touch scrolling.
- Verify the `//go:embed` + SPA-fallback static serving handles deep links (refresh on a nested
  route must still return `index.html`).
- Verify `modernc.org/sqlite` builds with `CGO_ENABLED=0` for the target arch and that WAL/locking
  behaves on the mounted volume.
- **Secure context on mobile:** the clipboard API (`navigator.clipboard`) and PWA/service workers
  require a *secure context* — HTTPS or `localhost`. A phone hitting the hub at `http://10.0.0.x`
  is **not** secure, so a clipboard "copy" button and PWA install will silently fail there. Plain
  HTTP is fine for basic key/scroll testing, but to validate copy-out and PWA you need TLS (a
  self-signed cert accepted on the phone, or a Tailscale/Caddy TLS front). Plan the Phase 0.5 spike
  to serve over HTTPS if it is to exercise copy/paste and install-to-home-screen.

---

## 21. Final v1 target statement

AgentMon v1 should be:

> **A private-network, self-hosted, multi-server, mobile-usable, single-user supervision console for
> tmux-hosted AI coding agents, shipped as one hub container plus a per-server agent, where every
> session is identifiable by project, with reliable blocked/done state, hub-only terminal relay,
> local username/password login, versioned API, audit logging, and cheap seams that keep future
> multi-user/SSO additive.**

That is the line. Anything beyond that is later.
