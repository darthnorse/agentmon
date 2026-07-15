import type {
  ServerSummary, SessionInfo, Session, SeenRequest,
  PushSubscriptionJSON, VapidKeyResponse, CreateSessionRequest, PendingServer, ChangePasswordRequest,
  AllBoardResponse, ProjectBoardResponse, EpicPlanResponse, EpicArtifactResponse, EpicActionRequest,
  ProjectCreateRequest, ProjectPatchRequest, ProjectDTO, EpicUsage, ProjectUsage,
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

// Change the logged-in user's password (auto-CSRF). 401 if the current is wrong.
export const changePassword = (currentPassword: string, newPassword: string) =>
  request<void>("POST", "/auth/password", { currentPassword, newPassword } satisfies ChangePasswordRequest);

export const me = () => request<SessionInfo>("GET", "/me");

export const listServers = () => request<ServerSummary[]>("GET", "/servers");

export const listSessions = (serverId: string, target?: string) =>
  request<Session[]>(
    "GET",
    `/servers/${encodeURIComponent(serverId)}/sessions` +
      (target ? `?target=${encodeURIComponent(target)}` : ""),
  );

export const postSeen = (req: SeenRequest) => request<void>("POST", "/seen", req);

// React Query cache keys for the server/session lists. Every fetch AND invalidation
// (inbox, mobile terminal header, rename, kill, pending-approval) addresses the cache
// through these, so the keys are the single source of truth — a hand-written key that
// drifted from the fetch key would silently break cache sharing or no-op an invalidate.
export const serversKey = () => ["servers"] as const;
export const sessionsKey = (serverId: string) => ["sessions", serverId] as const;

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

// Kill (terminate) a tmux session. Irreversible; the caller confirms first.
// Auto-CSRF (mutating). 404 (already gone) is treated as success by the caller.
export const killSession = (serverId: string, name: string, target?: string) =>
  request<void>(
    "POST",
    `/servers/${encodeURIComponent(serverId)}/sessions/kill` +
      (target ? `?target=${encodeURIComponent(target)}` : ""),
    { name },
  );

// Admit UI: list agents awaiting admission, then approve (→ active) or reject
// (remove the pending enrollment). approve/reject are mutating (auto-CSRF).
export const listPending = () => request<PendingServer[]>("GET", "/servers/pending");

export const approveServer = (id: string) =>
  request<void>("POST", `/servers/${encodeURIComponent(id)}/approve`);

export const rejectServer = (id: string) =>
  request<void>("POST", `/servers/${encodeURIComponent(id)}/reject`);

// Web-Push (M9). VAPID public key is non-secret; subscribe/unsubscribe are mutating
// (auto-CSRF). Unsubscribe sends only the endpoint (the server's PK).
export const getVapidPublicKey = () => request<VapidKeyResponse>("GET", "/push/vapid");

export const subscribePush = (sub: PushSubscriptionJSON) =>
  request<void>("POST", "/push/subscribe", sub);

export const unsubscribePush = (endpoint: string) =>
  request<void>("POST", "/push/unsubscribe", { endpoint });

// ---- Orchestrator board (sub-3) ----
// All board query keys share the ["board", …] prefix: one
// invalidateQueries({ queryKey: ["board"] }) refreshes every board view.
export const allBoardKey = () => ["board"] as const;
export const projectBoardKey = (projectId: string) => ["board", projectId] as const;
export const epicPlanKey = (projectId: string, epicId: string) => ["epic-plan", projectId, epicId] as const;
export const epicArtifactKey = (projectId: string, epicId: string, path: string) =>
  ["epic-artifact", projectId, epicId, path] as const;
export const epicUsageKey = (projectId: string, epicId: string) => ["epic-usage", projectId, epicId] as const;
export const projectUsageKey = (projectId: string) => ["project-usage", projectId] as const;
// A runner session lives under the project's TARGET socket. Key by target so
// same-host projects on different targets don't collide; an empty target
// reuses the home screen's sessionsKey (identical default-target list).
export const boardSessionsKey = (serverId: string, target: string) =>
  target ? (["sessions", serverId, target] as const) : sessionsKey(serverId);

export const getAllBoard = () => request<AllBoardResponse>("GET", "/orchestrator/board");
export const getProjectBoard = (projectId: string) =>
  request<ProjectBoardResponse>("GET", `/orchestrator/projects/${encodeURIComponent(projectId)}/board`);
export const getEpicPlan = (projectId: string, epicId: string) =>
  request<EpicPlanResponse>(
    "GET",
    `/orchestrator/projects/${encodeURIComponent(projectId)}/epics/${encodeURIComponent(epicId)}/plan`,
  );
export const getEpicArtifact = (projectId: string, epicId: string, path: string) =>
  request<EpicArtifactResponse>(
    "GET",
    `/orchestrator/projects/${encodeURIComponent(projectId)}/epics/${encodeURIComponent(epicId)}/artifact?path=${encodeURIComponent(path)}`,
  );
export const getEpicUsage = (projectId: string, epicId: string) =>
  request<EpicUsage>(
    "GET",
    `/orchestrator/projects/${encodeURIComponent(projectId)}/epics/${encodeURIComponent(epicId)}/usage`,
  );
export const getProjectUsage = (projectId: string) =>
  request<ProjectUsage>("GET", `/orchestrator/projects/${encodeURIComponent(projectId)}/usage`);
export const epicAction = (projectId: string, body: EpicActionRequest) =>
  request<{ ok: boolean }>("POST", `/orchestrator/projects/${encodeURIComponent(projectId)}/actions`, body);
export const createProject = (body: ProjectCreateRequest) =>
  request<ProjectDTO>("POST", "/orchestrator/projects", body);
export const patchProject = (projectId: string, body: ProjectPatchRequest) =>
  request<ProjectDTO>("PATCH", `/orchestrator/projects/${encodeURIComponent(projectId)}`, body);
export const deleteProject = (projectId: string) =>
  request<{ ok: boolean }>("DELETE", `/orchestrator/projects/${encodeURIComponent(projectId)}`);
