# Orchestrator Runner Implementation Report

Date: 2026-07-11
Branch: `feat/orchestrator-runner`
Status: complete — Tasks 1–19 implemented, verified, and committed.

## Completed tasks

- Task 1: shared ack-on-next-drain batch wire format.
  - Commit: `5253a35 feat(shared): OrchestratorReportBatch — ack-on-next-drain wire format`
- Task 2: tmux pane-to-session resolution.
  - Commit: `8dd1c03 feat(agent): tmux.SessionNameForPane — pane-to-session resolution for the report intake`
- Task 3: bounded report store with ack semantics.
  - Commit: `42d7cf7 feat(agent): report.Store — buffered orchestrator reports with ack-on-next-drain semantics`
- Task 4: loopback report intake and exported hook target helpers.
  - Commit: `e5649bc feat(agent): orchestrator report intake — loopback POST with server-side session stamping`
- Task 5: bearer-authenticated report drain handler.
  - Commit: `04c0f9a feat(agent): report drain endpoint — ack-on-next-drain protocol`
- Task 6: live report intake and drain routes.
  - Commit: `0cf33a9 feat(agent): mount orchestrator report intake + drain routes`
- Task 7: optional tmux session command.
  - Commit: `0ab45d0 feat(agent): tmux.CreateSession takes an optional session command`
- Task 8: agent-side `CreateSessionRequest.Command` forwarding.
  - Commit: `f454aa1 feat(agent): execute CreateSessionRequest.Command — lift the shell-only rejection at the exec boundary`
- Task 9: hub-side command forwarding.
  - Commit: `cdb7cd3 feat(hub): forward CreateSessionRequest.Command to the agent — New-Session-with-command`
- Task 10: registry ack-cursor drain transport.
  - Commit: `36a3fd0 feat(hub): DrainReports speaks the ack-on-next-drain protocol`
- Task 11: orchestrator drain ack state and `KillSession` seam.
  - Commit: `fc6bc32 feat(hub): orchestrator remembers drain cursors and acks on the next poll; AgentAPI gains KillSession`
- Task 12: best-effort runner retirement on Cancel and Retry.
  - Commit: `67757b8 feat(hub): Cancel/Retry retire the epic's runner session (best-effort KillSession)`
- Task 13: `agentmon report` CLI.
  - Commit: `26860ed feat(agent): agentmon report subcommand — posts runner stage reports to the loopback intake`
- Task 14: strict epic-file parsing and issue stamp-back.
  - Commit: `149c4c5 feat(agent): epicfile — strict epic front-matter parser with issue stamp-back`
- Task 15: idempotent `agentmon import-epics` CLI.
  - Commit: `e43f048 feat(agent): agentmon import-epics — idempotent epic-file → GitHub issue import with stamp-back`
- Task 16: project-host `agentmon doctor` CLI.
  - Commit: `a3d273a feat(agent): agentmon doctor — validates gh auth, clone, reporter, providers, codex sandbox`
- Task 17: embedded runner files and `agentmon install-skills`.
  - Commit: `17ed804 feat(agent): embed runner skills in the binary + agentmon install-skills`
- Task 18: installer distribution of the runner CLI and skills.
  - Commit: `8fafef1 feat(hub): installer distributes the agentmon CLI symlink + runner skills on install and update`
- Task 19: runner CLI and reporter configuration documentation.
  - Commit: `c553d89 docs: runner CLI + hook_token reporter note in config reference`

## Checkpoint review fixes

External checkpoint reviews found no transcription defects. Their plan-level and hardening fixes were applied directly on the branch and preserved by all later work:

- `4d198bb fix(review): checkpoint-1 multi-review findings (tasks 1-6)`
  - Preserved the constant report tmux timeout and clarified design-document wording.
- `700f730 fix(review): checkpoint-2 multi-review findings (tasks 7-12)`
  - Added the tmux `--` command separator, command redaction on create failure, and agent-side NUL rejection.
- `9434b40 fix(review): checkpoint-3 multi-review findings (tasks 13-16)`
  - Hardened and refactored the report, import, doctor, and hooks CLIs and their tests. The committed helper signatures and implementations remained authoritative over stale earlier plan snippets.

## Verification

Before every implementation commit, the required workspace gate passed:

```text
cd /root/agentmon/shared && go build ./... && go test ./...
cd /root/agentmon/agent  && go build ./... && go test ./...
cd /root/agentmon/hubd   && go build ./... && go test ./...
```

`GOCACHE=/tmp/agentmon-go-cache` was used because the default sandbox cache was read-only.

The final suite contains 695 passing test/subtest events across 26 packages:

| Module | Packages | Passing test/subtest events |
|---|---:|---:|
| `shared` | 1 | 45 |
| `agent` | 10 | 270 |
| `hubd` | 15 | 380 |
| **Total** | **26** | **695** |

Task-specific red/green verification also passed:

- Tasks 1–5: batch shape, pane resolution, store semantics, intake stamping/rejections, and drain acknowledgments.
- Task 6: full route-wiring gate.
- Tasks 7–9: positional tmux command execution and command forwarding at both HTTP boundaries.
- Tasks 10–12: registry batch transport, next-poll acknowledgments, and Cancel/Retry session retirement.
- Task 13: report validation, loopback authentication headers, rejection surfacing, and repository URL normalization.
- Task 14: strict front matter, list parsing, error cases, and issue insertion/replacement.
- Task 15: issue creation/stamping, dependency linking, idempotency, symbolic dry-run previews, and unresolved-ref rejection.
- Task 16: successful and failed host checks, provider detection, reporter dry-run, and Codex network/root/sandbox-mode checks.
- Task 17: all three embedded files install byte-for-byte to the expected provider paths; the authored files under `agent/internal/runnerfiles/files/**` were never edited.
- Task 18: rendered installer content, both update/fresh call sites, and Bash syntax.
- Task 19: full workspace gate after documentation-only changes.

## Rule-7 stop and resolution

Task 13 stopped at its first green-test attempt because the planned `reportTestServer` fixture supplied only `listen`, `server_id`, and a bare `hook_token`. The existing authoritative `config.Load` resolves `hub_token` and `directive_key` unconditionally, and secrets require `env:` or `file:` references, so tests failed with `config: empty secret ref`.

The amended plan corrected the fixture to provide environment-backed `hub_token`, `directive_key`, and `hook_token`, mirroring the existing hooks CLI fixture. After retranscription, the Task 13 command suite and full gate passed. No implementation behavior was improvised during the stop.

## Amended-plan resolutions

- Task 15 dry-run now validates dependency references and previews unstamped sibling dependencies with symbolic epic basenames.
- Task 16 validates an explicit Codex `sandbox_mode`; read-only configurations fail even when writable roots and network access otherwise look valid.
- Task 19 places the commented top-level `hook_token` example before `[[targets]]`, preventing it from being misread as an ignored target field when uncommented.

## Final state

- Tasks 1–19 and all plan checkboxes are complete.
- Full shared, agent, and hub build/test gates are green.
- The runner skill source content was not modified.
- No pushes or commit trailers were produced.

## Rollout note (final review)

Deploy order is hub first, then update EVERY agent before enabling (or
unpausing) orchestrator projects: the new hub's kickoff sends a session
Command that pre-branch agents reject with 400, which stalls each ready epic
and consumes one of its attempts until a human retries after the agents
update. The reverse direction (pre-branch hub polling a new agent) fails
report decoding every tick but loses nothing; it is unreachable through
supported distribution because agents download their binary from the hub.
