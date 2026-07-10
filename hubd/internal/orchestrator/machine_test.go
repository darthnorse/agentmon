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
