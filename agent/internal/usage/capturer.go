package usage

import (
	"context"
	"os"
	"time"

	"agentmon/shared"
)

// NewCapturer builds a report.UsageCapturer (spelled out structurally here,
// not by name, so this package need not import report). paneInfo returns
// (pid, cwd, command, sinceCreated) for a pane; production binds
// tmux.PaneInfo. Roots are resolved once at construction from $HOME.
//
// since=sinceCreated, NOT time.Time{}/epoch, bounds child-transcript/rollout
// enumeration to the CURRENT session's lifetime. Retries reuse the same
// worktree (the orchestrator's runner is "attempt-agnostic"), so an epoch
// since would pull a PRIOR attempt's codex rollout / Claude transcript out of
// that shared worktree into THIS attempt's usage and overcount it. A retry's
// session is always created later than the attempt it replaces, so
// sinceCreated (tmux's session_created) is a safe, tight lower bound. The
// parent transcript stays fd-bound (openTranscriptFDs) regardless of since —
// it is pinned to the exact pid tmux reports right now, not to a time window.
//
// v1 limitation: under max_parallel>1, sibling subagents that share a project
// cwd cannot be told apart by directory scoping alone — only the fd-bound
// parent transcript is exact. Accepted for v1.
func NewCapturer(paneInfo func(ctx context.Context, socket, pane string) (pid int, cwd, command string, since time.Time, err error)) func(context.Context, string, string) []shared.Usage {
	claudeRoot := os.ExpandEnv("$HOME/.claude/projects")
	codexRoot := os.ExpandEnv("$HOME/.codex/sessions")
	return func(ctx context.Context, socket, pane string) []shared.Usage {
		pid, cwd, _, since, err := paneInfo(ctx, socket, pane)
		if err != nil {
			return nil
		}
		var s Sources
		// Parent transcript: bound to the runner process's open fd (session-safe
		// even when concurrent attempts share a project dir).
		for _, f := range openTranscriptFDs(pid) {
			if isCodexPath(f, codexRoot) {
				s.Codex = append(s.Codex, f)
			} else {
				s.Claude = append(s.Claude, f)
				// /multi-review lens subagent transcripts live nested under
				// this parent session's own directory — bind them to THIS
				// parent (not a directory-wide glob) to avoid cross-session
				// contamination when concurrent attempts share a project cwd.
				s.Claude = append(s.Claude, subagentTranscripts(f)...)
			}
		}
		// Child sessions in the worktree (codex exec, subagents), scoped to
		// this session's lifetime — see since= discussion above.
		s.Codex = append(s.Codex, enumerateChildRollouts(codexRoot, cwd, since)...)
		s.Claude = append(s.Claude, enumerateChildTranscripts(claudeRoot, cwd, since)...)
		return Aggregate(s)
	}
}
