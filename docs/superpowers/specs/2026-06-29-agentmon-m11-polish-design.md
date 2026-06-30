# M11 — UX polish + M8-deferred (Phase 4c): the last v1 gaps

## 1. Goal

Close the remaining §17-Phase-4 polish items and fold in the M8 deferrals: **focus-next-blocked**, **per-user
prefs** (terminal theme/font + a `done`-too alert toggle), the **mobile §6.2 sectioned inbox**, the
**server-dot REST fallback**, the **terminal-WS `{t:state}` frame**, and the **hub `TouchLastSeen` poller
patch**. M11 is the **third and final Phase-4 sub-milestone** (M9 → M10 → **M11**).

These are mostly small, independent web changes plus one focused hub patch. None change the data plane or the
public wire contracts (the `{t:state}` frame and the server `state` field already exist).

## 2. Locked decisions (conservative — made autonomously while the owner is away)

- **§18-Q12 desktop layout = KEEP the M5 grid-first + sidebar.** A pivot to inbox+single-terminal is a major
  UX change and is **not** something to do unattended; the design explicitly frames the grid as optional
  (§6.5/§18-Q12). M11 polishes *within* the existing grid. **Flagged for the owner** to revisit the pivot.
- **Prefs persistence = `localStorage` (zustand `persist`), per-device.** v1 is single-user, one device at a
  time (§11.7), so per-device prefs are sufficient and far simpler than a hub prefs table/endpoint (which
  §5.3 anticipates as a later enhancement). The persisted prefs: `fontSizeDesktop` (default 13),
  `fontSizeMobile` (default 10 — the owner's chosen mobile size becomes the configurable default),
  `terminalTheme` (`dark`|`light`|`highContrast`, default `dark`), `alertOnDone` (default `false`).
- **Themes = 3 presets** (`dark` = the current `#111418`/`#cdd6e0`, `light`, `highContrast`) as xterm `ITheme`
  objects (§14.3 "minimum contrast themes"). Live-applied via `term.options` (no remount).
- **`done`-too alerts** extend the M9 blocked-only alert when `alertOnDone` is on — same tab-aware, tiers
  1/2 path; Web-Push stays **blocked-only** (a `done` push would be noisy; revisit later).
- **Server-dot REST fallback = render session-less servers** in the sidebar from `ServerSummary.state` (or
  `unknown`) instead of hiding them — the exact M8 deferral. No hub change needed for the dot (the M7
  `/servers` overlay already carries `state`; a session-less server simply has no projection entry → renders
  `unknown`).
- **`{t:state}` terminal-WS frame** is consumed into the **same `session-state` store** (`applyDelta`) so the
  focused tile's dot updates at terminal-WS latency. SSE remains the source of truth; this is purely a
  latency nicety for the open pane (idempotent current-state).

## 3. Contracts touched

All additive / internal — **no public wire change**:
- The hub already emits the terminal-WS `{"t":"state","state","session"}` text frame (M7, `ws.go`); M11 makes
  the web *read* it. The server `ServerSummary.state` field already exists (M7). The hub `TouchLastSeen`
  patch is internal (poller calls an existing registry method).
- New web-internal `store/prefs.ts` (persisted) — not a wire contract.

## 4. Components (per item)

**Hub — `state/poller.go`:** add `TouchLastSeen(ctx, id) error` to the `ServerLister` interface
(`*registry.Registry` already satisfies it) and call `_ = p.lister.TouchLastSeen(ctx, id)` after a successful
`State()` poll and after a successful degraded `Sessions()` poll, so an actively-polled server's
`last_seen_at` refreshes without a browser hit.

**Web — prefs foundation (`store/prefs.ts`, new):** zustand + `persist` (localStorage key `agentmon-prefs`)
exposing `fontSizeDesktop`, `fontSizeMobile`, `terminalTheme`, `alertOnDone` + setters. `lib/terminal-themes.ts`
(new): the 3 `ITheme` presets + a `themeOf(name)` helper.

**Web — terminal theme/font (`XTerm.tsx`, `TerminalView.tsx`, `routes/terminal.tsx`, `GridView.tsx`):**
`XTerm` accepts `fontSize` + `theme` and applies them live (an effect that sets `term.options.fontSize`/
`term.options.theme` + refits on change, not only at mount). `TerminalView` threads `theme`. The mobile route
+ desktop grid read the font size from prefs (mobile default 10, desktop 13) and the theme from prefs.

**Web — settings UI (`components/SettingsPanel.tsx`, new; mounted in `routes/index.tsx` header):** a small
popover/dialog (gear button beside `EnableAlerts`) — font-size steppers (desktop + mobile), a theme select,
and an "Alert when a session finishes (done)" checkbox — all bound to `store/prefs`.

**Web — `done`-too alerts (`hooks/useStateStream.ts`, `hooks/useAttentionAlerts.ts`):** in `onDelta`, when
`usePrefs.getState().alertOnDone`, also fire on a transition into `done` for a non-focused key (reusing the
pure transition helper, generalized to a target state); the toast/Notification copy reads "finished" for
`done` vs "needs input" for `blocked`.

**Web — mobile sectioned inbox (`components/SessionList.tsx`):** replace the flat `sortBlockedFirst` render
with four grouped sections — **Needs attention** (`blocked`), **Done** (`done`), **Working** (`working`),
**Idle** (`idle`+`unknown`) — each a labeled header (from `STATE_META`) above its rows; empty sections are
omitted; the search filter still applies. Row template unchanged.

**Web — server-dot REST fallback (`components/Sidebar.tsx`, `components/DesktopShell.tsx`,
`routes/index.tsx`):** pass the `servers` list into `DesktopShell`→`Sidebar`; seed the server tree from
**all** servers (not only those with sessions); a session-less server renders a row with its
`ServerSummary.state` dot (or `unknown`) and no children.

**Web — `{t:state}` frame (`lib/ws-terminal.ts`, `hooks/useTerminalSession.ts`):** add an `onState?(frame)`
handler to `TerminalSocketHandlers`; parse the string frame at the existing drop site
(`if (typeof ev.data === "string")`) as `{t:"state",state,session}`; `useTerminalSession` wires it to
`useSessionState.getState().applyDelta(...)` for the open pane's key (so `GridView`'s dot updates live).

