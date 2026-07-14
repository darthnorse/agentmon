import { describe, expect, it } from "vitest";
import type { EpicDTO, EpicStage } from "@/lib/contracts";
import { isValidSessionName } from "@/lib/session-name";
import {
  boardStats, canApprove, cardProvider, fmtElapsed, groupByColumn, isPlanGate,
  mergeMode, parseVerdict, planSessionName, sessionSlug, stageMeta, STAGE_META,
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

describe("canApprove (mirrors hub Approve preconditions)", () => {
  it("is true only for an escalated epic that already has a PR", () => {
    expect(canApprove(epic({ stage: "escalated", pr: 58 }))).toBe(true);
    // pre-PR escalation (blocked/DISCUSS) → hub returns "no PR to merge"
    expect(canApprove(epic({ stage: "escalated", pr: 0 }))).toBe(false);
    // stalled → hub returns "epic is not escalated"
    expect(canApprove(epic({ stage: "stalled", pr: 58 }))).toBe(false);
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
  it("planSessionName seeds a unique, readable, valid name per launch", () => {
    // uniq before the readable hint (lowercased, first 4 words); empty vibe → no hint
    expect(planSessionName("agentmon", "Add project-scoped requirements now", "a1b2"))
      .toBe("plan-agentmon-a1b2-add-project-scoped-requirements-now");
    expect(planSessionName("school", "", "z9")).toBe("plan-school-z9");
    expect(planSessionName("school", "   ", "z9")).toBe("plan-school-z9");
    // different uniq → different name (this is what keeps relaunch off the 409
    // attach-existing path that used to drop the vibe)
    expect(planSessionName("s", "x", "aaa")).not.toBe(planSessionName("s", "x", "bbb"));
    // truncation at 64 must NOT eat the uniqueness token: a long project + hint
    // still keeps `-<uniq>` right after the base.
    const long = planSessionName("x".repeat(50), "some very long vibe text goes here", "u9");
    expect(long.length).toBeLessThanOrEqual(64);
    expect(long.startsWith(`plan-${"x".repeat(50)}-u9`)).toBe(true);
  });
  it("keeps uniq intact for a long PROJECT NAME, not just a long hint (regression: relaunch collision)", () => {
    // sessionSlug("plan", project) alone can fill the whole 64-char budget, so a
    // long PROJECT name — not just a long hint — could truncate `uniq` off the end
    // via the final slice. Two launches would then collapse to the same name, hit
    // openOrFocusSession's 409 attach-existing path, and silently drop the vibe
    // again — the exact bug this helper exists to prevent.
    const longProject = "x".repeat(80);
    const n1 = planSessionName(longProject, "add dark mode", "aaa111");
    const n2 = planSessionName(longProject, "add dark mode", "bbb222");
    expect(n1.length).toBeLessThanOrEqual(64);
    expect(isValidSessionName(n1)).toBe(true);
    expect(n1).toContain("aaa111"); // uniq survived truncation
    expect(n2).toContain("bbb222");
    expect(n1).not.toBe(n2);        // → distinct per launch, no collision
  });
  it("planSessionName always yields a tmux-valid name, even for pathological vibes", () => {
    // A malformed name would 400 the launch (server-side ValidateSessionName has
    // the SAME regex as NAME_RE) — so every vibe must slug down to a valid name.
    const nasties = ["", "   ", "!@#$%^&*()", "日本語 テスト", "'; rm -rf / #", "-".repeat(80),
      "a".repeat(200), "x  y\t\r\nz", "🚀 rocket launch now please", "UPPER Case MiXeD"];
    for (const v of nasties) {
      const n = planSessionName("weird project! name", v, "l3k9ab");
      expect(isValidSessionName(n), `vibe=${JSON.stringify(v)} → ${n}`).toBe(true);
    }
  });
  it("fmtElapsed formats minutes/hours/days", () => {
    const t0 = Date.parse("2026-07-11T10:00:00Z");
    expect(fmtElapsed("2026-07-11T09:20:00Z", t0)).toBe("40m");
    expect(fmtElapsed("2026-07-11T07:30:00Z", t0)).toBe("2h 30m");
    expect(fmtElapsed("2026-07-09T09:00:00Z", t0)).toBe("2d 1h");
  });
});
