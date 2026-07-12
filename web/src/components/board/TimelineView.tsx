import * as React from "react";
import { Button } from "@/components/ui/button";
import { ProviderTag } from "@/components/ProviderTag";
import { StageChip } from "@/components/board/StageChip";
import { cardProvider, stageMeta } from "@/lib/board";
import type { EpicDTO, ProjectDTO } from "@/lib/contracts";
import {
  arrowPath, fmtDur, ganttBar, ganttTicks, ganttWindow, type GanttRange,
} from "@/lib/gantt";
import { cn } from "@/lib/utils";

const RANGES: GanttRange[] = ["24h", "7d", "all"];
const LEFT_RAIL = 260; // px — title rail width, matches the grid template below

export function TimelineView({ epics, projects, groupByProject, onOpenEpic }: {
  epics: EpicDTO[]; projects: Map<string, ProjectDTO>; groupByProject: boolean;
  onOpenEpic(id: string): void;
}) {
  const [range, setRange] = React.useState<GanttRange>("all");
  const now = Date.now();
  const w = ganttWindow(epics, now, range);
  const bodyRef = React.useRef<HTMLDivElement>(null);
  const [arrows, setArrows] = React.useState<string[]>([]);

  // Rows: optionally grouped under project headers, started-first inside a group.
  const rows = React.useMemo(() => {
    const byStart = (a: EpicDTO, b: EpicDTO) =>
      (a.started_at ? Date.parse(a.started_at) : Infinity) - (b.started_at ? Date.parse(b.started_at) : Infinity) || a.issue - b.issue;
    if (!groupByProject) return epics.slice().sort(byStart).map((e) => ({ kind: "epic" as const, epic: e }));
    const out: Array<{ kind: "header"; name: string } | { kind: "epic"; epic: EpicDTO }> = [];
    for (const p of projects.values()) {
      const es = epics.filter((e) => e.project_id === p.id).sort(byStart);
      if (es.length === 0) continue;
      out.push({ kind: "header", name: p.name });
      for (const e of es) out.push({ kind: "epic", epic: e });
    }
    return out;
  }, [epics, projects, groupByProject]);

  // Dependency arrows: measured from the rendered bars (positions are % of an
  // unknown track width), recomputed after paint and on resize.
  React.useEffect(() => {
    const el = bodyRef.current;
    if (!el || !w) { setArrows([]); return; }
    const compute = () => {
      const bodyRect = el.getBoundingClientRect();
      const paths: string[] = [];
      for (const r of rows) {
        if (r.kind !== "epic") continue;
        for (const dep of r.epic.blocked_by ?? []) {
          const from = el.querySelector<HTMLElement>(`[data-bar="${r.epic.project_id}:${dep}"]`);
          const to = el.querySelector<HTMLElement>(`[data-bar="${r.epic.project_id}:${r.epic.issue}"]`);
          if (!from || !to) continue;
          const fb = from.getBoundingClientRect();
          const tb = to.getBoundingClientRect();
          paths.push(arrowPath(
            { x: fb.right - bodyRect.left, y: fb.top + fb.height / 2 - bodyRect.top },
            { x: tb.left - bodyRect.left, y: tb.top + tb.height / 2 - bodyRect.top },
          ));
        }
      }
      setArrows(paths);
    };
    const raf = requestAnimationFrame(compute);
    window.addEventListener("resize", compute);
    return () => { cancelAnimationFrame(raf); window.removeEventListener("resize", compute); };
  }, [rows, w]);

  if (!w) {
    return <div className="p-4 text-sm text-muted-foreground">Nothing has started yet — the Timeline draws real bars only.</div>;
  }
  const ticks = ganttTicks(w);
  const nowPct = Math.min(((now - w.t0) / (w.t1 - w.t0)) * 100, 100);

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-1 self-end">
        {RANGES.map((r) => (
          <Button key={r} variant={range === r ? "default" : "outline"} size="sm" onClick={() => setRange(r)}>{r}</Button>
        ))}
      </div>
      <div className="overflow-x-auto rounded-xl border border-border bg-card">
        <div className="relative min-w-[900px]" ref={bodyRef}>
          {/* axis */}
          <div className="grid border-b border-border" style={{ gridTemplateColumns: `${LEFT_RAIL}px 1fr` }}>
            <div className="px-3 py-2 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">Epic</div>
            <div className="relative h-8">
              {ticks.map((t, i) => (
                <span key={i} className="absolute top-0 flex h-full items-center border-l border-border pl-1.5 text-[11px] text-muted-foreground" style={{ left: `${t.pct}%` }}>
                  {t.label}
                </span>
              ))}
            </div>
          </div>
          {/* rows */}
          {rows.map((r, i) =>
            r.kind === "header" ? (
              <div key={`h${i}`} className="border-t border-border px-3 py-1.5 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">
                {r.name}
              </div>
            ) : (
              <TimelineRow key={r.epic.id} epic={r.epic} project={projects.get(r.epic.project_id)} w={w} now={now}
                onOpen={() => onOpenEpic(r.epic.id)} />
            ),
          )}
          {/* now line + arrows overlays */}
          <div className="pointer-events-none absolute inset-y-0" style={{ left: `calc(${LEFT_RAIL}px + (100% - ${LEFT_RAIL}px) * ${nowPct / 100})` }}>
            <div className="h-full w-px bg-primary/80" />
            <span className="absolute left-1 top-9 rounded bg-primary px-1 text-[10px] font-semibold text-primary-foreground">now</span>
          </div>
          <svg className="pointer-events-none absolute inset-0 h-full w-full" aria-hidden>
            {arrows.map((d, i) => (
              <path key={i} d={d} fill="none" className="stroke-muted-foreground/50" strokeWidth={1.3} />
            ))}
          </svg>
        </div>
      </div>
    </div>
  );
}

