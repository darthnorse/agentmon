package orchestrator

import (
	"strings"
	"testing"
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
