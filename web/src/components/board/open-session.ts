import { toast } from "sonner";
import { ApiError, createSession, listSessions, sessionsKey } from "@/lib/api-client";
import type { Session } from "@/lib/contracts";
import { queryClient } from "@/lib/query-client";
import { paneKey, usePanes } from "@/store/panes";

// `any` in PARAM position (not the return) is deliberate: under strict
// function types, TanStack's real navigate — `(opts: SpecificShape) => …` —
// is NOT assignable to `(opts: unknown) => …` (a fn taking a narrow arg
// can't stand in for one called with `unknown`). `any` disables that check so
// board callers can pass `useNavigate()`'s result verbatim.
type Navigate = (opts: any) => unknown;

interface OpenOpts {
  serverId: string; serverName: string; target: string; name: string; cwd?: string; command?: string;
  // Existing runner session (drawer attach path). When supplied, never create.
  session?: Session;
}

// Create a session with an optional kickoff command, or attach to an existing
// board runner session, then open its interactive terminal the way today's UI
// does. An existing-name 409 is treated as "already there": re-list and open it.
export async function openOrFocusSession(opts: OpenOpts, isDesktop: boolean, navigate: Navigate): Promise<void> {
  let session: Session | undefined = opts.session;
  if (!session) {
    try {
      const body: { name: string; cwd?: string; command?: string } = { name: opts.name };
      if (opts.cwd) body.cwd = opts.cwd;
      if (opts.command) body.command = opts.command;
      session = await createSession(opts.serverId, body, opts.target);
      queryClient.setQueryData<Session[]>(sessionsKey(opts.serverId), (old) => [
        ...(old ?? []).filter((s) => !(s.name === session!.name && s.target === session!.target)),
        session!,
      ]);
    } catch (err) {
      if (!(err instanceof ApiError) || err.status !== 409) throw err;
      // Already exists — find it in a fresh list. A failed re-list must NOT
      // throw out of this helper: fall through to the home navigation below so
      // the button click always resolves.
      try {
        const list = await listSessions(opts.serverId, opts.target || undefined);
        session = list.find((s) => s.name === opts.name);
      } catch {
        /* re-list failed — session stays undefined → navigate home */
      }
    }
  }
  void queryClient.invalidateQueries({ queryKey: sessionsKey(opts.serverId) });
  const pane = session?.windows[0]?.panes[0];
  if (!session || !pane) {
    void navigate({ to: "/" });
    return;
  }
  if (isDesktop) {
    const res = usePanes.getState().openPane({
      serverId: opts.serverId, paneId: pane.id, target: session.target,
      session: session.name, serverName: opts.serverName, state: session.state,
    });
    if (!res.ok && res.reason === "cap") {
      toast("Close a terminal tile first (6 open max).");
      return;
    }
    usePanes.getState().focus(paneKey(opts.serverId, session.target, session.name, pane.id));
    void navigate({ to: "/" });
  } else {
    void navigate({
      to: "/t/$serverId/$paneId",
      params: { serverId: opts.serverId, paneId: pane.id },
      search: { target: session.target, session: session.name },
    });
  }
}
