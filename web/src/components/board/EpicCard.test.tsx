import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({ epicAction: vi.fn(), invalidateQueries: vi.fn() }));
vi.mock("@/lib/api-client", async (importOriginal) => {
  const mod = await importOriginal<typeof import("@/lib/api-client")>();
  return { ...mod, epicAction: h.epicAction };
});
vi.mock("@/lib/query-client", () => ({ queryClient: { invalidateQueries: h.invalidateQueries } }));
vi.mock("sonner", () => ({ toast: Object.assign(vi.fn(), { error: vi.fn() }) }));

import { EpicCard } from "@/components/board/EpicCard";
import type { EpicDTO, ProjectDTO } from "@/lib/contracts";

const project: ProjectDTO = {
  id: "p1", name: "school", repo: "o/r", server_id: "h1", target: "", workdir: "/w",
  base_branch: "main", provider: "claude", required_reviews: [], max_parallel: 1,
  paused: false, require_ci: true,
};
const epic = (over: Partial<EpicDTO>): EpicDTO => ({
  id: "e1", project_id: "p1", issue: 15, title: "GDPR consent", labels: [], blocked_by: [],
  stage: "queued", attempt: 1, session: "", branch: "", pr: 0, needs: "", issue_state: "open",
  queued_at: "", started_at: "", stage_updated_at: "", merged_at: "", ...over,
});

describe("EpicCard", () => {
  beforeEach(() => { h.epicAction.mockReset(); h.epicAction.mockResolvedValue({ ok: true }); });

  it("opens the drawer on click", () => {
    const onOpen = vi.fn();
    render(<EpicCard epic={epic({})} project={project} onOpen={onOpen} />);
    fireEvent.click(screen.getByRole("button", { name: /#15/ }));
    expect(onOpen).toHaveBeenCalled();
  });

  it("escalated card shows needs + verdict facts and confirms approve without opening", () => {
    const onOpen = vi.fn();
    render(
      <EpicCard
        epic={epic({
          stage: "escalated", pr: 58, needs: "2 findings need a decision",
          verdict: JSON.stringify({ Findings: { Unresolved: 2 }, Tests: { Passed: 47, Failed: 0 } }),
        })}
        project={project}
        onOpen={onOpen}
      />,
    );
    expect(screen.getByText("2 findings need a decision")).toBeInTheDocument();
    expect(screen.getByText(/2 unresolved/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Approve & merge" }));
    fireEvent.click(screen.getByRole("button", { name: "Merge?" }));
    expect(h.epicAction).toHaveBeenCalledWith("p1", { action: "approve", epic_id: "e1" });
    expect(onOpen).not.toHaveBeenCalled();
  });

  it("plan-gate card swaps the primary action to Review plan (opens drawer)", () => {
    const onOpen = vi.fn();
    render(
      <EpicCard epic={epic({ stage: "escalated", needs: "plan-gate: plan ready at docs/plans/epic-15.md" })} project={project} onOpen={onOpen} />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Review plan" }));
    expect(onOpen).toHaveBeenCalled();
    expect(h.epicAction).not.toHaveBeenCalled();
  });

  it("queued card lists blockers; working card shows session + live state", () => {
    const { unmount } = render(<EpicCard epic={epic({ stage: "queued", blocked_by: [13, 14] })} project={project} onOpen={() => {}} />);
    expect(screen.getByText("#13")).toBeInTheDocument();
    unmount();
    render(
      <EpicCard
        epic={epic({ id: "e2", stage: "implementing", session: "epic-15-x", started_at: "2026-07-11T08:00:00Z" })}
        project={project}
        liveState="blocked"
        onOpen={() => {}}
      />,
    );
    expect(screen.getByText("epic-15-x")).toBeInTheDocument();
    expect(screen.getByText(/blocked/)).toBeInTheDocument();
  });

  it("shows the project chip only in All view", () => {
    render(<EpicCard epic={epic({ id: "e3" })} project={project} showProject onOpen={() => {}} />);
    expect(screen.getByText("school")).toBeInTheDocument();
  });
});
