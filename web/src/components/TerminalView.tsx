import * as React from "react";
import { XTerm } from "@/components/XTerm";
import { MobileKeyBar } from "@/components/MobileKeyBar";
import { useTerminalSession } from "@/hooks/useTerminalSession";

export function TerminalView({
  serverId, paneId, target, showKeyBar = false,
}: {
  serverId: string;
  paneId: string;
  target: string;
  showKeyBar?: boolean;
}) {
  const targetObj = React.useMemo(() => ({ serverId, paneId, target }), [serverId, paneId, target]);
  const { xtermRef, controller, connected, handleData, handleResize } = useTerminalSession(targetObj);

  return (
    <div className="relative flex h-full w-full flex-col">
      {!connected && (
        <div className="absolute left-0 right-0 top-0 z-10 bg-destructive px-2 py-1 text-center text-xs font-semibold text-destructive-foreground">
          disconnected — reconnecting…
        </div>
      )}
      <div className="min-h-0 flex-1">
        <XTerm ref={xtermRef} onData={handleData} onResize={handleResize} />
      </div>
      {showKeyBar && <MobileKeyBar controller={controller} />}
    </div>
  );
}
