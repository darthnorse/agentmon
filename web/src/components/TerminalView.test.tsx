import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";

// xterm.js needs a real canvas/WebGL; mock the DOM wrapper to a smoke double.
// The imperative handle exposes a module-level `focus` spy (via useImperativeHandle)
// so tests can observe TerminalView's `active`-prop focus handoff without needing a
// real xterm instance. useTerminalSession (and its own onOpen-time focus() call) still
// runs for real — only the DOM/xterm.js internals are stubbed out.
const focus = vi.fn();
vi.mock("@/components/XTerm", async () => {
  const React = await import("react");
  const { forwardRef, useImperativeHandle } = React;
  return {
    XTerm: forwardRef((_p: unknown, ref: React.Ref<unknown>) => {
      useImperativeHandle(ref, () => ({
        focus,
        write: vi.fn(),
        fit: vi.fn(),
        reset: vi.fn(),
        blur: vi.fn(),
        appCursor: () => false,
        getSelection: () => "",
        paste: vi.fn(),
        scrollLines: vi.fn(),
      }));
      return <div data-testid="xterm" />;
    }),
  };
});
// Avoid opening a real socket in jsdom.
const open = vi.fn();
const dispose = vi.fn();
const retryNow = vi.fn();
vi.mock("@/lib/ws-terminal", async (orig) => {
  const mod = await (orig as any)();
  return { ...mod, TerminalSocket: class { constructor() {} open = open; dispose = dispose; send() {} resize() {} retryNow = retryNow; } };
});

import { TerminalView } from "@/components/TerminalView";

describe("TerminalView", () => {
  beforeEach(() => {
    focus.mockClear();
  });

  it("focuses the terminal when it becomes active", () => {
    const { rerender } = render(
      <TerminalView serverId="s1" paneId="%0" target="default" active={false} />,
    );
    expect(focus).not.toHaveBeenCalled();
    rerender(<TerminalView serverId="s1" paneId="%0" target="default" active={true} />);
    expect(focus).toHaveBeenCalledTimes(1);
  });

  it("does not focus on mount when active is undefined (grid path unchanged)", () => {
    render(<TerminalView serverId="s1" paneId="%0" target="default" />);
    expect(focus).not.toHaveBeenCalled();
  });

  it("refocuses on a focusNonce bump even when active stays true (repeated window-switch chord)", () => {
    const { rerender } = render(
      <TerminalView serverId="s1" paneId="%0" target="default" active={true} focusNonce={0} />,
    );
    expect(focus).toHaveBeenCalledTimes(1); // initial focus on becoming active
    rerender(<TerminalView serverId="s1" paneId="%0" target="default" active={true} focusNonce={1} />);
    expect(focus).toHaveBeenCalledTimes(2); // nonce change forces a refocus though active is unchanged
  });

  it("mounts the terminal and opens a socket; shows the key bar when the keyboard is up", () => {
    // The key bar only renders while the soft keyboard is up — simulate that (visible
    // viewport much shorter than the layout viewport).
    vi.stubGlobal("innerHeight", 800);
    vi.stubGlobal("visualViewport", { height: 400, scale: 1, addEventListener: vi.fn(), removeEventListener: vi.fn() });
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

  it("kicks the socket to reconnect when the tile becomes active", () => {
    retryNow.mockClear();
    const { rerender } = render(
      <TerminalView serverId="s" paneId="%1" target="default" active={false} />,
    );
    expect(retryNow).not.toHaveBeenCalled();
    rerender(<TerminalView serverId="s" paneId="%1" target="default" active={true} />);
    expect(retryNow).toHaveBeenCalled();
  });

  it("shows 'session ended' + close instead of the reconnect banner when ended", () => {
    const onClose = vi.fn();
    render(
      <TerminalView serverId="s" paneId="%1" target="default" ended onClose={onClose} />,
    );
    expect(screen.getByText("session ended")).toBeInTheDocument();
    expect(screen.queryByText(/connecting|reconnecting/)).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "close" }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("shows the normal connecting banner when not ended", () => {
    render(<TerminalView serverId="s" paneId="%1" target="default" />);
    expect(screen.getByText("connecting…")).toBeInTheDocument();
    expect(screen.queryByText("session ended")).toBeNull();
  });

  it("omits the close button when no onClose is provided", () => {
    render(<TerminalView serverId="s" paneId="%1" target="default" ended />);
    expect(screen.getByText("session ended")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "close" })).toBeNull();
  });
});
