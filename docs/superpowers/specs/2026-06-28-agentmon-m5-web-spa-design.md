# AgentMon — M5: Web SPA (login + session list + xterm.js terminal on the M4 relay)

*Design spec. **M5** of [Phase 1](2026-06-27-agentmon-phase-1-design.md) — the **browser half** of the
"multi-server mobile-capable terminal spine". The hub's backend spine is complete:
[M3 (auth + registry + REST)](2026-06-27-agentmon-m3-hub-auth-registry-rest.md),
[Agent Onboarding](2026-06-28-agentmon-agent-onboarding-design.md), and
[M4 (WS relay + directive minting)](2026-06-28-agentmon-m4-ws-relay-directive-minting-design.md).
This milestone is **entirely web-side** (`web/`): it builds the real SPA against M4's already-live,
already-tested wire contract. The input-fidelity decisions are inherited verbatim from the
[Phase 0.5 spike](../../../spike-0.5/) (design §18.1/§18.2/§18.5, RESOLVED).*

Date: 2026-06-28

---

## 1. Purpose & scope

Replace the `web/src/routes/{index,login}.tsx` stubs ("lands in M5") with the real browser experience:
a user logs in, sees their servers and sessions, and opens a live `xterm.js` terminal on any pane —
through the hub relay — on both desktop and mobile.

```text
browser SPA ──fetch /api/v1──▶ hub        (login, /me, servers, sessions)
browser SPA ──WS  /api/v1/…/io──▶ hub ──WS──▶ agent ──▶ tmux pane   (terminal)
```

After M5, the remaining Phase-1 browser deliverables are met (design §17 Phase 1): "browser opens a pane
from either server via hub relay", "mobile full-screen terminal view works", "basic key bar works",
"scrollback snapshot works", "reconnect after phone sleep works".

### 1.1 In scope

- **Login form** — `POST /api/v1/auth/login`, store the returned `csrfToken`, redirect to the dashboard;
  `GET /api/v1/me` for session bootstrap; `POST /api/v1/auth/logout` (with `X-CSRF-Token`).
- **Server → session list** — `GET /api/v1/servers`, `GET /api/v1/servers/{id}/sessions`; a flat,
  searchable, project-identifiable list (session name + cwd + command). **No state dots / blocked-first
  sorting** — session state does not exist until Phase 3 (`shared.Session` has no `State` field).
- **xterm.js terminal** on the M4 relay WS `GET /api/v1/servers/{id}/panes/{paneId}/io?target=`, speaking
  the transparent protocol: binary frames = raw I/O, JSON `{type:"resize",cols,rows}` control frame.
- **Desktop: live tiled grid** — N sessions open as tiles, **each its own WS, all streaming live**;
  click-to-**expand** one full-size to work, then back to grid.
- **Mobile: one full-screen terminal** + the required key bar (design §6.2) + a session switcher (no grid).
- **Reconnect** after phone sleep / network change; **resize** on fit/rotation/keyboard.
- **Tooling:** Tailwind + shadcn/ui component primitives; Vitest + Testing Library (TDD); a CI web-test
  step; the dev Vite WS-proxy fix.

### 1.2 Out of scope (deferred to later phases, per design §17)

- **Session state** (blocked/done/working/idle dots), rollups, per-principal seen, and **blocked-first
  sorting** — Phase 3 (needs the hook pipeline; no state data exists yet).
- **The agent inbox as an attention queue** (state-sorted) — Phase 3/4. M5 ships a plain searchable list.
- **Per-user saved layouts / grid persistence, "focus next blocked", toast/sound alerts** — Phase 4.
- **PWA install + Web Push** — Phase 4 (design §14.2).
- **Read-only / write-lock** — explicitly dropped from M5 by the product owner. The hub always mints `rw`
  (M4 locked decision); true `ro` enforcement waits for real authz (design §6.3/§7.5/§11.8). No
  client-side visual lock either — the §6.2 key bar's `[Lock]` button is **omitted** in M5.
