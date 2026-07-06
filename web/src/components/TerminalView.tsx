import * as React from "react";
import type { ITheme } from "@xterm/xterm";
import { XTerm } from "@/components/XTerm";
import { MobileKeyBar } from "@/components/MobileKeyBar";
import { useTerminalSession } from "@/hooks/useTerminalSession";

export function TerminalView({
  serverId, paneId, target, showKeyBar = false, active, focusNonce, fontSize, theme,
  ended, onClose,
}: {
  serverId: string;
  paneId: string;
  target: string;
  showKeyBar?: boolean;
  active?: boolean;
  focusNonce?: number;
  fontSize?: number;
  theme?: ITheme;
  // The pane is confirmed gone (absent from a successful sessions fetch). Display-
  // only: the socket keeps its slow retry so a recycled pane id self-heals.
  ended?: boolean;
  onClose?: () => void;
}) {
  const targetObj = React.useMemo(() => ({ serverId, paneId, target }), [serverId, paneId, target]);
  const { xtermRef, controller, connected, everConnected, handleData, handleResize, retryNow } =
    useTerminalSession(targetObj);

  // Hand keyboard focus to this pane's xterm when it becomes active (the mobile pool's
  // visible pane, or a desktop grid tile). The effect also re-runs when `focusNonce`
  // changes, so the desktop grid can force a refocus on a repeated window-switch chord
  // even when `active` did not change (e.g. focus had left the grid). Guarded on
  // active===true; callers that pass no `active` are unaffected, and `focusNonce` is
  // undefined on the mobile path so it never re-triggers there.
  React.useEffect(() => {
    if (active) {
      xtermRef.current?.focus();
      // A tile brought to the front must not sit out a reconnect backoff window.
      retryNow();
    }
  }, [active, focusNonce, retryNow]);

  return (
    <div className="relative flex h-full w-full flex-col">
      {!connected && (
        ended ? (
          <div className="absolute left-0 right-0 top-0 z-10 flex items-center justify-center gap-3 bg-muted px-2 py-1 text-center text-xs font-semibold text-muted-foreground">
            <span>session ended</span>
            {onClose && (
              <button type="button" className="underline underline-offset-2" onClick={onClose}>
                close
              </button>
            )}
          </div>
        ) : (
          <div className="absolute left-0 right-0 top-0 z-10 bg-destructive px-2 py-1 text-center text-xs font-semibold text-destructive-foreground">
            {everConnected ? "disconnected — reconnecting…" : "connecting…"}
          </div>
        )
      )}
      <div className="min-h-0 flex-1">
        <XTerm ref={xtermRef} onData={handleData} onResize={handleResize} fontSize={fontSize} theme={theme} />
      </div>
      {showKeyBar && <MobileKeyBar controller={controller} />}
    </div>
  );
}
