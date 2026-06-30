import { describe, it, expect, vi, beforeEach } from "vitest";
import { login, logout, me, listServers, listSessions, renameSession, listPending, approveServer, rejectServer, changePassword, setCsrfToken, ApiError } from "@/lib/api-client";

function mockFetch(status: number, body: unknown) {
  // A 204/null-body status cannot carry a body in undici → pass null when no body.
  const hasBody = body !== undefined;
  return vi.fn(
    async () =>
      new Response(hasBody ? JSON.stringify(body) : null, {
        status,
        headers: hasBody ? { "Content-Type": "application/json" } : {},
      }),
  );
}

describe("api-client", () => {
  beforeEach(() => { setCsrfToken(""); });

  it("login POSTs credentials with same-origin and no CSRF header (token empty)", async () => {
    const f = mockFetch(200, { principalId: "p", username: "u", displayName: "U", csrfToken: "tok" });
    vi.stubGlobal("fetch", f);
    const info = await login("u", "pw");
    expect(info.csrfToken).toBe("tok");
    const [url, init] = f.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/v1/auth/login");
    expect(init.method).toBe("POST");
    expect(init.credentials).toBe("same-origin");
    expect(init.body).toBe(JSON.stringify({ username: "u", password: "pw" }));
    expect((init.headers as Record<string, string>)["X-CSRF-Token"]).toBeUndefined();
  });

  it("GET requests never carry a CSRF header", async () => {
    const f = mockFetch(200, []);
    vi.stubGlobal("fetch", f);
    setCsrfToken("tok");
    await listServers();
    const init = (f.mock.calls[0] as unknown as [string, RequestInit])[1];
    expect((init.headers as Record<string, string>)["X-CSRF-Token"]).toBeUndefined();
    expect(init.method).toBe("GET");
  });

  it("changePassword POSTs {currentPassword,newPassword} with CSRF", async () => {
    const f = mockFetch(204, undefined);
    vi.stubGlobal("fetch", f);
    setCsrfToken("tok");
    await changePassword("oldpw", "newpassword1");
    const [url, init] = f.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/v1/auth/password");
    expect(init.method).toBe("POST");
    expect(init.body).toBe(JSON.stringify({ currentPassword: "oldpw", newPassword: "newpassword1" }));
    expect((init.headers as Record<string, string>)["X-CSRF-Token"]).toBe("tok");
  });

  it("logout (mutation) sends X-CSRF-Token when a token is set", async () => {
    const f = mockFetch(204, undefined);
    vi.stubGlobal("fetch", f);
    setCsrfToken("tok");
    await logout();
    const [url, init] = f.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/v1/auth/logout");
    expect((init.headers as Record<string, string>)["X-CSRF-Token"]).toBe("tok");
  });

  it("listSessions escapes the target query", async () => {
    const f = mockFetch(200, []);
    vi.stubGlobal("fetch", f);
    await listSessions("srv 1", "t/x");
    expect((f.mock.calls[0] as unknown as [string, RequestInit])[0]).toBe("/api/v1/servers/srv%201/sessions?target=t%2Fx");
  });

  it("renameSession posts {from,to} with CSRF to the rename route", async () => {
    const f = mockFetch(201, { name: "newname", server: "s", target: "default", windows: [] });
    vi.stubGlobal("fetch", f);
    setCsrfToken("tok");
    const out = await renameSession("srv-1", "old", "newname", "default");
    expect(out.name).toBe("newname");
    const [url, init] = f.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/v1/servers/srv-1/sessions/rename?target=default");
    expect(init.method).toBe("POST");
    expect(init.body).toBe(JSON.stringify({ from: "old", to: "newname" }));
    expect((init.headers as Record<string, string>)["X-CSRF-Token"]).toBe("tok");
  });

  it("renameSession omits the target query when not given", async () => {
    const f = mockFetch(201, { name: "n", server: "s", target: "default", windows: [] });
    vi.stubGlobal("fetch", f);
    await renameSession("srv-1", "old", "n");
    expect((f.mock.calls[0] as unknown as [string, RequestInit])[0]).toBe("/api/v1/servers/srv-1/sessions/rename");
  });

  it("listPending GETs the pending route without CSRF", async () => {
    const f = mockFetch(200, [{ id: "web-01", hostname: "web-01", url: "http://x" }]);
    vi.stubGlobal("fetch", f);
    setCsrfToken("tok");
    const out = await listPending();
    expect(out[0].id).toBe("web-01");
    const [url, init] = f.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/v1/servers/pending");
    expect(init.method).toBe("GET");
    expect((init.headers as Record<string, string>)["X-CSRF-Token"]).toBeUndefined();
  });

  it("approveServer POSTs to the approve route with CSRF (escaped id)", async () => {
    const f = mockFetch(204, undefined);
    vi.stubGlobal("fetch", f);
    setCsrfToken("tok");
    await approveServer("web 01");
    const [url, init] = f.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/v1/servers/web%2001/approve");
    expect(init.method).toBe("POST");
    expect((init.headers as Record<string, string>)["X-CSRF-Token"]).toBe("tok");
  });

  it("rejectServer POSTs to the reject route", async () => {
    const f = mockFetch(204, undefined);
    vi.stubGlobal("fetch", f);
    await rejectServer("web-01");
    expect((f.mock.calls[0] as unknown as [string, RequestInit])[0]).toBe("/api/v1/servers/web-01/reject");
  });

  it("listSessions omits the query when no target", async () => {
    const f = mockFetch(200, []);
    vi.stubGlobal("fetch", f);
    await listSessions("s");
    expect((f.mock.calls[0] as unknown as [string, RequestInit])[0]).toBe("/api/v1/servers/s/sessions");
  });

  it("throws ApiError with the status and parsed message on non-2xx", async () => {
    vi.stubGlobal("fetch", mockFetch(401, { error: "invalid credentials" }));
    await expect(me()).rejects.toMatchObject({ status: 401, message: "invalid credentials" });
    expect((await me().catch((e) => e)) instanceof ApiError).toBe(true);
  });

  it("throws ApiError (not SyntaxError) when the error body is non-JSON HTML (e.g. proxy 502)", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response("<html>502 Bad Gateway</html>", { status: 502 })),
    );
    const err = await me().catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(502);
    // message must NOT be a SyntaxError blurb — it should be a plain string
    expect(typeof (err as ApiError).message).toBe("string");
    expect((err as ApiError).message).not.toMatch(/unexpected token/i);
  });

  it("postSeen POSTs the body with X-CSRF-Token", async () => {
    const f = mockFetch(204, undefined);
    vi.stubGlobal("fetch", f);
    setCsrfToken("tok");
    const { postSeen } = await import("@/lib/api-client");
    await postSeen({ serverId: "s", target: "default", sessionName: "x" });
    const [url, init] = f.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/v1/seen");
    expect(init.method).toBe("POST");
    expect(init.body).toBe(JSON.stringify({ serverId: "s", target: "default", sessionName: "x" }));
    expect((init.headers as Record<string, string>)["X-CSRF-Token"]).toBe("tok");
  });

  it("getVapidPublicKey GETs /push/vapid and returns the publicKey (no CSRF header)", async () => {
    const f = mockFetch(200, { publicKey: "BPubKey" });
    vi.stubGlobal("fetch", f);
    setCsrfToken("tok");
    const { getVapidPublicKey } = await import("@/lib/api-client");
    const res = await getVapidPublicKey();
    expect(res.publicKey).toBe("BPubKey");
    const [url, init] = f.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/v1/push/vapid");
    expect(init.method).toBe("GET");
    expect((init.headers as Record<string, string>)["X-CSRF-Token"]).toBeUndefined();
  });

  it("subscribePush POSTs the subscription body with X-CSRF-Token", async () => {
    const f = mockFetch(204, undefined);
    vi.stubGlobal("fetch", f);
    setCsrfToken("tok");
    const { subscribePush } = await import("@/lib/api-client");
    const sub = { endpoint: "https://push.example/abc", keys: { p256dh: "pk", auth: "au" } };
    await subscribePush(sub);
    const [url, init] = f.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/v1/push/subscribe");
    expect(init.method).toBe("POST");
    expect(init.body).toBe(JSON.stringify(sub));
    expect((init.headers as Record<string, string>)["X-CSRF-Token"]).toBe("tok");
  });

  it("unsubscribePush POSTs the endpoint with X-CSRF-Token", async () => {
    const f = mockFetch(204, undefined);
    vi.stubGlobal("fetch", f);
    setCsrfToken("tok");
    const { unsubscribePush } = await import("@/lib/api-client");
    await unsubscribePush("https://push.example/abc");
    const [url, init] = f.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/v1/push/unsubscribe");
    expect(init.method).toBe("POST");
    expect(init.body).toBe(JSON.stringify({ endpoint: "https://push.example/abc" }));
    expect((init.headers as Record<string, string>)["X-CSRF-Token"]).toBe("tok");
  });

  it("createSession POSTs the body to /servers/{id}/sessions with X-CSRF-Token and returns the Session", async () => {
    const session = {
      name: "dockmon", server: "s", target: "default", cwd: "/home", command: "",
      windows: [{ id: "@1", index: "0", name: "w", panes: [{ id: "%1", command: "bash", cwd: "/home" }] }],
    };
    const f = mockFetch(201, session);
    vi.stubGlobal("fetch", f);
    setCsrfToken("tok");
    const { createSession } = await import("@/lib/api-client");
    const res = await createSession("srv 1", { name: "dockmon", cwd: "/home" });
    expect(res.name).toBe("dockmon");
    const [url, init] = f.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/v1/servers/srv%201/sessions");
    expect(init.method).toBe("POST");
    expect(init.body).toBe(JSON.stringify({ name: "dockmon", cwd: "/home" }));
    expect((init.headers as Record<string, string>)["X-CSRF-Token"]).toBe("tok");
  });
});
