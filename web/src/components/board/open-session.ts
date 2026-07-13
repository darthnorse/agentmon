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
export type Navigate = (opts: any) => unknown;

interface OpenOpts {
  serverId: string; serverName: string; target: string; name: string; cwd?: string; command?: string;
  // Existing runner session (drawer attach path). When supplied, never create.
  session?: Session;
}

// One canonical "tile grid is full" message, shared by every open-pane caller so
// the wording can't drift between the board flow and the home screen.
export const TILE_CAP_TOAST = "Close a terminal tile to open it (6 open max).";

// The shared "open this session's pane" tail used by the board drawer/switcher
// AND the home screen (create + focus-next-blocked). Desktop opens+focuses (i.e.
// expands/maximizes) the grid tile — the "glance" interaction — UNLESS `expand`
// is false, in which case it drops into the grid as a working tile (the
// Plan-epics/Doctor launches). Either way `openPane` collapses any prior
// expansion first, so a new tile is never hidden behind a maximized one. Returns
// "cap" when the grid is full (each caller words its own toast); mobile navigates
// to the terminal route. Returns "opened" once a pane is opened/focused (desktop)
// or navigated to (mobile).
export function openPaneTail(
  args: { serverId: string; serverName: string; target: string; session: string; paneId: string; state?: Session["state"] },
  isDesktop: boolean,
  navigate: Navigate,
  expand = true,
): "opened" | "cap" {
  if (!isDesktop) {
    void navigate({
      to: "/t/$serverId/$paneId",
      params: { serverId: args.serverId, paneId: args.paneId },
      search: { target: args.target, session: args.session },
    });
    return "opened";
  }
  const res = usePanes.getState().openPane({
    serverId: args.serverId, paneId: args.paneId, target: args.target,
    session: args.session, serverName: args.serverName, state: args.state,
  });
  if (!res.ok && res.reason === "cap") return "cap";
  // Glance (drawer/switcher, jump-to-blocked) expands the opened tile; a launch
  // (expand=false) leaves the grid view — openPane already collapsed any prior
  // expansion, so the new working tile is visible without maximizing it.
  if (expand) usePanes.getState().focus(paneKey(args.serverId, args.target, args.session, args.paneId));
  return "opened";
}

// Create a session with an optional kickoff command, or attach to an existing
// board runner session, then open its interactive terminal the way today's UI
// does. An existing-name 409 is treated as "already there": re-list and open it.
export async function openOrFocusSession(opts: OpenOpts, isDesktop: boolean, navigate: Navigate, expand = true): Promise<void> {
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
      if (err instanceof ApiError && err.status === 409) {
        // Already exists — find it in a fresh list. A failed re-list must NOT
        // throw: fall through and report below instead of leaking a rejection.
        try {
          const list = await listSessions(opts.serverId, opts.target || undefined);
          session = list.find((s) => s.name === opts.name);
        } catch { /* re-list failed — session stays undefined */ }
      } else {
        // Real failure (offline host, bad workdir, hub error). Callers invoke
        // this as `void`, so a throw would be an unhandled rejection with no user
        // feedback — surface it and stop.
        toast.error(err instanceof ApiError ? err.message : "Couldn't start the session.");
        return;
      }
    }
  }
  void queryClient.invalidateQueries({ queryKey: sessionsKey(opts.serverId) });
  const pane = session?.windows[0]?.panes[0];
  if (!session || !pane) {
    toast.error("Session couldn't be opened — it may have ended.");
    void navigate({ to: "/" });
    return;
  }
  const result = openPaneTail(
    { serverId: opts.serverId, serverName: opts.serverName, target: session.target,
      session: session.name, paneId: pane.id, state: session.state },
    isDesktop, navigate, expand,
  );
  if (result === "cap") {
    toast(TILE_CAP_TOAST);
    return;
  }
  // Desktop opened a tile in place — leave the board route so the grid shows it
  // (mobile already navigated to the terminal route inside openPaneTail).
  if (isDesktop) void navigate({ to: "/" });
}
