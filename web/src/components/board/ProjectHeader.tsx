import * as React from "react";
import { useNavigate } from "@tanstack/react-router";
import { Button } from "@/components/ui/button";
import { ConfirmButton } from "@/components/board/ConfirmButton";
import { openOrFocusSession } from "@/components/board/open-session";
import { useEpicActions } from "@/hooks/useEpicActions";
import { boardStats, MAX_PARALLEL_CEILING, sessionSlug } from "@/lib/board";
import type { EpicDTO, ProjectDTO } from "@/lib/contracts";
import { useMediaQuery } from "@/lib/use-media-query";
import { cn } from "@/lib/utils";

// Parse "47", "#47", or a GitHub issue/PR URL → issue number (0 = invalid).
function parseIssue(input: string): number {
  const t = input.trim();
  const m = t.match(/(?:issues|pull)\/(\d+)/) ?? t.match(/^#?(\d+)$/);
  if (!m) return 0;
  const n = Number(m[1]);
  return Number.isSafeInteger(n) && n > 0 ? n : 0;
}

export function ProjectHeader({ project, epics, onEdit }: {
  project: ProjectDTO; epics: EpicDTO[]; onEdit(): void;
}) {
  const navigate = useNavigate();
  const isDesktop = useMediaQuery("(min-width: 1024px)");
  const { act, busy } = useEpicActions(project.id);
  const [showRun, setShowRun] = React.useState(false);
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
      <Button variant="outline" size="sm"
        onClick={() => void openOrFocusSession(
          { serverId: project.server_id, serverName: project.name, target: project.target,
            name: sessionSlug("plan", project.name), cwd: project.workdir, command: 'claude "/plan-epics"' },
          isDesktop, navigate,
        )}>
        Plan epics…
      </Button>
      {/* require-CI is action-backed (set_require_ci), not a PATCH field —
          spec §9 wants pause/max-parallel/require-CI presented together. */}
      <Button variant="outline" size="sm" disabled={busy !== null}
        title="Require CI green before the merge gate lets an epic through"
        onClick={() => void act({ action: "set_require_ci", on: !project.require_ci },
          project.require_ci ? "CI gate off" : "CI gate on")}>
        CI gate: {project.require_ci ? "on" : "off"}
      </Button>
      <Button variant="outline" size="sm" onClick={onEdit}>Edit…</Button>
      {project.paused ? (
        <Button variant="outline" size="sm" disabled={busy !== null}
          onClick={() => void act({ action: "resume" }, "Project resumed")}>Resume</Button>
      ) : (
        <ConfirmButton label="Pause project" confirmLabel="Pause?" disabled={busy !== null}
          onConfirm={() => void act({ action: "pause" }, "Project paused — running epics finish")} />
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
          <Button size="sm" disabled={busy !== null || parseIssue(issue) === 0}
            onClick={() => {
              const n = parseIssue(issue);
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
