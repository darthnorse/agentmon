import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { useQuery } from "@tanstack/react-query";
import { ConfirmButton } from "@/components/board/ConfirmButton";
import { useEpicActions } from "@/hooks/useEpicActions";
import { ApiError, epicPlanKey, getEpicPlan } from "@/lib/api-client";
import type { EpicDTO, ProjectDTO } from "@/lib/contracts";

// Plan review "plan mode" (spec §8.2): render the plan committed on the epic
// branch. Reviewing a plan from a phone is the whole point — real markdown,
// not a <pre>.
//
// APPROVAL MECHANISM (verified against the runner skill + Orchestrator):
// a plan-gate epic is `escalated` with NO PR, so `Approve()` — which requires
// PRNumber>0 and merges — returns "no PR to merge" (orchestrator.go:793-805).
// The runner's epic-pipeline skill resumes past a plan gate on RETRY: a fresh
// session's assess-artifacts step finds the committed plan and continues
// (agent/internal/runnerfiles/files/claude/epic-pipeline.md:43,116-117;
// Retry() transitions escalated→queued + kills the session, orchestrator.go:849).
// So "Approve plan" fires the RETRY action, not approve.
export function PlanPanel({ epic, project }: { epic: EpicDTO; project: ProjectDTO }) {
  const { act, busy } = useEpicActions(epic.project_id);
  const q = useQuery({
    queryKey: epicPlanKey(epic.project_id, epic.id),
    queryFn: () => getEpicPlan(epic.project_id, epic.id),
    staleTime: 30_000,
    retry: false,
  });
  // Fallback when the proxy can't return the doc (missing / >256 KiB): a link
  // to the branch on GitHub so the human can still read the plan (spec §11).
  const branchUrl = epic.branch ? `https://github.com/${project.repo}/tree/${epic.branch}` : `https://github.com/${project.repo}`;

  return (
    <section className="flex flex-col gap-2">
      <div className="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">Plan review</div>
      {q.isLoading ? (
        <div className="text-xs text-muted-foreground">Loading plan…</div>
      ) : q.isError ? (
        <div className="rounded-md border border-border bg-card p-3 text-xs text-muted-foreground">
          <div>{q.error instanceof ApiError ? q.error.message : "Couldn't load the plan."}</div>
          <a href={branchUrl} target="_blank" rel="noreferrer" className="mt-1 inline-block text-primary underline">
            View the branch on GitHub ↗
          </a>
        </div>
      ) : q.data ? (
        <>
          <div className="font-mono text-[11px] text-muted-foreground">{q.data.path} @ {q.data.ref}</div>
          <div className="markdown max-h-[50vh] overflow-y-auto rounded-md border border-border bg-background p-3">
            <ReactMarkdown remarkPlugins={[remarkGfm]}>{q.data.markdown}</ReactMarkdown>
          </div>
          <ConfirmButton label="Approve plan" confirmLabel="Approve — runner resumes?" variant="default"
            className="self-start" disabled={busy !== null}
            onConfirm={() => void act({ action: "retry", epic_id: epic.id }, `Plan approved — #${epic.issue} resumes`)} />
        </>
      ) : null}
    </section>
  );
}
