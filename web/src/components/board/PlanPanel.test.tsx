import { render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({ getEpicPlan: vi.fn(), epicAction: vi.fn(), invalidateQueries: vi.fn() }));
vi.mock("@/lib/api-client", async (importOriginal) => {
  const mod = await importOriginal<typeof import("@/lib/api-client")>();
  return { ...mod, getEpicPlan: h.getEpicPlan, epicAction: h.epicAction };
});
vi.mock("@/lib/query-client", () => ({ queryClient: { invalidateQueries: h.invalidateQueries } }));
vi.mock("sonner", () => ({ toast: Object.assign(vi.fn(), { error: vi.fn() }) }));

import { PlanPanel } from "@/components/board/PlanPanel";
import { ApiError } from "@/lib/api-client";
import type { EpicDTO, ProjectDTO } from "@/lib/contracts";

const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
const wrapper = ({ children }: { children: ReactNode }) => (
  <QueryClientProvider client={qc}>{children}</QueryClientProvider>
);
const project: ProjectDTO = {
  id: "p1", name: "school", repo: "o/r", server_id: "h1", target: "", workdir: "/w",
  base_branch: "main", provider: "claude", required_reviews: [], max_parallel: 1, paused: false, require_ci: true, pinned: false,
};
const epic: EpicDTO = {
  id: "e1", project_id: "p1", issue: 7, title: "t", labels: [], blocked_by: [],
  stage: "escalated", attempt: 1, session: "", branch: "epic/7-x", pr: 0,
  needs: "plan-gate: plan ready at docs/plans/epic-7.md", issue_state: "open",
  queued_at: "", started_at: "", stage_updated_at: "", merged_at: "",
};

describe("PlanPanel", () => {
  beforeEach(() => { qc.clear(); h.getEpicPlan.mockReset(); h.epicAction.mockReset().mockResolvedValue({ ok: true }); });

  it("renders the plan markdown with path/ref and an approve action", async () => {
    h.getEpicPlan.mockResolvedValue({ path: "docs/plans/epic-7.md", ref: "epic/7-x", markdown: "# The Plan\n\n- step one" });
    render(<PlanPanel epic={epic} project={project} />, { wrapper });
    await waitFor(() => expect(screen.getByRole("heading", { name: "The Plan" })).toBeInTheDocument());
    expect(screen.getByText(/docs\/plans\/epic-7.md @ epic\/7-x/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Approve plan" })).toBeInTheDocument();
  });

  it("shows the hub's 404 message verbatim with a GitHub fallback link", async () => {
    h.getEpicPlan.mockRejectedValue(new ApiError(404, "no plan doc found at docs/plans/epic-7.md on epic/7-x"));
    render(<PlanPanel epic={epic} project={project} />, { wrapper });
    await waitFor(() => expect(screen.getByText(/no plan doc found/)).toBeInTheDocument());
    expect(screen.getByRole("link", { name: /View the branch on GitHub/ })).toHaveAttribute("href", "https://github.com/o/r/tree/epic/7-x");
  });
});
