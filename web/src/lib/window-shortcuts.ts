// Pure logic for the desktop "jump to window N" keyboard shortcuts. No DOM/xterm
// deps so it is unit-testable; hooks/useWindowSwitchShortcuts wires it to a document
// keydown listener, and GridView uses chordLabel for the per-tile number badges.

// Only Alt/Option is offered: on macOS the browser/OS reserves Cmd+1..9 (browser tabs)
// and Ctrl+1..9 (Mission Control Spaces), and it won't deliver those keydowns to the
// page even in the installed PWA — so a "Cmd/Ctrl" option would silently not work.
// Alt/Option+number is reserved by nothing and intercepts cleanly everywhere.
export type ShortcutScheme = "alt" | "off";

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
// event is not a window-switch chord. Requires Alt/Option alone and rejects any other
// modifier so we never clobber unrelated chords (e.g. Cmd+Shift+1). Platform-independent.
export function windowIndexFor(ev: ShortcutKeyEvent, scheme: ShortcutScheme): number | null {
  if (scheme === "off") return null;

  const m = /^Digit([1-9])$/.exec(ev.code);
  if (!m) return null;

  if (!ev.altKey || ev.ctrlKey || ev.metaKey || ev.shiftKey) return null;

  return Number(m[1]);
}

// Human-readable chord for window `n`, for the tile badge's title. "" when scheme is off.
export function chordLabel(scheme: ShortcutScheme, isMac: boolean, n: number): string {
  if (scheme === "off") return "";
  return isMac ? `⌥${n}` : `Alt+${n}`;
}
