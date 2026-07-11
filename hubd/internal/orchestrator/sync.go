package orchestrator

import (
	"regexp"
	"sort"
	"strconv"

	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/github"
)

// blockedByRe matches the body convention: "Blocked by #13" / "Blocked-by: #12, #14".
// (GitHub's native issue-relationships API can replace this later; the body
// convention is the v1 contract the import script writes.)
// \b anchor: without it "unblocked by #5" registers a phantom dependency.
var blockedByRe = regexp.MustCompile(`(?i)\bblocked[ -]by:?\s*((?:#\d+[,\s]*)+)`)
var issueRefRe = regexp.MustCompile(`#(\d+)`)

func ParseBlockedBy(body string) []int {
	seen := map[int]bool{}
	for _, m := range blockedByRe.FindAllStringSubmatch(body, -1) {
		for _, ref := range issueRefRe.FindAllStringSubmatch(m[1], -1) {
			if n, err := strconv.Atoi(ref[1]); err == nil {
				seen[n] = true
			}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]int, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Ints(out)
	return out
}

// IsOrchestratedIssue: only labeled issues enter the mirror — the orchestrator
// never touches issues it wasn't pointed at.
func IsOrchestratedIssue(labels []string) bool {
	return hasLabel(labels, "agentmon:epic") || hasLabel(labels, "agentmon:run")
}

func EpicFromIssue(p db.Project, is github.Issue, now string) db.Epic {
	return db.Epic{
		ProjectID:      p.ID,
		IssueNumber:    is.Number,
		Title:          is.Title,
		Labels:         is.Labels,
		BlockedBy:      ParseBlockedBy(is.Body),
		IssueState:     is.State,
		QueuedAt:       now,
		StageUpdatedAt: now,
	}
}
