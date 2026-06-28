import { describe, it, expect, vi, beforeEach } from "vitest";

vi.mock("@/lib/api-client", () => ({
  login: vi.fn(),
  logout: vi.fn(),
  me: vi.fn(),
  setCsrfToken: vi.fn(),
}));

import { useAuth } from "@/store/auth";
import * as api from "@/lib/api-client";

const info = { principalId: "p", username: "u", displayName: "U", csrfToken: "tok" };

describe("auth store", () => {
  beforeEach(() => {
    useAuth.getState().clear();
    vi.clearAllMocks();
  });

  it("signIn stores the session and pushes the csrf token to the api client", async () => {
    (api.login as any).mockResolvedValue(info);
    await useAuth.getState().signIn("u", "pw");
    expect(useAuth.getState().status).toBe("authed");
    expect(useAuth.getState().session?.username).toBe("u");
    expect(api.setCsrfToken).toHaveBeenCalledWith("tok");
  });

  it("bootstrap → authed when me() resolves", async () => {
    (api.me as any).mockResolvedValue(info);
    await useAuth.getState().bootstrap();
    expect(useAuth.getState().status).toBe("authed");
  });

  it("bootstrap → anon when me() rejects", async () => {
    (api.me as any).mockRejectedValue(new Error("401"));
    await useAuth.getState().bootstrap();
    expect(useAuth.getState().status).toBe("anon");
    expect(useAuth.getState().session).toBeNull();
  });

  it("signOut clears and resets the csrf token", async () => {
    (api.logout as any).mockResolvedValue(undefined);
    useAuth.getState().setSession(info);
    await useAuth.getState().signOut();
    expect(useAuth.getState().status).toBe("anon");
    expect(api.setCsrfToken).toHaveBeenLastCalledWith("");
  });
});
