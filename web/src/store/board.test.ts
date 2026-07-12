import { beforeEach, describe, expect, it } from "vitest";
import { needsByProject, useBoardAttention } from "@/store/board";
import type { BoardDeltaFrame, EpicDTO } from "@/lib/contracts";

const epic = (id: string, project: string, stage: string): EpicDTO => ({
  id, project_id: project, issue: 1, title: "t", labels: [], blocked_by: [],
  stage: stage as EpicDTO["stage"], attempt: 1, session: "", branch: "", pr: 0,
  needs: "", issue_state: "open", queued_at: "", started_at: "", stage_updated_at: "", merged_at: "",
});
const delta = (id: string, project: string, stage: string): BoardDeltaFrame =>
  ({ project_id: project, epic_id: id, issue: 1, stage: stage as BoardDeltaFrame["stage"], needs: "", title: "t" });

describe("board attention store", () => {
  beforeEach(() => useBoardAttention.getState().reset());

  it("snapshot rebuilds the attention set", () => {
    useBoardAttention.getState().applySnapshot([
      epic("a", "p1", "escalated"), epic("b", "p1", "stalled"), epic("c", "p2", "implementing"),
    ]);
    const at = useBoardAttention.getState().attention;
    expect(at.size).toBe(2);
    expect(needsByProject(at).get("p1")).toBe(2);
  });

  it("deltas add and clear attention", () => {
    useBoardAttention.getState().applyDelta(delta("a", "p1", "escalated"));
    expect(useBoardAttention.getState().attention.size).toBe(1);
    useBoardAttention.getState().applyDelta(delta("a", "p1", "implementing"));
    expect(useBoardAttention.getState().attention.size).toBe(0);
  });
});
