import * as React from "react";
import { Sidebar } from "@/components/Sidebar";
import { GridView } from "@/components/GridView";
import { usePanes, GRID_TILE_CAP } from "@/store/panes";
import type { SessionRow } from "@/components/SessionList";

export function DesktopShell({
  rows, query, onQueryChange,
}: {
  rows: SessionRow[];
  query: string;
  onQueryChange(q: string): void;
}) {
  const openPane = usePanes((s) => s.openPane);
  const [notice, setNotice] = React.useState<string | null>(null);

  function onOpen(row: SessionRow) {
    const r = openPane({
      serverId: row.server.id, paneId: row.pane.id, target: row.session.target,
      session: row.session.name, serverName: row.server.name,
    });
    if (!r.ok && r.reason === "cap") {
      setNotice(`Tile limit reached (${GRID_TILE_CAP}). Close a terminal to open another.`);
      setTimeout(() => setNotice(null), 4000);
    }
  }

  return (
    <div className="flex h-full">
      <Sidebar rows={rows} query={query} onQueryChange={onQueryChange} onOpen={onOpen} />
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
