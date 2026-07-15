import * as React from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { toast } from "sonner";
import { boardSessionsKey, listServers, listSessions, serversKey } from "@/lib/api-client";
import { openOrFocusSession } from "@/components/board/open-session";
import { findRunnerSession } from "@/lib/board";
import { useMediaQuery } from "@/lib/use-media-query";
import type { EpicDTO, ProjectDTO, Session } from "@/lib/contracts";

// Attach to an epic's live runner session — a Needs-you escalation waiting on a
// human decision, or a running epic — exactly as the board does (desktop grid
// tile via the pane store, mobile the /t terminal route). Shared by the card's
// "Open session" and the drawer so both resolve the session identically.
//
// The hub reaps a runner session only on merge/retry/cancel, never on escalate,
// so an escalated epic's session stays alive and attachable — this is how a
// human joins the conversation, decides the DISCUSS item, and lets the SAME
// session drive on to a PR (Retry, by contrast, kills it and re-runs).
//
// The session list is fetched FRESH on each click (fetchQuery + staleTime 0):
// this action opens a live terminal, so a stale cache could miss a
// just-appeared session or open a just-killed one. The server NAME is only a
// display label, so it's resolved best-effort (cache-first, failure falls back
// to the server id) and never blocks or aborts the attach.
export function useOpenRunnerSession(): (epic: EpicDTO, project: ProjectDTO) => Promise<void> {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const isDesktop = useMediaQuery("(min-width: 1024px)");
  return React.useCallback(
    async (epic: EpicDTO, project: ProjectDTO) => {
      let sessions: Session[];
      try {
        sessions = await qc.fetchQuery({
          queryKey: boardSessionsKey(project.server_id, project.target),
          queryFn: () => listSessions(project.server_id, project.target || undefined),
          staleTime: 0,
        });
      } catch {
        toast.error("Couldn't reach the host to open the session.");
        return;
      }
      const session = findRunnerSession(sessions, epic, project);
      if (!session) {
        toast.error("Session ended — nothing to attach to.");
        return;
      }
      let serverName = project.server_id;
      try {
        const servers = await qc.ensureQueryData({ queryKey: serversKey(), queryFn: listServers });
        serverName = servers.find((s) => s.id === project.server_id)?.name ?? project.server_id;
      } catch {
        // Display-label only — keep the id; a server-list failure must not block
        // attaching to a session we already resolved.
      }
      void openOrFocusSession(
        { serverId: project.server_id, serverName, target: project.target, name: session.name, session },
        isDesktop,
        navigate,
      );
    },
    [qc, isDesktop, navigate],
  );
}
