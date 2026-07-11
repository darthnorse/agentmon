# Orchestrator Core Implementation Report

Date: 2026-07-10
Branch: `feat/orchestrator-core`
Status: stopped during Task 15 because the prescribed `drainReports` code references a field absent from the shared wire contract.

## Completed tasks

- Task 1: GitHub and orchestrator configuration sections.
  - Commit: `bfb93c2 feat(hub): github + orchestrator config sections`
- Task 2: migration 0005 for projects, epics, and epic events.
  - Commit: `ba9c0b1 feat(hub): 0005 orchestrator schema (projects, epics, epic_events)`
- Task 3: projects store.
  - Commit: `2ce7715 feat(hub): projects store`
- Task 4: epics and epic-events store with guarded transitions.
  - Commit: `2d16b36 feat(hub): epics + epic_events store with guarded transitions`
- Task 5: shared epic stages and orchestrator report wire type.
  - Commit: `d097688 feat(shared): epic stages + orchestrator report wire type`
- Task 6: minimal GitHub REST client.
  - Commit: `6d350fb feat(hub): minimal github rest client`
- Task 7: webhook HMAC verification and event parsing.
  - Commit: `2d08be5 feat(hub): webhook hmac verify + event parse`
- Task 8: verdict block parser.
  - Commit: `5adea9a feat(hub): verdict block parser`
- Task 9: fail-closed merge gate.
  - Commit: `c585329 feat(hub): fail-closed merge gate`
- Task 10: epic-stage transition table.
  - Commit: `edf0457 feat(hub): epic stage transition table`
- Task 11: board-change broadcaster.
  - Commit: `036c544 feat(hub): board change broadcaster`
- Task 12: dependency-aware scheduler and kickoff commands.
  - Commit: `58fefd8 feat(hub): dependency-aware scheduler + kickoff commands`
- Task 13: orchestrator report draining from agents.
  - Commit: `b369704 feat(hub): drain orchestrator reports from agents`
- Task 14: issue mirror synchronization helpers.
  - Commit: `7b17b20 feat(hub): issue mirror sync helpers`

## Verification

Before each implementation commit, the required full module gate passed:

```text
cd /root/agentmon/hubd
GOCACHE=/tmp/agentmon-go-cache go build ./...
GOCACHE=/tmp/agentmon-go-cache go test ./...

ok  agentmon/hubd/cmd/agentmon-hubd
ok  agentmon/hubd/internal/agentbin
ok  agentmon/hubd/internal/api
ok  agentmon/hubd/internal/audit
ok  agentmon/hubd/internal/authn
ok  agentmon/hubd/internal/authz
ok  agentmon/hubd/internal/config
ok  agentmon/hubd/internal/db
ok  agentmon/hubd/internal/directive
ok  agentmon/hubd/internal/github
ok  agentmon/hubd/internal/orchestrator
ok  agentmon/hubd/internal/registry
ok  agentmon/hubd/internal/state
ok  agentmon/hubd/internal/webui
```

Task-specific verification also passed:

- Task 3: project store tests and clean hub build.
- Task 4: complete DB package tests and clean hub build.
- Task 5: complete shared module tests and clean hub build.
- Task 6: five GitHub REST-client tests.
- Task 7: complete GitHub package tests.
- Task 8: four verdict-parser tests.
- Task 9: ten merge-gate decision subtests.
- Task 10: transition-validity table test.
- Task 11: broadcaster tests under `go test -race`.
- Task 12: scheduler and provider tests.
- Task 13: complete registry package tests.
- Task 14: complete orchestrator package tests.

## Resolved plan mismatch

The earlier Task 3 helper collision was resolved by plan-fix commit `7f53b53`: `projects_test.go` now reuses the existing `enrollTestServer` helper from `state_test.go`. The corrected red test produced the intended missing project-store API errors, and Tasks 3–5 then completed normally.

## Stop condition

Task 15 Steps 1–2 were completed: the prescribed orchestrator test and fakes were added, and the required red test failed with `New undefined` as expected. Before Step 3 implementation, the plan's `drainReports` code was checked against the shared type registry and source.

The plan requires:

```go
o.d.DB.SetEpicPR(ctx, e.ID, r.PR, r.Branch)
```

However, `shared.OrchestratorReport` is defined as:

```go
type OrchestratorReport struct {
	Repo string
	Epic int
	Stage EpicStage
	Note string
	PR int
	Session string
	Ts string
}
```

There is no `Branch` field in the source type or the plan's shared type registry. Adding a wire field versus passing an empty branch changes the cross-module contract, so Task 15 was stopped without improvisation. Tasks 16 and later were not started.

## Worktree at stop

- `hubd/internal/orchestrator/orchestrator_test.go` contains the exact Task 15 Step 1 test and remains uncommitted.
- Plan checkboxes are ticked through Task 15 Step 2 and remain uncommitted.
