import * as React from "react";
import { Sidebar } from "@/components/Sidebar";
import { GridView } from "@/components/GridView";
import { usePanes, GRID_TILE_CAP } from "@/store/panes";
import { useFocusedSeen } from "@/hooks/useFocusedSeen";
import type { SessionRow } from "@/components/SessionList";
import type { SeenRequest, SessionState } from "@/lib/contracts";

export function DesktopShell({
  rows, query, onQueryChange, stateOf,
}: {
  rows: SessionRow[];
  query: string;
  onQueryChange(q: string): void;
  stateOf(row: SessionRow): SessionState;
}) {
  const openPane = usePanes((s) => s.openPane);
  const panes = usePanes((s) => s.panes);
  const focusedId = usePanes((s) => s.focusedId);
  const focusedReq = React.useMemo<SeenRequest | null>(() => {
    const p = panes.find((x) => x.id === focusedId);
    return p ? { serverId: p.serverId, target: p.target, sessionName: p.session } : null;
  }, [panes, focusedId]);
  useFocusedSeen(focusedReq);
  const [notice, setNotice] = React.useState<string | null>(null);
  const noticeTimer = React.useRef<ReturnType<typeof setTimeout>>(undefined);

  // Clear the notice timer on unmount to avoid setState-on-unmounted-component.
  React.useEffect(() => () => clearTimeout(noticeTimer.current), []);

  function onOpen(row: SessionRow) {
    const r = openPane({
      serverId: row.server.id, paneId: row.pane.id, target: row.session.target,
      session: row.session.name, serverName: row.server.name, state: row.session.state,
    });
    if (!r.ok && r.reason === "cap") {
      clearTimeout(noticeTimer.current);
      setNotice(`Tile limit reached (${GRID_TILE_CAP}). Close a terminal to open another.`);
      noticeTimer.current = setTimeout(() => setNotice(null), 4000);
    }
  }

  return (
    <div className="flex h-full">
      <Sidebar rows={rows} query={query} onQueryChange={onQueryChange} onOpen={onOpen} stateOf={stateOf} />
      <main className="min-w-0 flex-1">
        {notice && (
          <div className="bg-destructive px-3 py-1 text-center text-xs font-semibold text-destructive-foreground">
            {notice}
          </div>
        )}
        <GridView />
      </main>
    </div>
  );
}
