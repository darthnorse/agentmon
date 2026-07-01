import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MobileKeyBar } from "@/components/MobileKeyBar";
import type { TerminalController } from "@/hooks/useTerminalSession";

function makeController(over: Partial<TerminalController> = {}): TerminalController {
  return {
    sendKey: vi.fn(), toggleCtrl: vi.fn(), ctrlArmed: false,
    paste: vi.fn().mockResolvedValue(undefined), copy: vi.fn().mockResolvedValue(undefined),
    dismissKeyboard: vi.fn(), focusTerminal: vi.fn(),
    ...over,
  };
}

// The bar only renders while the soft keyboard is up — simulate that (the visible viewport
// is much shorter than the layout viewport) for the rendering tests.
function keyboardUp() {
  vi.stubGlobal("innerHeight", 800);
  vi.stubGlobal("visualViewport", { height: 400, scale: 1, addEventListener: vi.fn(), removeEventListener: vi.fn() });
}

describe("MobileKeyBar", () => {
  beforeEach(keyboardUp);
  afterEach(() => vi.unstubAllGlobals());

  it("renders the buttons in the requested order (Close pinned first, no Enter)", () => {
    render(<MobileKeyBar controller={makeController()} />);
    const labels = screen.getAllByRole("button").map((b) => b.textContent);
    expect(labels).toEqual(["⌨▾", "Tab", "⏎ Nl", "Esc", "↑", "↓", "←", "→", "Copy", "Paste", "Ctrl", "⇧Tab"]);
    expect(screen.queryByRole("button", { name: "Enter" })).toBeNull();
  });

  it("routes each bar key to controller.sendKey and keeps terminal focus", async () => {
    const c = makeController();
    render(<MobileKeyBar controller={c} />);
    await userEvent.click(screen.getByRole("button", { name: "⏎ Nl" }));
    expect(c.sendKey).toHaveBeenCalledWith("nl");
    expect(c.focusTerminal).toHaveBeenCalled();
    await userEvent.click(screen.getByRole("button", { name: "⇧Tab" }));
    expect(c.sendKey).toHaveBeenCalledWith("stab");
  });

  it("routes Copy/Paste and keeps terminal focus", async () => {
    const c = makeController();
    render(<MobileKeyBar controller={c} />);
    await userEvent.click(screen.getByRole("button", { name: "Copy" }));
    await userEvent.click(screen.getByRole("button", { name: "Paste" }));
    expect(c.copy).toHaveBeenCalled();
    expect(c.paste).toHaveBeenCalled();
    expect(c.focusTerminal).toHaveBeenCalledTimes(2);
  });

  it("Ctrl toggles, reflects the armed state, and keeps focus", async () => {
    const c = makeController({ ctrlArmed: true });
    render(<MobileKeyBar controller={c} />);
    const ctrl = screen.getByRole("button", { name: "Ctrl" });
    expect(ctrl).toHaveAttribute("aria-pressed", "true");
    await userEvent.click(ctrl);
    expect(c.toggleCtrl).toHaveBeenCalled();
    expect(c.focusTerminal).toHaveBeenCalled();
  });

  it("keeps the soft keyboard up: a bar key's mousedown is prevented (no focus steal)", () => {
    render(<MobileKeyBar controller={makeController()} />);
    const nl = screen.getByRole("button", { name: "⏎ Nl" });
    const prevented = !fireEvent.mouseDown(nl); // fireEvent returns false when preventDefault was called
    expect(prevented).toBe(true);
  });

  it("only the Close button dismisses the keyboard (it does not re-focus)", async () => {
    const c = makeController();
    render(<MobileKeyBar controller={c} />);
    await userEvent.click(screen.getByRole("button", { name: /close keyboard/i }));
    expect(c.dismissKeyboard).toHaveBeenCalledTimes(1);
    expect(c.focusTerminal).not.toHaveBeenCalled();
  });

  it("omits a Lock button (no read-only lock in M5)", () => {
    render(<MobileKeyBar controller={makeController()} />);
    expect(screen.queryByRole("button", { name: /lock/i })).toBeNull();
  });

  it("renders nothing while the soft keyboard is down (full-screen reading)", () => {
    vi.stubGlobal("visualViewport", undefined); // keyboard down → no visual-viewport shrink
    const { container } = render(<MobileKeyBar controller={makeController()} />);
    expect(container.firstChild).toBeNull();
  });
});
