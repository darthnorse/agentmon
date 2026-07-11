package orchestrator

import (
	"testing"

	"agentmon/hubd/internal/db"
)

func qe(issue int, stage, issueState string, deps ...int) db.Epic {
	return db.Epic{ID: SessionNameFor("proj", issue, 1), IssueNumber: issue, Stage: stage,
		IssueState: issueState, BlockedBy: deps}
}

func TestReadyEpics(t *testing.T) {
	epics := []db.Epic{
		qe(12, "merged", "closed"),
		qe(14, "merged", "closed", 12),
		qe(15, "escalated", "open", 12),
		qe(16, "implementing", "open", 14),
		qe(17, "queued", "open", 16),     // dep active → not ready
		qe(18, "queued", "open", 14),     // dep merged → ready
		qe(19, "queued", "open", 14, 15), // 15 escalated → not ready
		qe(20, "queued", "open", 99),     // unknown dep → blocked (fail closed)
		qe(21, "queued", "closed"),       // closed issue → never ready
		qe(22, "queued", "open"),         // no deps → ready
	}
	// capacity 2, one active (#16) → 1 slot; lowest issue number wins.
	got := ReadyEpics(epics, 2, false)
	if len(got) != 1 || got[0].IssueNumber != 18 {
		t.Fatalf("got %+v", got)
	}
	// capacity 3 → 2 slots → #18 and #22
	got = ReadyEpics(epics, 3, false)
	if len(got) != 2 || got[0].IssueNumber != 18 || got[1].IssueNumber != 22 {
		t.Fatalf("got %+v", got)
	}
	if len(ReadyEpics(epics, 2, true)) != 0 {
		t.Fatal("paused project must schedule nothing")
	}
	if len(ReadyEpics(epics, 1, false)) != 0 {
		t.Fatal("no capacity with one active epic at max_parallel=1")
	}
}

func TestKickoffAndProvider(t *testing.T) {
	if got := KickoffCommand("claude", 16); got != `IS_SANDBOX=1 claude --dangerously-skip-permissions "/epic-pipeline 16"` {
		t.Fatalf("claude kickoff = %q", got)
	}
	if got := KickoffCommand("codex", 16); got != `codex -a never "/epic-pipeline 16"` {
		t.Fatalf("codex kickoff = %q", got)
	}
	if SessionNameFor("proj", 16, 1) != "epic-proj-16" {
		t.Fatalf("session name = %q", SessionNameFor("proj", 16, 1))
	}
	if SessionNameFor("proj", 16, 3) != "epic-proj-16-r3" {
		t.Fatal("retry attempts must produce distinct session names")
	}
	// lossy slugs (truncated or emptied) must stay collision-free via the hash
	a := SessionNameFor("school-platform-api", 16, 1)
	b := SessionNameFor("school-platform-web", 16, 1)
	if a == b {
		t.Fatalf("truncated slugs must not collide: %q == %q", a, b)
	}
	if SessionNameFor("--", 16, 1) == SessionNameFor("++", 16, 1) {
		t.Fatal("punctuation-only names must not collide")
	}
	if ProviderFor("claude", []string{"agent:codex"}) != "codex" {
		t.Fatal("label override to codex")
	}
	if ProviderFor("codex", []string{"agent:claude"}) != "claude" {
		t.Fatal("label override to claude")
	}
	if ProviderFor("claude", nil) != "claude" {
		t.Fatal("project default")
	}
}

func TestCapacityCountsSessionsNotParkedStages(t *testing.T) {
	// An epic waiting in pr_open/merging has no live runner (§7.7) and must
	// not block new spawns at max_parallel=1.
	epics := []db.Epic{
		qe(10, "pr_open", "open"),
		qe(11, "merging", "open"),
		qe(12, "queued", "open"),
	}
	got := ReadyEpics(epics, 1, false)
	if len(got) != 1 || got[0].IssueNumber != 12 {
		t.Fatalf("pr_open/merging must not consume capacity, got %+v", got)
	}
	// but a stage with a live session still does
	epics = append(epics, qe(9, "implementing", "open"))
	if len(ReadyEpics(epics, 1, false)) != 0 {
		t.Fatal("implementing must consume the slot")
	}
}
