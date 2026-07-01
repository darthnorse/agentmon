import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SessionActionsMenu } from "./SessionActionsMenu";

vi.mock("@/lib/api-client", () => ({ killSession: vi.fn().mockResolvedValue(undefined), ApiError: class extends Error { status = 0; } }));
vi.mock("@/lib/query-client", () => ({ queryClient: { invalidateQueries: vi.fn() } }));

import { killSession } from "@/lib/api-client";

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
  });

  it("enters rename mode from the menu", async () => {
    render(<SessionActionsMenu {...row()} />);
    await userEvent.click(screen.getByRole("button", { name: /session actions/i }));
    await userEvent.click(screen.getByRole("menuitem", { name: /rename/i }));
    expect(screen.getByRole("textbox", { name: /new session name/i })).toBeInTheDocument();
  });
});
