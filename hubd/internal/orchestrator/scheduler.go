package orchestrator

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strings"

	"agentmon/hubd/internal/db"
	"agentmon/shared"
)

// sessionStages are the stages during which a runner session is expected to
// be alive — the capacity unit per spec §5 ("running sessions < max_parallel").
// pr_open/merging are excluded: the runner exits after reporting pr_open
// (§7.7), so an epic waiting on CI must not block new spawns. Escalated is
// also excluded even though a plan-gate hold can keep its session alive —
// documented tradeoff until liveness-aware capacity arrives with the sub-2
// agent contract.
var sessionStages = map[shared.EpicStage]bool{
	shared.EpicStarting: true, shared.EpicPlanning: true,
	shared.EpicImplementing: true, shared.EpicReviewing: true,
}

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
		if sessionStages[shared.EpicStage(e.Stage)] {
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

// SessionNameFor is the tmux session name for one epic attempt. The project
// slug keeps names unique across projects sharing a host (tmux names are
// host-global); the attempt suffix keeps a retry from colliding with a
// still-alive previous session (tmux rejects duplicate names with 409).
func SessionNameFor(project string, issue, attempt int) string {
	name := fmt.Sprintf("epic-%s-%d", projectSlug(project), issue)
	if attempt > 1 {
		name += fmt.Sprintf("-r%d", attempt)
	}
	return name
}

// projectSlug reduces a project name to a short tmux-safe token. Project
// names are UNIQUE in the DB, but truncation could collide two long names —
// when the slug is lossy (truncated or emptied), a 4-hex hash of the full
// name keeps it collision-free.
func projectSlug(name string) string {
	var b []byte
	total := 0
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			total++
			if len(b) < 12 {
				b = append(b, byte(r))
			}
		}
	}
	if len(b) == 0 || total > len(b) {
		h := fnv.New32a()
		h.Write([]byte(name))
		return fmt.Sprintf("%s%04x", string(b), h.Sum32()&0xffff)
	}
	return string(b)
}

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
