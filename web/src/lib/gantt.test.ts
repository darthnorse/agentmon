import { describe, expect, it } from "vitest";
import type { EpicDTO } from "@/lib/contracts";
import { arrowPath, fmtDur, ganttBar, ganttTicks, ganttWindow } from "@/lib/gantt";

const NOW = Date.parse("2026-07-11T12:00:00Z");
const epic = (over: Partial<EpicDTO>): EpicDTO => ({
  id: "e1", project_id: "p1", issue: 1, title: "t", labels: [], blocked_by: [],
  stage: "implementing", attempt: 1, session: "", branch: "", pr: 0, needs: "",
  issue_state: "open", queued_at: "", started_at: "2026-07-11T08:00:00Z",
  stage_updated_at: "2026-07-11T10:00:00Z", merged_at: "", ...over,
});

describe("ganttWindow", () => {
  it("spans earliest start to a bit past now; null when nothing started", () => {
    const w = ganttWindow([epic({})], NOW, "all")!;
    expect(w.t0).toBe(Date.parse("2026-07-11T08:00:00Z"));
    expect(w.t1).toBeGreaterThan(NOW);
    expect(ganttWindow([epic({ started_at: "" })], NOW, "all")).toBeNull();
  });
  it("clamps to the range", () => {
    const w = ganttWindow([epic({ started_at: "2026-07-01T00:00:00Z" })], NOW, "24h")!;
    expect(w.t0).toBe(NOW - 86400000);
  });
});

describe("ganttBar", () => {
  it("running bar grows to now with a live edge", () => {
    const w = ganttWindow([epic({})], NOW, "all")!;
    const b = ganttBar(epic({}), w, NOW)!;
    expect(b.live).toBe(true);
    expect(b.waitTailPct).toBe(0);
    expect(b.leftPct).toBe(0);
    expect(b.leftPct + b.widthPct).toBeGreaterThan(95);
  });
  it("escalated bar stops at stage_updated_at and grows a wait tail", () => {
    const w = ganttWindow([epic({})], NOW, "all")!;
    const b = ganttBar(epic({ stage: "escalated" }), w, NOW)!;
    expect(b.live).toBe(false);
    expect(b.waitTailPct).toBeGreaterThan(0);
  });
  it("merged bar ends at merged_at; queued epics have no bar", () => {
    const w = ganttWindow([epic({})], NOW, "all")!;
    const b = ganttBar(epic({ stage: "merged", merged_at: "2026-07-11T11:00:00Z" }), w, NOW)!;
    expect(b.live).toBe(false);
    expect(b.endMs).toBe(Date.parse("2026-07-11T11:00:00Z"));
    expect(ganttBar(epic({ stage: "queued", started_at: "" }), w, NOW)).toBeNull();
  });
});

describe("ticks + helpers", () => {
  it("short windows tick by hour, long windows by day, all within bounds", () => {
    const short = ganttTicks({ t0: NOW - 6 * 3600000, t1: NOW });
    const long = ganttTicks({ t0: NOW - 5 * 86400000, t1: NOW });
    expect(short.length).toBeGreaterThan(2);
    expect(long.length).toBeGreaterThanOrEqual(4);
    for (const t of [...short, ...long]) {
      expect(t.pct).toBeGreaterThanOrEqual(0);
      expect(t.pct).toBeLessThanOrEqual(100);
    }
  });
  it("fmtDur", () => {
    expect(fmtDur(40 * 60000)).toBe("40m");
    expect(fmtDur(5 * 3600000)).toBe("5h");
    expect(fmtDur(30 * 3600000)).toBe("1d 6h");
  });
  it("arrowPath draws an elbow", () => {
    expect(arrowPath({ x: 10, y: 5 }, { x: 100, y: 50 })).toBe("M10,5 H86 V50 H98");
  });
});
