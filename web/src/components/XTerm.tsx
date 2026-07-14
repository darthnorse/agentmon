import * as React from "react";
import { Terminal, type ITheme } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import { TERMINAL_THEMES } from "@/lib/terminal-themes";
import { keyOverride } from "@/lib/terminal-keys";
import { createTerminalGesture, selectionMouseEvent } from "@/lib/terminal-touch";
import "@xterm/xterm/css/xterm.css";

export interface XTermHandle {
  write(b: Uint8Array): void;
  fit(): { cols: number; rows: number } | null;
  focus(): void;
  blur(): void;
  appCursor(): boolean;
  getSelection(): string;
  paste(text: string): void;
  scrollLines(n: number): void;
  reset(): void;
}

export const XTerm = React.forwardRef<
  XTermHandle,
  { onData(d: string): void; onResize(cols: number, rows: number): void; fontSize?: number; theme?: ITheme }
>(function XTerm({ onData, onResize, fontSize = 13, theme = TERMINAL_THEMES.dark }, ref) {
  const hostRef = React.useRef<HTMLDivElement>(null);
  const termRef = React.useRef<Terminal | null>(null);
  const fitRef = React.useRef<FitAddon | null>(null);
  // Tracks whether the host was laid-out-hidden on the last ResizeObserver tick, so a
  // hidden→shown REVEAL can force a one-off repaint (see the RO below). Starts true:
  // a pane may mount hidden (pooled / expanded-mode non-focused tile).
  const wasHiddenRef = React.useRef(true);

  // keep the latest callbacks without re-creating the terminal
  const onDataRef = React.useRef(onData);
  const onResizeRef = React.useRef(onResize);
  // latest font size for the touch-swipe cell math (font size is user-configurable)
  const fontSizeRef = React.useRef(fontSize);
  fontSizeRef.current = fontSize;
  onDataRef.current = onData;
  onResizeRef.current = onResize;

  // Fit ONLY when the host is actually laid out. A pooled pane rendered display:none (an
  // eager-warmed pane that isn't focused, or the one just switched away from) has no layout;
  // iOS Safari's getComputedStyle then reports the host's width:100%/height:100% as the
  // literal "100%", so FitAddon parses 100px and resizes the terminal to a bogus ~13×6 —
  // which propagates (onResize → sock.resize) to the REAL tmux pane and permanently wraps
  // its scrollback narrow. clientWidth/Height are reliably 0 for a hidden element in every
  // browser, so this guard blocks the bad resize: a hidden pane keeps its last-good size and
  // the ResizeObserver re-fits it on reveal. (Chrome returns auto→NaN, caught by FitAddon's
  // own isNaN guard — this only bites iOS Safari, hence the mobile-only report.)
  const safeFit = React.useCallback((): { cols: number; rows: number } | null => {
    const host = hostRef.current;
    const term = termRef.current;
    const fit = fitRef.current;
    if (!host || !term || !fit || host.clientWidth === 0 || host.clientHeight === 0) return null;
    fit.fit();
    return { cols: term.cols, rows: term.rows };
  }, []);

  React.useImperativeHandle(ref, (): XTermHandle => ({
    write: (b) => termRef.current?.write(b),
    fit: safeFit,
    focus: () => termRef.current?.focus(),
    blur: () => termRef.current?.blur(),
    appCursor: () => !!termRef.current?.modes.applicationCursorKeysMode,
    getSelection: () => termRef.current?.getSelection() ?? "",
    paste: (text) => termRef.current?.paste(text),
    scrollLines: (n) => termRef.current?.scrollLines(n),
    reset: () => termRef.current?.reset(),
  }), []);

  React.useEffect(() => {
    const term = new Terminal({
      cursorBlink: true,
      fontSize,
      scrollback: 5000,
      fontFamily: "Menlo, Consolas, monospace",
      theme,
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.loadAddon(new WebLinksAddon());
    // WebGL is optional; load lazily with a fallback to the default renderer.
    // Guard on termRef.current: the component may unmount before the dynamic
    // import resolves, in which case `term` is already disposed.
    void import("@xterm/addon-webgl")
      .then(({ WebglAddon }) => {
        if (!termRef.current) return; // disposed before import resolved
        try {
          const addon = new WebglAddon();
          addon.onContextLoss(() => addon.dispose());
          termRef.current.loadAddon(addon);
        } catch { /* fall back to the default renderer */ }
      })
      .catch(() => {});
    term.open(hostRef.current!);
    termRef.current = term;
    fitRef.current = fit;
    safeFit(); // initial fit — a no-op if this pane mounted hidden; the RO refits on reveal
    term.onData((d) => onDataRef.current(d));
    // Shift+Enter → soft newline (see lib/terminal-keys): xterm emits CR for both
    // plain and shifted Enter, so intercept the shifted chord and send LF ourselves
    // (Claude Code's chat:newline) instead of xterm's default CR. Other Enter chords
    // fall through to xterm unchanged.
    term.attachCustomKeyEventHandler((ev) => {
      const override = keyOverride(ev);
      if (override === null) return true;
      ev.preventDefault();
      onDataRef.current(override);
      return false;
    });
    term.onResize(({ cols, rows }) => onResizeRef.current(cols, rows));

    // A stale renderer frame (the cursor drawn ~a line off, corrected only by the next
    // byte written) appears whenever the terminal stops being painted for a while: the
    // pane is display:none'd (an expanded-mode non-focused tile, or a route switch), OR
    // the whole PWA/tab is backgrounded (iPad app-switch). Force ONE repaint when it
    // comes back — cheap (one viewport redraw of content that's already there, so no
    // flicker), and scoped to a real visibility change, never a tile-to-tile focus switch.
    const forceRepaint = () => {
      const h = hostRef.current, t = termRef.current;
      if (!h || !t || h.clientWidth === 0 || h.clientHeight === 0) return; // hidden → skip
      try { t.refresh(0, Math.max(0, t.rows - 1)); } catch { /* renderer detached / mid context-loss */ }
    };
    // Element-level reveal (display:none → shown): the ResizeObserver sees size 0 → real.
    const ro = new ResizeObserver(() => {
      try {
        const hidden = !hostRef.current || hostRef.current.clientWidth === 0 || hostRef.current.clientHeight === 0;
        safeFit();
        if (wasHiddenRef.current && !hidden) forceRepaint();
        wasHiddenRef.current = hidden;
      } catch { /* detached */ }
    });
    ro.observe(hostRef.current!);
    // App/tab-level foreground: backgrounding the PWA doesn't change the element's size,
    // so the RO can't see it — the document's visibilitychange can. (This is the iPad
    // app-switch case.)
    const onVisible = () => { if (document.visibilityState === "visible") forceRepaint(); };
    document.addEventListener("visibilitychange", onVisible);

    // Touch gestures (quick drag = scroll, long-press + drag = select) live in
    // lib/terminal-touch so the state machine is unit-testable. We wire the xterm-specific bits:
    // synthetic mouse dispatch, scrollback scroll, live font size, whether the app has mouse
    // tracking on (→ force-select with shiftKey), and whether the platform is Mac-class (→ skip
    // the touch-select on Mac/iPad, which use a trackpad). Mobile-only; desktop uses real mouse
    // events untouched, and a quick swipe still scrolls, so this can't regress them.
    const host = hostRef.current!;
    const gesture = createTerminalGesture({
      fireMouse: (type, x, y, force) => {
        const target = type === "mousedown" ? (document.elementFromPoint(x, y) ?? host) : document;
        target.dispatchEvent(selectionMouseEvent(type, x, y, force));
      },
      scrollLines: (n) => term.scrollLines(n),
      fontSize: () => fontSizeRef.current,
      // Force-select (shiftKey) only when the running app has mouse tracking on, else a plain
      // synthetic click is forwarded to the app instead of selecting.
      mouseTracking: () => (termRef.current?.modes.mouseTrackingMode ?? "none") !== "none",
      // iPadOS reports as Mac (navigator.platform "MacIntel"); matches xterm's own isMac.
      isMac: () => /Mac/i.test(navigator.platform || ""),
    });
    host.addEventListener("touchstart", gesture.onStart, { passive: true });
    host.addEventListener("touchmove", gesture.onMove, { passive: false });
    host.addEventListener("touchend", gesture.onEnd, { passive: true });
    host.addEventListener("touchcancel", gesture.onEnd, { passive: true });

    return () => {
      ro.disconnect();
      document.removeEventListener("visibilitychange", onVisible);
      gesture.teardown();
      host.removeEventListener("touchstart", gesture.onStart);
      host.removeEventListener("touchmove", gesture.onMove);
      host.removeEventListener("touchend", gesture.onEnd);
      host.removeEventListener("touchcancel", gesture.onEnd);
      term.dispose();
      termRef.current = null;
      fitRef.current = null;
    };
  }, []);

  // Live-apply font size + theme changes without remounting (prefs are editable
  // while a pane is open). Refit so the new cell metrics reflow the grid.
  React.useEffect(() => {
    const term = termRef.current;
    if (!term) return;
    term.options.fontSize = fontSize;
    term.options.theme = theme;
    try { safeFit(); } catch { /* detached */ }
  }, [fontSize, theme]);

  return <div ref={hostRef} className="h-full w-full" />;
});
