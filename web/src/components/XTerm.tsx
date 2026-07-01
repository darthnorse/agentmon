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

    // touch swipe = scroll the scrollback (do not let the page scroll)
    const host = hostRef.current!;
    let startY: number | null = null;
    const onStart = (e: TouchEvent) => { if (e.touches.length === 1) startY = e.touches[0].clientY; };
    const onMove = (e: TouchEvent) => {
      if (startY === null || e.touches.length !== 1) return;
      const y = e.touches[0].clientY;
      const dy = startY - y;
      const cell = (fontSizeRef.current || 13) * 1.2;
      if (Math.abs(dy) > 6) {
        const lines = Math.trunc(dy / cell);
        if (lines !== 0) { term.scrollLines(lines); startY = y; }
        e.preventDefault();
      }
    };
    const onEnd = () => { startY = null; };
    host.addEventListener("touchstart", onStart, { passive: true });
    host.addEventListener("touchmove", onMove, { passive: false });
    host.addEventListener("touchend", onEnd, { passive: true });

    return () => {
      ro.disconnect();
      host.removeEventListener("touchstart", onStart);
      host.removeEventListener("touchmove", onMove);
      host.removeEventListener("touchend", onEnd);
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