- **Server-side per-principal relay concurrency cap** — carried from M4 to Phase 5. M5 adds a *client-side*
  soft cap on live grid tiles as a pragmatic bound (§5.3); the server-side 429 cap is still a follow-up.

---

## 2. The wire contract M5 builds against (frozen by M3/M4 — verified in source)

All of this is **live and tested**; M5 conforms to it and does not change hub code.

### 2.1 Auth & CSRF
- `POST /api/v1/auth/login` `{username,password}` → sets HttpOnly, `SameSite=Lax`, `Path=/` session
  cookie; body `{principalId,username,displayName,csrfToken}`. Pre-auth: checks **Origin**, not CSRF.
- `GET /api/v1/me` → same body (incl. `csrfToken`); behind `RequireAuth`; 401 if no/invalid cookie.
- `POST /api/v1/auth/logout` → behind `RequireAuth`; **mutating method ⇒ requires `X-CSRF-Token`**; 204.
- **CSRF rule** (`authn.RequireAuth`): `POST/PUT/PATCH/DELETE` require `X-CSRF-Token == session.CSRFToken`.
  In M5 the only mutation is logout. The SPA stores `csrfToken` (from login/`me`) and sends it on mutations.
- **Origin rule** (`authn.CheckOrigin`): a present `Origin` must equal `external_origin`; absent is allowed.
  The browser sets `Origin` automatically — the SPA cannot and need not set it. Prod is same-origin
  (embedded `dist/`), so `Origin == external_origin` automatically. **Dev** runs Vite on `:5173` proxying
  to the hub, so the **dev hub config must set `external_origin: http://localhost:5173`** (a config note,
  not SPA code).
- **Cookies:** same-origin requests; `fetch` uses `credentials: "same-origin"`. The terminal WS carries the
  cookie automatically on the handshake (browser cannot set WS headers — directive/Bearer are all hub-side).

### 2.2 Data
- `GET /api/v1/servers` → `[{id,name,labels,enabled}]` (`registry.ServerSummary`).
- `GET /api/v1/servers/{id}/sessions[?target=]` → `shared.Session[]`:
  `{name,server,target,cwd,command,windows:[{id,index,name,panes:[{id,command,cwd}]}]}`. Pane `id` is the
  tmux pane id (`%0`, `%1`, …). Empty `?target=` resolves to `default` hub-side. 502 `agent unavailable`
  if the agent is down.
- (`GET /api/v1/servers/{id}`, `/sessions/{name}`, `/audit` exist but M5 does not need them for the core
  flow; `me`/`servers`/`sessions` + the relay are the dependencies.)

### 2.3 Terminal WS — transparent protocol (design §18.1/§18.5 RESOLVED in the spike)
- `WS GET /api/v1/servers/{id}/panes/{paneId}/io?target=` — `paneId` must match `^%[0-9]+$` (hub guards it).
- **Binary frames both directions** = raw terminal bytes. The **first** binary frame is the scrollback
  snapshot — but the client need not special-case it: every binary frame is `term.write(bytes)`. (The
  snapshot is just terminal output; `term.reset()` on (re)connect + writing the snapshot repaints it.)
- **Client → hub control:** JSON text frame `{type:"resize",cols,rows}`.
- Hub mints `rw`, audits `terminal.open`, relays transparently. On agent-dial failure the hub returns 502
  *before* upgrading; the SPA surfaces that as a connect error + reconnect.

### 2.4 Input fidelity — inherited verbatim from `spike-0.5/static/index.html` (proven on tmux 3.5a + Claude Code v2.1.195)
- Forward `xterm.onData` bytes **verbatim** (UTF-8 via `TextEncoder`) as binary frames.
- **Soft newline = LF `0x0a`** (inserts newline without submitting); **Enter = CR `0x0d`** (submits).
- **Esc = lone `0x1b`**, sent immediately (no escape-time batching).
- **Tab = `0x09`**, **⇧Tab = `ESC [ Z`** (`\x1b[Z`).
- **Arrows are DECCKM-mode-aware:** `ESC O <d>` in application-cursor mode, else `ESC [ <d>`, where
  `d ∈ {A,B,C,D}` for ↑↓→← (right=`C`, left=`D`).
