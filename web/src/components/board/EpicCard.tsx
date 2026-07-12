import * as React from "react";
import { Button } from "@/components/ui/button";
import { ProviderTag } from "@/components/ProviderTag";
import { ConfirmButton } from "@/components/board/ConfirmButton";
import { useEpicActions } from "@/hooks/useEpicActions";
import {
  cardProvider, fmtElapsed, isPlanGate, mergeMode, parseVerdict, stageMeta,
} from "@/lib/board";
import type { EpicDTO, ProjectDTO } from "@/lib/contracts";
import type { SessionState } from "@/lib/contracts";
import { cn } from "@/lib/utils";

export function EpicCard({ epic, project, showProject = false, liveState, onOpen }: {
  epic: EpicDTO; project?: ProjectDTO; showProject?: boolean;
  liveState?: SessionState; onOpen(): void;
}) {
  const meta = stageMeta(epic.stage);
  const col = meta.column;
  const { act, busy } = useEpicActions(epic.project_id);
  const verdict = parseVerdict(epic.verdict);
  const planGate = col === "needs" && isPlanGate(epic.needs);
  const provider = cardProvider(epic.labels, project?.provider ?? "");
  const prUrl = project && epic.pr > 0 ? `https://github.com/${project.repo}/pull/${epic.pr}` : "";
  const compact = col === "done";

  const verdictFacts = verdict && (
    <div className="text-xs text-muted-foreground">
      {verdict.unresolvedCount} unresolved · tests {verdict.passed}✓{verdict.failed > 0 ? ` ${verdict.failed}✗` : ""}
    </div>
  );

  return (
    <div
      role="button"
      tabIndex={0}
      onClick={onOpen}
      onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); onOpen(); } }}
      className={cn(
        "flex cursor-pointer flex-col gap-2 rounded-lg border border-border bg-card p-3 text-left hover:border-muted-foreground/60 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring",
        col === "needs" && "border-red-500/50",
        compact && "gap-1 py-2",
      )}
    >
      <div className="flex items-center gap-1.5 text-xs">
        <span className={cn("inline-block size-2 flex-none rounded-full", meta.dotClass, liveState === "working" && "animate-pulse")} />
        <span className="font-semibold">{meta.label}</span>
        {liveState === "blocked" && <span className="text-red-400">· blocked</span>}
        <span className="ml-auto flex items-center gap-1.5">
          {showProject && project && (
            <span className="rounded border border-border px-1 text-[10px] text-muted-foreground">{project.name}</span>
          )}
          <ProviderTag provider={provider} />
        </span>
      </div>

      <div className={cn("text-sm font-medium leading-snug", compact && "truncate")}>
        <span className="text-muted-foreground">#{epic.issue}</span> {epic.title}
      </div>

      {!compact && project && (
        <div className="truncate font-mono text-[11px] text-muted-foreground">
          {project.repo}
          {epic.branch ? ` · ${epic.branch}` : ""}
        </div>
      )}

      {col === "working" && (
        <div className="border-t border-border pt-2 text-xs text-muted-foreground">
          {epic.session && <div className="truncate font-mono">{epic.session}</div>}
          {epic.started_at && <div>{fmtElapsed(epic.started_at, Date.now())} elapsed{epic.pr > 0 ? "" : " · no PR yet"}</div>}
        </div>
      )}

      {col === "needs" && (
        <div className="border-t border-border pt-2">
          <div className="text-[10px] font-bold uppercase tracking-wider text-red-400">Needs attention</div>
          <div className="mt-1 text-xs">{epic.needs || meta.label}</div>
          {verdictFacts && <div className="mt-1">{verdictFacts}</div>}
          {epic.pr > 0 && <div className="mt-1 text-xs text-muted-foreground">PR #{epic.pr}</div>}
          {/* Actions must never bubble into the drawer-open click. */}
          <div className="mt-2 flex flex-wrap gap-1.5" onClick={(e) => e.stopPropagation()}>
            {planGate ? (
              <Button size="sm" onClick={onOpen}>Review plan</Button>
            ) : (
              <ConfirmButton
                label={epic.pr > 0 ? "Approve & merge" : "Approve"}
                confirmLabel={epic.pr > 0 ? "Merge?" : "Approve?"}
                variant="default"
                disabled={busy !== null}
                onConfirm={() => void act({ action: "approve", epic_id: epic.id }, `Approving #${epic.issue}`)}
              />
            )}
            <ConfirmButton
              label="Retry"
              confirmLabel="Retry?"
              disabled={busy !== null}
              onConfirm={() => void act({ action: "retry", epic_id: epic.id }, `Retrying #${epic.issue}`)}
            />
            {prUrl && (
              <Button variant="outline" size="sm" asChild>
                <a href={prUrl} target="_blank" rel="noreferrer" onClick={(e) => e.stopPropagation()}>PR ↗</a>
              </Button>
            )}
          </div>
        </div>
      )}

      {col === "pr" && (
        <div className="border-t border-border pt-2 text-xs text-muted-foreground">
          <div>PR <span className="text-foreground">#{epic.pr}</span> · {mergeMode(epic.labels)}</div>
          {verdictFacts}
        </div>
      )}

      {col === "queued" && (
        <div className="border-t border-border pt-2 text-xs text-muted-foreground">
          {(epic.blocked_by ?? []).length > 0 ? (
            <div className="flex flex-wrap items-center gap-1">
              blocked by
              {(epic.blocked_by ?? []).map((n) => (
                <span key={n} className="rounded border border-border px-1 font-mono text-[10px]">#{n}</span>
              ))}
            </div>
          ) : (
            <div>ready — waiting for a slot</div>
          )}
          {project?.paused && <div className="mt-1 text-amber-500">held — project paused</div>}
        </div>
      )}

      {compact && (
        <div className="text-xs text-muted-foreground">
          {epic.stage === "merged"
            ? <>✓ {epic.started_at && epic.merged_at ? fmtElapsed(epic.started_at, Date.parse(epic.merged_at)) : "merged"}{epic.pr > 0 ? <> · PR #{epic.pr}</> : null}</>
            : <span className="italic">{meta.label}</span>}
        </div>
      )}
    </div>
  );
}
