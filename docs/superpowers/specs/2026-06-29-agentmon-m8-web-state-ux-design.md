# M8 — web supervision UX (Phase 3c): dots, blocked-first, live SSE, per-principal seen

**Status:** approved (brainstorm, 2026-06-29). Owner waived spec review; proceed to plan → implement.
**Branch:** `phase-3-m8-web-state`, off `main@2ff5dff`.
**Milestone:** M8 is the **web half** of Phase 3 and the last of three sub-milestones — **M6 agent → M7 hub → M8 web**. M6 and M7 are merged & live-accepted. M8 turns the hub state plane M7 serves into the actual supervision UX.
**Scope:** frontend-only (TypeScript / React / vitest). No Go/hub changes. No headless browser on this host — acceptance is vitest + headless SSE/REST/seen contract checks + a manual on-device §6.4 mobile checklist (owner-run).

---

## 1. Goal

Make the existing M5 React SPA a **blocked-first attention queue**:

- render **state dots** on every surface (session dots + rolled-up server dots), §9.1 colors;
- **sort blocked-first** (desktop sidebar + mobile list);
- update **live** from the hub SSE stream (snapshot + deltas + reconnect resync);
- mark sessions **seen on focus** (per-principal `done→idle`), with tab-aware suppression of the view you're actively in.

The SPA stays a **pure API client** (design §19): the hub already derived every state; the browser renders, subscribes, and posts `/seen`. No relay/aggregation logic moves into the browser. (Client-side server-dot rollup is presentation over already-derived session states — see §6.)

---

## 2. Locked decisions (from brainstorming, 2026-06-29)

1. **State-sync model = SSE single source of live truth, into a dedicated store.** Live session state flows SSE → a pure zustand store keyed by `(server,target,session)`. REST (`/servers`, `/sessions`) is first-paint + structure only and is the pre-SSE fallback; the live store wins whenever it has an entry, so a background `/sessions` refetch can never clobber a newer delta. (Chosen over patching the query cache via `setQueryData`, over refetch-on-delta, and over consuming the terminal-WS `{t:"state"}` frame — see §11 alternatives.)
2. **Desktop = keep M5 grid-first; enrich the sidebar.** The M5 live tiled grid + in-state expand/collapse is unchanged. M8 adds dots + server rollup + blocked-first sort + live updates to the **sidebar only**. The §18-Q12 grid-vs-single-terminal pivot stays a Phase-4 decision.
3. **Mobile = enhance the flat list.** Keep the single searchable `SessionList`; add state dots + blocked-first sort. No §6.2 section headers (deferred — the sectioned "agent inbox" is a Phase-4 enrichment).
4. **Seen trigger = on focus.** Mobile: opening the full-screen terminal marks the session seen. Desktop: **expanding a tile (⤢) / focusing it** marks it seen; a tile merely sitting open in the grid keeps its `done` dot until you drill in. The **actively-viewed** view is *continuously* suppressed (never flashes `done` at you, even on a re-alert).
5. **Terminal-WS `{t:"state"}` frame deferred.** SSE already covers the focused session at the same in-process latency; `ws-terminal.ts` keeps ignoring string frames. (Phase-4 latency optimization if ever needed.)

---

## 3. Contracts M8 consumes (verified against M7 hub source, all additive)

