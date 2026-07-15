import * as React from "react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { Button } from "@/components/ui/button";
import { ConfirmButton } from "@/components/board/ConfirmButton";
import { openOrFocusSession } from "@/components/board/open-session";
import { PlanEpicsModal } from "@/components/board/PlanEpicsModal";
import { useEpicActions } from "@/hooks/useEpicActions";
import { getProjectUsage, projectUsageKey } from "@/lib/api-client";
import { boardStats, MAX_PARALLEL_CEILING, planSessionName, sessionSlug } from "@/lib/board";
import type { EpicDTO, ProjectDTO } from "@/lib/contracts";
import { planCommand } from "@/lib/shell-quote";
import { useMediaQuery } from "@/lib/use-media-query";
import { fmtCost, fmtDuration, fmtTokens } from "@/lib/usage-format";
import { cn } from "@/lib/utils";

// Task 18: project-page usage rollup. Deliberately plain text rows — same
// treatment as EpicDrawer's UsageBreakdown (Task 17) — not a chart; it's a
// header summary, not a full panel. Renders nothing while loading beyond a
// subtle hint, and nothing at all once loaded if the project has zero usage
// (a brand-new project shouldn't grow a permanent empty block in the header).
function ProjectUsageSummary({ projectId }: { projectId: string }) {
  const usageQ = useQuery({
    queryKey: projectUsageKey(projectId),
    queryFn: () => getProjectUsage(projectId),
    staleTime: 30_000,
  });

  if (usageQ.isLoading) return <div className="w-full pt-2 text-xs text-muted-foreground">Loading usage…</div>;
  const usage = usageQ.data;
  const empty = !usage || (usage.tokens.total === 0 && usage.by_stage.length === 0 && usage.by_model.length === 0);
  if (empty) return null;

  return (
    <div className="flex w-full flex-col gap-1 pt-2 text-xs">
      <div className="font-medium">
        {fmtTokens(usage.tokens.total)} tok · {fmtCost(usage.cost)} · {fmtDuration(usage.duration_ms)}
      </div>
      {usage.by_stage.length > 0 && (
        <div className="flex flex-col gap-0.5 text-muted-foreground">
          {usage.by_stage.map((s, i) => (
            <div key={i}>{s.stage} — {fmtTokens(s.tokens.total)} tok · {fmtCost(s.cost)} · {fmtDuration(s.duration_ms)}</div>
          ))}
        </div>
      )}
      {usage.by_model.length > 0 && (
        <div className="flex flex-col gap-0.5 text-muted-foreground">
          {usage.by_model.map((m, i) => (
            <div key={i}>{m.provider}/{m.model} — {fmtTokens(m.tokens.total)} tok · {fmtCost(m.cost)}</div>
          ))}
        </div>
      )}
    </div>
  );
}

