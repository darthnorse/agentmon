import * as React from "react";
import { useNavigate } from "@tanstack/react-router";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { openOrFocusSession } from "@/components/board/open-session";
import { MAX_PARALLEL_CEILING, sessionSlug } from "@/lib/board";
import { ApiError, allBoardKey, createProject, patchProject } from "@/lib/api-client";
import type { ProjectCreateRequest, ProjectDTO, ProjectPatchRequest, ServerSummary } from "@/lib/contracts";
import { useMediaQuery } from "@/lib/use-media-query";
import { queryClient } from "@/lib/query-client";

const REPO_RE = /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/;

type Mode = { mode: "create"; servers: ServerSummary[]; onDone(project?: ProjectDTO): void }
  | { mode: "edit"; project: ProjectDTO; onDone(project?: ProjectDTO): void };

export function ProjectForm(props: Mode) {
  const editing = props.mode === "edit";
  const init = editing ? props.project : undefined;
  const [name, setName] = React.useState(init?.name ?? "");
  const [repo, setRepo] = React.useState(init?.repo ?? "");
  const [serverId, setServerId] = React.useState(init?.server_id ?? (props.mode === "create" ? props.servers[0]?.id ?? "" : ""));
  const [target, setTarget] = React.useState(init?.target ?? "");
  const [workdir, setWorkdir] = React.useState(init?.workdir ?? "");
  const [baseBranch, setBaseBranch] = React.useState(init?.base_branch ?? "main");
  const [provider, setProvider] = React.useState(init?.provider ?? "claude");
  const [reviews, setReviews] = React.useState((init?.required_reviews ?? ["cross-model"]).join(", "));
  const [requireCI, setRequireCI] = React.useState(init?.require_ci ?? true);
  const [maxParallel, setMaxParallel] = React.useState(init?.max_parallel ?? 1);
  const [busy, setBusy] = React.useState(false);
  const [created, setCreated] = React.useState<ProjectDTO | null>(null);

  const repoOk = REPO_RE.test(repo.trim());
  const canSubmit = name.trim() !== "" && workdir.trim() !== "" && baseBranch.trim() !== "" &&
    (editing || (repoOk && serverId !== "")) && !busy;

  const reviewList = () => reviews.split(",").map((r) => r.trim()).filter(Boolean);

  const submit = async () => {
    setBusy(true);
    try {
      if (props.mode === "edit") {
        const body: ProjectPatchRequest = {
          name: name.trim(), workdir: workdir.trim(), target: target.trim(),
          base_branch: baseBranch.trim(), provider, required_reviews: reviewList(),
        };
        const p = await patchProject(props.project.id, body);
        void queryClient.invalidateQueries({ queryKey: allBoardKey() });
        toast("Project updated");
        props.onDone(p);
      } else {
        const body: ProjectCreateRequest = {
          name: name.trim(), repo: repo.trim(), server_id: serverId, target: target.trim() || undefined,
          workdir: workdir.trim(), base_branch: baseBranch.trim(), provider,
          required_reviews: reviewList(), max_parallel: maxParallel, require_ci: requireCI,
        };
        const p = await createProject(body);
        void queryClient.invalidateQueries({ queryKey: allBoardKey() });
        setCreated(p);
      }
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : "Save failed");
    } finally {
      setBusy(false);
    }
  };

  if (created) return <DoctorVerify project={created} onDone={() => props.onDone(created)} />;

  const field = (id: string, label: string, node: React.ReactNode) => (
    <div className="space-y-1.5">
      <Label htmlFor={id}>{label}</Label>
      {node}
    </div>
  );
  const selectCls = "h-9 w-full rounded-md border border-input bg-background px-2 text-sm";

  return (
    <div className="grid gap-4 lg:grid-cols-2">
      <div className="space-y-3">
        {field("pf-name", "Name", <Input id="pf-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="school-platform" />)}
        {props.mode === "create"
          ? field("pf-repo", "Repo",
              <>
                <Input id="pf-repo" value={repo} onChange={(e) => setRepo(e.target.value)} placeholder="owner/repo, e.g. octocat/hello-world" spellCheck={false} />
                {repo && !repoOk && <p className="text-xs text-destructive">Use owner/repo (e.g. octocat/hello-world) — not a full URL or just the owner.</p>}
              </>)
          : field("pf-repo", "Repo (immutable)", <Input id="pf-repo" value={repo} disabled />)}
        {props.mode === "create"
          ? field("pf-server", "Host",
              <select id="pf-server" aria-label="Host" value={serverId} onChange={(e) => setServerId(e.target.value)} className={selectCls}>
                {/* listServers returns active registrations only (all
                    enabled:true, no health field) — every one is selectable;
                    doctor-verify catches a host that's actually down. */}
                {props.servers.map((s) => (
                  <option key={s.id} value={s.id}>{s.name}</option>
                ))}
              </select>)
          : field("pf-server", "Host (immutable)", <Input id="pf-server" value={init?.server_id ?? ""} disabled />)}
        {field("pf-target", "Target (optional)", <Input id="pf-target" value={target} onChange={(e) => setTarget(e.target.value)} placeholder="agent default" />)}
        {field("pf-workdir", "Workdir", <Input id="pf-workdir" value={workdir} onChange={(e) => setWorkdir(e.target.value)} placeholder="/srv/school-platform" />)}
        {field("pf-base", "Base branch", <Input id="pf-base" value={baseBranch} onChange={(e) => setBaseBranch(e.target.value)} />)}
        {field("pf-provider", "Default provider",
          <select id="pf-provider" aria-label="Default provider" value={provider} onChange={(e) => setProvider(e.target.value)} className={selectCls}>
            <option value="claude">Claude Code</option>
            <option value="codex">Codex</option>
          </select>)}
        {field("pf-reviews", "Required reviews", <Input id="pf-reviews" value={reviews} onChange={(e) => setReviews(e.target.value)} placeholder="cross-model" />)}
        {props.mode === "create" && (
          <>
            {field("pf-max", "Max parallel",
              <Input id="pf-max" type="number" min={1} max={MAX_PARALLEL_CEILING} value={maxParallel} onChange={(e) => setMaxParallel(Math.max(1, Math.min(MAX_PARALLEL_CEILING, Number(e.target.value) || 1)))} />)}
            <label className="flex items-center gap-2 text-sm">
              <input type="checkbox" checked={requireCI} onChange={(e) => setRequireCI(e.target.checked)} />
              Require CI green before merge
            </label>
          </>
        )}
        <div className="flex gap-2 pt-1">
          <Button size="sm" disabled={!canSubmit} onClick={() => void submit()}>
            {editing ? "Save changes" : "Register project"}
          </Button>
          <Button size="sm" variant="ghost" onClick={() => props.onDone()}>Cancel</Button>
        </div>
      </div>
      {props.mode === "create" && <HostChecklist provider={provider} />}
    </div>
  );
}

function HostChecklist({ provider }: { provider: string }) {
  const cmd = (s: string) => <code className="block overflow-x-auto rounded bg-background px-2 py-1 font-mono text-[11px]">{s}</code>;
  return (
    <div className="rounded-lg border border-border bg-card p-3 text-sm">
      <div className="font-semibold">On the host, once</div>
      <p className="mt-1 text-xs text-muted-foreground">A browser can't do these — set them up on the host, then Verify below.</p>
      <ol className="mt-2 space-y-2 text-xs">
        <li>Authenticate GitHub with push access (as the monitored OS user):{cmd("gh auth login")}</li>
        <li>Clone the repo at the workdir and set a git identity.</li>
        <li>Install the provider CLI + AgentMon hooks (existing installer).</li>
        {provider === "codex" && (
          <li>Codex: add the repo's <span className="font-mono">.git</span> to <span className="font-mono">writable_roots</span> and set <span className="font-mono">network_access = true</span> in <span className="font-mono">~/.codex/config.toml</span>; trust the hooks once interactively.</li>
        )}
      </ol>
    </div>
  );
}

function DoctorVerify({ project, onDone }: { project: ProjectDTO; onDone(): void }) {
  const navigate = useNavigate();
  const isDesktop = useMediaQuery("(min-width: 1024px)");
  return (
    <div className="rounded-lg border border-border bg-card p-4 text-sm">
      <div className="font-semibold">Project registered — verify the host</div>
      <p className="mt-1 text-muted-foreground">
        Run the doctor inside a session on <span className="font-mono">{project.name}</span>'s host to confirm gh auth,
        the clone, hooks, and (for Codex) the sandbox config. Green means onboarding is actually done.
      </p>
      <div className="mt-3 flex gap-2">
        <Button size="sm"
          onClick={() => void openOrFocusSession(
            { serverId: project.server_id, serverName: project.name, target: project.target,
              name: sessionSlug("doctor", project.name), cwd: project.workdir, command: "agentmon doctor" },
            isDesktop, navigate,
          )}>
          Run doctor on the host ↗
        </Button>
        <Button size="sm" variant="ghost" onClick={onDone}>Done</Button>
      </div>
      <p className="mt-3 text-xs text-muted-foreground">
        Next: <span className="font-medium">Plan epics…</span> to decompose work, or label a GitHub issue{" "}
        <span className="font-mono">agentmon:run</span> to dispatch a one-off.
      </p>
    </div>
  );
}
