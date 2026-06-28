import type { ServerSummary, SessionInfo, Session } from "@/lib/contracts";

const BASE = "/api/v1";

export class ApiError extends Error {
  constructor(public readonly status: number, message: string) {
    super(message);
    this.name = "ApiError";
  }
}

// The HttpOnly session cookie is unreadable to JS; the hub returns the CSRF token
// in the login/me body. We hold it here and attach it to mutating requests.
let csrfToken = "";
export function setCsrfToken(t: string): void { csrfToken = t; }
export function getCsrfToken(): string { return csrfToken; }

const MUTATING = new Set(["POST", "PUT", "PATCH", "DELETE"]);

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {};
  const init: RequestInit = { method, credentials: "same-origin", headers };
  if (body !== undefined) {
    headers["Content-Type"] = "application/json";
    init.body = JSON.stringify(body);
  }
  if (MUTATING.has(method) && csrfToken) headers["X-CSRF-Token"] = csrfToken;

  const res = await fetch(BASE + path, init);
  const text = await res.text();
  const data = text ? JSON.parse(text) : undefined;
  if (!res.ok) {
    const msg = (data && typeof data.error === "string" && data.error) || res.statusText || "request failed";
    throw new ApiError(res.status, msg);
  }
  return data as T;
}

export const login = (username: string, password: string) =>
  request<SessionInfo>("POST", "/auth/login", { username, password });

export const logout = () => request<void>("POST", "/auth/logout");

export const me = () => request<SessionInfo>("GET", "/me");

export const listServers = () => request<ServerSummary[]>("GET", "/servers");

export const listSessions = (serverId: string, target?: string) =>
  request<Session[]>(
    "GET",
    `/servers/${encodeURIComponent(serverId)}/sessions` +
      (target ? `?target=${encodeURIComponent(target)}` : ""),
  );
