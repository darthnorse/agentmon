import * as React from "react";
import { useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { BoardView } from "@/components/board/BoardView";
import { ProjectSwitcher } from "@/components/board/ProjectSwitcher";
import { allBoardKey, getAllBoard } from "@/lib/api-client";
import type { EpicDTO, SessionState } from "@/lib/contracts";
import { effectiveSessionState } from "@/lib/state";
import { needsByProject, useBoardAttention } from "@/store/board";
import { useStateSnapshot } from "@/store/session-state";

export interface ProjectsSearch { tab: "board" | "timeline"; epic: string; }

export const validateProjectsSearch = (s: Record<string, unknown>): ProjectsSearch => ({
  tab: s.tab === "timeline" ? "timeline" : "board",
  epic: typeof s.epic === "string" ? s.epic : "",
});

export function ProjectsIndexRoute() {
  return <ProjectsShell projectId={null} />;
}

export function ProjectDetailRoute() {
  // strict:false reads params without binding to a generated route id — the
  // repo's own terminal.tsx does exactly this (routes/terminal.tsx:23), which
  // sidesteps guessing the pathless-authRoute id.
  const { projectId } = useParams({ strict: false }) as { projectId: string };
  return <ProjectsShell projectId={projectId} />;
}

function ProjectsShell({ projectId }: { projectId: string | null }) {
  const navigate = useNavigate();
  // Both routes share the search schema; read it loosely to avoid binding to
  // one route id.
  const search = useSearch({ strict: false }) as Partial<ProjectsSearch>;
  const tab = search.tab === "timeline" ? "timeline" : "board";
  const epicId = search.epic ?? "";

  const boardQ = useQuery({ queryKey: allBoardKey(), queryFn: getAllBoard });
  const attention = useBoardAttention((s) => s.attention);
  const needs = React.useMemo(() => needsByProject(attention), [attention]);
  const snap = useStateSnapshot();

  const data = boardQ.data;
  const projects = React.useMemo(() => new Map((data?.projects ?? []).map((p) => [p.id, p])), [data]);
  const project = projectId ? projects.get(projectId) : undefined;
  const epics = React.useMemo(
    () => (data?.epics ?? []).filter((e) => !projectId || e.project_id === projectId),
    [data, projectId],
  );

  // Live session state for Working cards: hook-fed state keyed by the
  // project's server/target + the epic's session name. An empty project
  // target means "agent default", whose state frames label is "default".
  const liveStateOf = React.useCallback(
    (e: EpicDTO): SessionState | undefined => {
      const p = projects.get(e.project_id);
      if (!p || !e.session) return undefined;
      return effectiveSessionState(snap, p.server_id, p.target || "default", e.session, undefined);
    },
    [projects, snap],
  );

  const setSearch = (next: Partial<ProjectsSearch>) =>
    void navigate({ to: ".", search: (prev: Record<string, unknown>) => ({ ...validateProjectsSearch(prev), ...next }) });

  const openProject = (id: string | null) =>
    void navigate(
      id
        ? { to: "/projects/$projectId", params: { projectId: id }, search: { tab, epic: "" } }
        : { to: "/projects", search: { tab, epic: "" } },
    );

  return (
    <div className="flex h-full flex-col">
      <header
        className="flex flex-wrap items-center gap-3 border-b border-border bg-background px-4 py-2"
        style={{ paddingTop: "max(0.5rem, env(safe-area-inset-top))" }}
      >
        <button className="font-semibold" onClick={() => void navigate({ to: "/" })}>AgentMon</button>
        <span className="text-sm text-muted-foreground">
          / Projects{project ? <> / <span className="text-foreground">{project.name}</span></> : null}
        </span>
        {data && data.projects.length > 0 && (
          <ProjectSwitcher projects={data.projects} needs={needs} current={projectId ?? undefined} onSelect={openProject} />
        )}
        <span className="ml-auto" />
        {/* Task 18 mounts <ProjectHeader project={project} …/> here for the narrowed view. */}
        {/* Task 19 mounts the New-project button here for the All view. */}
      </header>

      <div className="min-h-0 flex-1 overflow-y-auto p-3">
        {boardQ.isLoading ? (
          <div className="flex h-full items-center justify-center text-sm text-muted-foreground">Loading…</div>
        ) : boardQ.isError || !data ? (
          <div className="flex h-full flex-col items-center justify-center gap-2 text-sm">
            <span className="text-destructive">Failed to load the board.</span>
            <Button variant="outline" size="sm" onClick={() => void boardQ.refetch()}>Retry</Button>
          </div>
        ) : !data.orchestrator_enabled ? (
          <DormantNotice />
        ) : projectId && !project ? (
          <div className="p-4 text-sm text-muted-foreground">Project not found — it may have been deleted.</div>
        ) : data.projects.length === 0 ? (
          <ZeroProjects />
        ) : (
          <>
            <div className="mb-3 flex items-center gap-1 border-b border-border">
              {(["board", "timeline"] as const).map((t) => (
                <button
                  key={t}
                  role="tab"
                  aria-selected={tab === t}
                  onClick={() => setSearch({ tab: t })}
                  className={
                    tab === t
                      ? "-mb-px border-b-2 border-primary px-3 py-2 text-sm font-semibold"
                      : "px-3 py-2 text-sm font-semibold text-muted-foreground hover:text-foreground"
                  }
                >
                  {t === "board" ? "Board" : "Timeline"}
                </button>
              ))}
            </div>
            {tab === "board" ? (
              <BoardView
                epics={epics}
                projects={projects}
                showProject={!projectId}
                liveStateOf={liveStateOf}
                onOpenEpic={(id) => setSearch({ epic: id })}
              />
            ) : (
              <div className="p-4 text-sm text-muted-foreground">Timeline coming in this branch — Task 14.</div>
            )}
            {/* Task 15 replaces this with <EpicDrawer …> resolution on epicId. */}
            {epicId ? null : null}
          </>
        )}
      </div>
    </div>
  );
}

function DormantNotice() {
  return (
    <div className="mx-auto max-w-lg rounded-lg border border-border bg-card p-4 text-sm">
      <div className="font-semibold">The orchestrator is dormant</div>
      <p className="mt-2 text-muted-foreground">
        It needs a GitHub token: add <code className="rounded bg-background px-1">github.token</code> to the hub
        config (<code className="rounded bg-background px-1">deploy/data/config.yaml</code> on the hub host) and
        restart the hub. See the README's orchestrator section for the config keys.
      </p>
    </div>
  );
}

// onNew is optional so Task 12 can render this before the create flow exists;
// Task 19 passes `onNew={() => setCreating(true)}` from ProjectsShell (setCreating
// lives in that scope — it must be passed IN as a prop, never referenced inside
// this standalone component).
function ZeroProjects({ onNew }: { onNew?: () => void }) {
  return (
    <div className="mx-auto max-w-lg rounded-lg border border-border bg-card p-4 text-sm">
      <div className="font-semibold">No projects yet</div>
      <p className="mt-2 text-muted-foreground">
        A project binds a GitHub repo to a host: the orchestrator turns issues into epics, runs them in tmux
        sessions on the host, and opens PRs — summoning you only at decision points.
      </p>
      {onNew ? (
        <Button size="sm" className="mt-3" onClick={onNew}>New project</Button>
      ) : (
        <p className="mt-2 text-muted-foreground">Registration UI lands later in this branch (Task 19).</p>
      )}
    </div>
  );
}
