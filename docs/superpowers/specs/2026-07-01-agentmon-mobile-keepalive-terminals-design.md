# Mobile keep-alive terminals — design

**Date:** 2026-07-01
**Status:** Approved (design), ready for implementation plan
**Scope:** Web SPA (`web/`), mobile terminal experience only. No hub/agent changes.

## Problem

On mobile, switching between session tabs (the header tab strip added in the
mobile-session-tabs feature) shows a brief "connecting…" state. That is a direct
consequence of the current implementation: each session is a route
(`/t/:serverId/:paneId`) and switching tabs `navigate`s to a new pane, and the
`TerminalView` is `key`ed by `serverId:target:paneId`, so a switch **remounts** the
terminal → fresh `XTerm` → fresh WebSocket → the socket must open and pull the pane
snapshot before it can paint. That reconnect gap is the flash.

The desktop grid does not have this problem: `GridView` keeps **every** open tile
mounted with its own live socket + scrollback (`display:none` on the non-visible
ones), so revealing one is instant. This feature brings that keep-mounted model to
the mobile terminal view.

## Goals

- **Instant, flash-free switching between tabs while in the mobile terminal view.**
- Preserve the existing mobile flow: list → tap a session → terminal view with a tab
  strip; **‹ Back** returns to the list.
- Preserve rename, the mobile key bar, focus/seen tracking, and reconnect-on-drop.

## Non-goals (explicit Level-1 scope)

- **Not** keeping sessions warm across the list↔terminal boundary. Going ‹ Back and
  re-opening a session is still a fresh connect. (Level 2 — a persistent terminal host
  that outlives route changes — was considered and deferred as YAGNI.)
- No changes to desktop `GridView` behavior or the desktop `usePanes` store.
- No deep-linking changes (push notifications open `/`, not `/t/…`; nothing external
  deep-links a terminal, so we are free to switch in-state).

## Chosen approach (Approach 1: route-local mounted pane pool)

Keep `/t/:serverId/:paneId` as the **entry point** only. Inside the mobile terminal
view, maintain a small **route-local pool** of open panes and render each as its own
persistent `TerminalView`, single-visible (focused pane shown, the rest `display:none`)
— the same keep-mounted technique `GridView` uses. Switching tabs changes the focused
pane **in-state** (no navigation), so the target terminal is already mounted +
connected → instant reveal, no reconnect, and no cross-session bleed (each pane only
ever shows its own content).

Because the pool is route-local, tapping **‹ Back** (`navigate({ to: "/" })`) unmounts
the route and tears down every pooled socket — which *is* Level 1, with no cleanup
bookkeeping.

Rejected alternatives:
- **Reuse the desktop `usePanes` store for mobile** — entangles mobile with desktop
  grid state (resize carries panes over) and fights the store's persist-forever design
  (Level-1 "die on Back" would need explicit closing). More coupling, different
  presentation.
- **Persistent terminal host above the router** — enables Level 2 but is over-built for
  the Level-1 we chose; portals/persistent-host complexity. Deferred.

## Warming policy: eager, capped at 4

On entering the terminal view, **eagerly** connect up to `MOBILE_POOL_CAP = 4`
sessions so every switch is instant from the first tap. The warm set is:
`{ focused session } ∪ (sessions from the list in strip order, until the pool reaches
the cap)`. The focused session is always included. At the typical 1–3 sessions this
warms everything; beyond the cap it falls back to lazy (a not-yet-pooled tab connects
on first focus, LRU-evicting the least-recently-focused non-focused pane).

Cap rationale: a mobile viewer holding ≤4 live relay sockets is well within the hub's
per-principal relay cap (32) and mirrors the desktop grid's own bounded pool (6).

## Architecture

### New: `useMobilePanePool` hook (`web/src/hooks/useMobilePanePool.ts`)

Route-local pool state (plain React state, dies with the route → Level 1). Pure,
unit-testable logic; no global store.

