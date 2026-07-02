# Desktop Window-Switch Keyboard Shortcuts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a desktop user press a configurable number chord (Cmd/Ctrl+N by default, or Alt/Option+N) to move keyboard focus to the Nth open grid tile.

**Architecture:** A pure, unit-tested helper (`lib/window-shortcuts.ts`) decides which window index a keyboard event requests under the active scheme. A capture-phase `keydown` hook (`hooks/useWindowSwitchShortcuts.ts`) beats xterm to the event, consumes it, and drives focus. `GridView` reuses `TerminalView`'s existing `active` prop to focus the target tile and renders number badges; the scheme is a new persisted preference edited in `SettingsPanel`.

**Tech Stack:** React 18 + TypeScript, Zustand (`usePrefs`, `usePanes`), Vitest + @testing-library/react + jsdom, xterm 6.

## Global Constraints

- All web source lives under `web/`; **all commands in this plan run from `web/`** (e.g. `cd web && npx vitest run ...`).
- Desktop grid only — do NOT touch the mobile route/pane-pool or `MobileKeyBar`.
- Follow existing patterns: pure key logic mirrors `web/src/lib/terminal-keys.ts`; the settings control mirrors the existing "Grid columns" `<select>` in `web/src/components/SettingsPanel.tsx`.
- Scheme type is exactly `type ShortcutScheme = "cmdCtrl" | "alt" | "off"`; the persisted pref key is exactly `windowSwitchShortcut`; default is exactly `"cmdCtrl"`.
- The digit is parsed from `KeyboardEvent.code` (`Digit1`..`Digit9`), never from `.key` (Mac ⌥1 changes `.key` to `¡` but not `.code`).
- Mac detection uses `/Mac/i.test(navigator.platform || "")` (same test as `web/src/components/XTerm.tsx`).
- Commit messages end with the trailer: `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.
- Work on a feature branch (e.g. `feat/window-switch-shortcuts`), not `main`.

---

## File Structure

- `web/src/lib/window-shortcuts.ts` — **new.** Pure logic: `ShortcutScheme`, `ShortcutKeyEvent`, `windowIndexFor`, `chordLabel`, `isMacPlatform`.
- `web/src/lib/window-shortcuts.test.ts` — **new.** Unit tests for the pure logic.
- `web/src/store/prefs.ts` — **modify.** Add the `windowSwitchShortcut` field, setter, and partialize entry.
- `web/src/store/prefs.test.ts` — **new.** Unit tests for the new pref (setter + persistence).
- `web/src/hooks/useWindowSwitchShortcuts.ts` — **new.** Capture-phase keydown listener wiring the pure logic to focus.
- `web/src/hooks/useWindowSwitchShortcuts.test.tsx` — **new.** Hook behavior tests.
- `web/src/components/SettingsPanel.tsx` — **modify.** Add the "Window switch shortcut" `<select>`.
- `web/src/components/SettingsPanel.test.tsx` — **modify.** Cover the new selector; add the field to `resetPrefs`.
- `web/src/components/GridView.tsx` — **modify.** Call the hook, track `activeWindowId`, pass `active` to tiles, render badges.

---

## Task 1: Pure chord logic (`lib/window-shortcuts.ts`)

**Files:**
- Create: `web/src/lib/window-shortcuts.ts`
- Test: `web/src/lib/window-shortcuts.test.ts`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type ShortcutScheme = "cmdCtrl" | "alt" | "off"`
  - `interface ShortcutKeyEvent { code: string; ctrlKey: boolean; metaKey: boolean; altKey: boolean; shiftKey: boolean }`
  - `function windowIndexFor(ev: ShortcutKeyEvent, scheme: ShortcutScheme, isMac: boolean): number | null`
  - `function chordLabel(scheme: ShortcutScheme, isMac: boolean, n: number): string`
  - `function isMacPlatform(): boolean`

- [ ] **Step 1: Write the failing test**

Create `web/src/lib/window-shortcuts.test.ts`:

