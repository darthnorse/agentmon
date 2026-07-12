import { describe, expect, it } from "vitest";
import { epicNotification, epicUrl, isEpicPush } from "@/lib/push-payload";

describe("push-payload", () => {
  it("recognizes epic pushes", () => {
    expect(isEpicPush({ type: "epic", project: "p1", epic_id: "e1", issue: 16, title: "t", needs: "n", stage: "escalated" })).toBe(true);
    expect(isEpicPush({ type: "blocked", server: "h", target: "t", session: "s" })).toBe(false);
    expect(isEpicPush(undefined)).toBe(false);
    expect(isEpicPush({ type: "epic" })).toBe(false);
  });

  it("rejects unsound / wrong-wire-key epic payloads", () => {
    const base = { type: "epic", project: "p1", epic_id: "e1", issue: 16, title: "t", needs: "n", stage: "escalated" };
    expect(isEpicPush({ ...base, issue: 1.5 })).toBe(false);      // non-integer
    expect(isEpicPush({ ...base, issue: -3 })).toBe(false);        // non-positive
    expect(isEpicPush({ ...base, needs: { x: 1 } })).toBe(false);  // object where a string is declared
    expect(isEpicPush({ ...base, epic_id: "" })).toBe(false);      // empty identifier
    // The hub must carry the issue number under `issue`, not `epic` — the bug that
    // made every real push fall through to the generic notification.
    expect(isEpicPush({ type: "epic", project: "p1", epic_id: "e1", epic: 16, title: "t", needs: "n", stage: "escalated" })).toBe(false);
  });

  it("builds a titled notification tagged per-epic", () => {
    const n = epicNotification({ type: "epic", project: "p1", epic_id: "e1", issue: 16, title: "Curriculum", needs: "plan-gate: ready", stage: "escalated" });
    expect(n.title).toBe("Epic #16 needs you");
    expect(n.options.body).toContain("Curriculum");
    expect(n.options.tag).toBe("epic:e1");
  });

  it("deep-links into the epic drawer", () => {
    expect(epicUrl({ type: "epic", project: "p1", epic_id: "e1", issue: 16, title: "t", needs: "n", stage: "stalled" }))
      .toBe("/projects/p1?tab=board&epic=e1");
  });
});
