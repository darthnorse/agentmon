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
  paused: false, require_ci: true, pinned: false, requirements: [],
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

  it("hides Approve for states the hub rejects (stalled, pre-PR escalation) but keeps Retry", () => {
    const { unmount } = render(
      <EpicCard epic={epic({ stage: "stalled", needs: "session died" })} project={project} onOpen={() => {}} />,
    );
    expect(screen.queryByRole("button", { name: /Approve/ })).toBeNull();
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
    unmount();
    render(
      <EpicCard epic={epic({ id: "e9", stage: "escalated", pr: 0, needs: "blocked: needs a decision" })} project={project} onOpen={() => {}} />,
    );
    expect(screen.queryByRole("button", { name: /Approve/ })).toBeNull();
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
  });

  it("offers Open session on a pre-PR escalation with a live session, without opening the drawer", () => {
    const onOpen = vi.fn();
    const onOpenSession = vi.fn();
    const e = epic({ stage: "escalated", pr: 0, session: "epic-15-x", needs: "blocked: needs a decision" });
    render(<EpicCard epic={e} project={project} onOpen={onOpen} onOpenSession={onOpenSession} />);
    fireEvent.click(screen.getByRole("button", { name: "Open session" }));
    expect(onOpenSession).toHaveBeenCalledWith(e, project);
    expect(onOpen).not.toHaveBeenCalled(); // action must not bubble into drawer-open
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Approve/ })).toBeNull(); // no PR → no approve
  });

  it("Enter on a nested action does not also open the drawer", () => {
    const onOpen = vi.fn();
    render(
      <EpicCard epic={epic({ stage: "escalated", pr: 58, needs: "2 findings need a decision" })} project={project} onOpen={onOpen} />,
    );
    // Keydown originating on a nested control must not bubble into drawer-open.
    fireEvent.keyDown(screen.getByRole("button", { name: "Retry" }), { key: "Enter" });
    expect(onOpen).not.toHaveBeenCalled();
    // The card itself still opens on Enter.
    fireEvent.keyDown(screen.getByRole("button", { name: /#15/ }), { key: "Enter" });
    expect(onOpen).toHaveBeenCalledTimes(1);
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

  it("shows a compact usage line when epic.usage is present", () => {
    render(
      <EpicCard
        epic={epic({ id: "e4", usage: { tokens: 1_240_000, cost: 3.4, duration_ms: 2_280_000 } })}
        project={project}
        onOpen={() => {}}
      />,
    );
    expect(screen.getByText("1.24M tok · ~$3.40 · 38m")).toBeInTheDocument();
  });

  it("renders no usage line when epic.usage is undefined", () => {
    render(<EpicCard epic={epic({ id: "e5" })} project={project} onOpen={() => {}} />);
    expect(screen.queryByText(/tok ·/)).toBeNull();
  });
});
