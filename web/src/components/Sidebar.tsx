import type { SessionRow } from "@/components/SessionList";
import { Input } from "@/components/ui/input";
import { matchesQuery } from "@/components/SessionList";
import { sortBlockedFirst, rollUp } from "@/lib/state";
import { StateDot } from "@/components/StateDot";
import type { SessionState } from "@/lib/contracts";

// Desktop servers→sessions tree. Dots roll up; blocked sorts first.
export function Sidebar({
  rows, query, onQueryChange, onOpen, stateOf,
}: {
  rows: SessionRow[];
  query: string;
  onQueryChange(q: string): void;
  onOpen(row: SessionRow): void;
  stateOf(row: SessionRow): SessionState;
}) {
  const filtered = rows.filter((r) => matchesQuery(r, query));
  const byServer = new Map<string, SessionRow[]>();
  for (const r of filtered) {
    const list = byServer.get(r.server.name) ?? [];
    list.push(r);
    byServer.set(r.server.name, list);
  }
  const groups = sortBlockedFirst(
    [...byServer.entries()].map(([serverName, list]) => ({
      serverName,
      list: sortBlockedFirst(list, stateOf),
      serverState: rollUp(...list.map(stateOf)),
    })),
    (g) => g.serverState,
  );

  return (
    <aside className="flex h-full w-72 flex-none flex-col border-r border-border">
      <div className="p-3">
        <Input placeholder="Search…" value={query} onChange={(e) => onQueryChange(e.target.value)}
          aria-label="Search sessions" />
      </div>
      <div className="flex-1 overflow-y-auto">
        {groups.map(({ serverName, list, serverState }) => (
          <div key={serverName}>
            <div className="flex items-center gap-2 px-3 py-1">
              <StateDot state={serverState} />
              <span className="text-xs font-semibold uppercase text-muted-foreground">{serverName}</span>
            </div>
            {list.map((row) => (
              <button
                key={`${row.session.target}:${row.session.name}:${row.pane.id}`}
                className="flex w-full items-center gap-2 px-4 py-2 text-left text-sm hover:bg-accent"
                onClick={() => onOpen(row)}
              >
                <StateDot state={stateOf(row)} />
                <div className="min-w-0">
                  <div className="truncate">{row.session.name}</div>
                  <div className="truncate text-xs text-muted-foreground">{row.session.cwd || "—"}</div>
                </div>
              </button>
            ))}
          </div>
        ))}
      </div>
    </aside>
  );
}
