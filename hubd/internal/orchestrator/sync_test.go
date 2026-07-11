package orchestrator

import (
	"reflect"
	"testing"

	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/github"
)

func TestParseBlockedBy(t *testing.T) {
	cases := []struct {
		body string
		want []int
	}{
		{"Blocked by #13", []int{13}},
		{"blocked-by: #12, #14", []int{12, 14}},
		{"Blocked by #14 and blocked by #12\nBlocked by #14", []int{12, 14}},
		{"nothing here", nil},
		{"#7 mentioned but not a dep", nil},
	}
	for _, c := range cases {
		if got := ParseBlockedBy(c.body); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q → %v, want %v", c.body, got, c.want)
		}
	}
}

func TestIsOrchestratedIssue(t *testing.T) {
	if !IsOrchestratedIssue([]string{"agentmon:epic"}) || !IsOrchestratedIssue([]string{"bug", "agentmon:run"}) {
		t.Fatal("epic/run labels must qualify")
	}
	if IsOrchestratedIssue([]string{"bug"}) || IsOrchestratedIssue(nil) {
		t.Fatal("unlabeled issues must not qualify")
	}
}

func TestEpicFromIssue(t *testing.T) {
	p := db.Project{ID: "p1"}
	is := github.Issue{Number: 15, Title: "GDPR", Body: "Blocked by #13", State: "open",
		Labels: []string{"agentmon:epic", "pr-gate"}}
	e := EpicFromIssue(p, is, "t0")
	if e.ProjectID != "p1" || e.IssueNumber != 15 || e.IssueState != "open" ||
		e.QueuedAt != "t0" || e.StageUpdatedAt != "t0" {
		t.Fatalf("got %+v", e)
	}
	if len(e.BlockedBy) != 1 || e.BlockedBy[0] != 13 || len(e.Labels) != 2 {
		t.Fatalf("got %+v", e)
	}
}

func TestParseBlockedByIgnoresUnblocked(t *testing.T) {
	if got := ParseBlockedBy("this is now unblocked by #5"); got != nil {
		t.Fatalf("'unblocked by' must not register a dependency, got %v", got)
	}
	if got := ParseBlockedBy("Blocked by #5"); len(got) != 1 || got[0] != 5 {
		t.Fatalf("real blocked-by must still parse, got %v", got)
	}
}
