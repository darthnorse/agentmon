import * as React from "react";
import type { ITheme } from "@xterm/xterm";
import { XTerm } from "@/components/XTerm";
import { MobileKeyBar } from "@/components/MobileKeyBar";
import { useTerminalSession } from "@/hooks/useTerminalSession";

export function TerminalView({
  serverId, paneId, target, showKeyBar = false, active, focusNonce, fontSize, theme,
}: {
  serverId: string;
  paneId: string;
  target: string;
  showKeyBar?: boolean;
  active?: boolean;
  focusNonce?: number;
  fontSize?: number;
  theme?: ITheme;
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
        <div className="absolute left-0 right-0 top-0 z-10 bg-destructive px-2 py-1 text-center text-xs font-semibold text-destructive-foreground">
          {everConnected ? "disconnected — reconnecting…" : "connecting…"}
        </div>
      )}
      <div className="min-h-0 flex-1">
        <XTerm ref={xtermRef} onData={handleData} onResize={handleResize} fontSize={fontSize} theme={theme} />
      </div>
      {showKeyBar && <MobileKeyBar controller={controller} />}
    </div>
  );
}
