import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MobileKeyBar } from "@/components/MobileKeyBar";
import type { TerminalController } from "@/hooks/useTerminalSession";

function makeController(over: Partial<TerminalController> = {}): TerminalController {
  return {
    sendKey: vi.fn(), toggleCtrl: vi.fn(), ctrlArmed: false,
    paste: vi.fn().mockResolvedValue(undefined), copy: vi.fn().mockResolvedValue(undefined),
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
});