```ts
import { describe, it, expect } from "vitest";
import { windowIndexFor, chordLabel } from "./window-shortcuts";

type Ev = Parameters<typeof windowIndexFor>[0];

function ev(over: Partial<Ev> = {}): Ev {
  return { code: "Digit1", ctrlKey: false, metaKey: false, altKey: false, shiftKey: false, ...over };
}

describe("windowIndexFor", () => {
  it("maps Digit1..9 to 1..9 for the matching modifier", () => {
    for (let n = 1; n <= 9; n++) {
      expect(windowIndexFor(ev({ code: `Digit${n}`, ctrlKey: true }), "cmdCtrl", false)).toBe(n);
    }
  });

  it("cmdCtrl uses metaKey on Mac and ctrlKey off Mac", () => {
    expect(windowIndexFor(ev({ code: "Digit2", metaKey: true }), "cmdCtrl", true)).toBe(2);
    expect(windowIndexFor(ev({ code: "Digit2", ctrlKey: true }), "cmdCtrl", true)).toBeNull(); // Ctrl left for the terminal on Mac
    expect(windowIndexFor(ev({ code: "Digit2", ctrlKey: true }), "cmdCtrl", false)).toBe(2);
    expect(windowIndexFor(ev({ code: "Digit2", metaKey: true }), "cmdCtrl", false)).toBeNull();
  });

  it("alt uses altKey on both platforms and reads code, not key (Mac ⌥1)", () => {
    expect(windowIndexFor(ev({ code: "Digit1", altKey: true }), "alt", true)).toBe(1);
    expect(windowIndexFor(ev({ code: "Digit1", altKey: true }), "alt", false)).toBe(1);
    expect(windowIndexFor(ev({ code: "Digit1", metaKey: true }), "alt", true)).toBeNull();
  });

  it("rejects extra modifiers on the chord", () => {
    expect(windowIndexFor(ev({ code: "Digit1", ctrlKey: true, shiftKey: true }), "cmdCtrl", false)).toBeNull();
    expect(windowIndexFor(ev({ code: "Digit1", ctrlKey: true, altKey: true }), "cmdCtrl", false)).toBeNull();
    expect(windowIndexFor(ev({ code: "Digit1", altKey: true, shiftKey: true }), "alt", false)).toBeNull();
  });

  it("returns null for Digit0, non-digit codes, and no modifier", () => {
    expect(windowIndexFor(ev({ code: "Digit0", ctrlKey: true }), "cmdCtrl", false)).toBeNull();
    expect(windowIndexFor(ev({ code: "KeyA", ctrlKey: true }), "cmdCtrl", false)).toBeNull();
    expect(windowIndexFor(ev({ code: "Digit1" }), "cmdCtrl", false)).toBeNull();
  });

  it("off scheme is always null", () => {
    expect(windowIndexFor(ev({ code: "Digit1", ctrlKey: true }), "off", false)).toBeNull();
    expect(windowIndexFor(ev({ code: "Digit1", altKey: true }), "off", true)).toBeNull();
  });
});

describe("chordLabel", () => {
  it("formats per scheme and platform", () => {
    expect(chordLabel("cmdCtrl", true, 1)).toBe("⌘1");
    expect(chordLabel("cmdCtrl", false, 1)).toBe("Ctrl+1");
    expect(chordLabel("alt", true, 2)).toBe("⌥2");
    expect(chordLabel("alt", false, 2)).toBe("Alt+2");
    expect(chordLabel("off", false, 1)).toBe("");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/lib/window-shortcuts.test.ts`
Expected: FAIL — `Failed to resolve import "./window-shortcuts"` / functions not defined.

- [ ] **Step 3: Write minimal implementation**

Create `web/src/lib/window-shortcuts.ts`:

