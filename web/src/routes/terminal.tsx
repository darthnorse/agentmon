import * as React from "react";
import { useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { useQuery, useQueries } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { MobileSessionTabs, buildTabs } from "@/components/MobileSessionTabs";
import { MobileTerminalStack } from "@/components/MobileTerminalStack";
import { flattenSessions, type SessionRow } from "@/components/SessionList";
import { listServers, listSessions, serversKey, sessionsKey } from "@/lib/api-client";
import { useStateSnapshot } from "@/store/session-state";
import { effectiveSessionState } from "@/lib/state";
import { useFocusedSeen } from "@/hooks/useFocusedSeen";
import { useMobilePanePool, MOBILE_POOL_CAP } from "@/hooks/useMobilePanePool";
import { paneIdentity } from "@/lib/pane-identity";
import { useVisualViewport } from "@/hooks/useVisualViewport";
import { usePrefs } from "@/store/prefs";
import { themeOf } from "@/lib/terminal-themes";
import type { Session, SessionState } from "@/lib/contracts";

export interface TerminalSearch { target: string; session: string; }

export function MobileTerminalRoute() {
  const { serverId, paneId } = useParams({ strict: false }) as { serverId: string; paneId: string };
  const { target, session } = useSearch({ strict: false }) as TerminalSearch;
  const navigate = useNavigate();
  const fontSize = usePrefs((s) => s.fontSizeMobile);
  const theme = themeOf(usePrefs((s) => s.terminalTheme));

  // Session list for the tab strip + eager warming (same cached keys as the inbox).
  const serversQ = useQuery({ queryKey: serversKey(), queryFn: listServers, staleTime: 15_000 });
  const servers = serversQ.data ?? [];
  const sessionQs = useQueries({
    queries: servers.map((s) => ({ queryKey: sessionsKey(s.id), queryFn: () => listSessions(s.id), staleTime: 15_000 })),
  });
  const byServer: Record<string, Session[]> = {};
  servers.forEach((s, i) => { byServer[s.id] = (sessionQs[i]?.data as Session[]) ?? []; });
  const rows = flattenSessions(servers, byServer);
  const snap = useStateSnapshot();
  const stateOf = (row: SessionRow): SessionState =>
    effectiveSessionState(snap, row.server.id, row.session.target, row.session.name, row.session.state);

  // Keep-alive pool (route-local → dies on Back). Seed the entered pane, focused.
  const pool = useMobilePanePool();
  React.useEffect(() => {
    pool.openAndFocus({ serverId, target, paneId });
    // Mount-only: the entered pane is fixed for this route mount.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Eager-warm up to the cap once the session list first arrives (focused always included).
  const warmedRef = React.useRef(false);
  React.useEffect(() => {
    if (warmedRef.current || rows.length === 0) return;
    warmedRef.current = true;
    const focusedIdent = paneIdentity(serverId, target, paneId);
    let warmed = 1; // the seeded/focused pane counts toward the cap
    for (const r of rows) {
      if (warmed >= MOBILE_POOL_CAP) break;
      const rid = paneIdentity(r.server.id, r.session.target, r.pane.id);
      if (rid === focusedIdent) continue;
      pool.open({ serverId: r.server.id, target: r.session.target, paneId: r.pane.id });
      warmed++;
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rows.length]);

  // The focused pane drives the header/tabs + seen tracking. Fall back to the URL pane
  // until the seed effect lands. The focused session NAME comes from the list (so a
  // rename reflects on refetch); fall back to the URL session name before the list loads.
  const focused = pool.panes.find(
    (p) => paneIdentity(p.serverId, p.target, p.paneId) === pool.focusedId,
  ) ?? { serverId, target, paneId };
  const focusedRow = rows.find(
    (r) => paneIdentity(r.server.id, r.session.target, r.pane.id) === paneIdentity(focused.serverId, focused.target, focused.paneId),
  );
  const focusedName = focusedRow?.session.name ?? session;

  useFocusedSeen({ serverId: focused.serverId, target: focused.target, sessionName: focusedName });

  const tabs = buildTabs(rows, { serverId: focused.serverId, target: focused.target, session: focusedName, paneId: focused.paneId }, stateOf);

  // Size the whole route to the visible viewport so the terminal + key bar stay ABOVE the
  // iOS soft keyboard (which overlays the page rather than shrinking it). Falls back to
  // h-full where visualViewport is unavailable.
  const { height: vvHeight } = useVisualViewport();

  return (
    <div className="flex h-full flex-col" style={{ height: vvHeight ? `${vvHeight}px` : undefined }}>
      <header
        className="flex items-center gap-2 border-b border-border bg-background px-2 py-2"
        style={{ paddingTop: "max(0.5rem, env(safe-area-inset-top))" }}
      >
        <Button variant="ghost" size="sm" className="flex-none" onClick={() => navigate({ to: "/" })}>‹ Back</Button>
        <MobileSessionTabs
          tabs={tabs}
          onSwitch={(tab) => pool.openAndFocus({ serverId: tab.serverId, target: tab.target, paneId: tab.paneId })}
          onRenamed={() => { /* rename reflects via the sessions refetch; URL is only the entry point */ }}
        />
      </header>
      <div className="min-h-0 flex-1">
        <MobileTerminalStack panes={pool.panes} focusedId={pool.focusedId} fontSize={fontSize} theme={theme} />
      </div>
    </div>
  );
}
