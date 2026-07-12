import { isTerminalStage, stageMeta } from "@/lib/board";
import type { EpicDTO } from "@/lib/contracts";

export type GanttRange = "24h" | "7d" | "all";
export interface GanttWindow { t0: number; t1: number; }
export interface GanttTick { pct: number; label: string; }
export interface GanttBar {
  leftPct: number; widthPct: number; waitTailPct: number; live: boolean;
  startMs: number; endMs: number;
}

const DAY = 86400000;
const HOUR = 3600000;

// Window: earliest visible start → now (+2% pad), clamped by the range.
// Null when nothing has started — the Timeline renders an empty-state note.
export function ganttWindow(epics: EpicDTO[], now: number, range: GanttRange): GanttWindow | null {
  // Filter on parseability, not just presence: one malformed started_at would make
  // Math.min return NaN, poisoning the whole (truthy) window to {NaN,NaN} and
  // bypassing the empty-state guard.
  const starts = epics.map((e) => Date.parse(e.started_at)).filter((n) => Number.isFinite(n));
  if (starts.length === 0) return null;
  let t0 = Math.min(...starts);
  if (range === "24h") t0 = Math.max(t0, now - DAY);
  if (range === "7d") t0 = Math.max(t0, now - 7 * DAY);
  const span = Math.max(now - t0, HOUR); // ≥1h so fresh boards still have width
  return { t0, t1: now + span * 0.02 };
}

export function ganttTicks(w: GanttWindow): GanttTick[] {
  const span = w.t1 - w.t0;
  const ticks: GanttTick[] = [];
  if (span >= 2 * DAY) {
    const d = new Date(w.t0);
    d.setHours(0, 0, 0, 0);
    if (d.getTime() < w.t0) d.setDate(d.getDate() + 1);
    // Step by calendar day (re-anchor to local midnight each iteration) so a DST
    // transition can't drift a label by an hour and shift/duplicate a day column.
    for (; d.getTime() < w.t1; d.setDate(d.getDate() + 1)) {
      const ms = d.getTime();
      ticks.push({ pct: ((ms - w.t0) / span) * 100, label: new Date(ms).toLocaleDateString(undefined, { weekday: "short", day: "numeric" }) });
    }
  } else {
    const step = Math.max(1, Math.ceil(span / HOUR / 12)) * HOUR;
    for (let ms = Math.ceil(w.t0 / step) * step; ms < w.t1; ms += step) {
      ticks.push({ pct: ((ms - w.t0) / span) * 100, label: new Date(ms).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" }) });
    }
  }
  return ticks;
}

const clampPct = (n: number) => Math.min(Math.max(n, 0), 100);

// Actuals only (spec D5). Bar = started_at → (terminal end | waiting-entry |
// now). Waiting epics (escalated/stalled) freeze the bar at stage_updated_at
// and grow a hatched wait-tail to now — "how long has this waited on me".
export function ganttBar(e: EpicDTO, w: GanttWindow, now: number): GanttBar | null {
  const startMs = Date.parse(e.started_at);
  if (!Number.isFinite(startMs)) return null; // unstarted ("") or unparseable → no bar
  const col = stageMeta(e.stage).column;
  const terminal = isTerminalStage(e.stage);
  const waiting = col === "needs";
  const rawEnd = terminal
    ? Date.parse(e.merged_at || e.stage_updated_at || e.started_at)
    : waiting
      ? (e.stage_updated_at ? Date.parse(e.stage_updated_at) : now)
      : now;
  const endMs = Number.isFinite(rawEnd) ? rawEnd : now; // malformed end → grow to now
  const span = w.t1 - w.t0;
  const leftPct = clampPct(((startMs - w.t0) / span) * 100);
  const rightPct = clampPct(((endMs - w.t0) / span) * 100);
  const nowPct = clampPct(((now - w.t0) / span) * 100);
  return {
    leftPct,
    // 0.8% floor keeps a sliver visible; capped to the remaining track so a bar at
    // the right edge can't overflow past 100%.
    widthPct: Math.min(Math.max(rightPct - leftPct, 0.8), 100 - leftPct),
    waitTailPct: waiting ? Math.max(nowPct - rightPct, 0) : 0,
    live: !terminal && !waiting,
    startMs,
    endMs,
  };
}

export function fmtDur(ms: number): string {
  const m = Math.round(ms / 60000);
  if (m < 60) return `${m}m`;
  const h = Math.round(m / 60);
  if (h < 24) return `${h}h`;
  return `${Math.floor(h / 24)}d ${h % 24}h`;
}

// Elbow connector (mockup style): out of the source bar, over, down/up, into
// the target bar. Pixel domain — the component measures its container.
export function arrowPath(from: { x: number; y: number }, to: { x: number; y: number }): string {
  const mx = Math.max(from.x + 10, to.x - 14);
  return `M${from.x},${from.y} H${mx} V${to.y} H${to.x - 2}`;
}
