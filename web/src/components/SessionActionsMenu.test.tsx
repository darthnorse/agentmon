import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SessionActionsMenu } from "./SessionActionsMenu";

const { closePaneMock } = vi.hoisted(() => ({ closePaneMock: vi.fn() }));

vi.mock("@/lib/api-client", () => ({ killSession: vi.fn().mockResolvedValue(undefined), ApiError: class extends Error { status = 0; } }));
vi.mock("@/lib/query-client", () => ({ queryClient: { invalidateQueries: vi.fn() } }));
vi.mock("@/store/panes", () => ({
  usePanes: { getState: () => ({ closePane: closePaneMock }) },
  paneKey: (serverId: string, target: string, session: string, paneId: string) => `${serverId}:${target}:${session}:${paneId}`,
}));
vi.mock("sonner", () => ({ toast: { error: vi.fn() } }));

import { killSession, ApiError } from "@/lib/api-client";
import { queryClient } from "@/lib/query-client";
import { toast } from "sonner";

function row() {
  return { serverId: "aigallery", serverName: "AG", target: "default", name: "proj", paneId: "%0", state: "idle" as const };
}

describe("SessionActionsMenu", () => {
  beforeEach(() => vi.clearAllMocks());

  it("shows the name and a menu button, not an open menu", () => {
    render(<SessionActionsMenu {...row()} />);
    expect(screen.getByText("proj")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /session actions/i })).toBeInTheDocument();
    expect(screen.queryByText(/kill session/i)).toBeNull();
  });

  it("opens the menu, then the kill modal, and kills on confirm", async () => {
    render(<SessionActionsMenu {...row()} />);
    await userEvent.click(screen.getByRole("button", { name: /session actions/i }));
    await userEvent.click(screen.getByRole("menuitem", { name: /kill session/i }));
    // modal is up
    await userEvent.click(screen.getByRole("button", { name: /^kill session$/i }));
    expect(killSession).toHaveBeenCalledWith("aigallery", "proj", "default");
    await waitFor(() => {
      expect(closePaneMock).toHaveBeenCalledWith("aigallery:default:proj:%0");
      expect(queryClient.invalidateQueries).toHaveBeenCalledWith({ queryKey: ["sessions", "aigallery"] });
    });
  });

  it("enters rename mode from the menu", async () => {
    render(<SessionActionsMenu {...row()} />);
    await userEvent.click(screen.getByRole("button", { name: /session actions/i }));
    await userEvent.click(screen.getByRole("menuitem", { name: /rename/i }));
    expect(screen.getByRole("textbox", { name: /new session name/i })).toBeInTheDocument();
  });

  // Fix 1: bubbling contract — name click must reach the row; ⋯ must not.
  it("bubbles click-on-name to the parent row but stops propagation for the ⋯ button", async () => {
    const onOpen = vi.fn();
    render(
      <div onClick={onOpen}>
        <SessionActionsMenu {...row()} />
      </div>,
    );
    // Clicking the session name bubbles to the row handler.
    await userEvent.click(screen.getByText("proj"));
    expect(onOpen).toHaveBeenCalledOnce();

    onOpen.mockClear();

    // Clicking the ⋯ button opens the menu — it must NOT bubble to the row.
    await userEvent.click(screen.getByRole("button", { name: /session actions/i }));
    expect(onOpen).not.toHaveBeenCalled();
  });

  // Fix 2: a non-404 kill error must toast, not vanish silently.
  it("toasts an error on a non-404 kill failure", async () => {
    const { ApiError: ApiErrorCtor } = await import("@/lib/api-client");
    const err = new ApiErrorCtor(500, "server error");
    vi.mocked(killSession).mockRejectedValueOnce(err);

    render(<SessionActionsMenu {...row()} />);
    await userEvent.click(screen.getByRole("button", { name: /session actions/i }));
    await userEvent.click(screen.getByRole("menuitem", { name: /kill session/i }));
    await userEvent.click(screen.getByRole("button", { name: /^kill session$/i }));

    await waitFor(() => {
      expect(toast.error).toHaveBeenCalledWith("Couldn't kill proj");
    });
    expect(closePaneMock).not.toHaveBeenCalled();
  });
});
