package orchestrator

import (
	"fmt"
	"sort"

	"agentmon/hubd/internal/db"
	"agentmon/shared"
)

// ReadyEpics computes which queued epics may start now. Pure function; deps
// are resolved against the passed slice (one project's epics). A blocked_by
// issue with no epic row BLOCKS — fail closed, same philosophy as the gate.
func ReadyEpics(epics []db.Epic, maxParallel int, paused bool) []db.Epic {
	if paused {
		return nil
	}
	byIssue := map[int]db.Epic{}
	active := 0
	for _, e := range epics {
		byIssue[e.IssueNumber] = e
		if activeStages[shared.EpicStage(e.Stage)] {
			active++
		}
	}
	capacity := maxParallel - active
	if capacity <= 0 {
		return nil
	}
	var ready []db.Epic
	for _, e := range epics {
		if e.Stage != string(shared.EpicQueued) || e.IssueState != "open" {
			continue
		}
		if !depsSatisfied(e, byIssue) {
			continue
		}
		ready = append(ready, e)
	}
	sort.Slice(ready, func(i, j int) bool { return ready[i].IssueNumber < ready[j].IssueNumber })
	if len(ready) > capacity {
		ready = ready[:capacity]
	}
	return ready
}

func depsSatisfied(e db.Epic, byIssue map[int]db.Epic) bool {
	for _, dep := range e.BlockedBy {
		d, ok := byIssue[dep]
		if !ok {
			return false // unknown dep blocks
		}
		if d.Stage != string(shared.EpicMerged) && d.IssueState != "closed" {
			return false
		}
	}
	return true
}

// KickoffCommand is what the spawned tmux session runs (tmux executes it via
// `sh -c`, so the env prefix is fine). Runners MUST be autonomous — a
// permission prompt is a stalled epic: Claude needs IS_SANDBOX=1 (root host) +
// --dangerously-skip-permissions; Codex needs approval policy "never". The
// /epic-pipeline skill (sub-project 2) does the rest; exact codex invocation
// is validated there and only lives here.
func KickoffCommand(provider string, issue int) string {
	prompt := fmt.Sprintf("/epic-pipeline %d", issue)
	if provider == "codex" {
		return fmt.Sprintf(`codex -a never %q`, prompt)
	}
	return fmt.Sprintf(`IS_SANDBOX=1 claude --dangerously-skip-permissions %q`, prompt)
}

func SessionNameFor(issue int) string { return fmt.Sprintf("epic-%d", issue) }

// ProviderFor resolves the runner: per-epic agent:* label beats project default.
func ProviderFor(projectDefault string, labels []string) string {
	if hasLabel(labels, "agent:codex") {
		return "codex"
	}
	if hasLabel(labels, "agent:claude") {
		return "claude"
	}
	return projectDefault
}