- **Sticky Ctrl:** arm on tap; next typed char → control byte (`a–z`→`c-96`, `A–Z`→`c-64`, else `c & 0x1f`),
  then disarm; any trailing chars of a multi-char `onData` are sent literally.
- **Paste:** `navigator.clipboard.readText()`; if it contains `\n` **or** length > 200, confirm first;
  then `term.paste(text)` — **xterm.js owns bracketed-paste framing** (single owner; never tmux paste-buffer).
- **Copy:** `term.getSelection()` → `navigator.clipboard.writeText()`. (Paste/copy need a secure context.)
- **Resize:** `xterm.onResize` → `{type:"resize",cols,rows}`. Sizing is `window-size latest` server-side
  (design §18.2), so the active viewer drives size; each tile is a **distinct** pane, so no same-pane
  sizing conflict arises in the grid.

---

## 3. Architecture overview

A client-rendered SPA (design §5.3: Vite + React + TS). Layering keeps the testable logic out of React:

```text
routes/        TanStack Router: /login, / (responsive shell), /t/$serverId/$paneId (mobile terminal)
components/     React: LoginForm, Sidebar, GridView, Terminal, MobileTerminal, MobileKeyBar, SessionList
store/          Zustand: auth (principal + csrfToken), panes (open tiles, focused id, soft cap)
lib/            PURE, framework-free, TDD-first: api-client, ws-terminal, keybar
```

The three `lib/` modules hold all the protocol logic and are unit-tested hard; React components are thin
and lightly tested. xterm.js lives only inside `Terminal.tsx`.

---

## 4. `lib/` — the tested core

### 4.1 `lib/api-client.ts`
A small typed wrapper over `fetch` against `/api/v1`.

- `credentials: "same-origin"` on every call (sends the session cookie).
- On mutating methods (`POST/PUT/PATCH/DELETE`) attach `X-CSRF-Token` from the current csrf token (read
  from the auth store / a module setter). **Not** attached on `GET`.
- Non-2xx → parse `{error}` and `throw new ApiError(status, message)`. `401` is distinguishable so the
  shell can clear auth and redirect to `/login`.
- Functions: `login(username,password) → SessionInfo`, `logout()`, `me() → SessionInfo`,
  `listServers() → ServerSummary[]`, `listSessions(serverId, target?) → Session[]`.
- Types hand-mirrored in `lib/contracts.ts` (already present; extend with `ServerSummary` + `SessionInfo`).

**Tested:** csrf header present on POST and absent on GET; `credentials` set; URL/query construction
(incl. `target` escaping); `ApiError` carries the right status and parsed message; 401 surfaces distinctly.

### 4.2 `lib/ws-terminal.ts`
A thin transport wrapping one `WebSocket` to the relay, decoupled from xterm.js via callbacks.

- `open()` builds the URL: scheme from `location.protocol` (`https:`→`wss:`), host from `location.host`,
  path `/api/v1/servers/{id}/panes/{paneId}/io?target=<enc>`; `binaryType = "arraybuffer"`.
- `onmessage`: binary (`ArrayBuffer`) → `onData(Uint8Array)`; string → ignored (relay does not send control
  to the client today; tolerate it).
- `send(bytes: Uint8Array)` → `ws.send(bytes)` (binary). `resize(cols,rows)` → `ws.send(JSON.stringify(...))`.
- Lifecycle callbacks `onOpen / onClose / onError`.
- **Reconnect:** on unexpected `close` (not a caller-initiated `dispose()`), schedule a reconnect with
  bounded backoff (start ~1.2s, cap ~10s); a `visibilitychange → visible` while disconnected reconnects
  immediately (phone wake). Each (re)connect fires `onOpen` so the consumer can `term.reset()` + send the
  current size; the fresh snapshot then repaints.

