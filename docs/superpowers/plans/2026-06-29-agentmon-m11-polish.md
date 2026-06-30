# M11 — UX polish + M8-deferred Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: subagent-driven-development / executing-plans. Steps use
> checkbox syntax. Executed via an ultracode Workflow (Phase A parallel [1-4]; Phase B parallel [5-6] after
> task 2). Each implement pipelined into an adversarial verify.

**Goal:** Close the Phase-4 polish items + M8 deferrals: focus-next-blocked, per-user prefs (terminal theme/
font + done-alert), mobile sectioned inbox, server-dot REST fallback, terminal-WS `{t:state}` frame, hub
`TouchLastSeen` poller patch. **Keep the desktop grid** (§18-Q12 pivot flagged, not done).

**Tech stack:** Go (`net/http`), TS/React (Vite, zustand + `zustand/middleware` persist [first use],
xterm.js, TanStack Router/Query), vitest.

## Global Constraints

- **Additive / internal only.** No public wire change (the `{t:state}` frame + server `state` field already
  exist). No new endpoints, authz, or secrets.
- Prefs persist to `localStorage` via zustand `persist` (key `agentmon-prefs`); per-device.
- `CGO_ENABLED=0` builds; `-race`, `gofmt` clean (Go). `tsc --noEmit` clean; existing tests stay green.
- Commit footer: `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.
- Do NOT touch the default tmux socket / prod hub / deploy/data in any test (these are unit tests only).

## File Structure

- `hubd/internal/state/poller.go` (modify) + `poller_test.go` — `ServerLister.TouchLastSeen` + calls.
- `web/src/store/prefs.ts` (new) + test — persisted prefs.
- `web/src/lib/terminal-themes.ts` (new) + test — 3 ITheme presets + `themeOf`.
- `web/src/components/XTerm.tsx`, `TerminalView.tsx`, `routes/terminal.tsx`, `GridView.tsx` (modify) — live font/theme.
- `web/src/components/SessionList.tsx` (modify) + test — sectioned inbox.
- `web/src/lib/ws-terminal.ts`, `hooks/useTerminalSession.ts` (modify) + tests — `{t:state}` frame.
- `web/src/hooks/useStateStream.ts`, `hooks/useAttentionAlerts.ts` (modify) — done-too alerts.
- `web/src/lib/focus-next.ts` (new) + test — `nextBlocked`.
- `web/src/components/SettingsPanel.tsx` (new) + test, `components/Sidebar.tsx`, `components/DesktopShell.tsx`,
  `routes/index.tsx` (modify) — settings UI + focus-next + server-dot fallback.

## Parallelization map

Phase A parallel: **T1** (hub), **T2** (prefs+themes+xterm), **T3** (inbox), **T4** (ws-state). Phase B
parallel after T2: **T5** (done-alerts), **T6** (shell integration — owns `routes/index.tsx`). HARD verify on
T1, T5, T6.

---

### Task 1 — Hub poller TouchLastSeen (HARD)

**Files:** `hubd/internal/state/poller.go` (modify), `hubd/internal/state/poller_test.go` (modify).
**Read first:** `poller.go:21-24` (`ServerLister interface { List; Get }`), `pollServer` (~148-159), the
degraded path (~297-302), `registry.go:84-87` (`TouchLastSeen` already on `*Registry`).
**Produces:** `ServerLister` gains `TouchLastSeen(ctx context.Context, id string) error`.

- [ ] **Step 1: failing test** — a fake `ServerLister` records `TouchLastSeen(id)` calls; drive a poll tick
  where `State()` succeeds and assert `TouchLastSeen` was called with the server id; and one where the agent
  404s into the degraded `Sessions()` path and `Sessions()` succeeds → also called. (Mirror the existing
  poller test harness.)
- [ ] **Step 2: run → fail** (`go test ./hubd/internal/state/ -run Poll -v`).
- [ ] **Step 3: implement** — add `TouchLastSeen(ctx, id string) error` to `ServerLister`; in `pollServer`
  after a successful `State()` (and after `resetBackoff`) `_ = p.lister.TouchLastSeen(ctx, id)`; same after a
  successful degraded `Sessions()` poll. `*registry.Registry` already satisfies the method, so no registry
  change — but any OTHER `ServerLister` impl in tests must add a no-op `TouchLastSeen`.
- [ ] **Step 4: run → pass** (`-race`). Full `go test ./hubd/...` green (interface change compiles).
- [ ] **Step 5: commit** — `fix(hub): poller refreshes server last_seen on the background poll (M11 T1)`.

---

### Task 2 — Prefs store + themes + live xterm font/theme (HARD on apply)

**Files:** `web/src/store/prefs.ts` (new), `web/src/store/prefs.test.ts`, `web/src/lib/terminal-themes.ts`
(new), `web/src/lib/terminal-themes.test.ts`, `web/src/components/XTerm.tsx` (modify),
`web/src/components/TerminalView.tsx` (modify), `web/src/routes/terminal.tsx` (modify),
`web/src/components/GridView.tsx` (modify).
**Read first:** `XTerm.tsx:21,47-54,110` (options + the once-`[]` effect), `TerminalView.tsx:6-14,26`,
`routes/terminal.tsx:25` (`fontSize={10}`), `GridView.tsx:62`.
**Produces:**
- `store/prefs.ts`: `usePrefs` (zustand + `persist`, name `agentmon-prefs`): `{ fontSizeDesktop:number(13),
  fontSizeMobile:number(10), terminalTheme:"dark"|"light"|"highContrast"("dark"), alertOnDone:boolean(false) }`
  + setters `setFontSizeDesktop/setFontSizeMobile/setTerminalTheme/setAlertOnDone`.
- `lib/terminal-themes.ts`: `export type ThemeName = "dark"|"light"|"highContrast"`; `export const TERMINAL_THEMES:
  Record<ThemeName, import("@xterm/xterm").ITheme>` (dark = current `{background:"#111418",foreground:"#cdd6e0"}`,
  light, highContrast); `export const themeOf = (n:ThemeName) => TERMINAL_THEMES[n]`.

- [ ] **Step 1: failing tests** — `prefs.test.ts`: defaults; a setter updates + the value rehydrates from a
  mocked `localStorage` (set the store, re-create, assert persisted). `terminal-themes.test.ts`: each preset
  has background+foreground; `themeOf` returns them.
- [ ] **Step 2: run → fail** (`npx vitest run src/store/prefs.test.ts src/lib/terminal-themes.test.ts`).
- [ ] **Step 3: implement** prefs + themes. Then make `XTerm` accept `fontSize` + `theme?: ITheme` and apply
  **live**: keep the mount effect, and add an effect with deps `[fontSize, theme]` that sets
  `term.current.options.fontSize = fontSize; term.current.options.theme = theme; fit.current?.fit();`
  (guard nulls). `TerminalView` gains a `theme?` prop threaded to `XTerm`. `routes/terminal.tsx` reads
  `usePrefs(s=>s.fontSizeMobile)` + `usePrefs(s=>s.terminalTheme)`→`themeOf`; `GridView`'s `<TerminalView>`
  reads `fontSizeDesktop` + theme.
- [ ] **Step 4: run → pass**; `tsc --noEmit` clean; existing XTerm/TerminalView/terminal tests green.
- [ ] **Step 5: commit** — `feat(web): per-user prefs store + live terminal theme/font (M11 T2)`.

---

### Task 3 — Mobile §6.2 sectioned inbox (MEDIUM)

**Files:** `web/src/components/SessionList.tsx` (modify), `web/src/components/SessionList.test.tsx` (modify).
**Read first:** `SessionList.tsx:7-12` (`SessionRow`), `:44` (`stateOf` prop), `:46` (flat
`sortBlockedFirst`), `:53-72` (the `<ul>` row template), `lib/state.ts` `STATE_META` (`:29-35`).
**Produces:** sectioned render — 4 groups in order: **Needs attention**(`blocked`), **Done**(`done`),
**Working**(`working`), **Idle**(`idle`+`unknown`).

- [ ] **Step 1: failing test** — render a list whose rows span all states; assert section headers appear in
  order, each row sits under the right header, an empty section's header is absent, and the search filter
  still narrows rows. (Extend the existing SessionList test.)
- [ ] **Step 2: run → fail.**
- [ ] **Step 3: implement** — after `rows.filter(matchesQuery)`, bucket by `stateOf(row)` into the 4 groups
  (idle+unknown share "Idle"); render, per non-empty group, a header `<li>` (label from `STATE_META` or a
  local map) then its rows (existing row template). Drop the flat `sortBlockedFirst` call (grouping subsumes
  blocked-first). Keep the row markup + keys identical.
- [ ] **Step 4: run → pass.**
- [ ] **Step 5: commit** — `feat(web): mobile sectioned inbox (Needs attention/Done/Working/Idle) (M11 T3)`.

---

### Task 4 — terminal-WS `{t:state}` frame (MEDIUM)

**Files:** `web/src/lib/ws-terminal.ts` (modify), `web/src/lib/ws-terminal.test.ts` (modify),
`web/src/hooks/useTerminalSession.ts` (modify).
**Read first:** `ws-terminal.ts:34-39` (`TerminalSocketHandlers`), `:83` (the string-frame drop site:
`if (typeof ev.data === "string") return;`), `hooks/useTerminalSession.ts:40-51` (handlers wiring),
`store/session-state.ts` (`applyDelta`, `stateKey`), the hub frame shape `{t:"state",state,session}` (M7).
**Produces:** `TerminalSocketHandlers.onState?(frame:{state:string;session:string})`.

- [ ] **Step 1: failing test** — feed the socket a string frame `{"t":"state","state":"blocked","session":
  "api"}` and assert `onState` is called with it; a non-state string / malformed JSON is ignored (no throw);
  binary frames still go to `onData`.
- [ ] **Step 2: run → fail.**
- [ ] **Step 3: implement** — at `:83`, `try { const m = JSON.parse(ev.data); if (m && m.t === "state")
  handlers.onState?.({state:m.state, session:m.session}); } catch {}` then return (still don't treat it as
  output). `useTerminalSession` passes `onState: (f) => useSessionState.getState().applyDelta({ server:
  serverId, target, session: f.session, state: f.state })` (build the delta shape `applyDelta` expects —
  confirm `StateEventFrame` fields against the store) so the open pane's dot updates live.
- [ ] **Step 4: run → pass**; existing ws-terminal/useTerminalSession tests green.
- [ ] **Step 5: commit** — `feat(web): consume terminal-WS {t:state} frame for the focused tile (M11 T4)`.

---

### Task 5 — `done`-too alerts (HARD) — needs T2

**Files:** `web/src/hooks/useStateStream.ts` (modify), `web/src/hooks/useAttentionAlerts.ts` (modify),
`web/src/lib/alerts.ts` (modify) + `alerts.test.ts`.
**Read first:** `useStateStream.ts:26-42` (the `onDelta` transition check), `useAttentionAlerts.ts:19-53`
(title/toast/Notification), `lib/alerts.ts` (`isAttentionTransition`, `blockedTitle`), `store/prefs.ts` (T2).
**Produces:** the alert path also fires on a transition into `done` when `usePrefs.getState().alertOnDone`.

- [ ] **Step 1: failing tests** — `alerts.test.ts`: add `isAlertTransition(prev,next,focusedKey,key,
  alertOnDone)` (generalizes `isAttentionTransition`): true for →blocked always; true for →done iff
  `alertOnDone`; false for →done when `!alertOnDone`; tab-aware; no re-fire. + a `doneTitle(session)` ("✅
  <session> finished"). Keep `isAttentionTransition`/`blockedTitle` as-is (re-export or wrap) so M9 tests
  stay green.
- [ ] **Step 2: run → fail.**
- [ ] **Step 3: implement** — `useStateStream` `onDelta` reads `usePrefs.getState().alertOnDone` and uses
  `isAlertTransition(...)`, passing the *which-state* to `onAttention` (e.g. `cb(frame)` where the frame
  carries `state`). `useAttentionAlerts` picks the title by `frame.state` (`blockedTitle` vs `doneTitle`) and
  the toast/Notification copy accordingly; sound/vibrate unchanged. Web-Push stays blocked-only (no change).
- [ ] **Step 4: run → pass**; M9 alert tests still green.
- [ ] **Step 5: commit** — `feat(web): optional alert on session done (prefs.alertOnDone) (M11 T5)`.

---

### Task 6 — Shell integration: settings UI + focus-next + server-dot fallback (HARD) — needs T2

**Files:** `web/src/lib/focus-next.ts` (new) + test, `web/src/components/SettingsPanel.tsx` (new) + test,
`web/src/components/Sidebar.tsx` (modify), `web/src/components/DesktopShell.tsx` (modify),
`web/src/routes/index.tsx` (modify). **This task OWNS `routes/index.tsx`** (no other task edits it).
**Read first:** `routes/index.tsx:82-95` (header), `:39-43` (`rows`,`snap`,`stateOf`), `:131-147` (desktop/
mobile open), `components/Sidebar.tsx:18-32,41-64` (byServer from rows; session-less hidden),
`DesktopShell.tsx:43-55` (Sidebar+GridView), `store/panes.ts` (`openPane`,`focus`), `lib/state.ts`
(`sortBlockedFirst`,`effectiveSessionState`).
**Produces:** `lib/focus-next.ts`: `nextBlocked(rows, stateOf, currentKey)` → the next `blocked` row after
`currentKey` in blocked-first order (wraps), or `null`; `SettingsPanel`; `Sidebar` renders session-less
servers.

- [ ] **Step 1: failing tests** — `focus-next.test.ts`: with rows in mixed states, `nextBlocked` returns the
  first blocked when `currentKey` is null; the NEXT blocked after the current (wrapping past the end); skips
  non-blocked; `null` when none blocked. `SettingsPanel.test.tsx`: renders font/theme/alertOnDone controls
  bound to `usePrefs` (changing a control updates the store). `Sidebar.test.tsx`: a server with no sessions
  still renders (with a fallback dot from `ServerSummary.state`/unknown).
- [ ] **Step 2: run → fail.**
- [ ] **Step 3: implement** —
  - `focus-next.ts`: order via `sortBlockedFirst(rows, stateOf)`, find index of `currentKey`, return the next
    row whose `stateOf === "blocked"` (cyclic), else null.
  - `SettingsPanel.tsx`: a popover (gear button) — desktop+mobile font steppers, theme `<select>`, alertOnDone
    checkbox; all `usePrefs`-bound.
  - `Sidebar.tsx` + `DesktopShell.tsx`: accept a `servers: ServerSummary[]` prop; seed the server tree from
    ALL servers; a server with no rows renders its name + a dot from `ServerSummary.state ?? "unknown"`.
  - `routes/index.tsx`: pass `servers` into `DesktopShell`; mount `<SettingsPanel/>` + a "Next blocked"
    button in the header; the button calls `nextBlocked(rows, stateOf, snap.focusedKey)` and, on a hit,
    desktop `usePanes.getState().openPane({...}); focus(id)` / mobile `navigate({...})`; disabled when null.
    Add the `n` keydown shortcut (window listener in a `useEffect`, guarded to not fire while typing in an
    input/the terminal).
- [ ] **Step 4: run → pass**; full `npx vitest run` + `tsc --noEmit` green.
- [ ] **Step 5: commit** — `feat(web): settings panel + focus-next-blocked + session-less server dots (M11 T6)`.

---

## Self-Review (plan vs spec)

- **Coverage:** §4 each component → a task (poller T1; prefs/themes/xterm T2; inbox T3; ws-state T4; done-too
  T5; settings+focus-next+server-dot T6). §18-Q12 = keep grid (no task; flagged).
- **Type consistency:** `usePrefs` fields (T2) consumed by T5 (`alertOnDone`) + T6 (font/theme via the
  panel) + T2's terminal wiring; `ThemeName`/`themeOf` (T2) used by terminal.tsx/GridView; `isAlertTransition`
  (T5) supersedes the M9 `isAttentionTransition` call in `useStateStream` while keeping the old export for M9
  tests; `nextBlocked` (T6) signature matches its caller.
- **Conflict guard:** only T6 edits `routes/index.tsx`; T2/T6 both read `usePrefs` but T2 creates it first
  (Phase A) and T6 (Phase B) consumes it.
- **Placeholders:** the "confirm against real code" notes (xterm live-apply, `StateEventFrame` fields, the
  poller test harness) are instructions, not gaps.

## Execution

Workflow: Phase A parallel [T1,T2,T3,T4]; Phase B parallel [T5,T6] (after T2). Each implement → adversarial
verify (HARD T1/T5/T6). Then opus whole-branch → `/multi-review --codex` → SAFE acceptance → local merge +
`m11-carryover.md` + the Phase-4 milestone-memory update.
