export type Provider = "claude" | "codex";

// pane_current_command is the pane's foreground process NAME. Fleet convention
// (README, hooks section): Claude Code and Codex are native installs, so the
// process is literally `claude` / `codex`. A wrapper install (npm → `node`)
// gets no tag — never a wrong one. Exact match, deliberately no heuristics.
export function providerOf(command: string | undefined): Provider | undefined {
  return command === "claude" || command === "codex" ? command : undefined;
}
