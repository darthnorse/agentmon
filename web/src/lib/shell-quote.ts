// POSIX single-quote a string so it survives `sh -c` intact (the agent runs the
// session command via tmux → sh -c). Nothing expands inside single quotes; an
// embedded ' is closed, escaped as \', and reopened.
export function shSingleQuote(s: string): string {
  return `'${s.replace(/'/g, `'\\''`)}'`;
}

// Build the /plan-epics launch command. Empty vibe keeps today's bare form
// (unchanged behavior); a vibe is seeded as $ARGUMENTS, shell-safe quoted.
export function planCommand(provider: "claude" | "codex", vibe: string): string {
  const v = vibe.trim();
  const prefix = provider === "codex"
    ? "codex -a never"
    : "IS_SANDBOX=1 claude --dangerously-skip-permissions";
  // Empty vibe keeps today's bare double-quoted form; a vibe is single-quoted.
  const arg = v ? shSingleQuote(`/plan-epics ${v}`) : `"/plan-epics"`;
  return `${prefix} ${arg}`;
}
