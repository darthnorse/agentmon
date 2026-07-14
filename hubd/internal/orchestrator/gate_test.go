package orchestrator

import (
	"strings"
	"testing"

	"agentmon/hubd/internal/db"
)

func cleanVerdict() *Verdict {
	return &Verdict{Schema: "v1", Epic: 15,
		Reviews: []string{"specialist", "simplifier", "deep-scan", "codex"},
		Tests:   VerdictTests{Passed: 10}}
}

func TestDecide(t *testing.T) {
	req := []string{"specialist", "codex"}
	cases := []struct {
		name   string
		in     GateInput
		merge  bool
		wait   bool
		reason string
	}{
		{"clean merges", GateInput{Verdict: cleanVerdict(), RequiredReviews: req, ChecksGreen: true}, true, false, ""},
		{"pending waits", GateInput{Verdict: cleanVerdict(), RequiredReviews: req, ChecksPending: true}, false, true, "pending"},
		{"pr-gate escalates", GateInput{Verdict: cleanVerdict(), Labels: []string{"pr-gate"}, ChecksGreen: true}, false, false, "pr-gate"},
		{"nil verdict escalates", GateInput{Verdict: nil, ChecksGreen: true}, false, false, "verdict"},
		{"verdict err escalates", GateInput{Verdict: cleanVerdict(), VerdictErr: ErrNoVerdict, ChecksGreen: true}, false, false, "verdict"},
		{"red checks escalate", GateInput{Verdict: cleanVerdict(), ChecksGreen: false}, false, false, "CI"},
		{"uncertain escalates", GateInput{Verdict: func() *Verdict { v := cleanVerdict(); v.Uncertain = true; return v }(), ChecksGreen: true}, false, false, "uncertain"},
		{"unresolved escalates", GateInput{Verdict: func() *Verdict { v := cleanVerdict(); v.Findings.Unresolved = 2; return v }(), ChecksGreen: true}, false, false, "unresolved"},
		{"failed tests escalate", GateInput{Verdict: func() *Verdict { v := cleanVerdict(); v.Tests.Failed = 1; return v }(), ChecksGreen: true}, false, false, "tests"},
		{"missing review escalates", GateInput{Verdict: func() *Verdict { v := cleanVerdict(); v.Reviews = []string{"specialist"}; return v }(), RequiredReviews: req, ChecksGreen: true}, false, false, "required reviews"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Decide(c.in)
			if got.Merge != c.merge || got.Wait != c.wait {
				t.Fatalf("got %+v", got)
			}
			if c.reason != "" && !strings.Contains(got.Reason, c.reason) {
				t.Fatalf("reason %q missing %q", got.Reason, c.reason)
			}
		})
	}
}

func TestDecideBindsVerdictToEpic(t *testing.T) {
	v := cleanVerdict() // Epic: 15
	got := Decide(GateInput{Verdict: v, Epic: 16, ChecksGreen: true})
	if got.Merge || got.Wait || !strings.Contains(got.Reason, "epic 15 != issue 16") {
		t.Fatalf("foreign-epic verdict must escalate, got %+v", got)
	}
	if got := Decide(GateInput{Verdict: v, Epic: 15, ChecksGreen: true}); !got.Merge {
		t.Fatalf("matching epic must still merge, got %+v", got)
	}
}

func TestDecideRequirements(t *testing.T) {
	reqs := []db.Requirement{{ID: "always-use-rls", Text: "Always use RLS"}, {ID: "wcag", Text: "WCAG 2.2 AA"}}
	withReqs := func(rs ...VerdictRequirement) *Verdict {
		v := cleanVerdict()
		v.Requirements = rs
		return v
	}
	allMet := []VerdictRequirement{
		{ID: "always-use-rls", Status: "met", Via: "cmd"},
		{ID: "wcag", Status: "met", Via: "review"},
	}
	cases := []struct {
		name   string
		in     GateInput
		merge  bool
		wait   bool
		reason string
	}{
		{"all met merges", GateInput{Verdict: withReqs(allMet...), Requirements: reqs, ChecksGreen: true}, true, false, ""},
		{"no platform reqs unchanged", GateInput{Verdict: cleanVerdict(), Requirements: nil, ChecksGreen: true}, true, false, ""},
		{"one unmet escalates", GateInput{Verdict: withReqs(
			VerdictRequirement{ID: "always-use-rls", Status: "met", Via: "cmd"},
			VerdictRequirement{ID: "wcag", Status: "unmet", Via: "review"}),
			Requirements: reqs, ChecksGreen: true}, false, false, "wcag (unmet)"},
		{"uncertain escalates", GateInput{Verdict: withReqs(
			VerdictRequirement{ID: "always-use-rls", Status: "uncertain", Via: "cmd"},
			VerdictRequirement{ID: "wcag", Status: "met", Via: "review"}),
			Requirements: reqs, ChecksGreen: true}, false, false, "always-use-rls (uncertain)"},
		{"absent from verdict escalates", GateInput{Verdict: withReqs(
			VerdictRequirement{ID: "always-use-rls", Status: "met", Via: "cmd"}),
			Requirements: reqs, ChecksGreen: true}, false, false, "wcag (missing)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Decide(c.in)
			if got.Merge != c.merge || got.Wait != c.wait {
				t.Fatalf("merge/wait = %v/%v, got %+v", c.merge, c.wait, got)
			}
			if c.reason != "" && !strings.Contains(got.Reason, c.reason) {
				t.Fatalf("reason %q missing %q", got.Reason, c.reason)
			}
		})
	}
}
