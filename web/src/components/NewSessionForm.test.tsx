import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const createSession = vi.fn();
vi.mock("@/lib/api-client", () => ({
  createSession: (...args: unknown[]) => createSession(...args),
  ApiError: class ApiError extends Error {
    constructor(public readonly status: number, message: string) {
      super(message);
      this.name = "ApiError";
    }
  },
}));

import { NewSessionForm } from "@/components/NewSessionForm";
import { ApiError } from "@/lib/api-client";

const session = {
  name: "dockmon", server: "s", target: "default", cwd: "/home", command: "",
  windows: [{ id: "@1", index: "0", name: "w", panes: [{ id: "%1", command: "bash", cwd: "/home" }] }],
};

describe("NewSessionForm", () => {
  beforeEach(() => { createSession.mockReset(); });

  it("disables submit while the name is empty or invalid", async () => {
    render(<NewSessionForm serverId="s" target="default" onCreated={vi.fn()} />);
    const submit = screen.getByRole("button", { name: /create/i });
    expect(submit).toBeDisabled(); // empty

    const name = screen.getByLabelText(/name/i);
    await userEvent.type(name, "has space");
    expect(submit).toBeDisabled(); // invalid charset

    await userEvent.clear(name);
    await userEvent.type(name, "-leading");
    expect(submit).toBeDisabled(); // invalid leading char

    await userEvent.clear(name);
    await userEvent.type(name, "dockmon");
    expect(submit).toBeEnabled();
  });

  it("submits a valid name and calls onCreated with the returned session", async () => {
    createSession.mockResolvedValue(session);
    const onCreated = vi.fn();
    render(<NewSessionForm serverId="s" target="default" onCreated={onCreated} />);
    await userEvent.type(screen.getByLabelText(/name/i), "dockmon");
    await userEvent.click(screen.getByRole("button", { name: /create/i }));
    await waitFor(() => expect(createSession).toHaveBeenCalledWith("s", { name: "dockmon" }));
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith(session));
  });

  it("includes cwd in the body when provided", async () => {
    createSession.mockResolvedValue(session);
    render(<NewSessionForm serverId="s" target="default" onCreated={vi.fn()} />);
    await userEvent.type(screen.getByLabelText(/name/i), "dockmon");
    await userEvent.type(screen.getByLabelText(/directory|cwd/i), "/home/proj");
    await userEvent.click(screen.getByRole("button", { name: /create/i }));
    await waitFor(() => expect(createSession).toHaveBeenCalledWith("s", { name: "dockmon", cwd: "/home/proj" }));
  });

  it("suggests the directory basename as the name until the name is edited", async () => {
    render(<NewSessionForm serverId="s" target="default" onCreated={vi.fn()} />);
    const name = screen.getByLabelText(/name/i) as HTMLInputElement;
    const cwd = screen.getByLabelText(/directory|cwd/i);

    // Typing the directory first auto-fills the name from its basename (§9.5).
    await userEvent.type(cwd, "/home/dev/streammon-api");
    expect(name.value).toBe("streammon-api");

    // Once the user edits the name, later cwd edits must not overwrite it.
    await userEvent.clear(name);
    await userEvent.type(name, "custom");
    await userEvent.clear(cwd);
    await userEvent.type(cwd, "/var/other");
    expect(name.value).toBe("custom");
  });

  it("shows an inline 'name already exists' error on 409", async () => {
    createSession.mockRejectedValue(new ApiError(409, "session already exists"));
    const onCreated = vi.fn();
    render(<NewSessionForm serverId="s" target="default" onCreated={onCreated} />);
    await userEvent.type(screen.getByLabelText(/name/i), "dockmon");
    await userEvent.click(screen.getByRole("button", { name: /create/i }));
    expect(await screen.findByText(/already exists/i)).toBeInTheDocument();
    expect(onCreated).not.toHaveBeenCalled();
  });
});
