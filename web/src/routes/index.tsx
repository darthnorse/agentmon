import * as React from "react";
import { useQuery, useQueries } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { listServers, listSessions } from "@/lib/api-client";
import { useAuth } from "@/store/auth";
import { Button } from "@/components/ui/button";
import { SessionList, flattenSessions, type SessionRow } from "@/components/SessionList";
import { DesktopShell } from "@/components/DesktopShell";
import { useMediaQuery } from "@/lib/use-media-query";
import { useStateSnapshot } from "@/store/session-state";
import { effectiveSessionState } from "@/lib/state";
import type { Session, SessionState } from "@/lib/contracts";

export function ShellRoute() {
  const navigate = useNavigate();
  const signOut = useAuth((s) => s.signOut);
  const [query, setQuery] = React.useState("");
  const isDesktop = useMediaQuery("(min-width: 1024px)");

  const serversQ = useQuery({ queryKey: ["servers"], queryFn: listServers });
  const servers = serversQ.data ?? [];

  const sessionQs = useQueries({
    queries: servers.map((s) => ({
      queryKey: ["sessions", s.id],
      queryFn: () => listSessions(s.id),
    })),
  });

  const byServer: Record<string, Session[]> = {};
  servers.forEach((s, i) => { byServer[s.id] = (sessionQs[i]?.data as Session[]) ?? []; });
  const rows = flattenSessions(servers, byServer);

  const snap = useStateSnapshot();
  const stateOf = (row: SessionRow): SessionState =>
    effectiveSessionState(snap, row.server.id, row.session.target, row.session.name, row.session.state);

  return (
    <div className="flex h-full flex-col">
      <header className="flex items-center justify-between border-b border-border px-4 py-2">
        <span className="font-semibold">AgentMon</span>
        <Button variant="ghost" size="sm" onClick={() => signOut().finally(() => navigate({ to: "/login" }))}>
          Sign out
        </Button>
      </header>
      <div className="min-h-0 flex-1">
        {serversQ.isLoading ? (
          <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
            Loading…
          </div>
        ) : serversQ.isError ? (
          <div className="flex h-full flex-col items-center justify-center gap-2 text-sm">
            <span className="text-destructive">Failed to load servers.</span>
            <Button variant="outline" size="sm" onClick={() => serversQ.refetch()}>Retry</Button>
          </div>
        ) : isDesktop ? (
          <DesktopShell rows={rows} query={query} onQueryChange={setQuery} stateOf={stateOf} />
        ) : (
          <SessionList
            rows={rows}
            query={query}
            onQueryChange={setQuery}
            stateOf={stateOf}
            onOpen={(row) =>
              navigate({
                to: "/t/$serverId/$paneId",
                params: { serverId: row.server.id, paneId: row.pane.id },
                search: { target: row.session.target, session: row.session.name },
              })
            }
          />
        )}
      </div>
    </div>
  );
}
