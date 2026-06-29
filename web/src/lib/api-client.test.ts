import { describe, it, expect, vi, beforeEach } from "vitest";
import { login, logout, me, listServers, listSessions, setCsrfToken, ApiError } from "@/lib/api-client";

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
});
