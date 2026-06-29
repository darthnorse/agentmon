# M8 → carry-over (web supervision UX — Phase 3c). **Phase 3 COMPLETE.**

M8 (the **web half** of Phase 3 — turn the M7 hub state plane into the supervision UX) is complete,
reviewed (workflow per-task adversarial verify + opus whole-branch + `/multi-review --codex`),
**safe-accepted on this host**, and merged to `main`. M8 is the last of three Phase-3 sub-milestones:
**M6 agent → M7 hub → M8 web — all done.** This captures the contract, decisions, deferrals, and the
Phase-4 handoff.

Branch: `phase-3-m8-web-state`, off `main@2ff5dff`. Spec:
`docs/superpowers/specs/2026-06-29-agentmon-m8-web-state-ux-design.md`. Plan:
`docs/superpowers/plans/2026-06-29-agentmon-m8-web-state-ux.md`.

## The KEY DECISION (resolved with owner): state-sync = SSE single source of truth → dedicated store
Live session state flows **SSE (`GET /api/v1/events`) → a pure zustand store** (`store/session-state.ts`)
keyed by the triple `(ServerSummary.id, target, session-name)`. REST (`/servers`, `/sessions`) is
first-paint + structure only; the live store wins whenever it has an entry, so a background `/sessions`
refetch can never clobber a newer delta. Server dots roll up **client-side** from live session states.
Seen is an **optimistic `done→idle` mask** + `POST /seen`, with the actively-viewed session
**continuously suppressed** (`focusedKey`). The terminal-WS `{t:"state"}` frame is **deferred** (SSE covers
the focused session at the same in-process latency). Chosen over `setQueryData`-into-the-query-cache (clobber
race) and refetch-on-delta (storm + lag).

Other locked design decisions (brainstorm): **desktop keeps M5 grid-first + enriched sidebar** (the
§18-Q12 grid-vs-single-terminal pivot stays Phase 4); **mobile enhances the flat list** (dots +
blocked-first, no §6.2 section headers yet); **seen-on-focus** = desktop expand (⤢) / mobile open terminal.

