import type { EpicDTO, EpicStage, ProjectDTO, Session } from "@/lib/contracts";
import type { Provider } from "@/lib/provider";

export const PLAN_GATE_PREFIX = "plan-gate:";
// Ceiling for a project's max_parallel — mirrors the hub's maxParallelCeiling
// (hubd/internal/api/orchestrator.go). Shared by the header stepper and the
// create form so the clamp lives in one place.
export const MAX_PARALLEL_CEILING = 32;

export type BoardColumn = "working" | "needs" | "pr" | "queued" | "done";
export const COLUMN_ORDER: BoardColumn[] = ["working", "needs", "pr", "queued", "done"];

export const COLUMN_META: Record<BoardColumn, { title: string; dotClass: string }> = {
  working: { title: "Working", dotClass: "bg-amber-600" },
  needs: { title: "Needs you", dotClass: "bg-red-500" },
  pr: { title: "PR open", dotClass: "bg-blue-500" },
  queued: { title: "Queued", dotClass: "bg-gray-500" },
  done: { title: "Done", dotClass: "bg-green-600" },
};

interface StageMeta { label: string; dotClass: string; barClass: string; column: BoardColumn; }

// Spec §8 stage colors — Tailwind palette classes (house convention, STATE_META style).
export const STAGE_META: Record<EpicStage, StageMeta> = {
  queued:       { label: "queued",       dotClass: "bg-gray-500",   barClass: "bg-gray-500",   column: "queued" },
  starting:     { label: "starting",     dotClass: "bg-violet-400", barClass: "bg-violet-400", column: "working" },
  planning:     { label: "planning",     dotClass: "bg-violet-500", barClass: "bg-violet-500", column: "working" },
  implementing: { label: "implementing", dotClass: "bg-amber-600",  barClass: "bg-amber-600",  column: "working" },
  reviewing:    { label: "reviewing",    dotClass: "bg-sky-600",    barClass: "bg-sky-600",    column: "working" },
  pr_open:      { label: "PR open",      dotClass: "bg-blue-500",   barClass: "bg-blue-500",   column: "pr" },
  merging:      { label: "merging",      dotClass: "bg-blue-400",   barClass: "bg-blue-400",   column: "pr" },
  merged:       { label: "merged",       dotClass: "bg-green-600",  barClass: "bg-green-600",  column: "done" },
  escalated:    { label: "escalated",    dotClass: "bg-red-500",    barClass: "bg-red-500",    column: "needs" },
  stalled:      { label: "stalled",      dotClass: "bg-red-400",    barClass: "bg-red-400",    column: "needs" },
  failed:       { label: "failed",       dotClass: "bg-red-900",    barClass: "bg-red-900",    column: "done" },
  canceled:     { label: "canceled",     dotClass: "bg-zinc-500",   barClass: "bg-zinc-500",   column: "done" },
};

// A stage this build doesn't know (newer hub) must stay VISIBLE — an unknown
// active stage parked in "working" beats vanishing from the board.
export function stageMeta(stage: string): StageMeta {
  return STAGE_META[stage as EpicStage] ?? { label: stage, dotClass: "bg-zinc-400", barClass: "bg-zinc-400", column: "working" };
}

// Terminal stages: the epic is done (merged) or dead (failed/canceled). Shared by
// the gantt bar (freeze the bar) and the drawer (hide live actions) so the set
// lives in one place.
export const isTerminalStage = (s: string): boolean =>
  s === "merged" || s === "failed" || s === "canceled";

const ts = (s: string) => (s ? Date.parse(s) : 0);

export function groupByColumn(epics: EpicDTO[]): Record<BoardColumn, EpicDTO[]> {
  const out: Record<BoardColumn, EpicDTO[]> = { working: [], needs: [], pr: [], queued: [], done: [] };
  for (const e of epics) out[stageMeta(e.stage).column].push(e);
  out.needs.sort((a, b) => ts(a.stage_updated_at) - ts(b.stage_updated_at)); // longest-waiting first
  out.working.sort((a, b) => ts(a.started_at) - ts(b.started_at));
  out.pr.sort((a, b) => ts(a.stage_updated_at) - ts(b.stage_updated_at));
  out.queued.sort((a, b) => a.issue - b.issue);
  out.done.sort((a, b) => ts(b.stage_updated_at) - ts(a.stage_updated_at)); // newest first
  return out;
}

