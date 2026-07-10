import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, act } from "@testing-library/react";

vi.mock("@/components/TerminalView", () => ({
  TerminalView: (p: any) => <div data-testid={`tv-${p.paneId}`} />,
}));

import { GridView } from "@/components/GridView";
import { usePanes } from "@/store/panes";
import { useSessionState } from "@/store/session-state";

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

  it("shows a state dot per tile from the live store", () => {
    useSessionState.getState().reset();
    useSessionState.getState().applySnapshot([{ server: "s", target: "default", session: "a", state: "blocked" }]);
    usePanes.getState().openPane({ serverId: "s", paneId: "%0", target: "default", session: "a", serverName: "h" });
    usePanes.getState().collapse();
    render(<GridView />);
    expect(screen.getByRole("img", { name: "blocked" })).toBeInTheDocument();
  });

  it("falls back to the pane's REST state before live state arrives", () => {
    useSessionState.getState().reset();
    usePanes.getState().openPane({ serverId: "s", paneId: "%0", target: "default", session: "a", serverName: "h", state: "working" });
    usePanes.getState().collapse();
    render(<GridView />);
    expect(screen.getByRole("img", { name: "working" })).toBeInTheDocument();
  });

  it("renaming an open pane does NOT remount its terminal (the WS survives)", () => {
    usePanes.getState().openPane({ serverId: "s", paneId: "%0", target: "default", session: "old", serverName: "h" });
    usePanes.getState().collapse();
    render(<GridView />);
    const before = screen.getByTestId("tv-%0");
    // renamePane changes the pane's id; if the tile keyed off it, React would
    // unmount/remount the terminal (new DOM node) and drop the WebSocket.
    act(() => usePanes.getState().renamePane("s:default:old:%0", "newname"));
    const after = screen.getByTestId("tv-%0");
    expect(after).toBe(before); // same DOM node ⇒ the tile (and its WS) was preserved
  });

  it("tags a tile whose pane identity maps to a provider", () => {
    usePanes.getState().openPane({ serverId: "s", paneId: "%0", target: "default", session: "a", serverName: "h" });
    usePanes.getState().collapse();
    const providers = new Map([["s:default:%0", "codex" as const]]);
    render(<GridView providers={providers} />);
    expect(screen.getByText("codex")).toHaveAttribute("title", "Codex");
  });
});
