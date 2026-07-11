package shared

// EpicStage is the orchestrator pipeline stage of one epic. The full state
// machine lives hub-side; agents and the report CLI only need the names.
type EpicStage string

const (
	EpicQueued       EpicStage = "queued"
	EpicStarting     EpicStage = "starting"
	EpicPlanning     EpicStage = "planning"
	EpicImplementing EpicStage = "implementing"
	EpicReviewing    EpicStage = "reviewing"
	EpicPROpen       EpicStage = "pr_open"
	EpicMerging      EpicStage = "merging"
	EpicMerged       EpicStage = "merged"
	EpicEscalated    EpicStage = "escalated"
	EpicStalled      EpicStage = "stalled"
	EpicFailed       EpicStage = "failed"
	EpicCanceled     EpicStage = "canceled"
)

var epicStages = map[EpicStage]bool{
	EpicQueued: true, EpicStarting: true, EpicPlanning: true, EpicImplementing: true,
	EpicReviewing: true, EpicPROpen: true, EpicMerging: true, EpicMerged: true,
	EpicEscalated: true, EpicStalled: true, EpicFailed: true, EpicCanceled: true,
}

func ValidEpicStage(s string) bool { return epicStages[EpicStage(s)] }

// ReportableStage is the subset a runner session may self-report. Everything
// else is hub- or GitHub-derived; a report claiming those is rejected.
func ReportableStage(s EpicStage) bool {
	switch s {
	case EpicPlanning, EpicImplementing, EpicReviewing, EpicPROpen, EpicEscalated:
		return true
	}
	return false
}

// OrchestratorReport is one runner stage report. The CLI posts it to the local
// agent's loopback intake; the hub drains buffered reports over its existing
// poll channel (hub dials agent — there is no agent→hub connection).
type OrchestratorReport struct {
	Repo    string    `json:"repo"`
	Epic    int       `json:"epic"`
	Stage   EpicStage `json:"stage"`
	Note    string    `json:"note,omitempty"`
	PR      int       `json:"pr,omitempty"`
	Session string    `json:"session"`
	Ts      string    `json:"ts"`
}

// OrchestratorReportBatch is one drain response (ack-on-next-drain protocol).
// Instance identifies the agent store's lifetime (minted at agent start): an
// ack whose instance does not match the store's current one deletes nothing,
// so a hub cursor that predates an agent restart can never delete fresh
// reports. Cursor is the highest buffered seq contained in Reports (0 when
// empty); the hub echoes instance+cursor on its NEXT drain to acknowledge —
// at-least-once delivery, duplicates rejected by the hub's guarded transitions.
type OrchestratorReportBatch struct {
	Instance string               `json:"instance"`
	Cursor   uint64               `json:"cursor"`
	Reports  []OrchestratorReport `json:"reports"`
}
