import * as React from "react";
import { TerminalSocket, type TerminalTarget } from "@/lib/ws-terminal";
import type { XTermHandle } from "@/components/XTerm";
import { useSessionState } from "@/store/session-state";
import { normalizeState, stateKey } from "@/lib/state";
import * as keys from "@/lib/keybar";
import { onReconnectKick } from "@/lib/reconnect-kick";
import { paneIdentity } from "@/lib/pane-identity";

export interface TerminalController {
  sendKey(k: keys.BarKey): void;
  toggleCtrl(): void;
  ctrlArmed: boolean;
  paste(): Promise<void>;
  copy(): Promise<void>;
  dismissKeyboard(): void;
  focusTerminal(): void;
}

export function useTerminalSession(target: TerminalTarget, opts?: { readOnly?: boolean }) {
  const xtermRef = React.useRef<XTermHandle>(null);
  const sockRef = React.useRef<TerminalSocket | null>(null);
  const ctrlArmedRef = React.useRef(false);
  // Watch-only preview: suppress input, resize, and focus so viewing an epic can
  // never type into or reshape the live runner's tmux pane. Held in a ref so the
  // socket effect (which only re-creates on target change) always sees the latest.
  const readOnlyRef = React.useRef(opts?.readOnly ?? false);
  readOnlyRef.current = opts?.readOnly ?? false;
  const [ctrlArmed, setCtrlArmed] = React.useState(false);
  const [connected, setConnected] = React.useState(false);
  const [everConnected, setEverConnected] = React.useState(false);

  const send = React.useCallback((b: Uint8Array) => sockRef.current?.send(b), []);

  // typed/pasted text from xterm → apply sticky Ctrl → socket
  const handleData = React.useCallback((d: string) => {
    if (readOnlyRef.current) return; // watch-only: never forward keystrokes to the runner
    if (ctrlArmedRef.current) {
      send(keys.encodeCtrl(d));
      ctrlArmedRef.current = false;
      setCtrlArmed(false);
      return;
    }
    send(keys.utf8(d));
  }, [send]);

  const handleResize = React.useCallback((cols: number, rows: number) => {
    if (readOnlyRef.current) return; // watch-only: never resize the real tmux pane
    sockRef.current?.resize(cols, rows);
  }, []);

  const retryNow = React.useCallback(() => sockRef.current?.retryNow(), []);

  React.useEffect(() => {
    const sock = new TerminalSocket(target, {
      onData: (b) => xtermRef.current?.write(b),
      onOpen: () => {
        setConnected(true);
        setEverConnected(true);
        xtermRef.current?.reset();           // fresh paint; snapshot arrives next as binary
        const size = xtermRef.current?.fit();
        if (size && !readOnlyRef.current) sock.resize(size.cols, size.rows);
        if (!readOnlyRef.current) xtermRef.current?.focus();
      },
      onClose: () => setConnected(false),
      // Live hub state delta for this pane. ONLY the focused pane consumes it (its
      // dot tracks blocked/done a touch sooner than SSE). A non-focused open tile
      // must NOT pre-write the shared store: the SSE alert gate reads the prior
      // state from that store, so a terminal-WS frame landing first would make the
      // gate see "no transition" and silently drop the attention alert (M9 core
      // loop). The focused pane is suppressed from alerts anyway, so it's safe.
      onState: (f) => {
        const key = stateKey(target.serverId, target.target, f.session);
        if (key === useSessionState.getState().focusedKey) {
          useSessionState.getState().applyDelta({
            server: target.serverId,
            target: target.target,
            session: f.session,
            state: normalizeState(f.state),
          });
        }
      },
    });
    sockRef.current = sock;
    sock.open();
    // Re-open/focus of this pane elsewhere in the UI (panes-store dedupe, tile
    // activation) must not leave this socket asleep in reconnect backoff.
    const offKick = onReconnectKick(
      paneIdentity(target.serverId, target.target, target.paneId),
      () => sock.retryNow(),
    );
    return () => { offKick(); sock.dispose(); sockRef.current = null; };
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
    dismissKeyboard() {
      xtermRef.current?.blur(); // drops focus on xterm's hidden textarea → soft keyboard closes
    },
    // Re-assert focus on xterm's hidden textarea so the soft keyboard stays up
    // after a key-bar tap. Called within the tap's click gesture, so iOS keeps
    // (or re-shows) the keyboard. Only dismissKeyboard() ever blurs.
    focusTerminal() {
      xtermRef.current?.focus();
    },
  }), [ctrlArmed, send]);

  return { xtermRef, controller, connected, everConnected, handleData, handleResize, retryNow };
}
