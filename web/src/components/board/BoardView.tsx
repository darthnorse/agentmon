import * as React from "react";
import { Button } from "@/components/ui/button";
import { EpicCard } from "@/components/board/EpicCard";
import {
  boardStats, COLUMN_META, COLUMN_ORDER, groupByColumn, type BoardColumn,
} from "@/lib/board";
import type { EpicDTO, ProjectDTO, SessionState } from "@/lib/contracts";
import { useMediaQuery } from "@/lib/use-media-query";
import { usePrefs } from "@/store/prefs";
import { cn } from "@/lib/utils";

const DONE_VISIBLE = 10;

export function BoardView({ epics, projects, showProject, liveStateOf, onOpenEpic, onOpenSession }: {
  epics: EpicDTO[]; projects: Map<string, ProjectDTO>; showProject: boolean;
  liveStateOf(e: EpicDTO): SessionState | undefined; onOpenEpic(id: string): void;
  onOpenSession?(epic: EpicDTO, project: ProjectDTO): void;
}) {
  const isDesktop = useMediaQuery("(min-width: 1024px)");
  const layout = usePrefs((s) => s.projectsBoardLayout);
  const setLayout = usePrefs((s) => s.setProjectsBoardLayout);
  const [allDone, setAllDone] = React.useState(false);
  const [doneOpen, setDoneOpen] = React.useState(false);
  const cols = React.useMemo(() => groupByColumn(epics), [epics]);
  const stats = React.useMemo(() => boardStats(epics), [epics]);
  const stacked = !isDesktop && layout === "stack";

  const cards = (col: BoardColumn) => {
    const list = col === "done" && !allDone ? cols.done.slice(0, DONE_VISIBLE) : cols[col];
    return (
      <>
        {list.map((e) => (
          <EpicCard key={e.id} epic={e} project={projects.get(e.project_id)} showProject={showProject}
            liveState={liveStateOf(e)} onOpen={() => onOpenEpic(e.id)} onOpenSession={onOpenSession} />
        ))}
        {col === "done" && !allDone && cols.done.length > DONE_VISIBLE && (
          <Button variant="ghost" size="sm" onClick={() => setAllDone(true)}>
            Show all ({cols.done.length})
          </Button>
        )}
      </>
    );
  };

  const header = (col: BoardColumn) => (
    <div className="flex items-center gap-2 px-1 text-[11px] font-bold uppercase tracking-wider text-muted-foreground">
      <span className={cn("inline-block size-2 rounded-full", COLUMN_META[col].dotClass)} />
      {COLUMN_META[col].title}
      <span className="ml-auto font-semibold">{cols[col].length}</span>
    </div>
  );

  return (
    <div className="flex flex-col gap-3">
      <BoardStatsStrip stats={stats} />
      {!isDesktop && (
        <div className="flex gap-1 self-end">
          <Button variant={layout === "stack" ? "default" : "outline"} size="sm" onClick={() => setLayout("stack")}>List</Button>
          <Button variant={layout === "columns" ? "default" : "outline"} size="sm" onClick={() => setLayout("columns")}>Columns</Button>
        </div>
      )}
      {stacked ? (
        <div className="flex flex-col gap-4">
          {COLUMN_ORDER.filter((c) => cols[c].length > 0).map((col) =>
            col === "done" ? (
              <section key={col} className="flex flex-col gap-2">
                <button type="button" aria-expanded={doneOpen} className="flex items-center gap-1 text-left" onClick={() => setDoneOpen((v) => !v)}>
                  <span aria-hidden="true">{doneOpen ? "▾" : "▸"}</span>
                  <span className="min-w-0 flex-1">{header(col)}</span>
                </button>
                {doneOpen && <div className="flex flex-col gap-2">{cards(col)}</div>}
              </section>
            ) : (
              <section key={col} className="flex flex-col gap-2">
                {header(col)}
                {cards(col)}
              </section>
            ),
          )}
        </div>
      ) : (
        <div className="overflow-x-auto pb-2">
          <div className="grid min-w-[1100px] grid-cols-5 items-start gap-3">
            {COLUMN_ORDER.map((col) => (
              <div key={col} className={cn("flex flex-col gap-2 rounded-xl border border-border bg-background/40 p-2", col === "needs" && cols.needs.length > 0 && "border-red-500/40")}>
                {header(col)}
                {cards(col)}
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

export function BoardStatsStrip({ stats }: { stats: ReturnType<typeof boardStats> }) {
  const tile = (label: string, value: number, attn = false) => (
    <div className={cn("rounded-lg border border-border bg-card px-3 py-2", attn && value > 0 && "border-red-500/50")}>
      <div className="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">{label}</div>
      <div className={cn("text-lg font-semibold tabular-nums", attn && value > 0 && "text-red-400")}>{value}</div>
    </div>
  );
  return (
    <div className="grid grid-cols-3 gap-2 sm:grid-cols-5">
      {tile("Merged", stats.merged)}
      {tile("Working", stats.working)}
      {tile("Needs you", stats.needs, true)}
      {tile("PRs open", stats.prOpen)}
      {tile("Queued", stats.queued)}
    </div>
  );
}