```ts
// Pure logic for the desktop "jump to window N" keyboard shortcuts. No DOM/xterm
// deps so it is unit-testable; hooks/useWindowSwitchShortcuts wires it to a document
// keydown listener, and GridView uses chordLabel for the per-tile number badges.

export type ShortcutScheme = "cmdCtrl" | "alt" | "off";

/** The minimal shape of a KeyboardEvent this module inspects. */
export interface ShortcutKeyEvent {
  code: string; // physical key, e.g. "Digit1" — stable across modifiers (Mac ⌥1 mangles `key`, not `code`)
  ctrlKey: boolean;
  metaKey: boolean;
  altKey: boolean;
  shiftKey: boolean;
}

// True on a Mac-class platform (incl. iPadOS, which reports "MacIntel"). Matches the
// test in XTerm.tsx. Impure (reads navigator); windowIndexFor takes isMac explicitly so
// its own tests never call this.
export function isMacPlatform(): boolean {
  return /Mac/i.test(navigator.platform || "");
}

// The 1-based window index (1..9) an event requests under `scheme`, or null when the
// event is not a window-switch chord. Requires the scheme's modifier and rejects any
// other modifier so we never clobber unrelated chords (e.g. Cmd+Shift+1).
export function windowIndexFor(
  ev: ShortcutKeyEvent,
  scheme: ShortcutScheme,
  isMac: boolean,
): number | null {
  if (scheme === "off") return null;

  const m = /^Digit([1-9])$/.exec(ev.code);
  if (!m) return null;

  if (scheme === "cmdCtrl") {
    const primary = isMac ? ev.metaKey : ev.ctrlKey;
    const other = isMac ? ev.ctrlKey : ev.metaKey;
    if (!primary || other || ev.altKey || ev.shiftKey) return null;
  } else {
    // "alt"
    if (!ev.altKey || ev.ctrlKey || ev.metaKey || ev.shiftKey) return null;
  }

  return Number(m[1]);
}

// Human-readable chord for window `n`, for the tile badge's title. "" when scheme is off.
export function chordLabel(scheme: ShortcutScheme, isMac: boolean, n: number): string {
  if (scheme === "off") return "";
  if (scheme === "alt") return isMac ? `⌥${n}` : `Alt+${n}`;
  return isMac ? `⌘${n}` : `Ctrl+${n}`;
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/lib/window-shortcuts.test.ts`
Expected: PASS (all cases green).

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/window-shortcuts.ts web/src/lib/window-shortcuts.test.ts
git commit -m "feat(web): pure window-switch chord logic (windowIndexFor/chordLabel)" \
  -m "Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: Persisted preference (`store/prefs.ts`)

**Files:**
- Modify: `web/src/store/prefs.ts`
- Test: `web/src/store/prefs.test.ts` (create)

**Interfaces:**
- Consumes: `ShortcutScheme` from Task 1.
- Produces on `usePrefs`: field `windowSwitchShortcut: ShortcutScheme` (default `"cmdCtrl"`) and `setWindowSwitchShortcut(v: ShortcutScheme): void`.

- [ ] **Step 1: Write the failing test**

Create `web/src/store/prefs.test.ts`:

```ts
import { describe, it, expect, beforeEach } from "vitest";
import { usePrefs, PREFS_STORAGE_KEY } from "./prefs";

describe("prefs: windowSwitchShortcut", () => {
  beforeEach(() => {
    localStorage.clear();
    usePrefs.setState({ windowSwitchShortcut: "cmdCtrl" });
  });

  it("updates via the setter", () => {
    usePrefs.getState().setWindowSwitchShortcut("alt");
    expect(usePrefs.getState().windowSwitchShortcut).toBe("alt");
    usePrefs.getState().setWindowSwitchShortcut("off");
    expect(usePrefs.getState().windowSwitchShortcut).toBe("off");
  });

  it("persists the choice to localStorage", () => {
    usePrefs.getState().setWindowSwitchShortcut("alt");
    const raw = JSON.parse(localStorage.getItem(PREFS_STORAGE_KEY) || "{}");
    expect(raw.state.windowSwitchShortcut).toBe("alt");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/store/prefs.test.ts`
Expected: FAIL — `setWindowSwitchShortcut is not a function` (and TS error on the missing field).

- [ ] **Step 3: Write minimal implementation**

In `web/src/store/prefs.ts`:

Add the import near the top (after the existing `ThemeName` import):

```ts
import type { ShortcutScheme } from "@/lib/window-shortcuts";
```

Add to the `PrefsState` interface (after `gridMaxColumns: number;` and its setter):

```ts
  windowSwitchShortcut: ShortcutScheme;
  setWindowSwitchShortcut(v: ShortcutScheme): void;
```

Add to the store object (after the `setGridMaxColumns` line):

```ts
      windowSwitchShortcut: "cmdCtrl",
      setWindowSwitchShortcut: (v) => set({ windowSwitchShortcut: v }),
```

Add to the `partialize` return object (after the `gridMaxColumns: s.gridMaxColumns,` line):