- `GET /api/v1/servers` → `[{id,name,labels,enabled,state?}]` — `state` is the server **rollup** dot, `omitempty` (absent before the first poll).
- `GET /api/v1/servers/{id}/sessions` (and `…/{name}`) → each `Session.state?` is the projection's global state **with the requesting principal's seen-projection applied** (falls back to the agent's inline state pre-poll).
- `GET /api/v1/events` (SSE):
  - first frame `event: snapshot`, `data:` = **array** of `{server,target,session,state}` (seen-projected for this principal);
  - then `event: state`, `data:` = a **single** `{server,target,session,state}` delta;
  - `: ping` heartbeats (SSE comments; `EventSource` ignores them).
  - **Seen map captured at connect time** — a `POST /seen` mid-stream is NOT reflected until the `EventSource` reconnects. Reconnect = fresh snapshot (the hub sends no `id:`, so there is no `Last-Event-ID` replay).
- `POST /api/v1/seen` body `{serverId,target,sessionName}` (cookie auth + **CSRF** `X-CSRF-Token`; `serverId`/`sessionName` required, `target` may be `""`; `204` on success).

**Field/value invariants** (mirror `agentmon/shared`):
- `SessionState` ∈ `blocked | done | working | idle | unknown`.
- Rollup priority `blocked(5) > done(4) > working(3) > idle(2) > unknown(1)`; empty/unrecognized → `unknown`.
- The **session join key is the triple `(server, target, session-name)`** — the same key the seen body and SSE frame use. The client always uses **`ServerSummary.id`** for the `server` component: it equals the SSE frame's `server`, the projection's `ServerID`, and the seen body's `serverId` (verified: the poller keys `SessionView.ServerID` and `stateEvent.Server` to the registry server id that `/servers` returns as `ServerSummary.id`). Do **not** key off `Session.server` (the agent-supplied field) — components already have `row.server.id` to hand. `POST /seen` must send the session's own `target` from the payload, never a hardcoded value.

**Verified hub behaviors this design relies on** (read from `hubd/internal/...`):
- A `done→done` **re-alert emits an SSE `state` delta**: `poller.finalize` publishes a `Change` whenever `committed[session]` is true, and a re-alert commits via `pane.DoneSeq > last.DoneSeq` — so a delta fires even when the state value is unchanged. This makes the "clear the optimistic seen mask on any delta for that key" rule safe (the re-alert always re-surfaces).
- REST `/sessions` re-projects seen **per request** with the current seen map, so a refetch after `POST /seen` already returns `idle` for that key (additional safety net behind the optimistic mask).
- The SSE stream is **session-level only** — it carries no server-rollup deltas. Live server dots must therefore be rolled up client-side (§6).

---

## 4. Architecture — pure modules (the testable core)

### 4.1 `lib/contracts.ts` (extend)
```ts
export type SessionState = "blocked" | "done" | "working" | "idle" | "unknown";
// add to existing interfaces:
//   Session.state?: SessionState
//   ServerSummary.state?: SessionState
export interface StateEventFrame { server: string; target: string; session: string; state: SessionState; }
export interface SeenRequest { serverId: string; target: string; sessionName: string; }
```

### 4.2 `lib/state.ts` (new — pure, no React)
- `STATE_PRIORITY: Record<SessionState, number>` = `{blocked:5, done:4, working:3, idle:2, unknown:1}`.
- `rollUp(...states: SessionState[]): SessionState` — max priority; empty/unrecognized → `unknown` (mirrors `shared.RollUp`).
- `STATE_META: Record<SessionState, { label: string; dotClass: string }>` — accessible label + the dot color class per state (blocked→red, done→blue, working→amber, idle→green, unknown→gray).
- `stateKey(server: string, target: string, session: string): string` — the `(server,target,session)` join key. Use a **control-character delimiter** (unit-separator `\u001f`) — tmux session names can contain spaces, so a printable delimiter could let distinct triples collide. The `server` component is always the `ServerSummary.id` (see §3).
- `present(state, { seen, focused }): SessionState` — the **mask selector**: returns `idle` iff `state === "done" && (seen || focused)`; every other state passes through unchanged (only `done` is maskable; `blocked`/`working`/`idle`/`unknown` are never masked).
- `sortBlockedFirst<T>(items: T[], stateOf: (t: T) => SessionState): T[]` — stable sort by `STATE_PRIORITY` descending; ties keep input order.

### 4.3 `lib/sse-state.ts` (new — thin `EventSource` transport, DI'd for tests)
Mirrors `ws-terminal.ts`'s `TerminalSocket` shape (injectable ctor + `loc` for tests; jsdom has no `EventSource`).
```ts
export interface StateStreamHandlers {
  onSnapshot(frames: StateEventFrame[]): void;
  onDelta(frame: StateEventFrame): void;
  onOpen?(): void;
  onError?(): void; // EventSource self-reconnects; this is for a connection indicator only
}
export interface StateStreamDeps { EventSourceCtor?: typeof EventSource; loc?: Loc; }
export class StateStream {
  constructor(handlers: StateStreamHandlers, deps?: StateStreamDeps);
  open(): void;     // connects to /api/v1/events; registers snapshot/state listeners
  dispose(): void;  // closes the EventSource
}
```
- Connects to `/api/v1/events` (same-origin → session cookie sent automatically; GET, no CSRF).
- `addEventListener("snapshot", …)` → parse the JSON array → `onSnapshot`. `addEventListener("state", …)` → parse one frame → `onDelta`. Malformed JSON is caught and dropped (defensive), never throws into the EventSource callback.
- **No custom reconnect/backoff** — `EventSource` reconnects natively; the hub omits `id:` so each reconnect replays the snapshot. Every (re)connect therefore re-fires `onSnapshot`, which is exactly the resync.

### 4.4 `store/session-state.ts` (new — zustand, pure reducer; the load-bearing logic)
```ts
interface SessionStateStore {
  live: Map<string, SessionState>;   // key = stateKey(...); from SSE
  seen: Set<string>;                 // optimistic per-principal seen overrides
  focusedKey: string | null;         // the session whose terminal is the active view
  connected: boolean;
  applySnapshot(frames: StateEventFrame[]): void;  // REPLACE live; CLEAR seen; connected=true
  applyDelta(frame: StateEventFrame): void;        // set live[key]; DELETE seen[key]
  markSeen(key: string): void;                     // seen.add(key)
  setFocusedKey(key: string | null): void;
  setConnected(b: boolean): void;
  reset(): void;                                   // for sign-out
}
// selectors (pure, exported, unit-tested + consumed by components):
effectiveSessionState(store, server, target, session, restFallback?): SessionState
effectiveServerState(store, sessions: {target,name,state?}[], serverId, restFallback?): SessionState
```
- `applySnapshot` replaces `live` wholesale and **clears `seen`** — a fresh snapshot is already server-seen-projected, so optimistic overrides are no longer needed (and a new turn since focus already shows as `done` in the snapshot).
- `applyDelta` patches one key and **clears that key from `seen`** — a delta means the state changed or a turn re-alerted; un-suppress so the re-alert surfaces (verified safe in §3).
- `effectiveSessionState` = `present(live.get(k) ?? restFallback ?? "unknown", { seen: seen.has(k), focused: focusedKey === k })`.
- `effectiveServerState` = `rollUp(...)` over **all** the server's sessions mapped through `effectiveSessionState` (each session handles its own REST fallback, so a server with a mix of live and not-yet-live sessions still rolls up correctly). If the server's **session list is empty/unloaded** (nothing to roll up), fall back to the REST `server.state` so the server dot paints before sessions land.

**Why two masks.** `focusedKey` is *continuous* suppression: the one terminal you're actively viewing is never shown as `done`, even when a re-alert delta arrives (deltas don't clear `focusedKey`). `seen` is the "I looked once, keep it `idle` until the next turn" memory: set when you focus, cleared per-key by the next delta, cleared wholesale by a snapshot. Both are needed; neither alone gives the §9.3 + §14.1 behavior.

