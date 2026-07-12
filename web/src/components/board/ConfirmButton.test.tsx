import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ConfirmButton } from "@/components/board/ConfirmButton";

describe("ConfirmButton", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("requires two clicks and disarms after 3s", () => {
    const onConfirm = vi.fn();
    render(<ConfirmButton label="Approve & merge" confirmLabel="Merge?" onConfirm={onConfirm} />);
    fireEvent.click(screen.getByRole("button", { name: "Approve & merge" }));
    expect(onConfirm).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Merge?" }));
    expect(onConfirm).toHaveBeenCalledTimes(1);
    // Disarm path
    fireEvent.click(screen.getByRole("button", { name: "Approve & merge" }));
    act(() => vi.advanceTimersByTime(3100));
    expect(screen.getByRole("button", { name: "Approve & merge" })).toBeInTheDocument();
  });
});
