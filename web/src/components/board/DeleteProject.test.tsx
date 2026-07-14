import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({ deleteProject: vi.fn(), invalidateQueries: vi.fn(), navigate: vi.fn() }));
vi.mock("@/lib/api-client", async (importOriginal) => ({ ...(await importOriginal<object>()), deleteProject: h.deleteProject }));
vi.mock("@/lib/query-client", () => ({ queryClient: { invalidateQueries: h.invalidateQueries } }));
vi.mock("sonner", () => ({ toast: Object.assign(vi.fn(), { error: vi.fn() }) }));

import { DeleteProject } from "@/components/board/DeleteProject";
import { ApiError } from "@/lib/api-client";
import type { ProjectDTO } from "@/lib/contracts";

const project: ProjectDTO = {
  id: "p1", name: "school", repo: "o/r", server_id: "h1", target: "", workdir: "/w",
  base_branch: "main", provider: "claude", required_reviews: [], max_parallel: 1, paused: false, require_ci: false, pinned: false, requirements: [],
};

describe("DeleteProject", () => {
  beforeEach(() => { h.deleteProject.mockReset(); h.invalidateQueries.mockReset(); });

  it("requires the exact name before deleting", async () => {
    h.deleteProject.mockResolvedValue({ ok: true });
    const onDeleted = vi.fn();
    render(<DeleteProject project={project} onDeleted={onDeleted} onCancel={() => {}} />);
    const del = screen.getByRole("button", { name: "Delete project" });
    expect(del).toBeDisabled();
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "school" } });
    expect(del).toBeEnabled();
    fireEvent.click(del);
    await waitFor(() => expect(onDeleted).toHaveBeenCalled());
  });

  it("surfaces the 409 active-epics message", async () => {
    h.deleteProject.mockRejectedValue(new ApiError(409, "project has 2 active epics — cancel or finish them first"));
    render(<DeleteProject project={project} onDeleted={() => {}} onCancel={() => {}} />);
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "school" } });
    fireEvent.click(screen.getByRole("button", { name: "Delete project" }));
    await waitFor(() => expect(screen.getByText(/2 active epics/)).toBeInTheDocument());
  });
});
