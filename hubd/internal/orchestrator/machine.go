package orchestrator

import "agentmon/shared"

// forwardOrder positions the happy-path stages; a forward jump is legal
// (runners may skip stages: pipeline:light, missed reports).
var forwardOrder = map[shared.EpicStage]int{
	shared.EpicQueued: 0, shared.EpicStarting: 1, shared.EpicPlanning: 2,
	shared.EpicImplementing: 3, shared.EpicReviewing: 4, shared.EpicPROpen: 5,
	shared.EpicMerging: 6, shared.EpicMerged: 7,
}

var activeStages = map[shared.EpicStage]bool{
	shared.EpicStarting: true, shared.EpicPlanning: true, shared.EpicImplementing: true,
	shared.EpicReviewing: true, shared.EpicPROpen: true, shared.EpicMerging: true,
}

// ValidTransition is the single authority on legal stage moves. TransitionEpic
// guards racing writers; this guards nonsense.
func ValidTransition(from, to shared.EpicStage) bool {
	if from == to {
		return false
	}
	switch from {
	case shared.EpicMerged, shared.EpicFailed, shared.EpicCanceled:
		return false // terminal
	case shared.EpicQueued:
		// → failed: attempts-exhausted terminalization by the scheduler.
		return to == shared.EpicStarting || to == shared.EpicCanceled || to == shared.EpicFailed
	case shared.EpicEscalated:
		switch to {
		// Escalated is a PAUSE, not a dead end: resolving a DISCUSS resumes the
		// pipeline FORWARD (implement → review → PR), so accept any forward stage
		// the runner reports on resume — a runner that finishes the discussed work
		// and jumps straight to pr_open (skipping intermediate stage reports) must
		// not be stranded in escalated with its PR unrecorded.
		// → queued: Retry re-runs from artifacts.
		// → merged: a human merging the PR in GitHub is a spec-promised recovery
		//   (§6); reconcile/webhook observe it and must close the epic.
		case shared.EpicQueued, shared.EpicImplementing, shared.EpicReviewing, shared.EpicPROpen,
			shared.EpicMerging, shared.EpicMerged, shared.EpicCanceled:
			return true
		}
		return false
	case shared.EpicStalled:
		switch to {
		case shared.EpicQueued, shared.EpicMerged, shared.EpicFailed, shared.EpicCanceled:
			return true
		}
		return false
	}
	if !activeStages[from] {
		return false // unknown stage: nothing is legal from it
	}
	switch to {
	case shared.EpicEscalated, shared.EpicStalled, shared.EpicCanceled:
		return true
	case shared.EpicImplementing:
		if from == shared.EpicReviewing {
			return true // fix loop
		}
	}
	f, okF := forwardOrder[from]
	t, okT := forwardOrder[to]
	return okF && okT && t > f
}
