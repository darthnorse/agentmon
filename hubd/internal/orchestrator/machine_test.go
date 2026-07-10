package orchestrator

import (
	"agentmon/shared"
	"testing"
)

func TestValidTransition(t *testing.T) {
	ok := [][2]shared.EpicStage{
		{shared.EpicQueued, shared.EpicStarting},
		{shared.EpicStarting, shared.EpicPlanning},
		{shared.EpicStarting, shared.EpicImplementing}, // pipeline:light skips planning
		{shared.EpicPlanning, shared.EpicPROpen},       // forward jump
		{shared.EpicReviewing, shared.EpicImplementing}, // fix loop
		{shared.EpicPROpen, shared.EpicMerging},
		{shared.EpicMerging, shared.EpicMerged},
		{shared.EpicImplementing, shared.EpicEscalated},
		{shared.EpicPlanning, shared.EpicStalled},
		{shared.EpicEscalated, shared.EpicQueued},
		{shared.EpicEscalated, shared.EpicMerging},   // board Approve
		{shared.EpicEscalated, shared.EpicImplementing}, // plan-approval resume
		{shared.EpicStalled, shared.EpicQueued},
		{shared.EpicStalled, shared.EpicFailed},
		{shared.EpicQueued, shared.EpicCanceled},
	}
	for _, p := range ok {
		if !ValidTransition(p[0], p[1]) {
			t.Errorf("%s→%s should be valid", p[0], p[1])
		}
	}
	bad := [][2]shared.EpicStage{
		{shared.EpicMerged, shared.EpicQueued},      // terminal
		{shared.EpicCanceled, shared.EpicStarting},  // terminal
		{shared.EpicPROpen, shared.EpicPlanning},    // backward (not the fix loop)
		{shared.EpicQueued, shared.EpicMerged},      // queued only starts or cancels
		{shared.EpicMerging, shared.EpicQueued},
		{shared.EpicQueued, shared.EpicQueued},      // self-loop
	}
	for _, p := range bad {
		if ValidTransition(p[0], p[1]) {
			t.Errorf("%s→%s should be invalid", p[0], p[1])
		}
	}
}

func TestValidTransitionRecoveryToMerged(t *testing.T) {
	// Spec §6: a human merging the PR in GitHub must work from escalated (and
	// a stalled epic whose PR was merged must be closeable by reconcile).
	if !ValidTransition(shared.EpicEscalated, shared.EpicMerged) {
		t.Fatal("escalated→merged must be valid (human GitHub merge recovery)")
	}
	if !ValidTransition(shared.EpicStalled, shared.EpicMerged) {
		t.Fatal("stalled→merged must be valid (reconcile of merged PR)")
	}
}

func TestValidTransitionRejectsUnknownStages(t *testing.T) {
	for _, from := range []shared.EpicStage{"bogus", "", "deployed"} {
		if ValidTransition(from, shared.EpicCanceled) {
			t.Fatalf("unknown from-stage %q must not transition anywhere", from)
		}
	}
}
