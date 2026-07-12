import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({ listSessions: vi.fn() }));
vi.mock("@/lib/api-client", async (importOriginal) => {
  const mod = await importOriginal<typeof import("@/lib/api-client")>();
  return { ...mod, listSessions: h.listSessions };
});
vi.mock("@/components/TerminalView", () => ({
  TerminalView: (p: { paneId: string }) => <div data-testid="terminal" data-pane={p.paneId} />,
}));

import { TerminalPreview } from "@/components/board/TerminalPreview";
import type { EpicDTO, ProjectDTO } from "@/lib/contracts";

const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
const wrapper = ({ children }: { children: ReactNode }) => (
  <QueryClientProvider client={qc}>{children}</QueryClientProvider>
);
const project: ProjectDTO = {
  id: "p1", name: "school", repo: "o/r", server_id: "h1", target: "", workdir: "/w",
  base_branch: "main", provider: "claude", required_reviews: [], max_parallel: 1,
  paused: false, require_ci: false,
};
const epic = { id: "e1", project_id: "p1", issue: 1, title: "t", labels: [], blocked_by: [], stage: "implementing", attempt: 1, session: "epic-1-x", branch: "", pr: 0, needs: "", issue_state: "open", queued_at: "", started_at: "", stage_updated_at: "", merged_at: "" } as EpicDTO;

describe("TerminalPreview", () => {
  beforeEach(() => { qc.clear(); h.listSessions.mockReset(); });

  it("mounts a read-only preview of the matching session's first pane", async () => {
    h.listSessions.mockResolvedValue([
      { name: "epic-1-x", server: "h1", target: "default", cwd: "/w", command: "claude", windows: [{ id: "w1", index: "0", name: "", panes: [{ id: "pane1", command: "claude", cwd: "/w" }] }] },
    ]);
    const onOpenFull = vi.fn();
    render(<TerminalPreview project={project} epic={epic} onOpenFull={onOpenFull} />, { wrapper });
    await waitFor(() => expect(screen.getByTestId("terminal")).toHaveAttribute("data-pane", "pane1"));
    fireEvent.click(screen.getByRole("button", { name: "Open full session" }));
    expect(onOpenFull).toHaveBeenCalled();
  });

  it("shows session-ended when no session matches", async () => {
    h.listSessions.mockResolvedValue([]);
    render(<TerminalPreview project={project} epic={epic} onOpenFull={() => {}} />, { wrapper });
    await waitFor(() => expect(screen.getByText(/session ended/i)).toBeInTheDocument());
  });
});
