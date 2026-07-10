package orchestrator

import (
	"fmt"
	"strings"
)

type GateInput struct {
	Verdict         *Verdict
	VerdictErr      error
	Labels          []string
	RequiredReviews []string
	ChecksGreen     bool
	ChecksPending   bool
}

// GateResult: Merge, Wait, or (neither) escalate-with-Reason.
type GateResult struct {
	Merge  bool
	Wait   bool
	Reason string
}

// Decide is the deterministic merge gate. It FAILS CLOSED: every ambiguous
// input escalates. The verdict is parsed data — a runner cannot argue past it.
func Decide(in GateInput) GateResult {
	if in.ChecksPending {
		return GateResult{Wait: true, Reason: "checks pending"}
	}
	if hasLabel(in.Labels, "pr-gate") {
		return GateResult{Reason: "pr-gate label: human merges"}
	}
	if in.VerdictErr != nil || in.Verdict == nil {
		return GateResult{Reason: "missing or malformed verdict"}
	}
	if !in.ChecksGreen {
		return GateResult{Reason: "CI checks failing"}
	}
	v := in.Verdict
	if v.Uncertain {
		return GateResult{Reason: "runner flagged uncertainty"}
	}
	if n := max(v.Findings.Unresolved, len(v.Unresolved)); n > 0 {
		return GateResult{Reason: fmt.Sprintf("%d unresolved review findings", n)}
	}
	if v.Tests.Failed > 0 {
		return GateResult{Reason: fmt.Sprintf("tests failing (%d)", v.Tests.Failed)}
	}
	if missing := missingReviews(in.RequiredReviews, v.Reviews); len(missing) > 0 {
		return GateResult{Reason: "missing required reviews: " + strings.Join(missing, ", ")}
	}
	return GateResult{Merge: true}
}

func hasLabel(labels []string, want string) bool {
	for _, l := range labels {
		if l == want {
			return true
		}
	}
	return false
}

func missingReviews(required, got []string) []string {
	have := map[string]bool{}
	for _, g := range got {
		have[g] = true
	}
	var missing []string
	for _, r := range required {
		if !have[r] {
			missing = append(missing, r)
		}
	}
	return missing
}
