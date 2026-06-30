import type {
  ServerSummary, SessionInfo, Session, SeenRequest,
  PushSubscriptionJSON, VapidKeyResponse, CreateSessionRequest,
} from "@/lib/contracts";

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
  let data: unknown;
  try {
    data = text ? JSON.parse(text) : undefined;
  } catch {
    data = undefined; // non-JSON body (e.g. proxy HTML 502) — let the error path use statusText
  }
  if (!res.ok) {
    const errData = data as Record<string, unknown> | undefined;
    const msg = (errData && typeof errData.error === "string" && errData.error) || res.statusText || "request failed";
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

export const postSeen = (req: SeenRequest) => request<void>("POST", "/seen", req);

// Create a tmux session (M10). The hub re-lists after create and returns the full
// Session so the web can open the new terminal atomically. Auto-CSRF (mutating).
// An empty target lets the hub/agent resolve the default target (v1 single-target).
export const createSession = (serverId: string, body: CreateSessionRequest, target?: string) =>
  request<Session>(
    "POST",
    `/servers/${encodeURIComponent(serverId)}/sessions` +
      (target ? `?target=${encodeURIComponent(target)}` : ""),
    body,
  );

// Rename a tmux session (header inline-edit + the session-row action). The hub
// re-lists and returns the renamed Session. Auto-CSRF (mutating).
export const renameSession = (serverId: string, from: string, to: string, target?: string) =>
  request<Session>(
    "POST",
    `/servers/${encodeURIComponent(serverId)}/sessions/rename` +
      (target ? `?target=${encodeURIComponent(target)}` : ""),
    { from, to },
  );

// Web-Push (M9). VAPID public key is non-secret; subscribe/unsubscribe are mutating
// (auto-CSRF). Unsubscribe sends only the endpoint (the server's PK).
export const getVapidPublicKey = () => request<VapidKeyResponse>("GET", "/push/vapid");

export const subscribePush = (sub: PushSubscriptionJSON) =>
  request<void>("POST", "/push/subscribe", sub);

export const unsubscribePush = (endpoint: string) =>
  request<void>("POST", "/push/unsubscribe", { endpoint });
