import * as React from "react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { ConfirmButton } from "@/components/board/ConfirmButton";
import { PlanPanel } from "@/components/board/PlanPanel";
import { StageChip } from "@/components/board/StageChip";
import { TerminalPreview } from "@/components/board/TerminalPreview";
import { useEpicActions } from "@/hooks/useEpicActions";
import {
  boardSessionsKey, getProjectBoard, listServers, listSessions, projectBoardKey, serversKey,
} from "@/lib/api-client";
import { canApprove, findRunnerSession, isPlanGate, isTerminalStage, mergeMode, parseVerdict, stageMeta } from "@/lib/board";
import type { EpicDTO, ProjectDTO } from "@/lib/contracts";
import { useMediaQuery } from "@/lib/use-media-query";
import { cn } from "@/lib/utils";
import { paneKey, usePanes } from "@/store/panes";

export function EpicDrawer({ epic, project, onClose }: {
  epic: EpicDTO; project: ProjectDTO; onClose(): void;
}) {
  const meta = stageMeta(epic.stage);
  const { act, busy } = useEpicActions(epic.project_id);
  const verdict = parseVerdict(epic.verdict);
  const planGate = isPlanGate(epic.needs);
  const running = meta.column === "working";
  const waiting = meta.column === "needs";
  const terminal = isTerminalStage(epic.stage);
  const [confirmCancel, setConfirmCancel] = React.useState(false);
  const [guidance, setGuidance] = React.useState("");
  const navigate = useNavigate();
  const isDesktop = useMediaQuery("(min-width: 1024px)");
  const serversQ = useQuery({ queryKey: serversKey(), queryFn: listServers });
  // Pass the project's TARGET (Finding: a non-default-target project's runner
  // lives under that socket, not the agent default). Key by target too so two
  // projects on the same host under different targets don't collide in cache;
  // an empty target reuses the home screen's sessionsKey (same default list).
  const sessKey = boardSessionsKey(project.server_id, project.target);
  const sessionsQ = useQuery({ queryKey: sessKey, queryFn: () => listSessions(project.server_id, project.target || undefined) });

  // Open the runner session exactly as today's UI would (spec §8.3): desktop
  // grid tile via the pane store + home, mobile the /t terminal route.
  const openFullSession = React.useCallback(() => {
    const session = findRunnerSession(sessionsQ.data, epic, project);
    const pane = session?.windows[0]?.panes[0];
    if (!session || !pane) {
      toast.error("Session ended — nothing to attach to.");
      return;
    }
    if (isDesktop) {
      const serverName = serversQ.data?.find((s) => s.id === project.server_id)?.name ?? project.server_id;
      const res = usePanes.getState().openPane({
        serverId: project.server_id, paneId: pane.id, target: session.target,
        session: session.name, serverName, state: session.state,
      });
      if (!res.ok && res.reason === "cap") {
        toast("Close a terminal tile first (6 open max).");
        return;
      }
      usePanes.getState().focus(paneKey(project.server_id, session.target, session.name, pane.id));
      void navigate({ to: "/" });
    } else {
      void navigate({
        to: "/t/$serverId/$paneId",
        params: { serverId: project.server_id, paneId: pane.id },
        search: { target: session.target, session: session.name },
      });
    }
  }, [sessionsQ.data, serversQ.data, isDesktop, epic.session, project, navigate]);

  // Lazy detail fetch: the per-project board carries each epic's last-20
  // events (spec §5.1 keeps them out of the all-board payload).
  const detailQ = useQuery({ queryKey: projectBoardKey(epic.project_id), queryFn: () => getProjectBoard(epic.project_id) });
  const events = detailQ.data?.events[epic.id] ?? [];

  React.useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") onClose(); };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);

  const gh = `https://github.com/${project.repo}`;

  const section = (title: string, body: React.ReactNode) => (
    <section className="flex flex-col gap-2">
      <div className="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">{title}</div>
      {body}
    </section>
  );

  return (
    <div className="fixed inset-0 z-50" role="dialog" aria-modal="true" aria-label={`Epic ${epic.issue} detail`}>
      <div className="absolute inset-0 bg-black/50" onClick={onClose} />
      <aside className="absolute inset-y-0 right-0 flex w-full flex-col border-l border-border bg-background sm:max-w-[560px]">
        <div className="flex items-start gap-2 border-b border-border p-4">
          <h2 className="text-[15px] font-semibold leading-snug">
            <span className="text-muted-foreground">#{epic.issue}</span> {epic.title}
          </h2>
          <Button variant="ghost" size="sm" className="ml-auto flex-none" onClick={onClose} aria-label="close">✕</Button>
        </div>
        <div className="flex items-center gap-2 border-b border-border px-4 py-2 text-xs text-muted-foreground">
          <StageChip stage={epic.stage} />
          <span className="font-mono">{project.repo}</span>
          <span>attempt {epic.attempt}</span>
        </div>

        <div className="flex min-h-0 flex-1 flex-col gap-5 overflow-y-auto p-4">
          {waiting && (
            <div className={cn("rounded-lg border border-red-500/40 bg-card p-3 text-sm")}>
              <div className="font-semibold text-red-400">⚠ {epic.needs || meta.label}</div>
              {verdict && (
                <>
                  <div className="mt-1 text-xs text-muted-foreground">
                    findings {verdict.found} found · {verdict.resolved} resolved · {verdict.unresolvedCount} unresolved
                    · tests {verdict.passed}✓{verdict.failed > 0 ? ` ${verdict.failed}✗` : ""}
                    {verdict.uncertain ? " · runner uncertain" : ""}
                  </div>
                  {verdict.unresolved.length > 0 && (
                    <ul className="mt-2 list-disc pl-5 text-xs">
                      {verdict.unresolved.map((u, i) => <li key={i}>{u}</li>)}
                    </ul>
                  )}
                </>
              )}
            </div>
          )}

          {planGate && <PlanPanel epic={epic} project={project} />}

          {running && epic.session && (
            <TerminalPreview project={project} epic={epic} onOpenFull={openFullSession} />
          )}

          {section("Actions", (
            <div className="flex flex-wrap gap-1.5">
              {canApprove(epic) && (
                <ConfirmButton label={`Approve & merge PR #${epic.pr}`} confirmLabel="Sure?"
                  variant="default" disabled={busy !== null}
                  onConfirm={() => void act({ action: "approve", epic_id: epic.id }, `Approving #${epic.issue}`)} />
              )}
              {waiting && (
                <ConfirmButton label="Retry epic" confirmLabel="Retry?" disabled={busy !== null}
                  onConfirm={() => void act({ action: "retry", epic_id: epic.id }, `Retrying #${epic.issue}`)} />
              )}
              {!terminal && (
                <Button variant="outline" size="sm" className="text-red-400" onClick={() => setConfirmCancel(true)}>
                  Cancel epic
                </Button>
              )}
              {epic.pr > 0 && (
                <Button variant="outline" size="sm" asChild>
                  <a href={`${gh}/pull/${epic.pr}`} target="_blank" rel="noreferrer">PR #{epic.pr} ↗</a>
                </Button>
              )}
              <Button variant="outline" size="sm" asChild>
                <a href={`${gh}/issues/${epic.issue}`} target="_blank" rel="noreferrer">Issue #{epic.issue} ↗</a>
              </Button>
            </div>
          ))}

          {(running || waiting) && epic.session && section("Guidance", (
            <div className="flex flex-col gap-2">
              <textarea
                value={guidance}
                onChange={(e) => setGuidance(e.target.value)}
                placeholder="Type guidance for the runner session…"
                rows={3}
                className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
              />
              <Button size="sm" className="self-end" disabled={!guidance.trim() || busy !== null}
                onClick={() => {
                  const text = guidance.trim();
                  void act({ action: "guidance", epic_id: epic.id, text }, "Guidance sent").then((ok) => { if (ok) setGuidance(""); });
                }}>
                Send guidance
              </Button>
              <p className="text-xs text-muted-foreground">Delivered into the runner's terminal as a submitted message.</p>
            </div>
          ))}

          {section("Pipeline stages", (
            events.length === 0 ? (
              <div className="text-xs text-muted-foreground">{detailQ.isLoading ? "Loading…" : "No transitions recorded."}</div>
            ) : (
              <div className="flex flex-col gap-1.5 text-xs">
                {events.map((ev, i) => (
                  <div key={i} className="flex items-center gap-2">
                    <span className={cn("inline-block size-1.5 flex-none rounded-full", stageMeta(ev.to).dotClass)} />
                    <span>{ev.from} → {ev.to}</span>
                    {ev.note && <span className="truncate text-muted-foreground" title={ev.note}>· {ev.note}</span>}
                    <span className="ml-auto flex-none text-muted-foreground">{ev.source} · {ev.ts.slice(11, 16)}</span>
                  </div>
                ))}
              </div>
            )
          ))}

          {section("Details", (
            <div className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-xs">
              <span className="text-muted-foreground">Branch</span><span className="font-mono">{epic.branch || "—"}</span>
              <span className="text-muted-foreground">Blocked by</span>
              <span>{(epic.blocked_by ?? []).length > 0 ? (epic.blocked_by ?? []).map((n) => `#${n}`).join(", ") : "—"}</span>
              <span className="text-muted-foreground">Session</span><span className="font-mono">{epic.session || "—"}</span>
              <span className="text-muted-foreground">Host</span><span className="font-mono">{project.server_id}{project.target ? ` · ${project.target}` : ""}</span>
              <span className="text-muted-foreground">Autonomy</span><span>{mergeMode(epic.labels)}</span>
              <span className="text-muted-foreground">Queued</span><span>{epic.queued_at || "—"}</span>
              <span className="text-muted-foreground">Started</span><span>{epic.started_at || "—"}</span>
              <span className="text-muted-foreground">Merged</span><span>{epic.merged_at || "—"}</span>
            </div>
          ))}
        </div>
      </aside>

      {confirmCancel && (
        <div className="absolute inset-0 z-10 flex items-center justify-center bg-black/50 p-4" onClick={() => setConfirmCancel(false)}>
          <div className="w-full max-w-sm rounded-lg border border-border bg-background p-4 shadow-lg" role="dialog" aria-modal="true" onClick={(e) => e.stopPropagation()}>
            <h3 className="text-base font-semibold">Cancel epic #{epic.issue}?</h3>
            <p className="mt-2 text-sm text-muted-foreground">
              Kills the runner session and closes this attempt. The issue stays open on GitHub; Retry starts a fresh attempt later.
            </p>
            <div className="mt-4 flex justify-end gap-2">
              <Button variant="ghost" onClick={() => setConfirmCancel(false)}>Keep running</Button>
              <Button variant="destructive" disabled={busy !== null}
                onClick={() => void act({ action: "cancel", epic_id: epic.id }, `Canceled #${epic.issue}`).then(() => setConfirmCancel(false))}>
                Yes, cancel it
              </Button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
