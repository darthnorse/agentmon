// Hand-mirrored from Go module `agentmon/shared`. Keep names/shapes in sync.
export interface Pane { id: string; command: string; cwd: string; }
export interface Window { id: string; index: string; name: string; panes: Pane[]; }
export interface Session {
  name: string; server: string; target: string;
  cwd: string; command: string; windows: Window[];
}
// WS control frames (client → hub). Terminal data uses binary frames, not these.
export interface ResizeFrame { type: "resize"; cols: number; rows: number; }
export interface ErrorFrame { type: "error"; code: string; message: string; }
export interface ReconnectFrame { type: "reconnect"; status: string; }