**Tested** (with a fake `WebSocket`): URL building (`ws`/`wss`, `target` escaping); `resize` emits exactly
`{type:"resize",cols,rows}`; `send` forwards bytes as binary; an inbound `ArrayBuffer` invokes `onData`;
unexpected close schedules a reconnect, `dispose()` does not; backoff is bounded and resets on success.

### 4.3 `lib/keybar.ts`
Pure functions returning the exact byte sequences from §2.4 — the single source of truth for the key bar
and any keyboard shortcuts. A small `CtrlState` helper models the sticky-Ctrl transition
(`feed(char) → bytes` + disarm). No DOM, no xterm.

**Tested:** each named key → exact bytes (Esc/Tab/⇧Tab/arrows-both-modes/soft-newline/Enter); sticky-Ctrl
across `a–z`, `A–Z`, and a symbol; multi-char input with Ctrl armed sends the control byte then the rest.

---

## 5. Components, routing & layout

### 5.1 `Terminal.tsx` (shared by desktop tiles and mobile)
The only place xterm.js is touched. On mount: create `Terminal` (scrollback 5000, configurable font),
load `@xterm/addon-fit`, `@xterm/addon-web-links`, and **try** `@xterm/addon-webgl` with a **fallback** to
the default renderer if it throws or context-loss fires (design §14.3). `term.open(div)`; wire a
`ws-terminal` client: `onData → term.write`; `term.onData → ws.send` (through the sticky-Ctrl state);
`term.onResize → ws.resize`; `onOpen → term.reset(); fit(); ws.resize(cols,rows); term.focus()`. Ports the
spike's **touch-scroll** (swipe → `term.scrollLines`, suppress page scroll), **paste/copy** (with the
multiline/200-char confirm), and **visualViewport** sizing (keep the prompt above the soft keyboard;
re-`fit()` on rotation). A `ResizeObserver` on the container re-`fit()`s for the desktop grid/expand.
Props: `serverId, paneId, target, active` (active drives focus/visible-resize). Cleanup disposes the WS
(caller-initiated, no reconnect) and the xterm instance.

### 5.2 Responsive shell — `/`
A breakpoint (≈`lg`/1024px, via a `useMediaQuery`) selects the shell:
- **Desktop:** `Sidebar` (servers → sessions tree; click a session → open/focus a tile) beside `GridView`.
- **Mobile:** `SessionList` (flat, searchable by server / session / cwd). Tapping a session navigates to
  the terminal route.

Route guard: on entry, if the auth store is empty, call `me()`; 200 hydrates, 401 → redirect `/login`.
`/login` redirects to `/` if already authenticated.

### 5.3 Desktop grid + expand (keystone — preserves liveness)
`store/panes` holds the **open tiles** (`{serverId, paneId, target, session, window}`) and the
**focused/expanded** tile id. `GridView` renders every open tile as a mounted `<Terminal>` in a responsive
CSS grid (auto-fit; density follows count — naturally 4/6-up). **All tiles are live** (each its own WS).

**Expand is in-state, not a route change:** the focused tile renders full-area while the **other tiles stay
mounted** with `display:none`, so their WebSockets and scrollback survive the expand/collapse. A visible
**"⊟ grid"** control collapses back. **Esc is *not* bound to collapse** — Esc must reach the terminal
(Claude uses it to cancel). Each tile has a close (✕) control that disposes its WS and removes it.

**Soft cap** on simultaneously-live tiles (default **6**, a constant): opening beyond the cap warns and
declines (or the user closes one first). This bounds the agent load — recall M4 deferred the *server-side*
per-principal cap; each live tile spawns one tmux control subprocess on the agent, so the client cap is the
pragmatic Phase-1 bound, and the server-side 429 cap remains a Phase-5 hardening item.

### 5.4 Mobile terminal — `/t/$serverId/$paneId`
Search params carry `target` + `session` (+ window for the header). Full-screen `<Terminal active>` with a
header (`server / session / window / pane`, design §6.2) and the **key bar** (`MobileKeyBar`). The key bar
is the §6.2 row **minus `[Lock]`**: `[Esc] [Ctrl] [Tab] [⇧Tab] [↑] [↓] [←] [→] [⏎ nl] [Paste] [Copy]
[Enter]`. Buttons call `lib/keybar` encodings; `pointerdown.preventDefault()` keeps xterm's hidden textarea
focused (soft keyboard stays up). **Switching sessions:** OS back → `SessionList`, plus in-header prev/next
across the loaded session set. One terminal at a time (one live WS).

