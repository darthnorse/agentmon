package orchestrator

import (
	"testing"

	"agentmon/hubd/internal/db"
)

// u builds a claude/one-model boundary row with cumulative input `cum`.
func u(stage, ts string, cum int64) db.UsageRow {
	return db.UsageRow{ProjectID: "p", IssueNumber: 7, Attempt: 1, Stage: stage, CapturedAt: ts,
		Provider: "claude", Model: "claude-opus-4-8", Input: cum}
}

// stageInput sums Input across a stage's models in a derived attempt (test helper).
func stageInput(a UsageAttempt, stage string) int64 {
	var s int64
	for _, st := range a.Stages {
		if st.Stage == stage {
			s += st.Tokens.Input
		}
	}
	return s
}

func TestDeriveRecurringStagesStartBoundaryRule(t *testing.T) {
	rows := []db.UsageRow{
		u("planning", "2026-07-14T10:00:00Z", 100), u("implementing", "2026-07-14T10:05:00Z", 300),
		u("reviewing", "2026-07-14T10:08:00Z", 350), u("implementing", "2026-07-14T10:12:00Z", 600),
		u("reviewing", "2026-07-14T10:20:00Z", 900),
	}
	got := DeriveEpicUsage(rows, db.Epic{IssueNumber: 7, Attempt: 1, Stage: "merged"})
	a := got.Attempts[0]
	// interval attributed to STARTING boundary's stage:
	//   planning     = 100 (lead→S0) + (300-100)=200  = 300
	//   implementing = (350-300)=50   + (900-600)=300  = 350
	//   reviewing    = (600-350)=250                    = 250   [last boundary S4=reviewing is inert]
	if stageInput(a, "planning") != 300 || stageInput(a, "implementing") != 350 || stageInput(a, "reviewing") != 250 {
		t.Fatalf("attribution wrong: plan=%d impl=%d rev=%d", stageInput(a, "planning"), stageInput(a, "implementing"), stageInput(a, "reviewing"))
	}
	if a.Tokens.Input != 900 {
		t.Fatalf("attempt total = final cumulative 900, got %d", a.Tokens.Input)
	}
	if a.Outcome != "merged" || a.IsLowerBound {
		t.Fatalf("outcome/lowerbound wrong: %q %v", a.Outcome, a.IsLowerBound)
	}
}

func TestDeriveMultiProviderMidAppearance(t *testing.T) {
	// claude parent present throughout; codex child appears only at pr_open (cumulative 500).
	rows := []db.UsageRow{
		{ProjectID: "p", IssueNumber: 7, Attempt: 1, Stage: "reviewing", CapturedAt: "2026-07-14T10:00:00Z", Provider: "claude", Model: "claude-opus-4-8", Input: 200},
		{ProjectID: "p", IssueNumber: 7, Attempt: 1, Stage: "pr_open", CapturedAt: "2026-07-14T10:10:00Z", Provider: "claude", Model: "claude-opus-4-8", Input: 200},
		{ProjectID: "p", IssueNumber: 7, Attempt: 1, Stage: "pr_open", CapturedAt: "2026-07-14T10:10:00Z", Provider: "codex", Model: "gpt-5.6-sol", Input: 500},
	}
	got := DeriveEpicUsage(rows, db.Epic{IssueNumber: 7, Attempt: 1, Stage: "merged"})
	a := got.Attempts[0]
	// interval (reviewing@10:00 → pr_open@10:10] active stage = S0 = reviewing;
	// codex delta 500-0 = 500 attributed to reviewing (NOT pr_open). claude lead 200 → reviewing,
	// and claude's own second boundary is a zero delta (200→200), so the STAGE TOTAL (which,
	// by design, sums every model's contribution — Tokens is the aggregate ByModel exists to
	// break down, not a claude-only figure) is claude 200 + codex 500 = 700.
	//
	// NOTE (escalation, not a guess): the task brief for this test asserted
	// stageInput(a,"reviewing") == 200 ("claude reviewing want 200"), but that is mathematically
	// incompatible with the other two assertions below (codexUnderReviewing==500 sourced from
	// this SAME "reviewing" UsageStage's ByModel, and a.Tokens.Input==700) under any coherent
	// definition of UsageStage.Tokens as the sum of its own ByModel — which is the only
	// definition consistent with TestDeriveRecurringStagesStartBoundaryRule and with Cost being
	// derived from real totals. 200 would require silently dropping codex's contribution from
	// the stage aggregate while still reporting it in ByModel — a correctness/billing bug, not a
	// style choice. Corrected to 700 here; see task-12-report.md for the full writeup.
	if stageInput(a, "reviewing") != 700 {
		t.Fatalf("reviewing stage total (claude 200 + codex 500) want 700 got %d", stageInput(a, "reviewing"))
	}
	// codex appears under reviewing:
	var codexUnderReviewing int64
	for _, st := range a.Stages {
		if st.Stage == "reviewing" {
			for _, m := range st.ByModel {
				if m.Provider == "codex" {
					codexUnderReviewing = m.Tokens.Input
				}
			}
		}
	}
	if codexUnderReviewing != 500 {
		t.Fatalf("codex should attribute 500 to reviewing, got %d", codexUnderReviewing)
	}
	if a.Tokens.Input != 700 {
		t.Fatalf("attempt total 200+500=700, got %d", a.Tokens.Input)
	}
}

func TestDeriveOutcomeUsesEpicAttemptNotMaxRow(t *testing.T) {
	// Epic already advanced to attempt 2 (spawned, e.Attempt=2), but attempt 2
	// has not reported any usage yet — only attempt 1 rows exist.
	rows := []db.UsageRow{
		{ProjectID: "p", IssueNumber: 7, Attempt: 1, Stage: "planning", CapturedAt: "2026-07-14T10:00:00Z", Provider: "claude", Model: "claude-opus-4-8", Input: 100},
		{ProjectID: "p", IssueNumber: 7, Attempt: 1, Stage: "implementing", CapturedAt: "2026-07-14T10:05:00Z", Provider: "claude", Model: "claude-opus-4-8", Input: 300},
	}
	got := DeriveEpicUsage(rows, db.Epic{IssueNumber: 7, Attempt: 2, Stage: "implementing"})
	// attempt 1 is NOT the current attempt (e.Attempt==2), so it must read "retried", not e.Stage.
	var a1 *UsageAttempt
	for i := range got.Attempts {
		if got.Attempts[i].Attempt == 1 {
			a1 = &got.Attempts[i]
		}
	}
	if a1 == nil {
		t.Fatal("attempt 1 missing from output")
	}
	if a1.Outcome != "retried" || a1.IsLowerBound {
		t.Fatalf("prior attempt must be retried/false, got outcome=%q lowerbound=%v", a1.Outcome, a1.IsLowerBound)
	}
}

func TestDeriveIsLowerBoundNonTerminal(t *testing.T) {
	// A single boundary, current (only) attempt, epic still on a non-terminal stage: no
	// terminal reap boundary has landed yet, so this attempt's totals are a floor, not exact.
	rows := []db.UsageRow{
		u("planning", "2026-07-14T10:00:00Z", 100),
	}
	got := DeriveEpicUsage(rows, db.Epic{IssueNumber: 7, Attempt: 1, Stage: "reviewing"})
	a := got.Attempts[0]
	if a.Outcome != "reviewing" {
		t.Fatalf("outcome = epic stage for the current attempt, got %q", a.Outcome)
	}
	if !a.IsLowerBound {
		t.Fatalf("current attempt on a non-terminal epic stage should be is_lower_bound=true")
	}
}
