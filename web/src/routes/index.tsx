import * as React from "react";
import { toast } from "sonner";
import { useQuery, useQueries } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { allBoardKey, getAllBoard, listServers, listSessions, serversKey, sessionsKey } from "@/lib/api-client";
import { useAuth } from "@/store/auth";
import { Button } from "@/components/ui/button";
import { SessionList, flattenSessions, type SessionRow } from "@/components/SessionList";
import { NewSessionForm } from "@/components/NewSessionForm";
import { PendingAgents } from "@/components/PendingAgents";
import { DefaultPasswordBanner } from "@/components/DefaultPasswordBanner";
import { DesktopShell } from "@/components/DesktopShell";
import { PinnedProjects } from "@/components/board/PinnedProjects";
import { NeedsBadge } from "@/components/board/NeedsBadge";
import { SettingsPanel } from "@/components/SettingsPanel";
import { useMediaQuery } from "@/lib/use-media-query";
import { useStateSnapshot } from "@/store/session-state";
import { openPaneTail, TILE_CAP_TOAST } from "@/components/board/open-session";
import { useNeedsByProject, useNeedsTotal } from "@/store/board";
import { queryClient } from "@/lib/query-client";
import { effectiveSessionState } from "@/lib/state";
import { nextBlocked } from "@/lib/focus-next";
import { liveIdentSet, readyServerSet } from "@/lib/pane-identity";
import type { Session, SessionState } from "@/lib/contracts";

