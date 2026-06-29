import * as React from "react";
import { TerminalSocket, type TerminalTarget } from "@/lib/ws-terminal";
import type { XTermHandle } from "@/components/XTerm";
import * as keys from "@/lib/keybar";

export interface TerminalController {
  sendKey(k: keys.BarKey): void;
  toggleCtrl(): void;
  ctrlArmed: boolean;
  paste(): Promise<void>;
  copy(): Promise<void>;
}

export function useTerminalSession(target: TerminalTarget) {
  const xtermRef = React.useRef<XTermHandle>(null);
  const sockRef = React.useRef<TerminalSocket | null>(null);
  const ctrlArmedRef = React.useRef(false);
  const [ctrlArmed, setCtrlArmed] = React.useState(false);
  const [connected, setConnected] = React.useState(false);

  const send = React.useCallback((b: Uint8Array) => sockRef.current?.send(b), []);

  // typed/pasted text from xterm → apply sticky Ctrl → socket
  const handleData = React.useCallback((d: string) => {
    if (ctrlArmedRef.current) {
      send(keys.encodeCtrl(d));
      ctrlArmedRef.current = false;
      setCtrlArmed(false);
      return;
    }
    send(keys.utf8(d));
  }, [send]);

  const handleResize = React.useCallback((cols: number, rows: number) => {
    sockRef.current?.resize(cols, rows);
  }, []);

  React.useEffect(() => {
    const sock = new TerminalSocket(target, {
      onData: (b) => xtermRef.current?.write(b),
      onOpen: () => {
        setConnected(true);
        xtermRef.current?.reset();           // fresh paint; snapshot arrives next as binary
        const size = xtermRef.current?.fit();
        if (size) sock.resize(size.cols, size.rows);
        xtermRef.current?.focus();
      },
      onClose: () => setConnected(false),
    });
    sockRef.current = sock;
    sock.open();
    return () => { sock.dispose(); sockRef.current = null; };
    // re-create only when the pane target changes
  }, [target.serverId, target.paneId, target.target]);

  const controller = React.useMemo<TerminalController>(() => ({
    ctrlArmed,
    sendKey(k) {
      switch (k) {
        case "esc": return send(keys.ESC);
        case "tab": return send(keys.TAB);
        case "stab": return send(keys.SHIFT_TAB);
        case "enter": return send(keys.ENTER);
        case "nl": return send(keys.SOFT_NEWLINE);
        case "up": case "down": case "left": case "right":
          return send(keys.arrow(k, xtermRef.current?.appCursor() ?? false));
      }
    },
    toggleCtrl() {
      ctrlArmedRef.current = !ctrlArmedRef.current;
      setCtrlArmed(ctrlArmedRef.current);
    },
    async paste() {
      try {
        const text = await navigator.clipboard.readText();
        if (!text) return;
        if (text.includes("\n") || text.length > 200) {
          const lines = text.split("\n").length;
          if (!confirm(`Paste ${text.length} chars / ${lines} lines?`)) return;
        }
        xtermRef.current?.paste(text); // xterm owns bracketed-paste framing
      } catch { /* clipboard needs a secure context + permission */ }
    },
    async copy() {
      const sel = xtermRef.current?.getSelection() ?? "";
      if (!sel) return;
      try { await navigator.clipboard.writeText(sel); } catch { /* secure context */ }
    },
  }), [ctrlArmed, send]);

  return { xtermRef, controller, connected, handleData, handleResize };
}
