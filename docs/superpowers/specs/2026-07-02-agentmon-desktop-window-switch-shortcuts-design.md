# Desktop window-switch keyboard shortcuts — design

**Date:** 2026-07-02
**Scope:** web (desktop grid only; mobile untouched)
**Status:** approved, ready for implementation plan

## Goal

Let a desktop user jump keyboard focus between open grid tiles ("windows") with a
number chord — press the Nth number key to focus the Nth open tile. The chord scheme
is configurable so it works whether the app runs as an installed PWA or in a browser tab.

## Definitions

- **"window" = a grid tile** in `web/src/components/GridView.tsx`, backed by the
  `usePanes` store (`web/src/store/panes.ts`). `panes` is an ordered array; each pane
  has its own live terminal. `focus(id)` expands a tile full-screen; `collapse()`
  returns to the grid.
- **Grid order / numbering:** the `panes` array order (order opened). Tile *i* (0-based)
  maps to number *i+1*. Reading order in the grid is top-left → right → down, matching
  the array order.

## Behavior

Desktop grid only. A configurable chord + a number key (1–9) focuses the corresponding tile.

- **Grid view (no tile expanded):** move keyboard focus to tile N's terminal so the user
  can type into it immediately. The existing `focus-within` ring shows the active tile.
- **Expanded / full-screen mode (`focusedId != null`):** switch *which* tile is expanded
  to N (call `usePanes.focus(panes[N-1].id)`) and focus its terminal.
- Range is 1–9. The client tile cap (`GRID_TILE_CAP = 6`) means realistically 1–6.
- A number with no corresponding tile (N > open tiles) is a **no-op**, but the chord is
  still consumed (see "Claiming the chord").

## Configurable chord — the setting

New per-device preference **`windowSwitchShortcut`** in the persisted `usePrefs` store
(`web/src/store/prefs.ts`), surfaced as a `<select>` in the ⚙ settings popover
(`web/src/components/SettingsPanel.tsx`), placed next to "Grid columns".

Type: `type ShortcutScheme = "cmdCtrl" | "alt" | "off"`. **Default: `"cmdCtrl"`.**

| Value | Label (UI) | Modifier matched | Notes |
|---|---|---|---|
| `"cmdCtrl"` | **Cmd / Ctrl + number** | Cmd (metaKey) on Mac, Ctrl (ctrlKey) on Win/Linux | Default. Reliable in the installed PWA. In a plain browser tab, some browsers won't let the page suppress native tab-switching (Cmd/Ctrl+1..8), so it may double-trigger there. |
| `"alt"` | **Alt / Option + number** | Alt (altKey) — ⌥ on Mac | Always overridable in every browser and the PWA — the browser-tab-safe choice. |
| `"off"` | **Off** | — | No shortcuts, no number badges. |

Rationale for the default: the owner always uses the installed PWA (no browser tabs to
conflict with) and prefers the Cmd+N muscle memory. Browser-tab users can switch to
Alt/Option or Off.

## Discoverability — number badges

Each grid tile header shows a small muted keycap-style badge with its number (1–9),
**only on desktop and only when `windowSwitchShortcut !== "off"`**. The badge's `title`
spells the full chord for the active scheme and platform (e.g. `⌘1`, `Ctrl+1`, `⌥1`).
Badges appear/disappear live when the setting changes. Tiles beyond index 9 get no
badge (not reachable given the cap).

## Architecture / components

### New: `web/src/lib/window-shortcuts.ts` (pure, DOM-free, unit-testable)

Mirrors the existing pure key-logic pattern in `web/src/lib/terminal-keys.ts`.

```ts
export type ShortcutScheme = "cmdCtrl" | "alt" | "off";

export interface ShortcutKeyEvent {
  code: string;      // e.g. "Digit1"
  key: string;       // fallback / diagnostics
  ctrlKey: boolean;
  metaKey: boolean;
  altKey: boolean;
  shiftKey: boolean;
}

// The 1-based window index (1..9) the event requests under `scheme`, or null.
export function windowIndexFor(
  ev: ShortcutKeyEvent,
  scheme: ShortcutScheme,
  isMac: boolean,
): number | null;

// Human-readable chord for a given number, for badge titles.
export function chordLabel(scheme: ShortcutScheme, isMac: boolean, n: number): string;
```

Logic notes:
- Parse the digit from `ev.code` matching `/^Digit([1-9])$/`. **Use `code`, not `key`** —
  on Mac, ⌥1 changes `key` to `"¡"` but leaves `code === "Digit1"`.
- `"off"` → always `null`.
- `"cmdCtrl"`: require `isMac ? metaKey : ctrlKey`, and reject if the *other* accelerator
  or `altKey`/`shiftKey` is held. (On Mac, Ctrl+1 does NOT trigger — left for the terminal.)
- `"alt"`: require `altKey` and reject `ctrlKey`/`metaKey`/`shiftKey`.
- `Digit0` and non-digit codes → `null`.

### New: `web/src/hooks/useWindowSwitchShortcuts.ts`

- Installs a single **capture-phase** `keydown` listener on `document`
  (`addEventListener("keydown", handler, true)`) so it runs before xterm's own
  textarea handler. On a matched chord the handler calls `preventDefault()` and
  `stopPropagation()` so the keystroke never reaches xterm ("claiming the chord").
- Reads the current scheme via `usePrefs.getState()` and panes via
  `usePanes.getState()` **inside the handler** — the listener attaches once and does
  not re-bind on every pref/pane change.