function TimelineRow({ epic, project, w, now, onOpen }: {
  epic: EpicDTO; project?: ProjectDTO; w: { t0: number; t1: number }; now: number; onOpen(): void;
}) {
  const meta = stageMeta(epic.stage);
  const bar = ganttBar(epic, w, now);
  return (
    <div
      role="button"
      tabIndex={0}
      onClick={onOpen}
      onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); onOpen(); } }}
      className={cn("grid min-h-[46px] cursor-pointer border-t border-border/60 hover:bg-card/60", meta.column === "needs" && "bg-red-500/5")}
      style={{ gridTemplateColumns: `260px 1fr` }}
    >
      <div className="flex min-w-0 flex-col justify-center gap-0.5 px-3 py-1.5">
        <div className="truncate text-[13px] font-medium">
          <span className="text-muted-foreground">#{epic.issue}</span> {epic.title}
        </div>
        <div className="flex items-center gap-2">
          <StageChip stage={epic.stage} />
          <ProviderTag provider={cardProvider(epic.labels, project?.provider ?? "")} />
        </div>
      </div>
      <div className="relative">
        {bar ? (
          <div
            data-bar={`${epic.project_id}:${epic.issue}`}
            title={`#${epic.issue} ${epic.title} · ${meta.label} · ${fmtDur(bar.endMs - bar.startMs)}`}
            className={cn("absolute top-1/2 h-4 -translate-y-1/2 rounded", meta.barClass)}
            style={{ left: `${bar.leftPct}%`, width: `${bar.widthPct}%` }}
          >
            {bar.waitTailPct > 0 && (
              <div className="absolute left-full top-0 h-full rounded-r bg-red-500/30 [background-image:repeating-linear-gradient(135deg,transparent_0_4px,rgba(239,68,68,.5)_4px_8px)]"
                style={{ width: `${(bar.waitTailPct / bar.widthPct) * 100}%` }} />
            )}
            {bar.live && <div className="absolute -right-0.5 -top-0.5 h-5 w-1 animate-pulse rounded bg-amber-500 motion-reduce:animate-none" />}
            <span className="absolute left-full top-1/2 ml-2 -translate-y-1/2 whitespace-nowrap text-[11px] text-muted-foreground">
              {epic.stage === "merged" ? `✓ ${fmtDur(bar.endMs - bar.startMs)}` : fmtDur(bar.endMs - bar.startMs)}
            </span>
          </div>
        ) : (
          <div className="flex h-full items-center px-2 text-[11px] text-muted-foreground">
            queued{(epic.blocked_by ?? []).length > 0 ? ` · blocked by ${(epic.blocked_by ?? []).map((n) => `#${n}`).join(" ")}` : ""}
          </div>
        )}
      </div>
    </div>
  );
}
