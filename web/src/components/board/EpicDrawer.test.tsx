import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({
  epicAction: vi.fn(),
  getProjectBoard: vi.fn(),
  listServers: vi.fn(),
  listSessions: vi.fn(),
  openOrFocusSession: vi.fn(),
  invalidateQueries: vi.fn(),
}));
vi.mock("@/lib/api-client", async (importOriginal) => {
  const mod = await importOriginal<typeof import("@/lib/api-client")>();
  return {
    ...mod, epicAction: h.epicAction, getProjectBoard: h.getProjectBoard,
    listServers: h.listServers, listSessions: h.listSessions,
  };
});
vi.mock("@/components/board/open-session", () => ({ openOrFocusSession: h.openOrFocusSession }));
vi.mock("@/lib/query-client", () => ({ queryClient: { invalidateQueries: h.invalidateQueries } }));
vi.mock("sonner", () => ({ toast: Object.assign(vi.fn(), { error: vi.fn() }) }));
vi.mock("@/lib/use-media-query", () => ({ useMediaQuery: () => true }));
vi.mock("@tanstack/react-router", () => ({ useNavigate: () => vi.fn() }));

import { EpicDrawer } from "@/components/board/EpicDrawer";
import type { EpicDTO, ProjectDTO } from "@/lib/contracts";

const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
const wrapper = ({ children }: { children: ReactNode }) => (
  <QueryClientProvider client={qc}>{children}</QueryClientProvider>
);

const project: ProjectDTO = {
  id: "p1", name: "school", repo: "o/r", server_id: "h1", target: "", workdir: "/w",
  base_branch: "main", provider: "claude", required_reviews: [], max_parallel: 1,
  paused: false, require_ci: true, pinned: false, requirements: [],
};
const epic = (over: Partial<EpicDTO>): EpicDTO => ({
  id: "e1", project_id: "p1", issue: 15, title: "GDPR", labels: [], blocked_by: [],
  stage: "escalated", attempt: 1, session: "epic-15-x", branch: "epic/15-x", pr: 58,
  needs: "2 findings", issue_state: "open", queued_at: "", started_at: "2026-07-11T08:00:00Z",
  stage_updated_at: "2026-07-11T10:00:00Z", merged_at: "",
  verdict: JSON.stringify({ Findings: { Unresolved: 2 }, Unresolved: ["retention default?"], Tests: { Passed: 4, Failed: 0 } }),
  ...over,
});

describe("EpicDrawer", () => {
  beforeEach(() => {
    qc.clear();
    h.epicAction.mockReset().mockResolvedValue({ ok: true });
    h.openOrFocusSession.mockReset().mockResolvedValue(undefined);
    h.listServers.mockReset().mockResolvedValue([{ id: "h1", name: "host-one" }]);
    h.listSessions.mockReset().mockResolvedValue([
      { name: "epic-15-x", server: "h1", target: "default", cwd: "/w", command: "claude", windows: [{ id: "w1", index: "0", name: "", panes: [{ id: "pane1", command: "claude", cwd: "/w" }] }] },
    ]);
    h.getProjectBoard.mockReset().mockResolvedValue({
      project, epics: [],
      events: { e1: [{ from: "planning", to: "implementing", source: "report", note: "", ts: "2026-07-11T08:30:00Z" }] },
    });
  });

  it("renders verdict block, stage history, details, and GitHub links", async () => {
    render(<EpicDrawer epic={epic({})} project={project} onClose={() => {}} />, { wrapper });
    expect(screen.getByText(/2 findings/)).toBeInTheDocument();
    expect(screen.getByText(/retention default\?/)).toBeInTheDocument();
    await waitFor(() => expect(screen.getByText(/planning → implementing/)).toBeInTheDocument());
    expect(screen.getByRole("link", { name: /PR #58/ })).toHaveAttribute("href", "https://github.com/o/r/pull/58");
    expect(screen.getByRole("link", { name: /Issue #15/ })).toHaveAttribute("href", "https://github.com/o/r/issues/15");
    expect(screen.getByText("epic/15-x")).toBeInTheDocument();
  });

  it("cancel requires the modal confirm and posts the action", async () => {
    render(<EpicDrawer epic={epic({})} project={project} onClose={() => {}} />, { wrapper });
    fireEvent.click(screen.getByRole("button", { name: "Cancel epic" }));
    fireEvent.click(screen.getByRole("button", { name: "Yes, cancel it" }));
    await waitFor(() => expect(h.epicAction).toHaveBeenCalledWith("p1", { action: "cancel", epic_id: "e1" }));
  });

  it("sends guidance from the textarea", async () => {
    render(<EpicDrawer epic={epic({ stage: "implementing" })} project={project} onClose={() => {}} />, { wrapper });
    fireEvent.change(screen.getByPlaceholderText(/guidance/i), { target: { value: "focus on RLS" } });
    fireEvent.click(screen.getByRole("button", { name: "Send guidance" }));
    await waitFor(() =>
      expect(h.epicAction).toHaveBeenCalledWith("p1", { action: "guidance", epic_id: "e1", text: "focus on RLS" }));
  });

  it("delegates opening the existing runner session to the shared helper", async () => {
    render(<EpicDrawer epic={epic({ stage: "implementing" })} project={project} onClose={() => {}} />, { wrapper });
    fireEvent.click(await screen.findByRole("button", { name: "Open full session" }));
    expect(h.openOrFocusSession).toHaveBeenCalledWith(
      expect.objectContaining({ serverId: "h1", serverName: "host-one", name: "epic-15-x", session: expect.objectContaining({ name: "epic-15-x" }) }),
      true, expect.any(Function),
    );
  });

  it("escape closes", () => {
    const onClose = vi.fn();
    render(<EpicDrawer epic={epic({})} project={project} onClose={onClose} />, { wrapper });
    fireEvent.keyDown(document, { key: "Escape" });
    expect(onClose).toHaveBeenCalled();
  });
});
