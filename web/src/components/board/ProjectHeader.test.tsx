import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({ epicAction: vi.fn(), openOrFocusSession: vi.fn(), navigate: vi.fn() }));
vi.mock("@/lib/api-client", async (importOriginal) => ({ ...(await importOriginal<object>()), epicAction: h.epicAction }));
vi.mock("@/lib/query-client", () => ({ queryClient: { invalidateQueries: vi.fn() } }));
vi.mock("sonner", () => ({ toast: Object.assign(vi.fn(), { error: vi.fn() }) }));
vi.mock("@/components/board/open-session", () => ({ openOrFocusSession: h.openOrFocusSession }));
vi.mock("@/lib/use-media-query", () => ({ useMediaQuery: () => true }));
vi.mock("@tanstack/react-router", () => ({ useNavigate: () => h.navigate }));

import { ProjectHeader, parseIssue } from "@/components/board/ProjectHeader";
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

  it("Run doctor re-runs the host check in a session", () => {
    render(<ProjectHeader project={project} epics={[]} onEdit={() => {}} />);
    fireEvent.click(screen.getByRole("button", { name: "Run doctor…" }));
    expect(h.openOrFocusSession).toHaveBeenCalledWith(
      expect.objectContaining({ serverId: "h1", command: "agentmon doctor", cwd: "/w" }),
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

  it("disables Run for an out-of-range issue number", () => {
    render(<ProjectHeader project={project} epics={[]} onEdit={() => {}} />);
    fireEvent.click(screen.getByRole("button", { name: "Run issue…" }));
    fireEvent.change(screen.getByPlaceholderText(/issue number or URL/i), { target: { value: "99999999999999999999" } });
    expect(screen.getByRole("button", { name: "Run" })).toBeDisabled();
  });

  it("rejects an issue URL from a different repository", () => {
    render(<ProjectHeader project={project} epics={[]} onEdit={() => {}} />);
    fireEvent.click(screen.getByRole("button", { name: "Run issue…" }));
    // project.repo is "o/r"; a foreign repo's issue URL must NOT dispatch its
    // number into the current project — Run stays disabled and no action fires.
    fireEvent.change(screen.getByPlaceholderText(/issue number or URL/i), { target: { value: "https://github.com/other/repo/issues/9" } });
    expect(screen.getByRole("button", { name: "Run" })).toBeDisabled();
    expect(h.epicAction).not.toHaveBeenCalled();
  });
});

describe("parseIssue", () => {
  it("accepts bare numbers and #N regardless of repo", () => {
    expect(parseIssue("47", "o/r")).toBe(47);
    expect(parseIssue("#47", "o/r")).toBe(47);
    expect(parseIssue("  12  ", "o/r")).toBe(12);
  });
  it("accepts issue/PR URLs for THIS repo (case-insensitive)", () => {
    expect(parseIssue("https://github.com/o/r/issues/47", "o/r")).toBe(47);
    expect(parseIssue("https://github.com/o/r/pull/12", "o/r")).toBe(12);
    expect(parseIssue("https://github.com/O/R/issues/5", "o/r")).toBe(5);
  });
  it("rejects URLs whose owner/repo is not this project's repo", () => {
    expect(parseIssue("https://github.com/other/repo/issues/9", "o/r")).toBe(0);
    expect(parseIssue("https://github.com/o/other/issues/9", "o/r")).toBe(0);
  });
  it("rejects non-positive and non-safe integers", () => {
    expect(parseIssue("0", "o/r")).toBe(0);
    expect(parseIssue("99999999999999999999", "o/r")).toBe(0);
    expect(parseIssue("abc", "o/r")).toBe(0);
  });
});