### 5.5 `LoginForm.tsx`
Username/password (shadcn `Input`/`Button`/`Label` in a `Card`). Submit → `api.login` → store
`SessionInfo` → navigate `/`. Inline errors for `401 invalid credentials`, `429 too many attempts`,
`403 bad origin` (the last almost always means a dev `external_origin` misconfig — message hints at it).

---

## 6. Tooling, build & CI

- **Tailwind + shadcn/ui:** add Tailwind (config + PostCSS + `index.css` directives) and the shadcn
  primitives actually used (`Button, Input, Label, Card, Dialog`/`Sheet`, `ScrollArea`) with the `cn`
  util + Radix deps. Mobile-first; safe-area-inset padding (`viewport-fit=cover` is already in `index.html`).
- **xterm addons:** `@xterm/addon-web-links` and `@xterm/addon-webgl` added to `package.json` (fit + xterm
  already present).
- **Vitest + Testing Library:** add `vitest`, `jsdom`, `@testing-library/react`, `@testing-library/user-event`,
  `@testing-library/jest-dom`; a `test` block in `vite.config.ts` (or `vitest.config.ts`) with
  `environment: "jsdom"` + a setup file; scripts `test` (watch) and `test:run` (`vitest run`). xterm.js is
  mocked in component tests (no canvas/WebGL in jsdom).
- **CI:** the `web` job runs `npm ci`, `npm run test:run`, **then** `npm run build` (build already runs
  `tsc --noEmit && vite build`, so typecheck stays gated).
- **Dev proxy fix:** in `vite.config.ts`, the `/api` proxy entry must carry `ws: true` (the terminal WS is
  `/api/v1/.../io`); today only the unused `/ws` entry has it. Document the `external_origin: http://localhost:5173`
  dev-hub config requirement (§2.1).
- **Embedding unchanged:** `make embed` still copies `web/dist` → `hubd/internal/webui/dist`; the committed
  placeholder `index.html` stays the tracked one (CI guards that a built `/assets/` index is never committed).

---

## 7. Error handling & edge cases

- **401 anywhere** → clear auth store, redirect `/login`.
- **list 502 `agent unavailable`** → inline "agent unreachable" with a retry; other servers still usable.
- **WS connect/dial failure (502) or drop** → reconnect banner + bounded-backoff reconnect (§4.2); the
  tmux session persists server-side, so reconnect re-bootstraps with a fresh snapshot (design §6.4: phone
  sleep/app-switch reconnects, does not kill the session).
- **403 forbidden** (authz/origin) → non-fatal notice; login surfaces `bad origin` with the dev hint.
- **Large/multiline paste** → confirm (§2.4). **Copy with no selection** → flash hint.
- **Orientation / soft-keyboard** → visualViewport-driven re-fit (§5.1).

---

## 8. Testing strategy (TDD)

Test-first, per the kickoff directive. xterm.js and `WebSocket` are mocked; the protocol logic lives in
`lib/` precisely so it is testable without them.

- **Unit (`lib/`):** `keybar` (every key → exact bytes; sticky Ctrl), `ws-terminal` (URL ws/wss + escaping;
  resize JSON; binary passthrough; reconnect scheduling + bounded backoff with a fake WS), `api-client`
  (csrf only on mutations; credentials; error/401 parsing; URL construction).
- **Component (Testing Library):** `LoginForm` (success navigates, 401/429 show inline errors), `MobileKeyBar`
  (taps invoke the send handler with the right bytes — integrates `keybar`), `SessionList` (renders + filters),
  `GridView` expand (focusing a tile keeps the others mounted — the liveness invariant), `Terminal` smoke
  (mounts, opens the WS, writes inbound bytes to the mocked xterm).
