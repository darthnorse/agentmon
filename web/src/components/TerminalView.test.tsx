import { describe, it, expect, vi } from "vitest";
import { render } from "@testing-library/react";

// xterm.js needs a real canvas/WebGL; mock the DOM wrapper to a smoke double.
vi.mock("@/components/XTerm", async () => {
  const { forwardRef } = await import("react");
  return { XTerm: forwardRef((_p: unknown, _r: unknown) => <div data-testid="xterm" />) };
});
// Avoid opening a real socket in jsdom.
const open = vi.fn();
const dispose = vi.fn();
vi.mock("@/lib/ws-terminal", async (orig) => {
  const mod = await (orig as any)();
  return { ...mod, TerminalSocket: class { constructor() {} open = open; dispose = dispose; send() {} resize() {} } };
});

import { TerminalView } from "@/components/TerminalView";

describe("TerminalView", () => {
  it("mounts the terminal and opens a socket; shows the key bar when the keyboard is up", () => {
    // The key bar only renders while the soft keyboard is up — simulate that (visible
    // viewport much shorter than the layout viewport).
    vi.stubGlobal("innerHeight", 800);
    vi.stubGlobal("visualViewport", { height: 400, addEventListener: vi.fn(), removeEventListener: vi.fn() });
    const { getByTestId, getByText, unmount } = render(
      <TerminalView serverId="s" paneId="%0" target="default" showKeyBar />,
    );
    expect(getByTestId("xterm")).toBeInTheDocument();
    expect(open).toHaveBeenCalled();
    expect(getByText("Esc")).toBeInTheDocument(); // key bar present
    unmount();
    expect(dispose).toHaveBeenCalled(); // cleans up the socket
    vi.unstubAllGlobals();
  });
});
