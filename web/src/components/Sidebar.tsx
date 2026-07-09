import type { SessionRow } from "@/components/SessionList";
import type { ServerSummary, SessionState } from "@/lib/contracts";
import { Input } from "@/components/ui/input";
import { matchesQuery } from "@/components/SessionList";
import { sortBlockedFirst, rollUp } from "@/lib/state";
import { StateDot } from "@/components/StateDot";
import { SessionActionsMenu } from "@/components/SessionActionsMenu";
import { rowActivation } from "@/lib/row-activation";
import { providerOf } from "@/lib/provider";

// Desktop servers→sessions tree. Dots roll up; blocked sorts first. The tree is
// seeded from the full `servers` list so a session-less server still renders (its
// REST `state` dot, or `unknown`) — the M8-deferred server-dot fallback.
export function Sidebar({
  servers, rows, query, onQueryChange, onOpen, stateOf,
}: {
  servers: ServerSummary[];
  rows: SessionRow[];
  query: string;
  onQueryChange(q: string): void;
  onOpen(row: SessionRow): void;
  stateOf(row: SessionRow): SessionState;
}) {
  const q = query.trim().toLowerCase();
  const filtered = rows.filter((r) => matchesQuery(r, query));
  const rowsByServer = new Map<string, SessionRow[]>();
  for (const r of filtered) {
    const list = rowsByServer.get(r.server.id) ?? [];
    list.push(r);
    rowsByServer.set(r.server.id, list);
  }
  // Servers that have *any* session at all (pre-filter) — used to tell a truly
  // session-less server (always shown) from one whose sessions the filter hid.
  const hasSessions = new Set(rows.map((r) => r.server.id));

  const visibleGroups = servers
    .map((srv) => {
      const list = sortBlockedFirst(rowsByServer.get(srv.id) ?? [], stateOf);
      const serverState: SessionState = list.length
        ? rollUp(...list.map(stateOf))
        : (srv.state ?? "unknown");
      return { id: srv.id, serverName: srv.name, list, serverState, sessionLess: !hasSessions.has(srv.id) };
    })
    .filter((g) =>
      // Keep a server if it has matching sessions, or it is session-less and its
      // name passes the search (session-less servers can only match by name).
      g.list.length > 0 || (g.sessionLess && (!q || g.serverName.toLowerCase().includes(q))),
    );
  const groups = sortBlockedFirst(visibleGroups, (g) => g.serverState);

  return (
    <aside className="flex h-full w-72 flex-none flex-col border-r border-border">
      <div className="p-3">
        <Input placeholder="Search…" value={query} onChange={(e) => onQueryChange(e.target.value)}
          aria-label="Search sessions" />
      </div>
      <div className="flex-1 overflow-y-auto">
        {groups.map(({ id, serverName, list, serverState }) => (
          <div key={id}>
            <div className="flex items-center gap-2 px-3 py-1">
              <span className="text-xs font-semibold uppercase text-muted-foreground">{serverName}</span>
            </div>
            {list.map((row) => (
              <div
                key={`${row.session.target}:${row.session.name}:${row.pane.id}`}
                {...rowActivation(() => onOpen(row))}
                className="flex w-full cursor-pointer items-center gap-2 px-4 py-2 text-left text-sm hover:bg-accent"
              >
                <StateDot state={stateOf(row)} />
                <div className="min-w-0 flex-1">
                  <SessionActionsMenu
                    serverId={row.server.id}
                    serverName={serverName}
                    target={row.session.target}
                    name={row.session.name}
                    paneId={row.pane.id}
                    state={stateOf(row)}
                    provider={providerOf(row.session.command)}
                  />
                  <div className="truncate text-xs text-muted-foreground">{row.session.cwd || "—"}</div>
                </div>
              </div>
            ))}
          </div>
        ))}
      </div>
    </aside>
  );
}
