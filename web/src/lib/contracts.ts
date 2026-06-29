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
// Mirrors hubd registry.ServerSummary (browser-safe; no secrets).
export interface ServerSummary { id: string; name: string; labels: string[]; enabled: boolean; state?: SessionState; }
// Mirrors the hub's login/me JSON body.
export interface SessionInfo { principalId: string; username: string; displayName: string; csrfToken: string; }
// SSE snapshot-entry + delta shape (mirrors hubd api.stateEvent).
export interface StateEventFrame { server: string; target: string; session: string; state: SessionState; }
// POST /api/v1/seen body (mirrors hubd api.seenRequest).
export interface SeenRequest { serverId: string; target: string; sessionName: string; }
