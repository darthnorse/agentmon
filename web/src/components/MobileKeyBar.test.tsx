import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
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

// The bar only renders while the soft keyboard is up — simulate that (the visible viewport
// is much shorter than the layout viewport) for the rendering tests.
function keyboardUp() {
  vi.stubGlobal("innerHeight", 800);
  vi.stubGlobal("visualViewport", { height: 400, scale: 1, addEventListener: vi.fn(), removeEventListener: vi.fn() });
}

describe("MobileKeyBar", () => {
  beforeEach(keyboardUp);
  afterEach(() => vi.unstubAllGlobals());

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

  it("renders nothing while the soft keyboard is down (full-screen reading)", () => {
    vi.stubGlobal("visualViewport", undefined); // keyboard down → no visual-viewport shrink
    const { container } = render(<MobileKeyBar controller={makeController()} />);
    expect(container.firstChild).toBeNull();
  });

  it("dismisses the keyboard on a single tap of the pinned close button", async () => {
    const c = makeController();
    render(<MobileKeyBar controller={c} />);
    await userEvent.click(screen.getByRole("button", { name: /close keyboard/i }));
    expect(c.dismissKeyboard).toHaveBeenCalledTimes(1);
  });
});
