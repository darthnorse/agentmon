import { paneIdentity } from "@/lib/pane-identity";

export type Provider = "claude" | "codex";

// pane_current_command is the pane's foreground process NAME. Fleet convention
// (README, hooks section): Claude Code and Codex are native installs, so the
// process is literally `claude` / `codex`. A wrapper install (npm → `node`)
// gets no tag — never a wrong one. Exact match, deliberately no heuristics.
//
// Callers derive from the ROW'S OWN pane (`row.pane.command`), not
// `session.command`: the latter is tmux's *active* pane, which in a split can be
// a different pane than the one a surface displays — tagging the displayed pane
// with another pane's command would be a wrong tag.
export function providerOf(command: string | undefined): Provider | undefined {
  return command === "claude" || command === "codex" ? command : undefined;
}

// Provider per pane identity for a flattened session-row list (the grid's tag
// lookup). Entries exist only for known providers — an absent key means no tag.
// Structural row type so lib code does not import component modules.
export const providerByIdent = (
  rows: ReadonlyArray<{ server: { id: string }; session: { target: string }; pane: { id: string; command: string } }>,
): Map<string, Provider> => {
  const m = new Map<string, Provider>();
  for (const r of rows) {
    const p = providerOf(r.pane.command);
    if (p) m.set(paneIdentity(r.server.id, r.session.target, r.pane.id), p);
  }
  return m;
};