- **CI** runs `test:run` ahead of the build.
- **Manual mobile pass** (design §6.4) is a Phase-1 acceptance item to run on a real iOS/Android device with
  a real Claude session; M5 wires the affordances the spike proved, but the on-device checklist is run as
  acceptance, not automated here.

---

## 9. Live acceptance (mirrors the M4 discipline — and its safety constraints)

Per memory [[dev-host-runs-hub-and-claude]] and the kickoff's CRITICAL SAFETY: this dev host runs the hub
**and** Claude Code's own tmux on the **default** socket. Any live relay test must target the `aigallery`
agent's **`agentmon`-socket** demo panes **only** (`demo-web=%0`, `demo-db=%1`) — **never the default
socket**. The agent is pinned to socket `agentmon`, so it can only reach the demo panes.

Procedure (as M4 did): `make embed` + build the hub; run it on a **loopback test port** against a **copy**
of the live SQLite (`deploy/data`) so the running container + its DB are untouched; set the test hub's
`external_origin` to the loopback origin; log in as `patrik`; then verify in the browser:
- login → dashboard lists `aigallery`;
- desktop grid: open **both** demo panes as **two live tiles**, confirm both stream;
- **expand** `demo-web`, type a marker (e.g. `echo AGENTMON_M5_OK`), confirm it runs and the others stay
  live on collapse;
- narrow the viewport (or device-emulate) → mobile path: list → full-screen terminal → key bar (Esc/Ctrl-C/
  arrows/⏎ nl/Enter) drives the demo pane; reconnect after a forced WS drop re-bootstraps.
Tear down the test hub + DB copy afterward; confirm Claude's default-socket session and the demo panes are
intact. (Full on-device iOS/Android §6.4 checklist is acceptance, run separately.)

---

## 10. Carried M4 minors folded in where M5 touches them

- **Per-principal relay concurrency cap (carried → Phase 5):** more relevant now that the grid opens N live
  WS. M5 adds the **client-side soft cap** (§5.3) as the pragmatic bound; the server-side 429 cap stays a
  Phase-5 follow-up (noted, not built).
- **Origin/CSRF rejects not audited (carried):** unchanged (hub-side observability gap). The SPA simply
  surfaces the 403 to the user (login's `bad origin` hint).
- **Empty `?target=` → hub substitutes `default` (carried):** M5 sends the session's actual `target` from the
  list payload, so it does not rely on the substitution for the demo (whose target is labeled `default`).
- The remaining M4 carries (close-code relay, `agentWSURL` path-prefix, nitpicks) are hub-side and untouched
  by M5.

---

## 11. Acceptance criteria

- `web/` builds (`tsc --noEmit && vite build`) and `npm run test:run` is green; CI runs both.
- Login → dashboard works against a live hub; logout clears the session.
- Servers and their sessions render, identifiable by project name + cwd, searchable.
- A pane opens a live xterm.js terminal through the relay; the scrollback snapshot paints; typing,
  Esc/Ctrl/Tab/⇧Tab/arrows, soft-newline vs Enter, and paste all behave per §2.4.
- Desktop: ≥2 sessions stream live as grid tiles; expanding one keeps the others live; the soft cap holds.
- Mobile: full-screen terminal + key bar; session switching; reconnect after a drop without killing the
  tmux session.
- Live acceptance (§9) passes against the `agentmon`-socket demo panes, default socket untouched.

---

## 12. Open questions / explicit non-decisions

- **WebGL on mobile Safari:** include the addon with a fallback; the on-device §6.4 pass decides whether to
  default it off on iOS. Not a blocker for M5 code.
- **Tablet split view** (design §6.5): M5 maps tablet to the desktop shell above the breakpoint and the
  mobile shell below; a dedicated split view is Phase 4.
- **`/t` deep-link auth on hard refresh:** the route guard's `me()` covers it; if unauthenticated the user
  lands on `/login` and (optionally) is returned to the deep link after auth — return-to is a nicety, not a
  Phase-1 requirement.