export interface BoardStats { merged: number; working: number; needs: number; prOpen: number; queued: number; }

// The Merged tile counts merged only — failed/canceled live in the Done
// column but in no tile (spec §6).
export function boardStats(epics: EpicDTO[]): BoardStats {
  const s: BoardStats = { merged: 0, working: 0, needs: 0, prOpen: 0, queued: 0 };
  for (const e of epics) {
    if (e.stage === "merged") { s.merged++; continue; }
    if (e.stage === "failed" || e.stage === "canceled") continue;
    const col = stageMeta(e.stage).column;
    if (col === "working") s.working++;
    else if (col === "needs") s.needs++;
    else if (col === "pr") s.prOpen++;
    else if (col === "queued") s.queued++;
  }
  return s;
}

export const isPlanGate = (needs: string): boolean => needs.startsWith(PLAN_GATE_PREFIX);

// The hub's Approve accepts ONLY an escalated epic that already has a PR
// (orchestrator.go Approve: "epic is not escalated" / "no PR to merge"). Every
// other needs-column state — stalled, or a pre-PR (blocked/DISCUSS) escalation —
// would 409, so the card must not offer Approve for them.
export const canApprove = (e: EpicDTO): boolean => e.stage === "escalated" && e.pr > 0;

export interface VerdictSummary {
  unresolved: string[]; found: number; resolved: number; unresolvedCount: number;
  passed: number; failed: number; uncertain: boolean;
}

// The hub stores json.Marshal of the Go Verdict struct, which has yaml tags
// only — so the JSON keys are the CAPITALIZED Go field names (see the verdict
// marshaling in orchestrator.go + the struct in verdict.go).
export function parseVerdict(raw?: string): VerdictSummary | null {
  if (!raw) return null;
  try {
    const v = JSON.parse(raw) as {
      Findings?: { Found?: number; Resolved?: number; Unresolved?: number };
      Unresolved?: unknown; Tests?: { Passed?: number; Failed?: number }; Uncertain?: boolean;
    };
    return {
      unresolved: Array.isArray(v.Unresolved) ? v.Unresolved.filter((s): s is string => typeof s === "string") : [],
      found: v.Findings?.Found ?? 0,
      resolved: v.Findings?.Resolved ?? 0,
      unresolvedCount: v.Findings?.Unresolved ?? 0,
      passed: v.Tests?.Passed ?? 0,
      failed: v.Tests?.Failed ?? 0,
      uncertain: v.Uncertain === true,
    };
  } catch {
    return null;
  }
}

export function cardProvider(labels: string[] | null | undefined, fallback: string): Provider | undefined {
  const ls = labels ?? [];
  if (ls.includes("agent:codex")) return "codex";
  if (ls.includes("agent:claude")) return "claude";
  return fallback === "claude" || fallback === "codex" ? fallback : undefined;
}

export function mergeMode(labels: string[] | null | undefined): string {
  return (labels ?? []).includes("pr-gate") ? "pr-gate — you merge" : "auto-merge on green";
}

// Session names must satisfy NAME_RE (lib/session-name.ts): start alnum,
// then [A-Za-z0-9_-], max 64. The prefix starts with a letter, so the result
// always begins legally.
export function sessionSlug(prefix: string, name: string): string {
  const core = name.replace(/[^A-Za-z0-9_-]+/g, "-");
  return `${prefix}-${core || "project"}`.slice(0, 64);
}

export function fmtElapsed(fromIso: string, now: number): string {
  const ms = now - Date.parse(fromIso);
  if (!Number.isFinite(ms) || ms < 0) return "";
  const m = Math.floor(ms / 60000);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ${m % 60}m`;
  return `${Math.floor(h / 24)}d ${h % 24}h`;
}

// Match an epic to its live runner session. A non-default project target scopes
// the lookup to that socket; an empty target accepts the agent-default list. The
// drawer's open-full-session and the terminal preview share this so the predicate
// lives in one place.
export function findRunnerSession(
  sessions: Session[] | undefined, epic: EpicDTO, project: ProjectDTO,
): Session | undefined {
  return sessions?.find(
    (s) => s.name === epic.session && (project.target === "" || s.target === project.target),
  );
}
