import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MobileKeyBar } from "@/components/MobileKeyBar";
import type { TerminalController } from "@/hooks/useTerminalSession";

function makeController(over: Partial<TerminalController> = {}): TerminalController {
  return {
    sendKey: vi.fn(), toggleCtrl: vi.fn(), ctrlArmed: false,
    paste: vi.fn().mockResolvedValue(undefined), copy: vi.fn().mockResolvedValue(undefined),
    dismissKeyboard: vi.fn(),
    ...over,
  };
}

describe("MobileKeyBar", () => {
  it("routes each bar key to controller.sendKey", async () => {
    const c = makeController();
    render(<MobileKeyBar controller={c} />);
    await userEvent.click(screen.getByRole("button", { name: "Esc" }));
    await userEvent.click(screen.getByRole("button", { name: "Enter" }));
    expect(c.sendKey).toHaveBeenCalledWith("esc");
    expect(c.sendKey).toHaveBeenCalledWith("enter");
  });

  it("Ctrl toggles and reflects the armed state", async () => {
    const c = makeController({ ctrlArmed: true });
    render(<MobileKeyBar controller={c} />);
    const ctrl = screen.getByRole("button", { name: "Ctrl" });
    expect(ctrl).toHaveAttribute("aria-pressed", "true");
    await userEvent.click(ctrl);
    expect(c.toggleCtrl).toHaveBeenCalled();
  });

  it("omits a Lock button (no read-only lock in M5)", () => {
    render(<MobileKeyBar controller={makeController()} />);
    expect(screen.queryByRole("button", { name: /lock/i })).toBeNull();
  });

  it("hides the close-keyboard button when the soft keyboard is down", () => {
    // jsdom has no visualViewport → keyboardOpen is false.
    render(<MobileKeyBar controller={makeController()} />);
    expect(screen.queryByRole("button", { name: /close keyboard/i })).toBeNull();
  });

  it("shows the close-keyboard button while the keyboard is up and dismisses on a single tap", async () => {
    // Simulate the keyboard up: the visible viewport is much shorter than the layout viewport.
    vi.stubGlobal("innerHeight", 800);
    vi.stubGlobal("visualViewport", { height: 400, addEventListener: vi.fn(), removeEventListener: vi.fn() });
    try {
      const c = makeController();
      render(<MobileKeyBar controller={c} />);
      await userEvent.click(screen.getByRole("button", { name: /close keyboard/i }));
      expect(c.dismissKeyboard).toHaveBeenCalledTimes(1);
    } finally {
      vi.unstubAllGlobals();
    }
  });
});
