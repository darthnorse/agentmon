import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { usePanes } from "@/store/panes";

// Mock only the API boundary; the panes store + query client are real so the test
// verifies the actual open-pane re-key.
const h = vi.hoisted(() => {
  class ApiError extends Error {
    constructor(public status: number, message: string) {
      super(message);
    }
  }
  return { ApiError, renameSession: vi.fn() };
});
vi.mock("@/lib/api-client", () => ({
  renameSession: h.renameSession,
  createSession: vi.fn(), // NewSessionForm (source of isValidSessionName) imports this
  ApiError: h.ApiError,
}));

import { SessionNameEditor } from "@/components/SessionNameEditor";

async function startEditing() {
  await userEvent.click(screen.getByLabelText("Rename session"));
  const input = screen.getByLabelText("New session name");
  await userEvent.clear(input);
  return input;
}

describe("SessionNameEditor", () => {
  beforeEach(() => {
    h.renameSession.mockReset();
    usePanes.setState({ panes: [], focusedId: null });
  });

  it("saves: calls the API, re-keys the open pane, and reports the new name", async () => {
    usePanes.getState().openPane({ serverId: "s", paneId: "%0", target: "default", session: "old", serverName: "srv" });
    h.renameSession.mockResolvedValue({ name: "newname" });
    const onRenamed = vi.fn();
    render(<SessionNameEditor serverId="s" target="default" name="old" paneId="%0" onRenamed={onRenamed} />);

    const input = await startEditing();
    await userEvent.type(input, "newname{Enter}");

    await waitFor(() => expect(h.renameSession).toHaveBeenCalledWith("s", "old", "newname", "default"));
    expect(usePanes.getState().panes[0].id).toBe("s:default:newname:%0"); // pane re-keyed
    expect(onRenamed).toHaveBeenCalledWith("newname");
  });

  it("shows 'already exists' on 409 and does not re-key the pane", async () => {
    usePanes.getState().openPane({ serverId: "s", paneId: "%0", target: "default", session: "old", serverName: "srv" });
    h.renameSession.mockRejectedValue(new h.ApiError(409, "exists"));
    render(<SessionNameEditor serverId="s" target="default" name="old" paneId="%0" />);

    const input = await startEditing();
    await userEvent.type(input, "taken{Enter}");

    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent(/already exists/i));
    expect(usePanes.getState().panes[0].id).toBe("s:default:old:%0"); // unchanged
  });

  it("rejects an invalid name client-side without calling the API", async () => {
    render(<SessionNameEditor serverId="s" target="default" name="old" paneId="%0" />);
    const input = await startEditing();
    await userEvent.type(input, "bad name{Enter}");

    await waitFor(() => expect(screen.getByRole("alert")).toBeInTheDocument());
    expect(h.renameSession).not.toHaveBeenCalled();
  });

  it("Escape cancels editing without calling the API", async () => {
    render(<SessionNameEditor serverId="s" target="default" name="old" paneId="%0" />);
    const input = await startEditing();
    await userEvent.type(input, "whatever{Escape}");

    await waitFor(() => expect(screen.queryByLabelText("New session name")).not.toBeInTheDocument());
    expect(h.renameSession).not.toHaveBeenCalled();
  });
});
