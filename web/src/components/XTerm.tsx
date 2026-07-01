import * as React from "react";
import { Terminal, type ITheme } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import { TERMINAL_THEMES } from "@/lib/terminal-themes";
import { keyOverride } from "@/lib/terminal-keys";
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

    const ro = new ResizeObserver(() => { try { safeFit(); } catch { /* detached */ } });
    ro.observe(hostRef.current!);

    // Touch gestures on the terminal:
    //   • a quick vertical drag scrolls the scrollback (don't let the page scroll);
    //   • a long-press (~450ms held still) then drag SELECTS text.
    // xterm's selection is mouse-only — it binds no touch/pointer listeners — and our scroll
    // preventDefault otherwise suppresses the mouse events iOS would synthesize, so drag-select
    // never reaches xterm. We bridge the long-press drag to synthetic mouse events
    // (mousedown → mousemove* → mouseup) that drive xterm's SelectionService; then the key
    // bar's Copy (controller.copy → getSelection) copies it. Mobile-only; desktop uses real
    // mouse events untouched, and a quick swipe still scrolls, so this can't regress either.
    const host = hostRef.current!;
    const LONG_PRESS_MS = 450;
    const MOVE_CANCEL = 10; // px of travel that turns a still-pending hold into a scroll
    let startX = 0, startY = 0, lastX = 0, lastY = 0;
    let panMode: "pending" | "scroll" | "select" | null = null;
    let lpTimer: ReturnType<typeof setTimeout> | null = null;
    const clearLp = () => { if (lpTimer !== null) { clearTimeout(lpTimer); lpTimer = null; } };
    const fireMouse = (type: "mousedown" | "mousemove" | "mouseup", x: number, y: number) => {
      const target = type === "mousedown" ? (document.elementFromPoint(x, y) ?? host) : document;
      target.dispatchEvent(new MouseEvent(type, {
        bubbles: true, cancelable: true, view: window,
        clientX: x, clientY: y, button: 0, buttons: type === "mouseup" ? 0 : 1,
      }));
    };

    const onStart = (e: TouchEvent) => {
      if (e.touches.length !== 1) { panMode = null; clearLp(); return; }
      const t = e.touches[0];
      startX = lastX = t.clientX; startY = lastY = t.clientY;
      panMode = "pending";
      clearLp();
      lpTimer = setTimeout(() => {
        lpTimer = null;
        if (panMode !== "pending") return;     // already moved into a scroll
        panMode = "select";
        fireMouse("mousedown", startX, startY); // begin an xterm selection at the hold point
      }, LONG_PRESS_MS);
    };
    const onMove = (e: TouchEvent) => {
      if (panMode === null || e.touches.length !== 1) return;
      const t = e.touches[0];
      lastX = t.clientX; lastY = t.clientY;
      if (panMode === "select") {
        fireMouse("mousemove", t.clientX, t.clientY); // xterm extends the selection
        e.preventDefault();
        return;
      }
      if (panMode === "pending") {
        if (Math.abs(t.clientY - startY) + Math.abs(t.clientX - startX) < MOVE_CANCEL) return;
        panMode = "scroll"; clearLp();                // moved before the hold fired → scroll
      }
      const dy = startY - t.clientY;
      const cell = (fontSizeRef.current || 13) * 1.2;
      if (Math.abs(dy) > 6) {
        const lines = Math.trunc(dy / cell);
        if (lines !== 0) { term.scrollLines(lines); startY = t.clientY; }
      }
      e.preventDefault();
    };
    const onEnd = () => {
      clearLp();
      if (panMode === "select") fireMouse("mouseup", lastX, lastY); // finalize the selection
      panMode = null;
    };
    host.addEventListener("touchstart", onStart, { passive: true });
    host.addEventListener("touchmove", onMove, { passive: false });
    host.addEventListener("touchend", onEnd, { passive: true });
    host.addEventListener("touchcancel", onEnd, { passive: true });

    return () => {
      ro.disconnect();
      clearLp();
      host.removeEventListener("touchstart", onStart);
      host.removeEventListener("touchmove", onMove);
      host.removeEventListener("touchend", onEnd);
      host.removeEventListener("touchcancel", onEnd);
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
