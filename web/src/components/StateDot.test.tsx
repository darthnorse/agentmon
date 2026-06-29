import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { StateDot } from "@/components/StateDot";
import type { SessionState } from "@/lib/contracts";

describe("StateDot", () => {
  it("labels and colors each state", () => {
    const cases: [SessionState, string][] = [
      ["blocked", "bg-red-500"], ["done", "bg-blue-500"], ["working", "bg-amber-500"],
      ["idle", "bg-green-500"], ["unknown", "bg-zinc-400"],
    ];
    for (const [state, cls] of cases) {
      const { unmount } = render(<StateDot state={state} />);
      const dot = screen.getByRole("img", { name: state });
      expect(dot.className).toContain(cls);
      unmount();
    }
  });

  it("renders the unknown dot for an out-of-enum value (no crash)", () => {
    render(<StateDot state={"weird" as SessionState} />);
    const dot = screen.getByRole("img", { name: "unknown" });
    expect(dot.className).toContain("bg-zinc-400");
  });
});