export function ShellRoute() {
  const navigate = useNavigate();
  const signOut = useAuth((s) => s.signOut);
  const [query, setQuery] = React.useState("");
  const [showNew, setShowNew] = React.useState(false);
  const [newServerId, setNewServerId] = React.useState("");
  const isDesktop = useMediaQuery("(min-width: 1024px)");
  const needsTotal = useNeedsTotal();

  const serversQ = useQuery({ queryKey: serversKey(), queryFn: listServers });
  const servers = serversQ.data ?? [];

  // Pinned-project chips: the board query (shared ["board"] cache key) supplies
  // the project list + pin flags; per-project needs come from the app-wide
  // board-attention store (same source as the "Projects" total badge).
  const boardQ = useQuery({ queryKey: allBoardKey(), queryFn: getAllBoard });
  const pinnedNeeds = useNeedsByProject();

  const sessionQs = useQueries({
    queries: servers.map((s) => ({
      queryKey: sessionsKey(s.id),
      queryFn: () => listSessions(s.id),
    })),
  });

  const byServer: Record<string, Session[]> = {};
  servers.forEach((s, i) => { byServer[s.id] = (sessionQs[i]?.data as Session[]) ?? []; });
  const rows = flattenSessions(servers, byServer);

  // Pane-liveness for the "session ended" banner: a pane counts as gone ONLY when
  // its server's sessions query has succeeded (readyServers) and the fresh list
  // does not contain the pane. Query errors / not-yet-loaded → unknown → keep the
  // ordinary reconnecting banner.
  const readyServers = React.useMemo(() => readyServerSet(servers, sessionQs), [servers, sessionQs]);
  const livePaneIds = React.useMemo(() => liveIdentSet(rows), [rows]);

  const snap = useStateSnapshot();
  const stateOf = (row: SessionRow): SessionState =>
    effectiveSessionState(snap, row.server.id, row.session.target, row.session.name, row.session.state);

  // create → auto-open the new session's first pane (desktop: grid tile; mobile: navigate),
  // then invalidate so the list reflects it on next paint.
  const onCreated = (serverId: string, serverName: string, session: Session) => {
    const pane = session.windows[0]?.panes[0];
    let opened = false;
    if (pane) {
      const result = openPaneTail(
        { serverId, serverName, target: session.target, session: session.name, paneId: pane.id, state: session.state },
        isDesktop, navigate,
      );
      if (result === "cap") {
        toast(`Session “${session.name}” created`, { description: TILE_CAP_TOAST });
      }
      opened = result !== "cap";
    }
    // No pane to open (e.g. the post-create re-list hadn't observed it yet) — still
    // confirm the create so the action never silently no-ops; the list refresh shows it.
    if (!opened && !pane) toast(`Session “${session.name}” created`);
    // Seed the cache with the session the hub just returned BEFORE invalidating:
    // the new tile mounts on this render, and until the refetch lands the stale
    // list would count its pane as gone (readyServers ✓, livePaneIds ✗) and flash
    // a false "session ended" banner. The refetch then reconciles.
    queryClient.setQueryData<Session[]>(sessionsKey(serverId), (old) => [
      ...(old ?? []).filter((s) => !(s.name === session.name && s.target === session.target)),
      session,
    ]);
    queryClient.invalidateQueries({ queryKey: sessionsKey(serverId) });
    setShowNew(false);
  };

  const newServer = servers.find((s) => s.id === newServerId) ?? servers[0];

  // Focus-next-blocked: jump to the next blocked session (blocked-first, wrapping).
  // Desktop opens/focuses its grid tile; mobile navigates to the terminal route.
  const nextBlockedRow = nextBlocked(rows, stateOf, snap.focusedKey);
  const goNextBlocked = React.useCallback(() => {
    const row = nextBlockedRow;
    if (!row) return;
    const result = openPaneTail(
      { serverId: row.server.id, serverName: row.server.name, target: row.session.target,
        session: row.session.name, paneId: row.pane.id, state: row.session.state },
      isDesktop, navigate,
    );
    if (result === "cap") {
      toast(`“${row.session.name}” needs attention`, { description: TILE_CAP_TOAST });
    }
  }, [nextBlockedRow, isDesktop, navigate]);

  // `n` jumps to the next blocked session. Guard against firing while the user is
  // typing in an input/textarea (the xterm input is a textarea) or a select.
  const goNextRef = React.useRef(goNextBlocked);
  goNextRef.current = goNextBlocked;
  React.useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "n" || e.metaKey || e.ctrlKey || e.altKey) return;
      const t = e.target as HTMLElement | null;
      const tag = t?.tagName;
      if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || t?.isContentEditable) return;
      e.preventDefault();
      goNextRef.current();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  return (
    <div className="flex h-full flex-col">
      <header
        className="flex items-center justify-between border-b border-border bg-background px-4 py-2"
        // In an installed PWA (iOS black-translucent status bar + viewport-fit=cover)
        // the content flows under the status bar/notch — inset the top so the bar shows.
        style={{ paddingTop: "max(0.5rem, env(safe-area-inset-top))" }}
      >
        <div className="flex min-w-0 items-center gap-3">
          <span className="shrink-0 font-semibold">AgentMon</span>
          <PinnedProjects
            projects={boardQ.data?.projects ?? []}
            needs={pinnedNeeds}
            onOpen={(id) => navigate({ to: "/projects/$projectId", params: { projectId: id }, search: { tab: "board", epic: "" } })}
          />
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" className="relative" onClick={() => navigate({ to: "/projects", search: { tab: "board", epic: "" } })}>
            Projects
            <NeedsBadge count={needsTotal} className="absolute -right-1.5 -top-1.5" />
          </Button>
          {servers.length > 0 && (
            <Button variant="outline" size="sm" onClick={() => setShowNew((v) => !v)}>
              {showNew ? "Close" : "New session"}
            </Button>
          )}
          <Button
            variant="outline"
            size="sm"
            className="hidden sm:inline-flex"
            onClick={goNextBlocked}
            disabled={!nextBlockedRow}
            title="Jump to the next blocked session (n)"
          >
            Next blocked ⟶
          </Button>
          <SettingsPanel onSignOut={() => signOut().finally(() => navigate({ to: "/login" }))} />
        </div>
      </header>
      <DefaultPasswordBanner />
      <PendingAgents />
      {showNew && newServer && (
        <div className="border-b border-border px-4 py-3">
          {servers.length > 1 && (
            <div className="mb-2 flex items-center gap-2 text-sm">
              <label htmlFor="new-session-server">Server</label>
              <select
                id="new-session-server"
                value={newServer.id}
                onChange={(e) => setNewServerId(e.target.value)}
                className="h-9 rounded-md border border-input bg-background px-2 text-sm"
              >
                {servers.map((s) => (
                  <option key={s.id} value={s.id}>{s.name}</option>
                ))}
              </select>
            </div>
          )}
          <NewSessionForm
            key={newServer.id}
            serverId={newServer.id}
            target=""
            onCreated={(session) => onCreated(newServer.id, newServer.name, session)}
          />
        </div>
      )}
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
          <DesktopShell servers={servers} rows={rows} query={query} onQueryChange={setQuery}
            stateOf={stateOf} livePaneIds={livePaneIds} readyServers={readyServers} />
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