```ts
interface PoolPane {
  serverId: string;
  target: string;
  paneId: string;
  session: string;   // display name at open time; label follows focus/URL, see Rename
  serverName: string;
}
interface MobilePanePool {
  panes: PoolPane[];          // insertion order (stable render order)
  focusedId: string | null;   // paneIdentity of the focused pane
  open(p: PoolPane): void;    // add if absent (dedupe by identity), LRU-evict past cap, do NOT change focus
  focus(id: string): void;    // set focused; if absent this is a no-op (caller opens first)
  openAndFocus(p: PoolPane): void; // open (if absent) + focus, in one commit
}
```

- **Identity** is `paneIdentity(serverId, target, paneId)` = `${serverId}:${target}:${paneId}`
  — name-independent, matching the tab-identity fix already shipped in
  `MobileSessionTabs`. Extract that helper to a shared module (`lib/pane-identity.ts`)
  and reuse it in both places (removes duplication).
- **LRU:** track last-focused order; when `open`/`openAndFocus` would exceed
  `MOBILE_POOL_CAP`, close the least-recently-focused pane that is not currently
  focused. (At ≤4 sessions this never triggers.)
- Cap constant lives beside the hook: `export const MOBILE_POOL_CAP = 4;`

### New: `MobileTerminalStack` component (`web/src/components/MobileTerminalStack.tsx`)

Renders the single-visible mounted pool.

```tsx
function MobileTerminalStack({
  panes, focusedId, fontSize, theme,
}: { panes: PoolPane[]; focusedId: string | null; fontSize: number; theme: ITheme; }) { … }
```

- One wrapper `<div>` per pane, keyed by `paneIdentity`, each containing a
  `<TerminalView>`. Visible wrapper is `flex`; others `display:none` (mirrors
  `GridView`). Each `TerminalView` keeps its own socket + scrollback alive.
- Only the focused pane's `TerminalView` gets `showKeyBar` (so exactly one key bar,
  and only for the interactive pane).
- Passes `active={identity === focusedId}` to each `TerminalView` (see focus handoff).

### Edit: `TerminalView` (`web/src/components/TerminalView.tsx`)

Add an optional `active?: boolean` prop. When it transitions to `true`, focus the
xterm (`xtermRef.current?.focus()`) so the newly-revealed pane receives keyboard/soft-
keyboard input. Default `undefined` → **no new behavior** for existing callers
(`GridView`, and the desktop path), so the grid is untouched. Implement as a small
`useEffect` guarded on `active === true`.

### Edit: `routes/terminal.tsx` (`MobileTerminalRoute`)

- Keep reading `serverId/paneId/target/session` from the URL — used only to **seed**
  the pool on mount (`openAndFocus` the entered pane).
- Instantiate `useMobilePanePool`. Eager-warm effect: once the session list
  (`rows`, already fetched here for the tabs) is available, `open()` the warm set up to
  the cap (focused always included).
- Compute the focused pane's `{serverId, target, session}` from the pool and pass it to
  `useFocusedSeen` (replacing the URL-derived value) so seen/focus tracking follows the
  in-state focus as you switch.
- Render `<MobileTerminalStack …>` in place of the single `<TerminalView>`. **Remove**
  the `key={serverId:target:paneId}` on `TerminalView` (the pool now prevents both the
  flash and the bleed).
- Build the tab strip from the list as today, but the **active tab = focused pane**
  (from the pool), and `onSwitch(tab)` calls `pool.openAndFocus(...)` instead of
  `navigate(...)`. `onRenamed` still renames the focused pane and updates the pool's
  label; identity is name-independent so the pool is undisturbed.

### Edit: `MobileSessionTabs.tsx`

- `buildTabs` "current" now means the **focused pane** rather than the URL. The
  signature stays the same (it already takes a `current` shape); the route passes the
  focused pane's fields. Reuse the shared `paneIdentity` helper.
- `onSwitch` semantics move to the route (focus, not navigate). No change to the
  component's props contract.

## Data flow

1. List tap → `navigate("/t/…")` → `MobileTerminalRoute` mounts.
2. Route seeds pool via `openAndFocus(entered pane)`; eager-warm effect fills the pool
   to the cap once `rows` load.
3. `MobileTerminalStack` mounts one `TerminalView` per pooled pane; each opens its own
   WS (`useTerminalSession`) and paints on `onOpen`. Non-focused are `display:none`.
