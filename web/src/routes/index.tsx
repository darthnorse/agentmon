import * as React from "react";
import { useQuery, useQueries } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { listServers, listSessions } from "@/lib/api-client";
import { useAuth } from "@/store/auth";
import { Button } from "@/components/ui/button";
import { SessionList, flattenSessions, type SessionRow } from "@/components/SessionList";
import type { Session } from "@/lib/contracts";

export function ShellRoute() {
  const navigate = useNavigate();
  const signOut = useAuth((s) => s.signOut);
  const [query, setQuery] = React.useState("");

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

  function open(_row: SessionRow) {
    // TODO(Task 8): navigate to /t/$serverId/$paneId once that route is registered
    navigate({ to: "/" });
  }

  return (
    <div className="flex h-full flex-col">
      <header className="flex items-center justify-between border-b border-border px-4 py-2">
        <span className="font-semibold">AgentMon</span>
        <Button variant="ghost" size="sm" onClick={() => signOut().then(() => navigate({ to: "/login" }))}>
          Sign out
        </Button>
      </header>
      <div className="min-h-0 flex-1">
        <SessionList rows={rows} query={query} onQueryChange={setQuery} onOpen={open} />
      </div>
    </div>
  );
}
