// Hand-mirrored from Go module `agentmon/shared`. Keep names/shapes in sync.
export type SessionState = "blocked" | "done" | "working" | "idle" | "unknown";
export interface Pane { id: string; command: string; cwd: string; }
export interface Window { id: string; index: string; name: string; panes: Pane[]; }
export interface Session {
  name: string; server: string; target: string;
  cwd: string; command: string; windows: Window[];
  state?: SessionState;
}
// WS control frames (client → hub). Terminal data uses binary frames, not these.
export interface ResizeFrame { type: "resize"; cols: number; rows: number; }
export interface ErrorFrame { type: "error"; code: string; message: string; }
export interface ReconnectFrame { type: "reconnect"; status: string; }
// Mirrors hubd registry.ServerSummary (browser-safe; no secrets). `state` is the
// hub's server rollup (mirrors the wire); the desktop sidebar uses it to ORDER
// session-less server groups blocked-first and, when it is known (≠ unknown), as
// that group's only header dot — servers with sessions render no header dot.
export interface ServerSummary { id: string; name: string; labels: string[]; enabled: boolean; state?: SessionState; }
// Mirrors the hub's login/me JSON body.
export interface SessionInfo { principalId: string; username: string; displayName: string; csrfToken: string; mustChangePassword?: boolean; }
// POST /api/v1/auth/password body.
export interface ChangePasswordRequest { currentPassword: string; newPassword: string; }
// SSE snapshot-entry + delta shape (mirrors hubd api.stateEvent).
export interface StateEventFrame { server: string; target: string; session: string; state: SessionState; }
// POST /api/v1/seen body (mirrors hubd api.seenRequest).
export interface SeenRequest { serverId: string; target: string; sessionName: string; }
// POST /api/v1/servers/{id}/sessions body (mirrors shared.CreateSessionRequest).
// Custom commands are accepted end-to-end. The response is a full Session.
export interface CreateSessionRequest { name: string; cwd?: string; command?: string; }
// POST /api/v1/servers/{id}/sessions/rename body (mirrors shared.RenameSessionRequest).
// `to` is validated by the same name rule as create. The response is the renamed Session.
export interface RenameSessionRequest { from: string; to: string; }
// GET /api/v1/servers/pending item (mirrors registry.PendingServer) — an agent
// awaiting admission. No secrets; enough to verify it before approving.
export interface PendingServer { id: string; hostname: string; url: string; os?: string; arch?: string; }
// POST /api/v1/push/subscribe body (mirrors the browser PushSubscription.toJSON shape
// the hub's api.push decodes). The endpoint is the natural unique key (server PK).
export interface PushSubscriptionJSON { endpoint: string; keys: { p256dh: string; auth: string }; }
// GET /api/v1/push/vapid response (mirrors hubd api.VapidHandler). The VAPID public
// key is non-secret; the client feeds it to pushManager.subscribe.
export interface VapidKeyResponse { publicKey: string; }

// ---- Orchestrator board (sub-3) ----
export type EpicStage =
  | "queued" | "starting" | "planning" | "implementing" | "reviewing"
  | "pr_open" | "merging" | "merged" | "escalated" | "stalled" | "failed" | "canceled";

// One platform-invariant requirement (mirrors Go db.Requirement). `id` is a
// stable kebab slug the epic-02 gate matches on; `text` doubles as the review
// lens; `check_cmd` optionally certifies it via a shell exit code.
export interface Requirement { id: string; text: string; check_cmd?: string; }

export interface ProjectDTO {
  id: string; name: string; repo: string; server_id: string; target: string;
  workdir: string; base_branch: string; provider: string;
  required_reviews: string[] | null; max_parallel: number; paused: boolean;
  require_ci: boolean; pinned: boolean; requirements: Requirement[];
  counts?: Record<string, number>;
}

export interface EpicDTO {
  id: string; project_id: string; issue: number; title: string;
  labels: string[] | null; blocked_by: number[] | null; stage: EpicStage;
  attempt: number; session: string; branch: string; pr: number;
  verdict?: string; needs: string; issue_state: string;
  queued_at: string; started_at: string; stage_updated_at: string; merged_at: string;
  usage?: UsageRollup;
}

export interface EpicEventDTO { from: string; to: string; source: string; note: string; ts: string; }

export interface AllBoardResponse { orchestrator_enabled: boolean; projects: ProjectDTO[]; epics: EpicDTO[]; }
export interface ProjectBoardResponse { project: ProjectDTO; epics: EpicDTO[]; events: Record<string, EpicEventDTO[]>; }
export interface EpicPlanResponse { path: string; ref: string; markdown: string; }
export interface EpicArtifactResponse { path: string; ref: string; markdown: string; }

// Usage tracking DTO family (mirrors Go shared.Usage* types)
export interface TokenTotals { input: number; output: number; cache_read: number; cache_write: number; total: number; }
export interface ModelUsage { provider: string; model: string; tokens: TokenTotals; cost: number | null; }
export interface UsageStage { stage: string; duration_ms: number; tokens: TokenTotals; cost: number | null; by_model: ModelUsage[]; }
export interface UsageAttempt { attempt: number; outcome: string; duration_ms: number; tokens: TokenTotals; cost: number | null; is_lower_bound: boolean; stages: UsageStage[]; }
export interface EpicUsage { tokens: TokenTotals; cost: number | null; duration_ms: number; by_model: ModelUsage[]; attempts: UsageAttempt[]; }
export interface ProjectStageUsage { stage: string; tokens: TokenTotals; cost: number | null; duration_ms: number; }
export interface ProjectUsage { tokens: TokenTotals; cost: number | null; duration_ms: number; by_stage: ProjectStageUsage[]; by_model: ModelUsage[]; }
// Inline light rollup carried on board epic/project DTOs (omitted when absent):
export interface UsageRollup { tokens: number; cost: number | null; duration_ms: number; }

// SSE `board` delta — hubd/internal/api/orchestrator_events.go:74
export interface BoardDeltaFrame {
  project_id: string; epic_id: string; issue: number; stage: EpicStage; needs: string; title: string;
}

export interface ProjectCreateRequest {
  name: string; repo: string; server_id: string; target?: string; workdir: string;
  base_branch?: string; provider?: string; required_reviews?: string[];
  requirements?: Requirement[]; max_parallel?: number; require_ci?: boolean;
}
export interface ProjectPatchRequest {
  name?: string; workdir?: string; target?: string; base_branch?: string;
  provider?: string; required_reviews?: string[]; requirements?: Requirement[];
}
export interface EpicActionRequest {
  action: string; epic_id?: string; issue?: number; value?: number; on?: boolean; text?: string;
}
