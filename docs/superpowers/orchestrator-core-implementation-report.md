# Orchestrator Core Implementation Report

Date: 2026-07-10
Branch: `feat/orchestrator-core`
Status: complete — Tasks 1–22 implemented, verified, and committed.

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
- Task 15: orchestrator core loop for sync, reports, stalls, gating, and scheduling.
  - Commit: `fd7fae6 feat(hub): orchestrator core loop — sync, reports, stalls, gate, schedule`
- Task 16: orchestrator authorization, audit, and board push dispatcher.
  - Commit: `d67a560 feat(hub): orchestrator authz actions, audit methods, board push dispatcher`
- Task 17: HMAC-authenticated GitHub webhook endpoint.
  - Commit: `4eba10d feat(hub): public hmac-authenticated github webhook endpoint`
- Task 18: orchestrator projects, board, and actions endpoints.
  - Commit: `acafad3 feat(hub): orchestrator projects/board/actions endpoints`
- Task 19: board SSE stream.
  - Commit: `e369476 feat(hub): board SSE stream`
- Task 20: guidance injection over the agent websocket.
  - Commit: `5c15d0a feat(hub): send guidance text into live sessions over agent ws`
- Task 21: main wiring and two-epic end-to-end integration.
  - Commit: `e6a299d feat(hub): wire orchestrator into main — gated on github token`
- Task 22: per-project CI requirement.
  - Commit: `24ffe45 feat(hub): per-project require_ci closes the pre-check-registration merge window`

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
- Task 15: complete orchestrator package tests under `go test -race`.
- Task 16: orchestrator and audit tests under `go test -race`.
- Task 17: webhook handler tests.
- Task 18: complete API package tests.
- Task 19: complete API package tests under `go test -race`.
- Task 20: agentws and API tests.
- Task 21: two-epic integration, all-module verification, and no-token boot smoke test.
- Task 22: project persistence, gate behavior, and complete hub suite.

## Resolved plan mismatch

The earlier Task 3 helper collision was resolved by plan-fix commit `7f53b53`: `projects_test.go` now reuses the existing `enrollTestServer` helper from `state_test.go`. The corrected red test produced the intended missing project-store API errors, and Tasks 3–5 then completed normally.

## Task 15 contract resolution

The report-drain ambiguity was resolved explicitly: `SetEpicPR` preserves the stored epic branch with `e.Branch`; the runner report deliberately has no branch field. Task 15 Step 3 was then implemented without changing `shared.OrchestratorReport`. The updated contracts are honored: `GateInput.Epic` receives `e.IssueNumber`, and `MergePR` receives the evaluated head SHA.

## Resolved kickoff expectation

Task 15 Step 4 ran the prescribed race suite:

```text
GOCACHE=/tmp/agentmon-go-cache go test ./internal/orchestrator/ -v -race
```

The stale Task 15 `TestTickSyncsAndSpawns` expectation was corrected by plan commit `eb24add` to match Task 12's authoritative autonomous kickoff command:

```text
IS_SANDBOX=1 claude --dangerously-skip-permissions "/epic-pipeline 16"
```

After that correction, the entire Task 15 race suite passed, as did the final full-module build and test gate.

## Resolved Task 21 fixtures

Task 21 Step 1 added the prescribed two-epic integration test. The session-name fixture was corrected by plan commit `c7e0e4b` to use `sessionName(1)` and `sessionName(2)`. Step 2 then ran:

```text
GOCACHE=/tmp/agentmon-go-cache go test ./internal/orchestrator/ -run TestTwoEpicChain -v
```

The stale verdict fixture was corrected by plan commit `37969a1` to carry `epic: 1`, matching the checkpoint-2 binding contract. The integration test then passed and Task 21 completed.

## Final verification

- `hubd`: `go build ./... && go test ./...` passed, including `internal/agentws`, `internal/api`, `internal/github`, and `internal/orchestrator`.
- `agent`: `go build ./...` passed.
- `shared`: `go test ./` passed.
- No GitHub token: the hub boot smoke test reached `agentmon-hubd dev listening on :0` without a nil-orchestrator panic.
- No pushes were performed.
