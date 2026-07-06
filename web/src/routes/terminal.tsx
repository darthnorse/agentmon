import * as React from "react";
import { useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { useQuery, useQueries } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { MobileSessionTabs, buildTabs, nextFocusAfterClose, type SessionTab } from "@/components/MobileSessionTabs";
import { MobileTerminalStack } from "@/components/MobileTerminalStack";
import { flattenSessions, type SessionRow } from "@/components/SessionList";
import { listServers, listSessions, serversKey, sessionsKey } from "@/lib/api-client";
import { useStateSnapshot } from "@/store/session-state";
import { effectiveSessionState } from "@/lib/state";
import { useFocusedSeen } from "@/hooks/useFocusedSeen";
import { useMobilePanePool, MOBILE_POOL_CAP } from "@/hooks/useMobilePanePool";
import { paneIdentity, liveIdentSet, paneEnded, readyServerSet } from "@/lib/pane-identity";
import { useVisualViewport } from "@/hooks/useVisualViewport";
import { usePrefs } from "@/store/prefs";
import { useMobileOpenTabs } from "@/store/mobile-open-tabs";
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
  // useLayoutEffect (not useEffect) so the seeded pane is committed BEFORE the first
  // paint — otherwise the stack renders empty for one frame on entry (blank terminal
  // region) before the post-paint effect adds it, undercutting the flash-free goal.
  const pool = useMobilePanePool();
  const openTabs = useMobileOpenTabs((s) => s.open);
  const addOpenTab = useMobileOpenTabs((s) => s.add);
  const removeOpenTab = useMobileOpenTabs((s) => s.remove);
  React.useLayoutEffect(() => {
    addOpenTab({ serverId, target, paneId }); // entering = open/reopen (idempotent)
    pool.openAndFocus({ serverId, target, paneId });
    // Mount-only: the entered pane is fixed for this route mount.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Hoisted pane-liveness (shared by warming and the "session ended" banner).
  const liveIdents = React.useMemo(() => liveIdentSet(rows), [rows]);
  const readyServers = React.useMemo(() => readyServerSet(servers, sessionQs), [servers, sessionQs]);
  const endedIds = React.useMemo(() => {
    const out = new Set<string>();
    for (const p of pool.panes) {
      if (paneEnded(readyServers, liveIdents, p.serverId, p.target, p.paneId)) {
        out.add(paneIdentity(p.serverId, p.target, p.paneId));
      }
    }
    return out;
  }, [pool.panes, readyServers, liveIdents]);

  // Eager-warm the open set into the pool (up to the cap), but only entries that resolve to a
  // LIVE session row — mirroring the tab bar — so a stale/dead persisted entry (session killed
  // elsewhere, server offline, tmux restart with reused pane ids) never opens a socket or steals a
  // pool slot from a real tab. The focused/entered pane is seeded separately (mount effect above),
  // so first paint is unaffected; this runs once the session list is present.
  const warmedRef = React.useRef(false);
  React.useEffect(() => {
    if (warmedRef.current || openTabs.length === 0 || rows.length === 0) return;
    warmedRef.current = true;
    const focusedIdent = paneIdentity(serverId, target, paneId);
    let warmed = 1; // the seeded/focused pane counts toward the cap
    for (const t of openTabs) {
      if (warmed >= MOBILE_POOL_CAP) break;
      const tid = paneIdentity(t.serverId, t.target, t.paneId);
      if (tid === focusedIdent || !liveIdents.has(tid)) continue; // skip focused (seeded) + stale (not live)
      pool.open({ serverId: t.serverId, target: t.target, paneId: t.paneId });
      warmed++;
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [openTabs.length, rows.length]);

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

  const tabs = buildTabs(openTabs, rows, { serverId: focused.serverId, target: focused.target, session: focusedName, paneId: focused.paneId }, stateOf);

  // Size the whole route to the visible viewport so the terminal + key bar stay ABOVE the
  // iOS soft keyboard (which overlays the page rather than shrinking it). Falls back to
  // h-full where visualViewport is unavailable.
  const { height: vvHeight } = useVisualViewport();

  // Close = remove from the open set + drop the pane from the pool (frees the socket). The
  // tmux session keeps running. Closing the active tab focuses a neighbor first; closing the
  // last open tab returns to the session list.
  // The one raw removal both close paths share: open-set entry + pool slot (frees
  // the socket). Kept single so the two paths below can never drift.
  const dropPane = (id: string) => { removeOpenTab(id); pool.close(id); };
  const handleClose = (tab: SessionTab) => {
    const closingId = tab.key; // buildTabs sets key === paneIdentity(serverId, target, paneId)
    if (tab.active) {
      const neighbor = nextFocusAfterClose(tabs, closingId);
      if (neighbor) {
        pool.openAndFocus({ serverId: neighbor.serverId, target: neighbor.target, paneId: neighbor.paneId });
        dropPane(closingId);
      } else {
        dropPane(closingId);
        navigate({ to: "/" }); // closed the last tab → back to the session list
      }
    } else {
      dropPane(closingId);
    }
  };

  // Ended-banner close: identical to closing the pane's tab; fall back to a direct
  // removal for the (edge) pooled pane that has no tab row.
  const handleClosePane = (id: string) => {
    const tab = tabs.find((t) => t.key === id);
    if (tab) {
      handleClose(tab);
      return;
    }
    dropPane(id);
    if (pool.focusedId === id) navigate({ to: "/" });
  };

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
          onClose={handleClose}
        />
      </header>
      <div className="min-h-0 flex-1">
        <MobileTerminalStack panes={pool.panes} focusedId={pool.focusedId}
          fontSize={fontSize} theme={theme}
          endedIds={endedIds} onClosePane={handleClosePane} />
      </div>
    </div>
  );
}
