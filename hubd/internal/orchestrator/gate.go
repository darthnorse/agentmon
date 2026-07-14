package orchestrator

import (
	"fmt"
	"slices"
	"strings"

	"agentmon/hubd/internal/db"
)

// PROVENANCE CONTRACT: the Verdict is the assigned runner's self-report,
// parsed from the PR body. The gate defends against a runner ARGUING past it
// (verdict is data, not argument) — it does NOT authenticate the author. The
// v1 threat model is private repos where PR bodies are editable only by the
// owner and the runners; callers assembling GateInput MUST source the PR via
// the epic's tracked PRNumber and pass Epic so a copied/foreign verdict
// escalates. Merges are additionally SHA-pinned at the client (MergePR), so
// code pushed after evaluation cannot ride a stale verdict in.
type GateInput struct {
	Verdict         *Verdict
	VerdictErr      error
	Epic            int // expected issue number; 0 skips the binding check
	Labels          []string
	RequiredReviews []string
	Requirements    []db.Requirement // project platform requirements; each must be reported `met`
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
	v := in.Verdict
	if in.Epic != 0 && v.Epic != in.Epic {
		return GateResult{Reason: fmt.Sprintf("verdict epic %d != issue %d", v.Epic, in.Epic)}
	}
	if !in.ChecksGreen {
		return GateResult{Reason: "CI checks failing"}
	}
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
	if unmet := unmetRequirements(in.Requirements, v.Requirements); len(unmet) > 0 {
		return GateResult{Reason: "platform requirements not met: " + strings.Join(unmet, ", ")}
	}
	return GateResult{Merge: true}
}

func hasLabel(labels []string, want string) bool {
	return slices.Contains(labels, want)
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

// unmetRequirements mirrors missingReviews for platform requirements: given the
// project's platform requirement set and the verdict's self-reported results, it
// returns one "id (category)" token for every requirement not present-and-`met`.
// A requirement the runner never reported (e.g. added to the project after its
// epics were imported) surfaces as "(missing)" — failing closed, the safe drift
// direction. Via is not consulted: a `met` is trusted regardless of how it was
// certified (v1 trust model). Inputs are pre-validated by ParseVerdict, so a
// present-not-met status is always "unmet" or "uncertain", surfaced verbatim.
func unmetRequirements(required []db.Requirement, reported []VerdictRequirement) []string {
	status := make(map[string]string, len(reported))
	for _, r := range reported {
		status[r.ID] = r.Status
	}
	var unmet []string
	for _, req := range required {
		switch s, ok := status[req.ID]; {
		case !ok:
			unmet = append(unmet, req.ID+" (missing)")
		case s != "met":
			unmet = append(unmet, req.ID+" ("+s+")")
		}
	}
	return unmet
}