- Computes `isMac` from `navigator.platform` (same test already used in
  `web/src/components/XTerm.tsx`: `/Mac/i.test(navigator.platform || "")`) and passes it
  to `windowIndexFor`.
- **Editable-field guard:** if the focused element is an editable field that is *not* the
  terminal (an `<input>`, a `<textarea>` / contenteditable outside an `.xterm` subtree),
  skip — so the chord doesn't hijack the session-rename or change-password fields. The
  terminal's own `.xterm-helper-textarea` is inside `.xterm`, so it is not skipped.
- On a match with a valid tile: set the active tile (see GridView) and, if
  `usePanes.getState().focusedId != null`, call `usePanes.getState().focus(panes[N-1].id)`
  to move the expansion. The hook takes `setActiveWindowId` (or an `onJump(id)` callback)
  from GridView.

### Claiming the chord

When the active scheme matches a `Digit1..9` event, the handler consumes it
(`preventDefault` + `stopPropagation`) **regardless of whether a tile exists at N**. This
gives predictable ownership and suppresses accidental browser tab-switching. Caveat: for
`"cmdCtrl"` in a plain browser tab, some browsers don't allow overriding native tab
switching, so it may still switch tabs — documented, and the reason the `"alt"` option
exists.

### Modified: `web/src/store/prefs.ts`

Add `windowSwitchShortcut: ShortcutScheme` (default `"cmdCtrl"`),
`setWindowSwitchShortcut(v)`, and include it in `partialize`.

### Modified: `web/src/components/GridView.tsx`

- Track `activeWindowId: string | null` in component state (starts `null` → grid does not
  autofocus any tile on mount, preserving current behavior).
- Call `useWindowSwitchShortcuts({ onJump })` where `onJump(id)` sets `activeWindowId` and
  handles the expanded-mode re-expansion.
- Pass `active={activeWindowId === p.id}` to each `TerminalView`. `TerminalView` already
  focuses its xterm via `useEffect(() => { if (active) xtermRef.current?.focus() }, [active])`,
  so no new focus plumbing is needed.
- Add `onFocusCapture={() => setActiveWindowId(p.id)}` on each tile wrapper so
  `activeWindowId` stays in sync with real DOM focus (clicking a tile updates it). This
  makes "press the same number again after clicking elsewhere" reliably re-focus.
- Render the number badge in each tile header, gated on
  `windowSwitchShortcut !== "off"` and `index < 9`, with `title={chordLabel(...)}`.

### Modified: `web/src/components/SettingsPanel.tsx`

Add a labeled `<select>` "Window switch shortcut" with the three options, bound to
`windowSwitchShortcut` / `setWindowSwitchShortcut`, following the existing "Grid columns"
select pattern.

## Data flow

1. User presses e.g. Cmd+2 → document capture-phase `keydown` fires before xterm.
2. Handler: not an editable non-terminal field → `windowIndexFor(ev, "cmdCtrl", isMac)`
   → `2`.
3. `preventDefault()` + `stopPropagation()` (xterm never sees it).
4. `panes = usePanes.getState().panes`; if `2 <= panes.length` → `onJump(panes[1].id)`.
5. GridView `onJump`: `setActiveWindowId(id)`; if `focusedId != null`
   → `usePanes.getState().focus(id)`.
6. `activeWindowId` change → `TerminalView.active` true for that tile → its effect calls
   `xtermRef.current.focus()` → keyboard focus lands in tile 2's terminal.

## Edge cases

- **Same number twice:** already focused → no-op (harmless).
- **Click tile A, then press A's number:** `onFocusCapture` set `activeWindowId` to A on
  click; pressing its number is a no-op but focus is already correct. Pressing a
  *different* number changes `activeWindowId` and refocuses.
- **N > open tiles:** consumed, no focus change.
- **Scheme "off":** `windowIndexFor` returns `null` → handler does nothing, no badges.
- **Browser tab + "cmdCtrl":** may still trigger native tab switching (documented).
- **Rename / password field focused:** editable-field guard skips the chord.
- **Mobile:** GridView is desktop-only; mobile route/pool is untouched.

## Testing

- **`web/src/lib/window-shortcuts.test.ts` (new):** `windowIndexFor` across schemes:
  - `cmdCtrl` on Mac: metaKey+Digit1 → 1; ctrlKey+Digit1 → null; metaKey+Shift+Digit1 → null.
  - `cmdCtrl` on PC: ctrlKey+Digit1 → 1; metaKey+Digit1 → null.
  - `alt` on Mac & PC: altKey+Digit1 → 1 (verify `code`-based: `key:"¡", code:"Digit1"` → 1);
    metaKey+Digit1 → null.
  - `off` → always null; `Digit0` → null; non-digit code → null; Digit1..9 → 1..9.
  - `chordLabel` sanity for each scheme/platform.
- **`web/src/components/SettingsPanel.test.tsx`:** the new selector renders its three
  options and calls `setWindowSwitchShortcut` on change.
- **Optional `GridView` test:** badges render with correct numbers when scheme is on and
  are absent when "off".

## Out of scope (YAGNI)

- Cycle next/prev shortcuts (Alt+[ / Alt+]) — direct number jump only for now.
- A "9 → last window" browser-style convention.
- Numpad digit support (top-row `Digit1..9` only).
- Persisting the pref server-side (per-device localStorage, consistent with other prefs).