---

## 5. Wiring & seen-on-focus

- **`hooks/useStateStream.ts`** (new): mounts one `StateStream` at the authed `ShellRoute`; pumps `onSnapshot→applySnapshot`, `onDelta→applyDelta`, `onOpen/onError→setConnected`; `dispose()` on unmount. One stream serves both desktop and mobile.
- **`lib/api-client.ts`** (extend): `postSeen(req: SeenRequest): Promise<void>` → `request("POST","/seen",req)` (the existing `request` attaches `X-CSRF-Token` to mutating methods automatically).
- **Focus → seen** (`enterFocus(key, req)` helper used by both surfaces):
  1. `setFocusedKey(key)` + optimistic `markSeen(key)` (snappy; survives a failed POST locally),
  2. `postSeen(req).catch(() => {})` — best-effort, fire-and-forget (like the terminal WS; a failed POST just won't persist server-side).
  - **Desktop:** an effect in `GridView`/`DesktopShell` watches `panes.focusedId`; on change → `enterFocus` for the focused pane's `(serverId, target, session)`; on `focusedId === null` → `setFocusedKey(null)`.
  - **Mobile:** `MobileTerminalRoute` calls `enterFocus` on mount and `setFocusedKey(null)` on unmount.
- **Sign-out** clears the store (`reset()`) alongside the existing panes + query-cache reset in `store/auth.ts`'s `resetGridAndCache()`.

---

## 6. Presentational changes (components stay pure — DI `stateOf`)

`Sidebar` and `SessionList` already receive `rows`. They additionally receive a `stateOf(row) => SessionState` prop, wired by the container (`ShellRoute`/`DesktopShell`) from the store. This keeps the components pure and unit-testable with a fake `stateOf` (the M5 discipline).

- **`components/StateDot.tsx`** (new): a small themeable circle — `<span role="img" aria-label={STATE_META[state].label} className={…rounded-full + STATE_META[state].dotClass}>`. Not raw emoji (consistent rendering, themeable, assertable by `aria-label`). Maps to §9.1 semantics.
- **`Sidebar.tsx`** (enrich, desktop):
  - per-session `StateDot` (effective state);
  - a **server rollup `StateDot`** beside each server header, computed client-side via `effectiveServerState` over that server's sessions (seeded by REST `server.state`);
  - **blocked-first sort** of sessions within each server *and* of the servers themselves (a server holding a blocked session floats up);
  - search unchanged.
- **`SessionList.tsx`** (enrich, mobile): per-row `StateDot`; **blocked-first sort** (stable, so within a state group the existing order holds); search unchanged.
- **`GridView.tsx`**: a `StateDot` in each tile header (the tile session's effective state). The expand/collapse/close affordances are unchanged.
- **`routes/index.tsx` (`ShellRoute`)**: mount `useStateStream()`; build `stateOf` from the store (`effectiveSessionState` with REST `row.session.state` fallback); pass it to `SessionList` and `DesktopShell`→`Sidebar`.

No change is required to the sessions query's `staleTime`/refetch policy: because the live store is a separate source, a background `/sessions` refetch refreshes the *list/structure* without clobbering live *state*.

---

## 7. Data flow (end to end)

```
GET /servers, /sessions (REST)  ──► TanStack Query ──► rows (list + structure + fallback state)
GET /events (SSE)  ──► StateStream ──► session-state store (live/seen/focusedKey)
                                              │
            stateOf(row) = effectiveSessionState(store, row, row.session.state)
                                              ▼
   Sidebar / SessionList / GridView  ──► StateDot + blocked-first sort
   server header dot = effectiveServerState(store, server's sessions, server.state)

focus a terminal (⤢ desktop / open mobile) ─► enterFocus(key): setFocusedKey + markSeen + POST /seen(CSRF)
SSE delta for key ─► applyDelta: live[key]=…, seen.delete(key)   (re-alert re-surfaces)
SSE reconnect ─► onSnapshot ─► applySnapshot: replace live, clear seen   (resync; server already seen-projected)
```

---

## 8. Testing (vitest) — risk-tiered

**Hard adversarial verify** (load-bearing async/logic):
- `store/session-state.test.ts` — snapshot replace + clears seen; delta patch + clears that key's seen; `markSeen`/`setFocusedKey`; `present`/selectors: `done`+seen→idle, `done`+focused→idle, focused continuous suppression across a re-alert delta, `blocked`/`working` never masked, REST fallback before first snapshot, reconnect drops a stale optimistic mask.
- `lib/sse-state.test.ts` — fake `EventSource`: snapshot listener parses an array → `onSnapshot`; state listener parses one → `onDelta`; reconnect (re-fired snapshot) re-syncs; malformed JSON dropped; `dispose()` closes.
- seen-on-focus wiring — focus change → `setFocusedKey` + optimistic `markSeen` + `postSeen` called with the correct `{serverId,target,sessionName}`; POST failure is swallowed; unfocus clears `focusedKey`.

**Light verify** (pure presentational): `lib/state.ts` (rollup priority, `present`, `stateKey` collision-safety, `sortBlockedFirst` stability), `StateDot` (aria-label/color per state), `Sidebar` (server rollup dot + blocked-first sort of sessions and servers — no test exists today), extend `SessionList` (blocked-first + dots + search still works) and `GridView` (header dot).

All new/changed code is TDD (test first). The full suite (existing 53 + new) must stay green; `tsc --noEmit` clean; `vite build` emits `dist/`.

---

## 9. Acceptance (no headless browser on this host)

1. **vitest** full suite green + `tsc --noEmit` + `vite build`.
2. **Headless SSE/REST/seen contract probe** (node, mirroring M5's ws-probe), against a **scratch hub on a loopback port** built to scratch + a **`.backup` COPY** of `deploy/data`, with state driven by **synthesized hooks to a scratch agent on a throwaway tmux socket** (M7's live-acceptance method). Asserts:
   - correct-Origin login → cookie + `csrfToken`;
   - `/servers` + `/sessions` carry `state` after a poll;
   - `GET /events` yields `event: snapshot` (array) then `event: state` deltas as state is driven (`working→done→blocked`), seen-projected;
   - `POST /seen` (with `X-CSRF-Token`) → `204`; the projection then reads `idle` for that key (REST refetch and/or a fresh SSE snapshot after reconnect);
   - a new-turn `done` re-alert re-emits a `state` delta.
3. **Manual on-device** iOS/Android §6.4 checklist — flagged for the owner (cannot be automated here).

**CRITICAL SAFETY** (memory `dev-host-runs-hub-and-claude` + `live-deployment`): this host runs the **production hub** (docker, behind Caddy at `https://agentmon.runald.net`) and Claude's own tmux on the **default** socket (session 0). All live testing uses a scratch hub on loopback + a COPY of `deploy/data` + a throwaway tmux socket — **never** session 0, the `agentmon` demo panes, `~/.claude/settings.json`, or prod `deploy/data`. Do **not** redeploy the prod hub or agent without owner say-so.

---

## 10. Scope boundaries — explicitly OUT of M8 (Phase 4+)

Toast / sound / vibrate / Web-Push attention alerts (§18-Q9); "focus next blocked"; per-user layout/prefs; terminal theme/font; the desktop grid-vs-inbox pivot (§18-Q12); the mobile §6.2 sectioned inbox; installable PWA. **Hub `TouchLastSeen` poller gap** stays deferred (it's a Go/hub patch; M8 is web-only). Read-only lock / `[Lock]` button (waits for real authz, per M5).

---

## 11. Alternatives considered (and rejected)

- **(b) SSE deltas invalidate queries → refetch.** Seen "just works" (REST re-projects per-request), but a refetch storm + dot-update lag on every state change and a heavier full-list fetch per delta. Architecturally inferior for a live dashboard.
- **(a-literal) SSE patches the `["sessions",id]` cache via `setQueryData`.** Entangles list-freshness with state-freshness; needs `staleTime: Infinity` to dodge a refetch-vs-delta clobber, which then costs list auto-refresh, and scatters cache writes. The dedicated store keeps a clean separation and one pure, testable selector.
- **(c) Also consume the terminal-WS `{t:"state"}` frame for the focused tile.** A second state source to reconcile for a marginal (in-process) latency gain; the frame is delta-only and the least-tested hub path. Deferred to Phase 4.

---

## 12. Build sequence (becomes the plan's work-list → workflow)

1. **Foundation** (serial, everything imports it): `lib/contracts.ts` additions + `lib/state.ts` + their tests.
2. **Parallel fan-out** (disjoint new files): `lib/sse-state.ts`, `store/session-state.ts`, `components/StateDot.tsx` (+ tests).
3. **Components** (parallel; disjoint files): `Sidebar.tsx`, `SessionList.tsx`, `GridView.tsx` (+ tests).
4. **Integration** (touches `routes/index.tsx`, `hooks/useStateStream.ts`, `lib/api-client.ts`, `routes/terminal.tsx`, `store/auth.ts`): `useStateStream`, `postSeen`, focus-seen wiring.
5. **Verify**: full suite + acceptance (§9).

The plan assigns parallelism and worktree isolation (only where tasks mutate the same file); each implement task is pipelined with an adversarial verify, risk-tiered per §8.
