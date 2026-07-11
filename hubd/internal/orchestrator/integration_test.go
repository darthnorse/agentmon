package orchestrator

import (
	"context"
	"testing"

	"agentmon/hubd/internal/github"
	"agentmon/shared"
)

func TestTwoEpicChainEndToEnd(t *testing.T) {
	cleanVerdict := "```yaml\nagentmon-verdict: v1\nepic: 1\nreviews: [codex]\n" +
		"findings: {found: 0, resolved: 0, unresolved: 0}\ntests: {passed: 1, failed: 0}\n" +
		"uncertain: false\nlearnings_updated: true\n```"
	gh := &fakeGH{issues: map[int]github.Issue{1: {Number: 1, Title: "scaffold", State: "open", Labels: []string{"agentmon:epic"}}, 2: {Number: 2, Title: "auth", State: "open", Labels: []string{"agentmon:epic"}, Body: "Blocked by #1"}}, prs: map[int]github.PullRequest{}, checks: map[string][]github.CheckRun{}}
	ag := &fakeAgents{sessions: []string{sessionName(1), sessionName(2)}}
	o, d := newTestOrch(t, gh, ag)
	ctx := context.Background()
	o.Tick(ctx)
	if len(ag.created) != 1 || ag.created[0].Name != sessionName(1) {
		t.Fatalf("created = %+v", ag.created)
	}
	e2, _ := d.GetEpicByIssue(ctx, "p1", 2)
	if e2.Stage != "queued" {
		t.Fatalf("epic 2 = %+v", e2)
	}
	gh.prs[10] = github.PullRequest{Number: 10, State: "open", Body: cleanVerdict, HeadSHA: "s1", HeadRef: "epic/1-scaffold"}
	ag.reports = []shared.OrchestratorReport{{Repo: "o/r", Epic: 1, Stage: shared.EpicPROpen, PR: 10, Session: sessionName(1), Ts: "t"}}
	o.Tick(ctx)
	e1, _ := d.GetEpicByIssue(ctx, "p1", 1)
	if e1.Stage != "merged" {
		t.Fatalf("epic 1 = %+v", e1)
	}
	o.Tick(ctx)
	if len(ag.created) != 2 || ag.created[1].Name != sessionName(2) {
		t.Fatalf("created = %+v", ag.created)
	}
}
