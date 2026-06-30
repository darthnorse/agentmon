import * as React from "react";
import { Terminal, type ITheme } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import { TERMINAL_THEMES } from "@/lib/terminal-themes";
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

  React.useImperativeHandle(ref, (): XTermHandle => ({
    write: (b) => termRef.current?.write(b),
    fit: () => {
      fitRef.current?.fit();
      const t = termRef.current;
      return t ? { cols: t.cols, rows: t.rows } : null;
    },
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
    fit.fit();
    term.onData((d) => onDataRef.current(d));
    term.onResize(({ cols, rows }) => onResizeRef.current(cols, rows));
    termRef.current = term;
    fitRef.current = fit;

    const ro = new ResizeObserver(() => { try { fit.fit(); } catch { /* detached */ } });
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
    try { fitRef.current?.fit(); } catch { /* detached */ }
  }, [fontSize, theme]);

  return <div ref={hostRef} className="h-full w-full" />;
});
