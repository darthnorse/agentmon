import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const signIn = vi.fn();
vi.mock("@/store/auth", () => ({
  useAuth: (sel: any) => sel({ signIn }),
}));

import { LoginForm } from "@/components/LoginForm";

describe("LoginForm", () => {
  beforeEach(() => { signIn.mockReset(); });

  it("submits credentials and calls onSuccess", async () => {
    signIn.mockResolvedValue(undefined);
    const onSuccess = vi.fn();
    render(<LoginForm onSuccess={onSuccess} />);
    await userEvent.type(screen.getByLabelText(/username/i), "patrik");
    await userEvent.type(screen.getByLabelText(/password/i), "secret");
    await userEvent.click(screen.getByRole("button", { name: /sign in/i }));
    await waitFor(() => expect(signIn).toHaveBeenCalledWith("patrik", "secret"));
    await waitFor(() => expect(onSuccess).toHaveBeenCalled());
  });

  it("shows an error message when sign in fails", async () => {
    signIn.mockRejectedValue(Object.assign(new Error("invalid credentials"), { status: 401 }));
    render(<LoginForm onSuccess={vi.fn()} />);
    await userEvent.type(screen.getByLabelText(/username/i), "x");
    await userEvent.type(screen.getByLabelText(/password/i), "y");
    await userEvent.click(screen.getByRole("button", { name: /sign in/i }));
    expect(await screen.findByText(/invalid credentials/i)).toBeInTheDocument();
  });
});