## What shipped (13 SDD tasks via an ultracode Workflow, TDD throughout; 90 web tests)
Pure core: `lib/contracts.ts` (`SessionState`, `Session.state?`, `ServerSummary.state?`, `StateEventFrame`,
`SeenRequest`); `lib/state.ts` (`STATE_PRIORITY`, `rollUp`, `STATE_META`, `stateKey` (`\u001f`-delimited),
`present` mask, `normalizeState` clamp, `effectiveSessionState`, `sortBlockedFirst`). Transport/store:
`lib/sse-state.ts` (`StateStream` — DI'd `EventSource`, self-reconnect replays snapshot); `store/session-state.ts`
(immutable `applySnapshot`/`applyDelta`/`markSeen`/`setFocusedKey`/`reset` + `useStateSnapshot`). UI:
`components/StateDot.tsx`; enriched `SessionList`/`Sidebar`/`GridView` (dots + blocked-first + server rollup
dot); `DesktopShell` (seen-on-expand) + `ShellRoute` (live `stateOf`). Wiring: `hooks/useStateStream.ts`
(one stream) + `components/AuthLayout.tsx` (mounts it around the auth `Outlet`, so the stream survives
mobile list↔terminal nav); `hooks/useFocusedSeen.ts` (`useLayoutEffect`: focus + optimistic seen + best-effort
`postSeen`); `api-client.postSeen`; `store/auth.ts` resets the live store on sign-out; `store/panes.ts`
carries a REST `state` first-paint fallback; `router.tsx` auth route → `AuthLayout`.

## Public contracts M8 consumes (verified against M7 hub source — all additive, NO hub change)
- `GET /api/v1/servers` → `[{id,name,labels,enabled,state?}]` (`state` = server rollup dot, omitempty).
- `GET /api/v1/servers/{id}/sessions` → each `Session.state?` = projection state, **seen-projected** for the
  principal (falls back to the agent inline state pre-poll).
- `GET /api/v1/events` (SSE): `event: snapshot` `data:` = **array** of `{server,target,session,state}`
  (seen-projected at **connect time**); then `event: state` single deltas; `: ping` heartbeats. EventSource
  self-reconnects → re-snapshot (no `Last-Event-ID`).
- `POST /api/v1/seen` `{serverId,target,sessionName}` (+ `X-CSRF-Token`; `204`).
- Wire join key everywhere = `ServerSummary.id` (= SSE `server` = seen `serverId` = projection `ServerID`).
- States `blocked(5) > done(4) > working(3) > idle(2) > unknown(1)`; only `done` is maskable.

## Invariants the web upholds (don't regress)
- **Store immutability**: every mutation allocates a fresh `Map`/`Set` so zustand selectors re-render.
- **Two masks**: `seen` (set on focus; cleared per-key by the next delta = re-alert; cleared wholesale by a
  snapshot) + `focusedKey` (continuous suppression of the active view; deltas never clear it). A `done→done`
  re-alert **does** emit an SSE delta (hub `poller.finalize` publishes when `committed[session]`), so the
  optimistic mask correctly re-surfaces it.
- **Crash safety**: `normalizeState` clamps any out-of-enum/empty wire state to `unknown` at the store
  boundary + selector + a `StateDot` fallback — a garbage agent state cannot white-screen the dashboard or
  NaN-corrupt the sort.
- **No render loop**: `useFocusedSeen`'s effect dep is the panes-derived key (not the store `focusedKey`);
  `setFocusedKey` writes a store `DesktopShell` doesn't subscribe to.
- **Security**: CSRF on `POST /seen`, none on the GET SSE; `EventSource` same-origin `withCredentials`; state
  renders as React text (no XSS); no tokens in the bundle.

## Review path
**Implement workflow** (ultracode): 13 tasks across 4 phases (foundation → core → surfaces → integration),
each implement agent **pipelined into an adversarial verify** (hard tier on store/SSE/seen/wiring, light on
presentational). One blocking issue caught by verify (a `tsc` `as const`-on-conditional in two test files,
fixed). **Opus whole-branch review**: **Ready to merge YES**, no Critical/Important — traced the full pipeline
against the M7 hub source and confirmed correctness + contract conformance. **`/multi-review --codex`**
(feature-dev:code-reviewer + code-simplifier + deep-scan + codex gpt-5.5): **6 fixes applied** (commit
`80f647f`) — out-of-enum crash guard (`normalizeState`, deep-scan+opus), focus one-frame flash
(`useLayoutEffect`, codex+opus), grid REST first-paint fallback (codex+opus), `useStateSnapshot` dedup
(simplifier), SSE delta array-guard (codex), unused-import cleanup — each with a regression test where it
was a bug. The correctness lens returned **clean**.

## SAFE acceptance — DONE this session (2026-06-29, on this host)
No Go/hub change (`git diff main…HEAD` = `web/` + docs only), so the hub binary is identical to the
live-accepted M7. **vitest 90/90 green; `tsc --noEmit` clean; `vite build` OK.** Source-level contract
conformance independently verified by opus + deep-scan against `events.go`/`seen.go`/`poller.go`/`shared`.
**Runtime contract probe (18/18)** against a **scratch hub on a loopback port (`127.0.0.1:19388`) with a
FRESH empty DB** (zero prod data, no prod-agent polling, **no tmux/scratch-agent** — the safest possible
setup): the hub serves the **M8 SPA** (`GET /` + the M8 `index-*.js` bundle); wrong-Origin login → 403,
correct → 200 + HttpOnly cookie + `csrfToken`; `/me`, `/servers` (`[]`); SSE `event: snapshot` →
`data: []` (array) + `: ping`; `POST /seen` → 403 without CSRF, 204 with CSRF + `{serverId,target,sessionName}`.
Post-test: prod hub container Up, prod agent active, default tmux session 0 intact, `deploy/data` never
opened, scratch torn down, repo clean.

**FLAGGED for the owner (not run here):**
1. The full **hook-driven live state-flow replay** (working→done→blocked→seen→`done` re-alert over SSE). It
   re-validates **M7's already-accepted** path on the unchanged hub; best run with your oversight since it
   needs a scratch agent on a throwaway tmux socket on this prod host.
2. The on-device **iOS/Android §6.4 mobile checklist** (touch scroll, key bar, reconnect, cross-session
   alert) — needs a real device; there is no headless browser on this host, so the React render itself was
   never machine-tested (vitest + contract probe are the proxy, as in M5/M7).

## Deferred (with rationale)
- **Server-dot REST fallback** (`effectiveServerState` / `ServerSummary.state`) — surfaced by 3 reviewers but
  the only real fix is to **render session-less servers** from REST `state`, a behavior change beyond §17
  (M5 deliberately hides servers with no sessions; it self-heals once `/sessions` loads). `ServerSummary.state`
  is kept as the wire mirror (commented). Revisit in Phase 4 if a session-less server should still show a dot.
- **Terminal-WS `{t:"state"}` frame** — Phase-4 latency optimization for the one focused tile; SSE already
  covers it. `ws-terminal.ts` still ignores string frames.
- **Mobile §6.2 sectioned inbox** ("Needs attention / Done / Working / Idle" headers) — Phase 4; M8 ships the
  blocked-first flat list.
- **Hub `TouchLastSeen` poller gap** (carried from M7) — still deferred; it's a Go/hub patch and M8 is
  web-only. A server's `last_seen_at` only refreshes on browser-driven handlers, not the every-3s `/state`
  poll. Fold into a hub patch or Phase 4.
- **`connected` store field** — written by `useStateStream` onOpen/onError, read by no UI yet; harmless
  forward-scaffolding for a Phase-4 SSE connection indicator.
- **Nitpicks left**: `STATE_META.label` (kept as an a11y indirection); `Sidebar` groups by `server.name`
  not `id` (pre-existing M5 pattern; names unique in practice).

## Phase 4 (next milestone — UX polish & alerts, design §17 Phase 4 / §18-Q9)
Toast / sound / vibrate / **Web-Push attention alerts** (the core "a *different* session went blocked reaches
you" loop, §18-Q9 — M7+M8 laid the data path); **"focus next blocked"**; per-user **layout/prefs**;
terminal **theme/font**; the desktop **grid-vs-inbox pivot** decision (§18-Q12); the mobile **sectioned
inbox**; installable **PWA**; the server-dot REST fallback + the WS state frame above.

## Verification at merge
Full web suite green (20 files / **90 tests**, pristine); `tsc --noEmit` clean; `vite build` emits `dist/`
(only the pre-existing xterm >500 KB chunk warning, a known M5 deferral). Go side untouched (M8 changed no
hub/agent code). Runtime web↔hub contract probe accepted on this host against a loopback scratch hub + fresh
DB. **NOT pushed and NOT deployed** — local merge only; the prod hub redeploy (`docker compose up -d --build`)
remains owner-only.
