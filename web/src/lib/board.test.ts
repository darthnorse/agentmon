import { describe, expect, it } from "vitest";
import type { EpicDTO, EpicStage } from "@/lib/contracts";
import {
  boardStats, cardProvider, fmtElapsed, groupByColumn, isPlanGate,
  mergeMode, parseVerdict, sessionSlug, stageMeta, STAGE_META,
} from "@/lib/board";

const epic = (over: Partial<EpicDTO>): EpicDTO => ({
  id: "e1", project_id: "p1", issue: 1, title: "t", labels: [], blocked_by: [],
  stage: "queued", attempt: 1, session: "", branch: "", pr: 0, needs: "",
  issue_state: "open", queued_at: "", started_at: "", stage_updated_at: "", merged_at: "",
  ...over,
});

describe("stage → column mapping", () => {
  it("maps all 13 stages to a column", () => {
    const stages: EpicStage[] = ["queued", "starting", "planning", "implementing", "reviewing",
      "pr_open", "merging", "merged", "escalated", "stalled", "failed", "canceled"];
    for (const s of stages) expect(STAGE_META[s].column).toBeTruthy();
  });
  it("survives an unknown future stage without crashing", () => {
    const m = stageMeta("deploying");
    expect(m.column).toBe("working");
    expect(m.label).toBe("deploying");
  });
});

describe("groupByColumn ordering", () => {
  it("needs-you sorts oldest wait first, queued by issue, done newest first", () => {
    const cols = groupByColumn([
      epic({ id: "a", issue: 3, stage: "escalated", stage_updated_at: "2026-07-11T10:00:00Z" }),
      epic({ id: "b", issue: 1, stage: "stalled", stage_updated_at: "2026-07-11T08:00:00Z" }),
      epic({ id: "c", issue: 9, stage: "queued" }),
      epic({ id: "d", issue: 2, stage: "queued" }),
      epic({ id: "e", issue: 4, stage: "merged", stage_updated_at: "2026-07-11T09:00:00Z" }),
      epic({ id: "f", issue: 5, stage: "canceled", stage_updated_at: "2026-07-11T11:00:00Z" }),
    ]);
    expect(cols.needs.map((e) => e.id)).toEqual(["b", "a"]);
    expect(cols.queued.map((e) => e.issue)).toEqual([2, 9]);
    expect(cols.done.map((e) => e.id)).toEqual(["f", "e"]);
  });
});

describe("boardStats", () => {
  it("counts merged only in the merged tile; failed/canceled in no tile", () => {
    const s = boardStats([
      epic({ stage: "merged" }), epic({ stage: "failed" }), epic({ stage: "canceled" }),
      epic({ stage: "implementing" }), epic({ stage: "escalated" }), epic({ stage: "pr_open" }),
      epic({ stage: "queued" }),
    ]);
    expect(s).toEqual({ merged: 1, working: 1, needs: 1, prOpen: 1, queued: 1 });
  });
});

describe("verdict + plan-gate", () => {
  it("parses the hub's capitalized verdict JSON", () => {
    const v = parseVerdict(JSON.stringify({
      Findings: { Found: 5, Resolved: 3, Unresolved: 2 },
      Unresolved: ["a", "b"], Tests: { Passed: 47, Failed: 0 }, Uncertain: true,
    }));
    expect(v).toEqual({ unresolved: ["a", "b"], found: 5, resolved: 3, unresolvedCount: 2, passed: 47, failed: 0, uncertain: true });
  });
  it("returns null on absent or malformed verdicts", () => {
    expect(parseVerdict(undefined)).toBeNull();
    expect(parseVerdict("not json")).toBeNull();
  });
  it("detects plan-gate escalations by note prefix", () => {
    expect(isPlanGate("plan-gate: plan ready at docs/plans/epic-7.md")).toBe(true);
    expect(isPlanGate("2 findings need a decision")).toBe(false);
  });
});

describe("small helpers", () => {
  it("cardProvider prefers labels over the project default", () => {
    expect(cardProvider(["agent:codex"], "claude")).toBe("codex");
    expect(cardProvider([], "claude")).toBe("claude");
    expect(cardProvider(null, "nope")).toBeUndefined();
  });
  it("mergeMode reads the pr-gate label", () => {
    expect(mergeMode(["pr-gate"])).toContain("you merge");
    expect(mergeMode([])).toContain("auto-merge");
  });
  it("sessionSlug produces valid tmux session names", () => {
    expect(sessionSlug("plan", "school platform!")).toBe("plan-school-platform-");
    expect(sessionSlug("doctor", "")).toBe("doctor-project");
    expect(sessionSlug("plan", "x".repeat(80)).length).toBeLessThanOrEqual(64);
  });
  it("fmtElapsed formats minutes/hours/days", () => {
    const t0 = Date.parse("2026-07-11T10:00:00Z");
    expect(fmtElapsed("2026-07-11T09:20:00Z", t0)).toBe("40m");
    expect(fmtElapsed("2026-07-11T07:30:00Z", t0)).toBe("2h 30m");
    expect(fmtElapsed("2026-07-09T09:00:00Z", t0)).toBe("2d 1h");
  });
});
