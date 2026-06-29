import * as React from "react";
import type { Session, ServerSummary, Window, Pane } from "@/lib/contracts";
import { Input } from "@/components/ui/input";

export interface SessionRow {
  server: ServerSummary;
  session: Session;
  window: Pick<Window, "id" | "index" | "name">;
  pane: Pane;
}

// Each session is shown by its first pane (the session's primary terminal).
export function flattenSessions(
  servers: ServerSummary[],
  byServer: Record<string, Session[]>,
): SessionRow[] {
  const rows: SessionRow[] = [];
  for (const server of servers) {
    for (const session of byServer[server.id] ?? []) {
      const win = session.windows[0];
      const pane = win?.panes[0];
      if (!win || !pane) continue;
      rows.push({ server, session, window: { id: win.id, index: win.index, name: win.name }, pane });
    }
  }
  return rows;
}

export function matchesQuery(row: SessionRow, q: string): boolean {
  if (!q) return true;
  const hay = `${row.server.name} ${row.session.name} ${row.session.cwd} ${row.session.command}`.toLowerCase();
  return hay.includes(q.toLowerCase());
}

export function SessionList({
  rows, query, onQueryChange, onOpen,
}: {
  rows: SessionRow[];
  query: string;
  onQueryChange(q: string): void;
  onOpen(row: SessionRow): void;
}) {
  const filtered = rows.filter((r) => matchesQuery(r, query));
  return (
    <div className="flex h-full flex-col">
      <div className="p-3">
        <Input placeholder="Search server, session, path…" value={query}
          onChange={(e) => onQueryChange(e.target.value)} aria-label="Search sessions" />
      </div>
      <ul className="flex-1 overflow-y-auto">
        {filtered.map((row) => (
          <li key={`${row.server.id}:${row.session.target}:${row.session.name}:${row.pane.id}`}>
            <button
              className="w-full border-b border-border px-4 py-3 text-left hover:bg-accent"
              onClick={() => onOpen(row)}
            >
              <div className="font-medium">{row.session.name}</div>
              <div className="text-xs text-muted-foreground">
                {row.server.name} · {row.session.cwd || "—"}
              </div>
            </button>
          </li>
        ))}
        {filtered.length === 0 && (
          <li className="px-4 py-6 text-center text-sm text-muted-foreground">No sessions</li>
        )}
      </ul>
    </div>
  );
}
