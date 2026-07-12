import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({ epicAction: vi.fn(), openOrFocusSession: vi.fn(), navigate: vi.fn() }));
vi.mock("@/lib/api-client", async (importOriginal) => ({ ...(await importOriginal<object>()), epicAction: h.epicAction }));
vi.mock("@/lib/query-client", () => ({ queryClient: { invalidateQueries: vi.fn() } }));
vi.mock("sonner", () => ({ toast: Object.assign(vi.fn(), { error: vi.fn() }) }));
vi.mock("@/components/board/open-session", () => ({ openOrFocusSession: h.openOrFocusSession }));
vi.mock("@/lib/use-media-query", () => ({ useMediaQuery: () => true }));
vi.mock("@tanstack/react-router", () => ({ useNavigate: () => h.navigate }));

import { ProjectHeader } from "@/components/board/ProjectHeader";
import type { EpicDTO, ProjectDTO } from "@/lib/contracts";

const project: ProjectDTO = {
  id: "p1", name: "school", repo: "o/r", server_id: "h1", target: "", workdir: "/w",
  base_branch: "main", provider: "claude", required_reviews: [], max_parallel: 2,
  paused: false, require_ci: true,
};

describe("ProjectHeader", () => {
  beforeEach(() => { h.epicAction.mockReset().mockResolvedValue({ ok: true }); h.openOrFocusSession.mockReset(); });

  it("shows slot usage and steps max_parallel", async () => {
    const epics: EpicDTO[] = [{ ...({} as EpicDTO), id: "e1", project_id: "p1", stage: "implementing", issue: 1, title: "t", labels: [], blocked_by: [], attempt: 1, session: "", branch: "", pr: 0, needs: "", issue_state: "open", queued_at: "", started_at: "", stage_updated_at: "", merged_at: "" }];
    render(<ProjectHeader project={project} epics={epics} onEdit={() => {}} />);
    expect(screen.getByText(/1\/2 slots/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "increase max parallel" }));
    expect(h.epicAction).toHaveBeenCalledWith("p1", { action: "set_max_parallel", value: 3 });
  });

  it("toggles require-CI via set_require_ci", () => {
    render(<ProjectHeader project={project} epics={[]} onEdit={() => {}} />);
    fireEvent.click(screen.getByRole("button", { name: /CI gate/ }));
    expect(h.epicAction).toHaveBeenCalledWith("p1", { action: "set_require_ci", on: false });
  });

  it("pause confirms, and Plan epics spawns an interactive session", async () => {
    render(<ProjectHeader project={project} epics={[]} onEdit={() => {}} />);
    fireEvent.click(screen.getByRole("button", { name: "Pause project" }));
    fireEvent.click(screen.getByRole("button", { name: "Pause?" }));
    expect(h.epicAction).toHaveBeenCalledWith("p1", { action: "pause" });
    fireEvent.click(screen.getByRole("button", { name: "Plan epics…" }));
    expect(h.openOrFocusSession).toHaveBeenCalledWith(
      expect.objectContaining({ serverId: "h1", command: 'claude "/plan-epics"', cwd: "/w" }),
      true, h.navigate,
    );
  });

  it("Run issue parses a number or a GitHub URL", async () => {
    render(<ProjectHeader project={project} epics={[]} onEdit={() => {}} />);
    fireEvent.click(screen.getByRole("button", { name: "Run issue…" }));
    fireEvent.change(screen.getByPlaceholderText(/issue number or URL/i), { target: { value: "https://github.com/o/r/issues/47" } });
    fireEvent.click(screen.getByRole("button", { name: "Run" }));
    expect(h.epicAction).toHaveBeenCalledWith("p1", { action: "run_issue", issue: 47 });
  });
});
