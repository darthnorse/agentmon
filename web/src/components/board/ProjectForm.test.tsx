import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({ createProject: vi.fn(), patchProject: vi.fn(), openOrFocusSession: vi.fn(), navigate: vi.fn(), invalidateQueries: vi.fn() }));
vi.mock("@/lib/api-client", async (importOriginal) => ({ ...(await importOriginal<object>()), createProject: h.createProject, patchProject: h.patchProject }));
vi.mock("@/lib/query-client", () => ({ queryClient: { invalidateQueries: h.invalidateQueries } }));
vi.mock("@/components/board/open-session", () => ({ openOrFocusSession: h.openOrFocusSession }));
vi.mock("@/lib/use-media-query", () => ({ useMediaQuery: () => true }));
vi.mock("@tanstack/react-router", () => ({ useNavigate: () => h.navigate }));
vi.mock("sonner", () => ({ toast: Object.assign(vi.fn(), { error: vi.fn() }) }));

import { ProjectForm } from "@/components/board/ProjectForm";
import type { ProjectDTO, ServerSummary } from "@/lib/contracts";

// listServers (Registry.List) returns ACTIVE registrations only, always
// enabled:true, with NO connectivity/health field — so the picker cannot
// distinguish "offline" and must not pretend to (Finding 10). Real
// connectivity is proven by the doctor-verify step, which fails loudly on a
// dead host.
const servers: ServerSummary[] = [
  { id: "h1", name: "aigallery", labels: [], enabled: true },
  { id: "h2", name: "carepath-dev", labels: [], enabled: true },
];

describe("ProjectForm create", () => {
  beforeEach(() => { h.createProject.mockReset(); h.openOrFocusSession.mockReset(); });

  it("validates repo and required fields before enabling submit", () => {
    render(<ProjectForm mode="create" servers={servers} onDone={() => {}} />);
    const submit = screen.getByRole("button", { name: "Register project" });
    expect(submit).toBeDisabled();
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "school" } });
    fireEvent.change(screen.getByLabelText("Repo"), { target: { value: "not-a-repo" } });
    fireEvent.change(screen.getByLabelText("Workdir"), { target: { value: "/srv/school" } });
    expect(submit).toBeDisabled();
    fireEvent.change(screen.getByLabelText("Repo"), { target: { value: "darthnorse/school" } });
    expect(submit).toBeEnabled();
  });

  it("creates the project and shows the doctor-verify step", async () => {
    h.createProject.mockResolvedValue({ id: "p1", name: "school", repo: "darthnorse/school", server_id: "h1", target: "", workdir: "/srv/school", base_branch: "main", provider: "claude", required_reviews: ["cross-model"], max_parallel: 1, paused: false, require_ci: true });
    render(<ProjectForm mode="create" servers={servers} onDone={() => {}} />);
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "school" } });
    fireEvent.change(screen.getByLabelText("Repo"), { target: { value: "darthnorse/school" } });
    fireEvent.change(screen.getByLabelText("Workdir"), { target: { value: "/srv/school" } });
    fireEvent.click(screen.getByRole("button", { name: "Register project" }));
    await waitFor(() => expect(screen.getByRole("button", { name: /Run doctor/ })).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: /Run doctor/ }));
    expect(h.openOrFocusSession).toHaveBeenCalledWith(
      expect.objectContaining({ serverId: "h1", command: "agentmon doctor", cwd: "/srv/school" }),
      true, h.navigate,
    );
  });

  it("lists every registered server as selectable", () => {
    render(<ProjectForm mode="create" servers={servers} onDone={() => {}} />);
    for (const name of ["aigallery", "carepath-dev"]) {
      const opt = screen.getByRole("option", { name: new RegExp(name) }) as HTMLOptionElement;
      expect(opt.disabled).toBe(false);
    }
  });
});

describe("ProjectForm requirements", () => {
  beforeEach(() => { h.createProject.mockReset(); h.patchProject.mockReset(); });

  it("sends added requirement rows on create (id blank — server derives it)", async () => {
    h.createProject.mockResolvedValue({ id: "p1", name: "school", repo: "darthnorse/school", server_id: "h1", target: "", workdir: "/srv/school", base_branch: "main", provider: "claude", required_reviews: [], max_parallel: 1, paused: false, require_ci: true, pinned: false, requirements: [] });
    render(<ProjectForm mode="create" servers={servers} onDone={() => {}} />);
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "school" } });
    fireEvent.change(screen.getByLabelText("Repo"), { target: { value: "darthnorse/school" } });
    fireEvent.change(screen.getByLabelText("Workdir"), { target: { value: "/srv/school" } });
    fireEvent.click(screen.getByRole("button", { name: "Add requirement" }));
    fireEvent.change(screen.getByLabelText("Requirement 1 text"), { target: { value: "Always use RLS" } });
    fireEvent.change(screen.getByLabelText("Requirement 1 check command"), { target: { value: "scripts/rls.sh" } });
    fireEvent.click(screen.getByRole("button", { name: "Register project" }));
    await waitFor(() => expect(h.createProject).toHaveBeenCalled());
    expect(h.createProject).toHaveBeenCalledWith(expect.objectContaining({
      requirements: [{ id: "", text: "Always use RLS", check_cmd: "scripts/rls.sh" }],
    }));
  });

  it("renders existing requirements in edit mode and removes a row, keeping ids", async () => {
    const project: ProjectDTO = { id: "p1", name: "school", repo: "darthnorse/school", server_id: "h1", target: "", workdir: "/srv/school", base_branch: "main", provider: "claude", required_reviews: [], max_parallel: 1, paused: false, require_ci: true, pinned: false, requirements: [{ id: "rls", text: "Always use RLS" }, { id: "wcag", text: "WCAG 2.2 AA" }] };
    h.patchProject.mockResolvedValue(project);
    render(<ProjectForm mode="edit" project={project} onDone={() => {}} />);
    expect((screen.getByLabelText("Requirement 1 text") as HTMLInputElement).value).toBe("Always use RLS");
    expect((screen.getByLabelText("Requirement 2 text") as HTMLInputElement).value).toBe("WCAG 2.2 AA");
    fireEvent.click(screen.getByRole("button", { name: "Remove requirement 1" }));
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(h.patchProject).toHaveBeenCalled());
    expect(h.patchProject).toHaveBeenCalledWith("p1", expect.objectContaining({
      requirements: [{ id: "wcag", text: "WCAG 2.2 AA" }],
    }));
  });
});
