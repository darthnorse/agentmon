import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";

vi.mock("@/components/TerminalView", () => ({
  TerminalView: (p: any) => (
    <div data-testid={`tv-${p.paneId}`} data-active={String(!!p.active)} data-keybar={String(!!p.showKeyBar)}>
      {p.ended ? (
        <div>
          session ended <button onClick={p.onClose}>close</button>
        </div>
      ) : null}
    </div>
  ),
}));

import { MobileTerminalStack } from "@/components/MobileTerminalStack";
import { paneIdentity } from "@/lib/pane-identity";
import { TERMINAL_THEMES } from "@/lib/terminal-themes";

const panes = [
  { serverId: "s1", target: "default", paneId: "%0" },
  { serverId: "s1", target: "default", paneId: "%1" },
];

describe("MobileTerminalStack", () => {
  it("mounts a terminal per pane; only the focused one is active + shows the key bar + is visible", () => {
    const focusedId = paneIdentity("s1", "default", "%1");
    const { container } = render(
      <MobileTerminalStack panes={panes} focusedId={focusedId} fontSize={13} theme={TERMINAL_THEMES.dark} />,
    );
    // both mounted (keep-alive)
    expect(screen.getByTestId("tv-%0")).toBeInTheDocument();
    expect(screen.getByTestId("tv-%1")).toBeInTheDocument();
    // only focused is active + has the key bar
    expect(screen.getByTestId("tv-%1").getAttribute("data-active")).toBe("true");
    expect(screen.getByTestId("tv-%0").getAttribute("data-active")).toBe("false");
    expect(screen.getByTestId("tv-%1").getAttribute("data-keybar")).toBe("true");
    expect(screen.getByTestId("tv-%0").getAttribute("data-keybar")).toBe("false");
    // only focused wrapper is visible
    const wrappers = Array.from(container.querySelectorAll("[data-pane-wrapper]")) as HTMLElement[];
    const visible = wrappers.filter((w) => w.style.display !== "none");
    expect(visible).toHaveLength(1);
  });

  it("threads ended + onClose(paneIdentity) through to the pane's TerminalView", () => {
    const onClosePane = vi.fn();
    render(
      <MobileTerminalStack
        panes={[{ serverId: "s", target: "default", paneId: "%0" }] as any}
        focusedId="s:default:%0"
        fontSize={14}
        theme={{} as any}
        endedIds={new Set(["s:default:%0"])}
        onClosePane={onClosePane}
      />,
    );
    // With the real TerminalView (or a props-recording mock) the ended banner is on:
    expect(screen.getByText("session ended")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "close" }));
    expect(onClosePane).toHaveBeenCalledWith("s:default:%0");
  });
});
