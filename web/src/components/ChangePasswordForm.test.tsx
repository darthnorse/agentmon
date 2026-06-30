import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

import { ChangePasswordForm } from "@/components/ChangePasswordForm";
import { useAuth } from "@/store/auth";
import { setCsrfToken } from "@/lib/api-client";

function mockFetch(status: number, body: unknown) {
  const has = body !== undefined;
  return vi.fn(async () => new Response(has ? JSON.stringify(body) : null, {
    status, headers: has ? { "Content-Type": "application/json" } : {},
  }));
}

const seed = (mustChange: boolean) =>
  useAuth.setState({
    session: { principalId: "u1", username: "admin", displayName: "admin", csrfToken: "tok", mustChangePassword: mustChange },
    status: "authed",
  });

async function fill(current: string, next: string, confirm: string) {
  await userEvent.type(screen.getByLabelText("Current password"), current);
  await userEvent.type(screen.getByLabelText("New password"), next);
  await userEvent.type(screen.getByLabelText("Confirm new password"), confirm);
}

describe("ChangePasswordForm", () => {
  beforeEach(() => {
    setCsrfToken("tok");
    seed(true);
    vi.unstubAllGlobals();
  });

  it("changes the password (CSRF + body) and clears the default-password nudge", async () => {
    const f = mockFetch(204, undefined);
    vi.stubGlobal("fetch", f);
    render(<ChangePasswordForm />);
    await fill("changeme123", "newsecret1", "newsecret1");
    await userEvent.click(screen.getByRole("button", { name: /change password/i }));

    await waitFor(() => expect(f).toHaveBeenCalled());
    const [url, init] = f.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/v1/auth/password");
    expect(JSON.parse(init.body as string)).toEqual({ currentPassword: "changeme123", newPassword: "newsecret1" });
    expect((init.headers as Record<string, string>)["X-CSRF-Token"]).toBe("tok");
    await waitFor(() => expect(useAuth.getState().session?.mustChangePassword).toBe(false));
  });

  it("shows an error on a wrong current password (401) and keeps the nudge", async () => {
    vi.stubGlobal("fetch", mockFetch(401, { error: "current password is incorrect" }));
    render(<ChangePasswordForm />);
    await fill("wrong", "newsecret1", "newsecret1");
    await userEvent.click(screen.getByRole("button", { name: /change password/i }));

    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent(/incorrect/i));
    expect(useAuth.getState().session?.mustChangePassword).toBe(true);
  });

  it("keeps submit disabled until the new password is long enough and matches", async () => {
    vi.stubGlobal("fetch", mockFetch(204, undefined));
    render(<ChangePasswordForm />);
    const btn = screen.getByRole("button", { name: /change password/i });
    expect(btn).toBeDisabled();
    await fill("x", "short", "short"); // < 8 chars
    expect(btn).toBeDisabled();
  });
});
