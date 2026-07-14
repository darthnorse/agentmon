import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({ desktop: true }));
vi.mock("@/lib/use-media-query", () => ({ useMediaQuery: () => h.desktop }));
vi.mock("@/lib/api-client", async (importOriginal) => ({ ...(await importOriginal<object>()), epicAction: vi.fn() }));
vi.mock("@/lib/query-client", () => ({ queryClient: { invalidateQueries: vi.fn() } }));
vi.mock("sonner", () => ({ toast: Object.assign(vi.fn(), { error: vi.fn() }) }));

import { BoardView } from "@/components/board/BoardView";
import { usePrefs } from "@/store/prefs";
import type { EpicDTO, ProjectDTO } from "@/lib/contracts";

const project: ProjectDTO = {
  id: "p1", name: "school", repo: "o/r", server_id: "h1", target: "", workdir: "/w",
  base_branch: "main", provider: "claude", required_reviews: [], max_parallel: 1,
  paused: false, require_ci: true, pinned: false, requirements: [],
};
const epic = (id: string, issue: number, stage: string): EpicDTO => ({
  id, project_id: "p1", issue, title: `t${issue}`, labels: [], blocked_by: [],
  stage: stage as EpicDTO["stage"], attempt: 1, session: "", branch: "", pr: 0, needs: "",
  issue_state: "open", queued_at: "", started_at: "", stage_updated_at: `2026-07-11T0${issue}:00:00Z`, merged_at: "",
});
const projects = new Map([["p1", project]]);
const base = { projects, showProject: false, liveStateOf: () => undefined, onOpenEpic: () => {} };

describe("BoardView", () => {
  beforeEach(() => { h.desktop = true; usePrefs.setState({ projectsBoardLayout: "stack" }); });

  it("desktop renders all five columns with counts and the stat strip", () => {
    render(<BoardView {...base} epics={[epic("a", 1, "implementing"), epic("b", 2, "escalated"), epic("c", 3, "merged")]} />);
    for (const title of ["Working", "Needs you", "PR open", "Queued", "Done"]) {
      expect(screen.getAllByText(title).length).toBeGreaterThan(0);
    }
    expect(screen.getByText("Merged")).toBeInTheDocument(); // stat tile
  });

  it("mobile stacked hides empty sections, collapses Done, and can toggle to columns", () => {
    h.desktop = false;
    render(<BoardView {...base} epics={[epic("a", 1, "escalated"), epic("b", 2, "merged")]} />);
    expect(screen.getAllByText("Needs you").length).toBeGreaterThan(0);
    expect(screen.queryByText("PR open")).not.toBeInTheDocument(); // empty section hidden
    // Done starts collapsed: the merged card is not visible until expanded.
    expect(screen.queryByText(/t2/)).not.toBeInTheDocument();
    fireEvent.click(screen.getByText(/Done/));
    expect(screen.getByText(/t2/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Columns" }));
    expect(usePrefs.getState().projectsBoardLayout).toBe("columns");
  });

  it("Done column truncates to 10 with a show-all expander", () => {
    const many = Array.from({ length: 14 }, (_, i) => epic(`m${i}`, i + 1, "merged"));
    render(<BoardView {...base} epics={many} />);
    fireEvent.click(screen.getByRole("button", { name: /Show all \(14\)/ }));
    expect(screen.getByText(/t14/)).toBeInTheDocument();
  });
});