```ts
        windowSwitchShortcut: s.windowSwitchShortcut,
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/store/prefs.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/store/prefs.ts web/src/store/prefs.test.ts
git commit -m "feat(web): add windowSwitchShortcut preference (default cmdCtrl)" \
  -m "Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: Keydown hook (`hooks/useWindowSwitchShortcuts.ts`)

**Files:**
- Create: `web/src/hooks/useWindowSwitchShortcuts.ts`
- Test: `web/src/hooks/useWindowSwitchShortcuts.test.tsx`

**Interfaces:**
- Consumes: `windowIndexFor`, `isMacPlatform` (Task 1); `usePrefs.getState().windowSwitchShortcut` (Task 2); `usePanes` store — `{ panes: OpenPane[]; focusedId: string | null; focus(id: string): void }` where each pane has an `id: string`.
- Produces: `function useWindowSwitchShortcuts(onFocusTile: (paneId: string) => void): void`.

- [ ] **Step 1: Write the failing test**

Create `web/src/hooks/useWindowSwitchShortcuts.test.tsx`:

```tsx
import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { renderHook, cleanup } from "@testing-library/react";
import { useWindowSwitchShortcuts } from "./useWindowSwitchShortcuts";
import { usePrefs } from "@/store/prefs";
import { usePanes } from "@/store/panes";

function pane(paneId: string) {
  return { id: `s:t:sess:${paneId}`, serverId: "s", paneId, target: "t", session: "sess", serverName: "srv" };
}

function press(over: Partial<KeyboardEventInit> = {}) {
  const ev = new KeyboardEvent("keydown", { bubbles: true, cancelable: true, ...over });
  document.dispatchEvent(ev);
  return ev;
}

