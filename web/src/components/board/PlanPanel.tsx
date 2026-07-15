import { ConfirmButton } from "@/components/board/ConfirmButton";
import { ArtifactPanel } from "@/components/board/ArtifactPanel";
import { useEpicActions } from "@/hooks/useEpicActions";
import { epicPlanKey, getEpicPlan } from "@/lib/api-client";
import type { EpicDTO, ProjectDTO } from "@/lib/contracts";

// Plan review "plan mode" (spec §8.2): render the plan committed on the epic
// branch via the shared ArtifactPanel, plus the approve control.
//
// APPROVAL MECHANISM (verified against the runner skill + Orchestrator):
// a plan-gate epic is `escalated` with NO PR, so `Approve()` — which requires
// PRNumber>0 and merges — returns "no PR to merge" (orchestrator.go). The
// runner's epic-pipeline skill resumes past a plan gate on RETRY: a fresh
// session's assess-artifacts step finds the committed plan and continues.
// So "Approve plan" fires the RETRY action, not approve.
export function PlanPanel({ epic, project }: { epic: EpicDTO; project: ProjectDTO }) {
  const { act, busy } = useEpicActions(epic.project_id);
  const branchUrl = epic.branch
    ? `https://github.com/${project.repo}/tree/${epic.branch}`
    : `https://github.com/${project.repo}`;

  return (
    <section className="flex flex-col gap-2">
      <div className="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">Plan review</div>
      <ArtifactPanel
        queryKey={epicPlanKey(epic.project_id, epic.id)}
        queryFn={() => getEpicPlan(epic.project_id, epic.id)}
        branchUrl={branchUrl}
      >
        <ConfirmButton label="Approve plan" confirmLabel="Approve — runner resumes?" variant="default"
          className="self-start" disabled={busy !== null}
          onConfirm={() => void act({ action: "retry", epic_id: epic.id }, `Plan approved — #${epic.issue} resumes`)} />
      </ArtifactPanel>
    </section>
  );
}
