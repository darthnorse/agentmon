import type { SessionRow } from "@/components/SessionList";
import { Input } from "@/components/ui/input";
import { matchesQuery } from "@/components/SessionList";

// Desktop servers→sessions tree. Clicking a session opens it as a live grid tile.
export function Sidebar({
  rows, query, onQueryChange, onOpen,
}: {
  rows: SessionRow[];
  query: string;
  onQueryChange(q: string): void;
  onOpen(row: SessionRow): void;
}) {
  const filtered = rows.filter((r) => matchesQuery(r, query));
  const byServer = new Map<string, SessionRow[]>();
  for (const r of filtered) {
    const list = byServer.get(r.server.name) ?? [];
    list.push(r);
    byServer.set(r.server.name, list);
  }
  return (
    <aside className="flex h-full w-72 flex-none flex-col border-r border-border">
      <div className="p-3">
        <Input placeholder="Search…" value={query} onChange={(e) => onQueryChange(e.target.value)}
          aria-label="Search sessions" />
      </div>
      <div className="flex-1 overflow-y-auto">
        {[...byServer.entries()].map(([serverName, list]) => (
          <div key={serverName}>
            <div className="px-3 py-1 text-xs font-semibold uppercase text-muted-foreground">{serverName}</div>
            {list.map((row) => (
              <button
                key={`${row.session.target}:${row.session.name}:${row.pane.id}`}
                className="block w-full px-4 py-2 text-left text-sm hover:bg-accent"
                onClick={() => onOpen(row)}
              >
                <div className="truncate">{row.session.name}</div>
                <div className="truncate text-xs text-muted-foreground">{row.session.cwd || "—"}</div>
              </button>
            ))}
          </div>
        ))}
      </div>
    </aside>
  );
}
