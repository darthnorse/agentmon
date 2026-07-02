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