describe("useWindowSwitchShortcuts", () => {
  beforeEach(() => {
    localStorage.clear();
    usePrefs.setState({ windowSwitchShortcut: "cmdCtrl" });
    usePanes.setState({ panes: [pane("p1"), pane("p2"), pane("p3")], focusedId: null });
    document.body.innerHTML = "";
    if (document.activeElement instanceof HTMLElement) document.activeElement.blur();
  });
  afterEach(() => cleanup()); // unmount the hook → remove its document listener between tests

  it("focuses the Nth tile and consumes the event (Ctrl+2, non-Mac jsdom)", () => {
    const onFocus = vi.fn();
    renderHook(() => useWindowSwitchShortcuts(onFocus));
    const ev = press({ code: "Digit2", ctrlKey: true });
    expect(onFocus).toHaveBeenCalledWith("s:t:sess:p2");
    expect(ev.defaultPrevented).toBe(true);
  });

  it("does nothing when the scheme is off", () => {
    usePrefs.setState({ windowSwitchShortcut: "off" });
    const onFocus = vi.fn();
    renderHook(() => useWindowSwitchShortcuts(onFocus));
    const ev = press({ code: "Digit2", ctrlKey: true });
    expect(onFocus).not.toHaveBeenCalled();
    expect(ev.defaultPrevented).toBe(false);
  });

  it("consumes but no-ops a number past the open tile count", () => {
    const onFocus = vi.fn();
    renderHook(() => useWindowSwitchShortcuts(onFocus));
    const ev = press({ code: "Digit8", ctrlKey: true });
    expect(onFocus).not.toHaveBeenCalled();
    expect(ev.defaultPrevented).toBe(true);
  });

  it("ignores the chord while a non-terminal input is focused", () => {
    const input = document.createElement("input");
    document.body.appendChild(input);
    input.focus();
    const onFocus = vi.fn();
    renderHook(() => useWindowSwitchShortcuts(onFocus));
    press({ code: "Digit1", ctrlKey: true });
    expect(onFocus).not.toHaveBeenCalled();
  });

  it("moves the expanded tile when one is expanded", () => {
    usePanes.setState({ focusedId: "s:t:sess:p1" });
    const onFocus = vi.fn();
    renderHook(() => useWindowSwitchShortcuts(onFocus));
    press({ code: "Digit3", ctrlKey: true });
    expect(usePanes.getState().focusedId).toBe("s:t:sess:p3");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/hooks/useWindowSwitchShortcuts.test.tsx`
Expected: FAIL — `Failed to resolve import "./useWindowSwitchShortcuts"`.

- [ ] **Step 3: Write minimal implementation**

Create `web/src/hooks/useWindowSwitchShortcuts.ts`:

```ts
import * as React from "react";
import { usePrefs } from "@/store/prefs";
import { usePanes } from "@/store/panes";
import { windowIndexFor, isMacPlatform } from "@/lib/window-shortcuts";

// Installs a single capture-phase keydown listener that maps the configured number
// chord (see lib/window-shortcuts) to "focus the Nth open grid tile". Capture phase +
// stopPropagation means the chord never reaches xterm's own key handler, so it is not
// typed into the terminal. Desktop-only — called by GridView.
export function useWindowSwitchShortcuts(onFocusTile: (paneId: string) => void): void {
  // Keep the latest callback in a ref so the listener attaches exactly once.
  const cb = React.useRef(onFocusTile);
  cb.current = onFocusTile;

  React.useEffect(() => {
    const isMac = isMacPlatform();

    const handler = (ev: KeyboardEvent) => {
      const scheme = usePrefs.getState().windowSwitchShortcut;
      if (scheme === "off") return;

      // Don't hijack the chord while typing in a non-terminal field (session rename,
      // change-password). The terminal's own textarea lives inside an `.xterm` subtree,
      // so it is NOT treated as an editable field here.
      if (isEditableNonTerminal(document.activeElement as HTMLElement | null)) return;

      const n = windowIndexFor(ev, scheme, isMac);
      if (n === null) return;

      // The chord belongs to the app: stop it before xterm and suppress the browser
      // default (e.g. tab switch), whether or not a tile exists at N.
      ev.preventDefault();
      ev.stopPropagation();

      const { panes, focusedId, focus } = usePanes.getState();
      const pane = panes[n - 1];
      if (!pane) return; // no tile at this number → consumed, no-op

      cb.current(pane.id);
      // If a tile is currently expanded full-screen, move the expansion to N too.
      if (focusedId !== null) focus(pane.id);
    };

    document.addEventListener("keydown", handler, true);
    return () => document.removeEventListener("keydown", handler, true);
  }, []);
}

// An editable element that is NOT the xterm terminal textarea.
function isEditableNonTerminal(el: HTMLElement | null): boolean {
  if (!el) return false;
  if (el.closest(".xterm")) return false; // the terminal itself — allow the chord
  const tag = el.tagName;
  return tag === "INPUT" || tag === "TEXTAREA" || el.isContentEditable;
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/hooks/useWindowSwitchShortcuts.test.tsx`
Expected: PASS (all 5 cases).

- [ ] **Step 5: Commit**

```bash
git add web/src/hooks/useWindowSwitchShortcuts.ts web/src/hooks/useWindowSwitchShortcuts.test.tsx
git commit -m "feat(web): capture-phase hook for window-switch shortcuts" \
  -m "Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: Settings selector (`components/SettingsPanel.tsx`)

**Files:**
- Modify: `web/src/components/SettingsPanel.tsx`
- Test: `web/src/components/SettingsPanel.test.tsx`

**Interfaces:**
- Consumes: `ShortcutScheme` (Task 1); `usePrefs` `windowSwitchShortcut` / `setWindowSwitchShortcut` (Task 2).
- Produces: a labeled `<select>` with accessible name `"Window switch shortcut"` and options `cmdCtrl` / `alt` / `off`.

- [ ] **Step 1: Write the failing test**

In `web/src/components/SettingsPanel.test.tsx`, update `resetPrefs` to include the new field (add the line inside the `usePrefs.setState({ ... })` object):

```ts
    windowSwitchShortcut: "cmdCtrl",
```

Then add these two tests inside the `describe("SettingsPanel", ...)` block:

```ts
  it("changes the window switch shortcut in the store", async () => {
    render(<SettingsPanel />);
    await userEvent.click(screen.getByRole("button", { name: "Settings" }));
    await userEvent.selectOptions(screen.getByLabelText("Window switch shortcut"), "alt");
    expect(usePrefs.getState().windowSwitchShortcut).toBe("alt");
  });

  it("reflects the current store value in the window shortcut select", async () => {
    usePrefs.setState({ windowSwitchShortcut: "off" });
    render(<SettingsPanel />);
    await userEvent.click(screen.getByRole("button", { name: "Settings" }));
    expect((screen.getByLabelText("Window switch shortcut") as HTMLSelectElement).value).toBe("off");
  });
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/components/SettingsPanel.test.tsx`
Expected: FAIL — `Unable to find a label with the text of: Window switch shortcut`.

- [ ] **Step 3: Write minimal implementation**

In `web/src/components/SettingsPanel.tsx`:

Add the import (after the existing `ThemeName` import):

```ts
import type { ShortcutScheme } from "@/lib/window-shortcuts";
```

Add the two store selectors (after the `setGridMaxColumns` selector line):

```ts
  const windowSwitchShortcut = usePrefs((s) => s.windowSwitchShortcut);
  const setWindowSwitchShortcut = usePrefs((s) => s.setWindowSwitchShortcut);
```

Add this block in the popover, immediately AFTER the closing `</div>` of the existing "Grid columns" block (the `<div className="mb-3">` that contains `id="settings-grid-cols"`) and BEFORE the `alertOnDone` `<label>`:

```tsx
          <div className="mb-3">
            <label htmlFor="settings-window-shortcut" className="mb-1 block text-xs font-medium text-muted-foreground">
              Window switch shortcut
            </label>
            <select
              id="settings-window-shortcut"
              value={windowSwitchShortcut}
              onChange={(e) => setWindowSwitchShortcut(e.target.value as ShortcutScheme)}
              className="h-8 w-full rounded-md border border-input bg-background px-2 text-sm"
            >
              <option value="cmdCtrl">Cmd / Ctrl + number</option>
              <option value="alt">Alt / Option + number</option>
              <option value="off">Off</option>
            </select>
          </div>
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/components/SettingsPanel.test.tsx`
Expected: PASS (existing tests + the two new ones).

- [ ] **Step 5: Commit**

```bash
git add web/src/components/SettingsPanel.tsx web/src/components/SettingsPanel.test.tsx
git commit -m "feat(web): window switch shortcut selector in Settings" \
  -m "Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Wire the grid (`components/GridView.tsx`)

**Files:**
- Modify: `web/src/components/GridView.tsx`

**Interfaces:**
- Consumes: `useWindowSwitchShortcuts` (Task 3); `chordLabel`, `isMacPlatform`, `ShortcutScheme` (Task 1); `usePrefs` `windowSwitchShortcut` (Task 2); `TerminalView`'s existing `active?: boolean` prop (already focuses its xterm on `active` becoming true).
- Produces: no exported surface change — behavior only (focus on chord + number badges).

> **Note — Rules of Hooks:** `GridView` early-returns when `panes.length === 0`. All new hook calls (`React.useState`, `React.useMemo`, `useWindowSwitchShortcuts`) MUST be added ABOVE that early return, alongside the existing store-hook calls.

> **Verification note:** `GridView` mounts real xterm terminals (canvas/WebGL) and the SSE state store, which the current jsdom test setup does not render. This task is therefore verified by typecheck + build (Task 6) and manual smoke-testing (`/run` the web app), not a new jsdom render test. The focus logic and label formatting it relies on are already unit-tested in Tasks 1 and 3.

- [ ] **Step 1: Add imports**

In `web/src/components/GridView.tsx`, add at the top of the import block:

```ts
import * as React from "react";
```

And add (grouped with the other `@/lib` / `@/hooks` imports):

```ts
import { useWindowSwitchShortcuts } from "@/hooks/useWindowSwitchShortcuts";
import { chordLabel, isMacPlatform } from "@/lib/window-shortcuts";
```

- [ ] **Step 2: Add state + hook wiring (above the early return)**

Inside `GridView`, after the existing `const gridMaxColumns = usePrefs(...)` line and BEFORE `if (panes.length === 0) {`, add:

```ts
  const windowSwitchShortcut = usePrefs((s) => s.windowSwitchShortcut);
  const isMac = React.useMemo(() => isMacPlatform(), []);
  const [activeWindowId, setActiveWindowId] = React.useState<string | null>(null);

  // On a jump: focus the target tile; if a tile is currently expanded, the hook has
  // already moved the expansion. setActiveWindowId drives TerminalView's `active` focus.
  useWindowSwitchShortcuts(setActiveWindowId);
```

- [ ] **Step 3: Sync focus + add the number badge + pass `active`**

Modify the tile wrapper `<div>` (the one keyed by `` `${p.serverId}:${p.target}:${p.paneId}` ``) to add `onFocusCapture` so `activeWindowId` tracks real DOM focus. Change the map callback signature from `panes.map((p) => {` to `panes.map((p, i) => {`, then add the handler to the wrapper div's props:

```tsx
              onFocusCapture={() => setActiveWindowId(p.id)}
```

Add the badge as the FIRST child inside the header's left `<span className="flex min-w-0 items-center gap-1.5">`, before `<StateDot ... />`:

```tsx
                  {windowSwitchShortcut !== "off" && i < 9 && (
                    <span
                      className="flex-none rounded border border-border px-1 text-[10px] leading-none text-muted-foreground"
                      title={chordLabel(windowSwitchShortcut, isMac, i + 1)}
                      aria-hidden="true"
                    >
                      {i + 1}
                    </span>
                  )}
```

Pass `active` to the tile's `TerminalView` — change:

```tsx
                <TerminalView serverId={p.serverId} paneId={p.paneId} target={p.target} fontSize={fontSize} theme={theme} />
```

to:

```tsx
                <TerminalView serverId={p.serverId} paneId={p.paneId} target={p.target} active={activeWindowId === p.id} fontSize={fontSize} theme={theme} />
```

- [ ] **Step 4: Verify typecheck passes**

Run: `cd web && npm run typecheck`
Expected: PASS (no TS errors). This confirms the JSX/prop/type wiring is correct even without a jsdom render test.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/GridView.tsx
git commit -m "feat(web): jump to grid tile by number chord + tile badges" \
  -m "Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: Full verification

**Files:** none (verification gate).

- [ ] **Step 1: Run the full test suite**

Run: `cd web && npm run test:run`
Expected: PASS — all suites green, including the new `window-shortcuts`, `prefs`, `useWindowSwitchShortcuts`, and updated `SettingsPanel` tests.

- [ ] **Step 2: Typecheck + production build**

Run: `cd web && npm run build`
Expected: PASS (`tsc --noEmit` clean, then `vite build` succeeds).

- [ ] **Step 3: Manual smoke test (see verification note in Task 5)**

Use `/run` to launch the web app, then with 3+ grid tiles open on desktop:
- Confirm each tile shows a number badge (1, 2, 3…) whose hover title matches the platform/scheme (e.g. `⌘1` on Mac PWA, `Ctrl+1` on Win/Linux).
- Press Cmd/Ctrl+2 → focus (the focus-within ring) moves to tile 2; typing goes to that terminal, and the digit is NOT inserted into the terminal.
- Expand a tile, press a different number → the expanded tile switches to that window.
- Open Settings → set "Window switch shortcut" to **Alt/Option + number** → badges' titles update; Alt/Option+N now jumps. Set to **Off** → badges disappear and the chord does nothing.
- (Browser tab, cmdCtrl scheme) note the documented caveat: some browsers may still switch tabs — this is expected and why Alt/Option exists.

---

## Self-Review

**Spec coverage:**
- Behavior — focus tile in place / switch expanded tile → Task 3 (hook) + Task 5 (GridView `active`/`onFocusCapture`). ✓
- Numbering by `panes` order, range 1–9, missing index no-op → Task 3 (`panes[n-1]`, guard) + Task 1 (`Digit1..9`). ✓
- Configurable pref `windowSwitchShortcut` default `cmdCtrl`, ⚙ select → Task 2 + Task 4. ✓
- Claiming the chord (capture + preventDefault + stopPropagation, regardless of tile presence) → Task 3. ✓
- `code`-not-`key`, Mac vs PC modifier, reject extra modifiers → Task 1 + tests. ✓
- Editable-field guard (rename/password) → Task 3 + test. ✓
- Number badges, desktop, gated on scheme≠off, chord title → Task 5. ✓
- Tests: `windowIndexFor`, SettingsPanel selector, hook behavior → Tasks 1, 3, 4. GridView badge render intentionally verified via typecheck + build + manual (jsdom can't mount xterm) — documented in Task 5. ✓
- Out of scope (cycle keys, last-window, numpad, server-side pref) → not implemented. ✓

**Placeholder scan:** No TBD/TODO; every code and test step has concrete content. ✓

**Type consistency:** `ShortcutScheme` ("cmdCtrl"|"alt"|"off"), `windowSwitchShortcut`, `setWindowSwitchShortcut`, `windowIndexFor(ev, scheme, isMac)`, `chordLabel(scheme, isMac, n)`, `isMacPlatform()`, `useWindowSwitchShortcuts(onFocusTile)`, pane `.id` — names and signatures match across Tasks 1–5. ✓
