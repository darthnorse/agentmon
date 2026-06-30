import { describe, it, expect, vi } from "vitest";
import { rowActivation } from "@/lib/row-activation";

describe("rowActivation", () => {
  const ev = (over: Record<string, unknown>) => ({ preventDefault: vi.fn(), ...over }) as never;

  it("opens on a click", () => {
    const onOpen = vi.fn();
    rowActivation(onOpen).onClick();
    expect(onOpen).toHaveBeenCalledTimes(1);
  });

  it("opens on the row's OWN Enter / Space keydown", () => {
    const onOpen = vi.fn();
    const el = {};
    rowActivation(onOpen).onKeyDown(ev({ key: "Enter", target: el, currentTarget: el }));
    rowActivation(onOpen).onKeyDown(ev({ key: " ", target: el, currentTarget: el }));
    expect(onOpen).toHaveBeenCalledTimes(2);
  });

  it("does NOT open when the keydown bubbled up from a child control (e.g. the rename pencil)", () => {
    const onOpen = vi.fn();
    // target (the inner button) differs from currentTarget (the row).
    rowActivation(onOpen).onKeyDown(ev({ key: "Enter", target: {}, currentTarget: {} }));
    expect(onOpen).not.toHaveBeenCalled();
  });

  it("ignores other keys on the row itself", () => {
    const onOpen = vi.fn();
    const el = {};
    rowActivation(onOpen).onKeyDown(ev({ key: "a", target: el, currentTarget: el }));
    expect(onOpen).not.toHaveBeenCalled();
  });
});
