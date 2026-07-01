import { useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { useQuery, useQueries } from "@tanstack/react-query";
import { TerminalView } from "@/components/TerminalView";
import { Button } from "@/components/ui/button";
import { MobileSessionTabs, buildTabs } from "@/components/MobileSessionTabs";
import { flattenSessions, type SessionRow } from "@/components/SessionList";
import { listServers, listSessions, serversKey, sessionsKey } from "@/lib/api-client";
import { useStateSnapshot } from "@/store/session-state";
import { effectiveSessionState } from "@/lib/state";
import { useFocusedSeen } from "@/hooks/useFocusedSeen";
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

  useFocusedSeen({ serverId, target, sessionName: session });

  // Header session tabs: reuse the SAME (cached) session list the inbox loads, so
  // switching is a cheap in-place navigate rather than a Back → list → tap round-trip.
  // staleTime keeps arriving-from-the-inbox from re-fetching the list it just loaded.
  const serversQ = useQuery({ queryKey: serversKey(), queryFn: listServers, staleTime: 15_000 });
  const servers = serversQ.data ?? [];
  const sessionQs = useQueries({
    queries: servers.map((s) => ({
      queryKey: sessionsKey(s.id), queryFn: () => listSessions(s.id), staleTime: 15_000,
    })),
  });
  const byServer: Record<string, Session[]> = {};
  servers.forEach((s, i) => { byServer[s.id] = (sessionQs[i]?.data as Session[]) ?? []; });
  const snap = useStateSnapshot();
  const stateOf = (row: SessionRow): SessionState =>
    effectiveSessionState(snap, row.server.id, row.session.target, row.session.name, row.session.state);
  const tabs = buildTabs(flattenSessions(servers, byServer), { serverId, target, session, paneId }, stateOf);

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
          onSwitch={(tab) =>
            navigate({
              to: "/t/$serverId/$paneId",
              params: { serverId: tab.serverId, paneId: tab.paneId },
              search: { target: tab.target, session: tab.name },
              replace: true,
            })
          }
          onRenamed={(to) => navigate({ to: ".", search: (s) => ({ ...s, session: to }), replace: true })}
        />
      </header>
      <div className="min-h-0 flex-1">
        {/* Key by the pane identity so switching sessions (paneId changes) remounts a
            fresh terminal — otherwise the old session's scrollback + connected state
            bleed under the new header until the new socket opens. A rename keeps the
            same paneId, so the WS survives (matching the grid's keying). */}
        <TerminalView
          key={`${serverId}:${target}:${paneId}`}
          serverId={serverId} paneId={paneId} target={target} showKeyBar fontSize={fontSize} theme={theme}
        />
      </div>
    </div>
  );
}