4. Tap a tab → `pool.openAndFocus(tab)` → focused wrapper flips to visible, previous
   hides. If already pooled: instant. If new: mounts + connects that once (+ LRU evict).
5. Focused `TerminalView` receives `active`, focuses its xterm; `useFocusedSeen`
   (keyed off focus) marks the focused session seen/focused and POSTs `/seen`.
6. ‹ Back → `navigate("/")` → route unmounts → all pooled `TerminalView`s unmount →
   all sockets `dispose()`d.

## Edge cases / error handling

- **Cap reached** (lazy path only, >4 sessions): LRU-evict the least-recently-focused
  non-focused pane (its socket closes; revisiting reconnects). Silent, no user-facing
  error. Never hit at ≤4 sessions.
- **Session killed elsewhere while pooled:** that pane's terminal shows disconnected
  (existing `TerminalView` banner); it drops from the strip on the next sessions
  refetch. Switching away is unaffected.
- **Rename of the focused pane:** identity is name-independent, so the pool pane is
  untouched; update its stored `session` label. The strip's active-tab label already
  follows the focused pane.
- **Hidden-pane fit:** a revealed pane refits via its `XTerm` `ResizeObserver`
  (`display:none` → visible triggers a size change), same as `GridView` today. A pane
  fitted while hidden (0×0) is harmless; it refits on reveal.
- **Focused pane not in the first `cap` of the list:** the warm set explicitly includes
  the focused session before filling the remainder, so it is always mounted.

## Testing

- **`useMobilePanePool`** (unit): open dedupes by identity; `openAndFocus` sets focus;
  `focus` on an absent id is a no-op; cap eviction removes the least-recently-focused
  non-focused pane and keeps the focused one; render/insertion order is stable.
- **`paneIdentity`** (unit): stable string; used by both pool and tabs.
- **`MobileTerminalStack`** (component): renders one `TerminalView` per pane; exactly
  the focused wrapper is visible; only the focused pane has `showKeyBar`; passes
  `active` to the focused pane only. (Mock `TerminalView` to a marker, as
  `terminal.test.tsx` already does.)
- **`TerminalView`** (component): focuses xterm when `active` flips true; no focus call
  when `active` is undefined (grid path unchanged).
- **`MobileTerminalRoute`** (route): seeds the pool from the URL; tapping an inactive
  tab focuses in-state **without navigating** (assert `navigate` not called for switch);
  eager-warm mounts up to the cap; `useFocusedSeen` follows the focused pane; ‹ Back
  navigates `/`.
- Full suite + typecheck + build green before commit.

## Files

- **New:** `web/src/hooks/useMobilePanePool.ts` (+ `.test.ts`)
- **New:** `web/src/components/MobileTerminalStack.tsx` (+ `.test.tsx`)
- **New:** `web/src/lib/pane-identity.ts` (+ `.test.ts`) — shared `paneIdentity` helper
- **Edit:** `web/src/components/TerminalView.tsx` (+ test for `active` focus)
- **Edit:** `web/src/routes/terminal.tsx` (seed pool, eager-warm, stack, focus-driven
  `useFocusedSeen`, remove per-switch navigate + the `TerminalView` key)
- **Edit:** `web/src/components/MobileSessionTabs.tsx` (reuse shared `paneIdentity`;
  active tab = focused pane)
- **Edit tests:** `web/src/routes/terminal.test.tsx`, `web/src/components/MobileSessionTabs.test.tsx`

## Risks / verification

- **Keyboard focus handoff on switch** is the fiddliest bit (iOS soft keyboard). Verify
  on a real device: switching tabs keeps the keyboard up and routes typing to the newly
  focused pane; the key bar targets the focused pane's controller.
- **Refit timing** when revealing a hidden pane — proven by `GridView`, but verify no
  stale cols/rows after a switch (send a resize on reveal if needed).
- **Eager warming vs. flaky mobile network** — up to 4 concurrent WS opens on entry;
  bounded and within the relay cap, but verify it doesn't stampede on a cold load.
- **Revertibility:** feature is isolated to the mobile terminal view + three new files;
  land as a single squashed feature branch so it reverts cleanly.
