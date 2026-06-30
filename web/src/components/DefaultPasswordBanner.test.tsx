import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { DefaultPasswordBanner } from "@/components/DefaultPasswordBanner";
import { useAuth } from "@/store/auth";

const session = (mustChange: boolean) => ({
  principalId: "u1", username: "admin", displayName: "admin", csrfToken: "tok", mustChangePassword: mustChange,
});

describe("DefaultPasswordBanner", () => {
  it("warns while the default password is in use", () => {
    useAuth.setState({ session: session(true), status: "authed" });
    render(<DefaultPasswordBanner />);
    expect(screen.getByRole("region", { name: /default password/i })).toBeInTheDocument();
  });

  it("renders nothing once the password is no longer the default", () => {
    useAuth.setState({ session: session(false), status: "authed" });
    const { container } = render(<DefaultPasswordBanner />);
    expect(container.firstChild).toBeNull();
  });
});
