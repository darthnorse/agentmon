package shared

import (
	"encoding/json"
	"testing"
)

func TestValidEpicStage(t *testing.T) {
	for _, s := range []string{"queued", "starting", "planning", "implementing",
		"reviewing", "pr_open", "merging", "merged", "escalated", "stalled", "failed", "canceled"} {
		if !ValidEpicStage(s) {
			t.Fatalf("%s should be valid", s)
		}
	}
	if ValidEpicStage("deployed") || ValidEpicStage("") {
		t.Fatal("unknown stages must be invalid")
	}
}

func TestReportableStage(t *testing.T) {
	for _, s := range []EpicStage{EpicPlanning, EpicImplementing, EpicReviewing, EpicPROpen, EpicEscalated} {
		if !ReportableStage(s) {
			t.Fatalf("%s should be reportable", s)
		}
	}
	for _, s := range []EpicStage{EpicQueued, EpicStarting, EpicMerging, EpicMerged, EpicStalled, EpicFailed, EpicCanceled} {
		if ReportableStage(s) {
			t.Fatalf("%s must not be runner-reportable", s)
		}
	}
}

func TestOrchestratorReportJSON(t *testing.T) {
	var r OrchestratorReport
	if err := json.Unmarshal([]byte(
		`{"repo":"o/r","epic":15,"stage":"pr_open","pr":58,"session":"epic-15","ts":"2026-07-10T14:00:00Z"}`), &r); err != nil {
		t.Fatal(err)
	}
	if r.Repo != "o/r" || r.Epic != 15 || r.Stage != EpicPROpen || r.PR != 58 {
		t.Fatalf("got %+v", r)
	}
}
