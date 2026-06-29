import { useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { TerminalView } from "@/components/TerminalView";
import { Button } from "@/components/ui/button";
import { useFocusedSeen } from "@/hooks/useFocusedSeen";

export interface TerminalSearch { target: string; session: string; }

export function MobileTerminalRoute() {
  const { serverId, paneId } = useParams({ strict: false }) as { serverId: string; paneId: string };
  const { target, session } = useSearch({ strict: false }) as TerminalSearch;
  const navigate = useNavigate();

  useFocusedSeen({ serverId, target, sessionName: session });

  return (
    <div className="flex h-full flex-col">
      <header className="flex items-center gap-2 border-b border-border px-2 py-2">
        <Button variant="ghost" size="sm" onClick={() => navigate({ to: "/" })}>‹ Back</Button>
        <div className="min-w-0">
          <div className="truncate font-medium">{session}</div>
          <div className="truncate text-xs text-muted-foreground">{serverId} · {paneId}</div>
        </div>
      </header>
      <div className="min-h-0 flex-1">
        <TerminalView serverId={serverId} paneId={paneId} target={target} showKeyBar fontSize={10} />
      </div>
    </div>
  );
}