**Web — focus-next-blocked (`routes/index.tsx` + `lib/focus-next.ts`, new pure helper):** `nextBlocked(rows,
stateOf, currentKey)` returns the next `blocked` row after the current focus in blocked-first order
(wrapping). A "Next blocked ⟶" button in the shell header (+ the `n` key): desktop `usePanes.openPane(...)`
then `focus(id)`; mobile `navigate` to the terminal route. No-op (disabled) when nothing is blocked.

## 5. Security / safety

Pure-polish: no new endpoints, no new authz surface, no secrets. Prefs are non-sensitive UI state in
localStorage. The `{t:state}` frame is already authenticated (it rides the existing authorized terminal WS)
and renders as text (no XSS). The hub `TouchLastSeen` patch only writes a timestamp. No change to the create/
relay/alert security surfaces.

## 6. Testing — risk-tiered (Go + vitest)

- HARD: `lib/focus-next.ts` `nextBlocked` (wrap-around, skip non-blocked, current-at-end, none-blocked → null,
  excludes the focused); the `done`-too transition gate (fires on →done only when `alertOnDone`, tab-aware,
  no double-fire). Hub `poller` TouchLastSeen called on the success + degraded paths (fake lister records).
- MEDIUM: `store/prefs.ts` persist/rehydrate (mock localStorage) + defaults; `SessionList` sectioning (rows
  bucket into the right sections, empty sections omitted, filter applies); `Sidebar` renders session-less
  servers with a fallback dot; `ws-terminal` parses a `{t:state}` string frame and calls `onState` (ignores
  malformed); `XTerm`/`TerminalView` apply font/theme.
- LIGHT: `SettingsPanel` (renders, toggles prefs), `terminal-themes` presets shape.

## 7. Acceptance (SAFE)

Static: full Go suite (`-race`, vet, gofmt) + web (tsc, vitest, build) green. Hub: a quick `poller` unit-test
proves `TouchLastSeen` fires on the poll path (no live agent needed). Web render is vitest-proxied (no
headless browser). **Flagged for the owner (on-device):** the visual polish — sectioned inbox layout, theme/
font live-apply, focus-next navigation, session-less-server dot — and the §18-Q12 grid-vs-inbox decision.

## 8. Scope boundaries — OUT of M11 / Phase 4

- The §18-Q12 desktop **grid→inbox pivot** (kept the grid; flagged).
- **Hub-persisted** prefs (localStorage only); a prefs table/endpoint is a later enhancement.
- `done` **Web-Push** (foreground `done` alert only, opt-in; push stays blocked-only).
- Session **kill/delete** UI; multi-target; OIDC/RBAC — all post-v1.

## 9. Build sequence (work-list → writing-plans → Workflow)

Phased to avoid `routes/index.tsx` write-conflicts (it's the header hotspot for the settings + next-blocked
buttons + passing `servers`):
1. **Hub** — poller `TouchLastSeen` (`poller.go` + test). [independent]
2. **Prefs + themes** — `store/prefs.ts` + `lib/terminal-themes.ts` + `XTerm`/`TerminalView` live font/theme.
   [foundation; no index.tsx]
3. **Sectioned inbox** — `SessionList.tsx`. [independent]
4. **`{t:state}` frame** — `ws-terminal.ts` + `useTerminalSession.ts`. [independent]
5. **`done`-too alerts** — `useStateStream.ts` + `useAttentionAlerts.ts` (+ the pure helper). [needs #2 prefs]
6. **Shell integration** — `SettingsPanel.tsx` + focus-next (`lib/focus-next.ts`) + server-dot fallback
   (`Sidebar.tsx`, `DesktopShell.tsx`) + all `routes/index.tsx` header/wiring. [needs #2 prefs; owns index.tsx]

Workflow: Phase A parallel [1, 2, 3, 4]; Phase B parallel [5, 6] (after 2). Each implement pipelined into an
adversarial verify (HARD on 1/5/6-focus-next). Then opus whole-branch → `/multi-review --codex` → SAFE
acceptance → local merge + carryover.
