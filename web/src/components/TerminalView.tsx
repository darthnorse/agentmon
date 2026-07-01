import * as React from "react";
import type { ITheme } from "@xterm/xterm";
import { XTerm } from "@/components/XTerm";
import { MobileKeyBar } from "@/components/MobileKeyBar";
import { useTerminalSession } from "@/hooks/useTerminalSession";

export function TerminalView({
  serverId, paneId, target, showKeyBar = false, active, fontSize, theme,
}: {
  serverId: string;
  paneId: string;
  target: string;
  showKeyBar?: boolean;
  active?: boolean;
  fontSize?: number;
  theme?: ITheme;
}) {
  const targetObj = React.useMemo(() => ({ serverId, paneId, target }), [serverId, paneId, target]);
  const { xtermRef, controller, connected, everConnected, handleData, handleResize } = useTerminalSession(targetObj);

  // When this pane becomes the visible/focused one in the mobile pool, hand keyboard
  // focus to its xterm. Guarded on active===true so existing callers (grid) that pass
  // no `active` are unaffected.
  React.useEffect(() => {
    if (active) xtermRef.current?.focus();
  }, [active]);

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
