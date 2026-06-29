import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen } from "@testing-library/react";

vi.mock("@/components/TerminalView", () => ({
  TerminalView: (p: any) => <div data-testid={`tv-${p.paneId}`} />,
}));

import { GridView } from "@/components/GridView";
import { usePanes } from "@/store/panes";

describe("GridView", () => {
  beforeEach(() => usePanes.setState({ panes: [], focusedId: null }));

  it("renders a live TerminalView per open pane", () => {
    usePanes.getState().openPane({ serverId: "s", paneId: "%0", target: "default", session: "a", serverName: "h" });
    usePanes.getState().openPane({ serverId: "s", paneId: "%1", target: "default", session: "b", serverName: "h" });
    usePanes.getState().collapse();
    render(<GridView />);
    expect(screen.getByTestId("tv-%0")).toBeInTheDocument();
    expect(screen.getByTestId("tv-%1")).toBeInTheDocument();
  });

  it("expanding one tile keeps the others MOUNTED (liveness invariant)", async () => {
    usePanes.getState().openPane({ serverId: "s", paneId: "%0", target: "default", session: "a", serverName: "h" });
    usePanes.getState().openPane({ serverId: "s", paneId: "%1", target: "default", session: "b", serverName: "h" });
    usePanes.getState().focus("s:default:a:%0"); // expand %0
    render(<GridView />);
    // both terminals still in the DOM (the non-focused one is hidden, not unmounted)
    expect(screen.getByTestId("tv-%0")).toBeInTheDocument();
    expect(screen.getByTestId("tv-%1")).toBeInTheDocument();
    // a collapse control is present while expanded
    expect(screen.getByRole("button", { name: /grid/i })).toBeInTheDocument();
  });
});
