import * as React from "react";
import type { Session, ServerSummary, Window, Pane, SessionState } from "@/lib/contracts";
import { Input } from "@/components/ui/input";
import { StateDot } from "@/components/StateDot";
import { SessionNameEditor } from "@/components/SessionNameEditor";
import { ProviderTag } from "@/components/ProviderTag";
import { providerOf } from "@/lib/provider";
import { rowActivation } from "@/lib/row-activation";
import { paneKey } from "@/store/panes";

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

// Mobile §6.2 sectioned inbox: 4 ordered groups. idle + unknown share the "Idle"
// bucket so the noisy unknown state doesn't sprout its own near-permanent section.
const SECTIONS: ReadonlyArray<{ key: string; label: string; states: ReadonlyArray<SessionState> }> = [
  { key: "attention", label: "Needs attention", states: ["blocked"] },
  { key: "done", label: "Done", states: ["done"] },
  { key: "working", label: "Working", states: ["working"] },
  { key: "idle", label: "Idle", states: ["idle", "unknown"] },
];

export function SessionList({
  rows, query, onQueryChange, onOpen, stateOf,
}: {
  rows: SessionRow[];
  query: string;
  onQueryChange(q: string): void;
  onOpen(row: SessionRow): void;
  stateOf(row: SessionRow): SessionState;
}) {
  const filtered = rows.filter((r) => matchesQuery(r, query));
  const groups = SECTIONS.map((section) => ({
    ...section,
    rows: filtered.filter((r) => section.states.includes(stateOf(r))),
  })).filter((g) => g.rows.length > 0);
  return (
    <div className="flex h-full flex-col">
      <div className="p-3">
        <Input placeholder="Search server, session, path…" value={query}
          onChange={(e) => onQueryChange(e.target.value)} aria-label="Search sessions" />
      </div>
      <ul className="flex-1 overflow-y-auto">
        {groups.map((group) => (
          <React.Fragment key={group.key}>
            <li
              data-section={group.key}
              className="border-b border-border bg-muted/40 px-4 py-1.5 text-xs font-semibold uppercase tracking-wide text-muted-foreground"
            >
              {group.label}
            </li>
            {group.rows.map((row) => (
              <li key={paneKey(row.server.id, row.session.target, row.session.name, row.pane.id)}>
                <div
                  {...rowActivation(() => onOpen(row))}
                  className="flex w-full cursor-pointer items-center gap-2 border-b border-border px-4 py-3 text-left hover:bg-accent"
                >
                  <StateDot state={stateOf(row)} />
                  <div className="min-w-0">
                    <span className="flex items-center gap-1.5">
                      <SessionNameEditor
                        className="font-medium"
                        serverId={row.server.id}
                        target={row.session.target}
                        name={row.session.name}
                        paneId={row.pane.id}
                      />
                      <ProviderTag provider={providerOf(row.session.command)} />
                    </span>
                    <div className="text-xs text-muted-foreground">{row.server.name} · {row.session.cwd || "—"}</div>
                  </div>
                </div>
              </li>
            ))}
          </React.Fragment>
        ))}
        {groups.length === 0 && (
          <li className="px-4 py-6 text-center text-sm text-muted-foreground">No sessions</li>
        )}
      </ul>
    </div>
  );
}
