import { act, fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { TimelineView } from "@/components/board/TimelineView";
import type { EpicDTO, ProjectDTO } from "@/lib/contracts";

const project: ProjectDTO = {
  id: "p1", name: "school", repo: "o/r", server_id: "h1", target: "", workdir: "/w",
  base_branch: "main", provider: "claude", required_reviews: [], max_parallel: 1,
  paused: false, require_ci: false, pinned: false, requirements: [],
};
const epic = (over: Partial<EpicDTO>): EpicDTO => ({
  id: "e1", project_id: "p1", issue: 1, title: "one", labels: [], blocked_by: [],
  stage: "implementing", attempt: 1, session: "", branch: "", pr: 0, needs: "",
  issue_state: "open", queued_at: "", started_at: "2026-07-11T08:00:00Z",
  stage_updated_at: "2026-07-11T09:00:00Z", merged_at: "", ...over,
});
const projects = new Map([["p1", project]]);

describe("TimelineView", () => {
  it("renders group header, bar rows, and barless queued rows", () => {
    render(
      <TimelineView groupByProject epics={[
        epic({}),
        epic({ id: "e2", issue: 2, title: "two", stage: "queued", started_at: "", blocked_by: [1] }),
      ]} projects={projects} onOpenEpic={() => {}} />,
    );
    expect(screen.getByText("school")).toBeInTheDocument();
    expect(screen.getByText(/one/)).toBeInTheDocument();
    expect(screen.getByText(/blocked by #1/)).toBeInTheDocument();
  });

  it("row click opens the drawer; range picker switches", () => {
    const onOpen = vi.fn();
    render(<TimelineView groupByProject={false} epics={[epic({})]} projects={projects} onOpenEpic={onOpen} />);
    fireEvent.click(screen.getByText(/one/));
    expect(onOpen).toHaveBeenCalledWith("e1");
    fireEvent.click(screen.getByRole("button", { name: "24h" }));
  });

  it("shows the empty note when nothing has started", () => {
    render(<TimelineView groupByProject={false} epics={[epic({ started_at: "", stage: "queued" })]} projects={projects} onOpenEpic={() => {}} />);
    expect(screen.getByText(/Nothing has started yet/)).toBeInTheDocument();
  });

  it("does not enter a perpetual requestAnimationFrame render/measure loop", () => {
    const cbs: FrameRequestCallback[] = [];
    vi.stubGlobal("requestAnimationFrame", (cb: FrameRequestCallback) => { cbs.push(cb); return cbs.length; });
    vi.stubGlobal("cancelAnimationFrame", () => {});
    try {
      render(
        <TimelineView groupByProject={false} projects={projects} onOpenEpic={() => {}} epics={[
          epic({}),
          epic({ id: "e2", issue: 2, blocked_by: [1], started_at: "2026-07-11T08:30:00Z" }),
        ]} />,
      );
      // Flush frames: with the fix the measured paths stabilize, setArrows bails, and
      // no further frame is scheduled. Without it, every flushed frame re-renders → a
      // fresh window object → the effect schedules another frame forever (bounded here).
      let frames = 0;
      while (cbs.length && frames < 60) {
        const cb = cbs.shift()!;
        act(() => { cb(0); });
        frames++;
      }
      expect(frames).toBeLessThan(60);
    } finally {
      vi.unstubAllGlobals();
    }
  });
});
