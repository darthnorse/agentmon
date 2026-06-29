import { describe, it, expect, vi, beforeEach } from "vitest";

vi.mock("@/lib/api-client", () => ({
  login: vi.fn(),
  logout: vi.fn(),
  me: vi.fn(),
  setCsrfToken: vi.fn(),
}));

vi.mock("@/lib/push", () => ({ disablePush: vi.fn() }));

import { useAuth } from "@/store/auth";
import { usePanes } from "@/store/panes";
import { useSessionState } from "@/store/session-state";
import * as api from "@/lib/api-client";
import * as push from "@/lib/push";

const info = { principalId: "p", username: "u", displayName: "U", csrfToken: "tok" };

describe("auth store", () => {
  beforeEach(() => {
    useAuth.getState().clear();
    vi.clearAllMocks();
    delete (navigator as any).serviceWorker;
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

  it("clear resets the panes grid", () => {
    usePanes.setState({
      panes: [{ id: "s:default:a:%0", serverId: "s", paneId: "%0", target: "default", session: "a", serverName: "h" }],
      focusedId: "s:default:a:%0",
    });
    useAuth.getState().clear();
    expect(usePanes.getState().panes).toHaveLength(0);
    expect(usePanes.getState().focusedId).toBeNull();
  });

  it("signOut resets the panes grid", async () => {
    (api.logout as any).mockResolvedValue(undefined);
    usePanes.setState({
      panes: [{ id: "s:default:a:%0", serverId: "s", paneId: "%0", target: "default", session: "a", serverName: "h" }],
      focusedId: null,
    });
    await useAuth.getState().signOut();
    expect(usePanes.getState().panes).toHaveLength(0);
  });

  it("clears the live-state store on sign-out", () => {
    useSessionState.getState().applySnapshot([{ server: "s", target: "t", session: "a", state: "blocked" }]);
    useAuth.getState().clear();
    expect(useSessionState.getState().live.size).toBe(0);
  });

  it("signOut best-effort unsubscribes push, and a throw never blocks logout", async () => {
    (api.logout as any).mockResolvedValue(undefined);
    const fakeReg = {} as ServiceWorkerRegistration;
    (navigator as any).serviceWorker = { getRegistration: () => Promise.resolve(fakeReg) };
    // even if push teardown rejects, sign-out must still resolve + clear
    (push.disablePush as any).mockRejectedValue(new Error("push teardown failed"));
    useAuth.getState().setSession(info);

    await expect(useAuth.getState().signOut()).resolves.toBeUndefined();

    expect(push.disablePush).toHaveBeenCalledWith(fakeReg);
    expect(useAuth.getState().status).toBe("anon");
    expect(useAuth.getState().session).toBeNull();
  });

  it("signOut does not hang when the service worker never activates", async () => {
    (api.logout as any).mockResolvedValue(undefined);
    // `.ready` never resolves (SW failed to activate); the fix must NOT await it.
    // getRegistration() resolves promptly to undefined → push teardown is skipped.
    (navigator as any).serviceWorker = {
      ready: new Promise(() => {}),
      getRegistration: () => Promise.resolve(undefined),
    };
    useAuth.getState().setSession(info);
    await expect(useAuth.getState().signOut()).resolves.toBeUndefined();
    expect(push.disablePush).not.toHaveBeenCalled();
    expect(useAuth.getState().status).toBe("anon");
  });

  it("signOut skips push teardown when serviceWorker is unavailable", async () => {
    (api.logout as any).mockResolvedValue(undefined);
    useAuth.getState().setSession(info);
    await useAuth.getState().signOut();
    expect(push.disablePush).not.toHaveBeenCalled();
    expect(useAuth.getState().status).toBe("anon");
  });

  it("signOut resolves and clears even when logout() rejects", async () => {
    (api.logout as any).mockRejectedValue(new Error("network down"));
    useAuth.getState().setSession(info);
    expect(useAuth.getState().status).toBe("authed");
    // must NOT reject — signOut swallows the logout error
    await expect(useAuth.getState().signOut()).resolves.toBeUndefined();
    // and must still clear locally
    expect(useAuth.getState().status).toBe("anon");
    expect(useAuth.getState().session).toBeNull();
    expect(api.setCsrfToken).toHaveBeenLastCalledWith("");
  });
});
