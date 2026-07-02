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