// Parse "47", "#47", or a GitHub issue/PR URL → issue number (0 = invalid).
// A URL/path form must name THIS project's repo (owner/repo right before
// issues|pull); pasting another repo's issue URL must NOT silently dispatch that
// number into the current project. Bare numbers are accepted as-is.
export function parseIssue(input: string, repo: string): number {
  const t = input.trim();
  const bare = t.match(/^#?(\d+)$/);
  if (bare) {
    const n = Number(bare[1]);
    return Number.isSafeInteger(n) && n > 0 ? n : 0;
  }
  const url = t.match(/([^/\s]+\/[^/\s]+)\/(?:issues|pull)\/(\d+)/);
  if (url && url[1].toLowerCase() === repo.toLowerCase()) {
    const n = Number(url[2]);
    return Number.isSafeInteger(n) && n > 0 ? n : 0;
  }
  return 0;
}

export function ProjectHeader({ project, epics, onEdit }: {
  project: ProjectDTO; epics: EpicDTO[]; onEdit(): void;
}) {
  const navigate = useNavigate();
  const isDesktop = useMediaQuery("(min-width: 1024px)");
  const { act, busy } = useEpicActions(project.id);
  const [showRun, setShowRun] = React.useState(false);
  const [showPlan, setShowPlan] = React.useState(false);
  const [issue, setIssue] = React.useState("");
  const working = boardStats(epics).working;

  return (
    <div className="flex flex-wrap items-center gap-2">
      <span className={cn(
        "inline-flex items-center gap-2 rounded-full border border-border bg-card px-3 py-1 text-xs font-semibold",
        project.paused && "text-muted-foreground",
      )}>
        <span className={cn("size-2 rounded-full", project.paused ? "bg-zinc-500" : "bg-amber-500", !project.paused && working > 0 && "animate-pulse")} />
        {project.paused ? "Paused" : `Running · ${working}/${project.max_parallel} slot${project.max_parallel === 1 ? "" : "s"}`}
      </span>

      <span className="inline-flex items-center overflow-hidden rounded-md border border-border text-xs">
        <span className="px-2 py-1 text-muted-foreground">max parallel</span>
        <button aria-label="decrease max parallel" className="px-2 py-1 hover:bg-accent disabled:opacity-40"
          disabled={busy !== null || project.max_parallel <= 1}
          onClick={() => void act({ action: "set_max_parallel", value: project.max_parallel - 1 })}>−</button>
        <span className="bg-card px-2 py-1 font-semibold tabular-nums">{project.max_parallel}</span>
        <button aria-label="increase max parallel" className="px-2 py-1 hover:bg-accent disabled:opacity-40"
          disabled={busy !== null || project.max_parallel >= MAX_PARALLEL_CEILING}
          onClick={() => void act({ action: "set_max_parallel", value: project.max_parallel + 1 })}>+</button>
      </span>

      <Button variant="outline" size="sm" onClick={() => setShowRun((v) => !v)}>Run issue…</Button>
      <Button variant="outline" size="sm" onClick={() => setShowPlan(true)}>
        Plan epics…
      </Button>
      <Button variant="outline" size="sm"
        title="Re-run the host prerequisite check (gh auth, clone, hooks, Codex sandbox) in a session"
        onClick={() => void openOrFocusSession(
          { serverId: project.server_id, serverName: project.name, target: project.target,
            name: sessionSlug("doctor", project.name), cwd: project.workdir, command: "agentmon doctor" },
          isDesktop, navigate, false, // launch → grid tile, not an expanded glance
        )}>
        Run doctor…
      </Button>
      {/* require-CI is action-backed (set_require_ci), not a PATCH field —
          spec §9 wants pause/max-parallel/require-CI presented together. */}
      <Button variant="outline" size="sm" disabled={busy !== null}
        title="Wait for CI checks before the merge gate lets an epic through (no effect on repos without CI; failing checks always block)."
        onClick={() => void act({ action: "set_require_ci", on: !project.require_ci },
          project.require_ci ? "Require CI off" : "Require CI on")}>
        Require CI: {project.require_ci ? "on" : "off"}
      </Button>
      <Button variant="outline" size="sm" disabled={busy !== null}
        title={project.pinned ? "Unpin from the home header" : "Pin to the home header"}
        onClick={() => void act({ action: "set_pinned", on: !project.pinned }, project.pinned ? "Unpinned" : "Pinned")}>
        {project.pinned ? "★ Pinned" : "☆ Pin"}
      </Button>
      <Button variant="outline" size="sm" onClick={onEdit}>Edit…</Button>
      {project.paused ? (
        <Button variant="outline" size="sm" disabled={busy !== null}
          onClick={() => void act({ action: "resume" }, "Project resumed")}>Resume</Button>
      ) : (
        <ConfirmButton label="Pause project" confirmLabel="Pause?" disabled={busy !== null}
          onConfirm={() => void act({ action: "pause" }, "Project paused — running epics finish")} />
      )}

      <ProjectUsageSummary projectId={project.id} />

      {showPlan && (
        <PlanEpicsModal
          project={project.name}
          onClose={() => setShowPlan(false)}
          onSubmit={(vibe) => {
            setShowPlan(false);
            // Each launch is a fresh, one-shot brainstorm seeded with this vibe.
            // A UNIQUE name keeps openOrFocusSession off the duplicate-name (409)
            // path — which attaches to an existing plan session and would silently
            // drop this vibe — and lets several epics be planned in parallel.
            const uniq = Date.now().toString(36).slice(-3) + Math.random().toString(36).slice(2, 6);
            void openOrFocusSession(
              { serverId: project.server_id, serverName: project.name, target: project.target,
                name: planSessionName(project.name, vibe, uniq), cwd: project.workdir,
                command: planCommand(project.provider === "codex" ? "codex" : "claude", vibe) },
              isDesktop, navigate, false, // launch → grid tile, not an expanded glance
            );
          }}
        />
      )}

      {showRun && (
        <div className="flex w-full items-center gap-2 pt-2">
          <input
            autoFocus
            value={issue}
            onChange={(e) => setIssue(e.target.value)}
            placeholder="issue number or URL (e.g. 47 or …/issues/47)"
            className="h-8 flex-1 rounded-md border border-input bg-background px-2 text-sm"
          />
          <Button size="sm" disabled={busy !== null || parseIssue(issue, project.repo) === 0}
            onClick={() => {
              const n = parseIssue(issue, project.repo);
              if (n === 0) return;
              void act({ action: "run_issue", issue: n }, `Dispatched #${n}`).then((ok) => { if (ok) { setIssue(""); setShowRun(false); } });
            }}>
            Run
          </Button>
        </div>
      )}
    </div>
  );
}
