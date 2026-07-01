import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { KillSessionModal } from "./KillSessionModal";

describe("KillSessionModal", () => {
  it("names the session + host and confirms on Kill", async () => {
    const onConfirm = vi.fn();
    const onClose = vi.fn();
    render(<KillSessionModal server="aigallery" name="proj" state="idle" onConfirm={onConfirm} onClose={onClose} />);
    expect(screen.getByText(/proj/)).toBeInTheDocument();
    expect(screen.getByText(/aigallery/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /kill session/i }));
    expect(onConfirm).toHaveBeenCalledOnce();
  });

  it("cancels without confirming", async () => {
    const onConfirm = vi.fn();
    const onClose = vi.fn();
    render(<KillSessionModal server="aigallery" name="proj" state="idle" onConfirm={onConfirm} onClose={onClose} />);
    await userEvent.click(screen.getByRole("button", { name: /cancel/i }));
    expect(onClose).toHaveBeenCalledOnce();
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it("warns when the agent is mid-task (working/blocked)", () => {
    const { rerender } = render(
      <KillSessionModal server="s" name="p" state="working" onConfirm={() => {}} onClose={() => {}} />,
    );
    expect(screen.getByText(/mid-task/i)).toBeInTheDocument();
    rerender(<KillSessionModal server="s" name="p" state="blocked" onConfirm={() => {}} onClose={() => {}} />);
    expect(screen.getByText(/mid-task/i)).toBeInTheDocument();
  });

  it("shows no mid-task warning for idle/done", () => {
    render(<KillSessionModal server="s" name="p" state="idle" onConfirm={() => {}} onClose={() => {}} />);
    expect(screen.queryByText(/mid-task/i)).toBeNull();
  });
});
