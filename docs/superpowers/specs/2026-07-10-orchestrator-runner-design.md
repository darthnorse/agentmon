# AgentMon Orchestrator — Runner (sub-project 2) — Design

**Date:** 2026-07-10. **Status:** approved design, feeds the sub-2 implementation plan.
**Parent spec:** `2026-07-10-agentmon-orchestrator-design.md` (§5 spawn, §7 runner
contract, §12 host prereqs, §13 decomposition). **Primary design input:**
`../epic-pipeline-design-inputs.md` (the dossier from the live sub-1 run).
Sub-1 shipped at `f306bbb`: the hub brain works but is dormant — agents cannot
execute kickoff commands, runners cannot report stages, and the skills do not
exist. Sub-2 is everything runner-side.

## 1. Scope

- Agent: loopback report intake, buffered store, ack-on-next-drain protocol.
- Agent: execute `CreateSessionRequest.Command` (lift the M10 shell-only
  rejection on both ends).
- Orchestrator: `KillSession` in the `AgentAPI` seam; Cancel/Retry retire
  runner sessions.
- `agentmon report` / `doctor` / `import-epics` / `install-skills` CLI
  subcommands.
- Skills: `epic-pipeline` (Claude), Codex playbook equivalent, `plan-epics`
  (Claude, interactive); idempotent epic import.
- Installer: distribute the binary symlink + binary-embedded skills.

### Non-goals (sub-2)

- Hub-dispatched doctor sessions + project-page doctor display → sub-3 (board
  surface). Sub-2 ships the tool; registration docs say to run it.
- Run/attempt tokens (D2), report-buffer persistence (D7), merged-worktree
  cleanup (D11) — deliberately deferred, see decisions.
- Board UI of any kind → sub-3.

## 2. Decisions log

- **D1 — Drain protocol: ack-on-next-drain.** Single GET endpoint; the hub's
  next poll acknowledges the previous batch by cursor; unacked reports are
  redelivered. At-least-once delivery; the hub's guarded transitions already
  reject duplicates (validated in sub-1). Chosen over separate peek+ack (second
  endpoint/round-trip, same crash semantics) and over keeping the destructive
  drain (the dossier-flagged loss window: an escalation note vanishing when the
  hub crashes between agent-clear and apply).
- **D2 — No run tokens.** Provenance = agent-side server-stamped session names
  (the CLI cannot forge its session) + the hub's assigned-session check
  (`orchestrator.go:307`) + attempt-suffixed session names (`-rN`). Residual
  risk on single-user trusted hosts ≈ nil. Revisit only if PR authorship opens
  to untrusted runners (same posture as the gate's "signed attestation is
  future work").
- **D3 — Resume: always-assess, no flag.** `/epic-pipeline N` always starts by
  assessing artifacts (worktree/branch, plan checkboxes, commits) and resumes
  from them; nothing found → fresh start. `KickoffCommand` stays exactly as
  shipped; kickoffs are idempotent; a manual re-kick after any crash
  self-heals. This is the dossier §4 resume-from-artifacts property made the
  default. A canceled attempt's leftover branch resumes the same way unless a
  human deletes it.
- **D4 — Skill distribution: agentmon repo + installer, embedded in the agent
  binary.** Skills live in `agent/internal/runnerfiles/files/` (one source of
  truth, reviewed like code) and are `go:embed`-ded into the agent binary —
  the installer is HUB-SERVED (`install.sh.tmpl`), so loose repo files cannot
  reach hosts; the binary is the artifact that already travels. A new
  `agentmon install-skills` subcommand writes them to `~/.claude/commands/`
  and `~/.codex/prompts/`; `install.sh` invokes it (via runuser, explicit
  `--home`) on BOTH the fresh and update paths, so the existing fleet update
  loop distributes skill updates with agent updates. Matches the
  `multi-review` host-level convention. Anti-lock-in property: the workflow
  lives in versioned markdown, not Go or schema — adapting to new
  models/workflows is a markdown edit + fleet update, never a protocol
  change. No per-epic authorship knob (YAGNI).
- **D5 — Skill authorship (process, not product).** Claude authors the skill/
  playbook markdown directly on the feature branch (prompt content routed
  through a plan is a lossy transcription step with no compiler to catch
  drift); Codex's plan tasks treat them as existing artifacts and only wire
  them (installer, doctor). Skills are reviewed at every checkpoint — they are
  in the branch diff.
- **D6 — CLI packaging: subcommands + symlink.** `report`, `doctor`,
  `import-epics`, and `install-skills` are subcommands of the existing
  `agentmon-agent` binary (which already routes `hooks`/`hook-test` and has
  config/token/port discovery); `install.sh` drops
  `/usr/local/bin/agentmon → agentmon-agent`. One artifact to build and
  update. (`import-epics` was originally sketched as a bash script; a Go
  subcommand won at planning time — front-matter parsing, issue-number
  stamp-back, and blocked-by rewriting get real tests with a fake `gh`
  runner, and there is no extra artifact to distribute.)
- **D7 — Report store: in-memory, 256 cap, drop-oldest.** Consistent with the
  hooks state machine. An agent restart loses at most a poll interval of
  reports; GitHub reconcile covers the gap. Overflow drops the OLDEST with a
  log line (the latest state supersedes intermediate transitions).
- **D8 — Verdict block unchanged (v1, final review only).** The gate parser is
  untouched. Checkpoint-review evidence = consolidated reports committed to the
  branch, not verdict fields.
- **D9 — Checkpoint FIX commits stay separate commits** (current practice) —
  review provenance stays legible; never folded into task commits.
- **D10 — `pipeline:light`** = no committed plan artifact, no checkpoints;
  implement directly, single pre-PR multi-review, full verdict block.
  Everything else (worktree, reporting, escalation, learnings) unchanged.
- **D11 — Worktree always** (`git worktree add ../<repo>-epic-N`), even at
  `max_parallel=1` — uniform behavior, the main clone stays clean for humans.
  Cleanup of merged worktrees deferred (ops/sub-3).
- **D12 — KillSession on Cancel and Retry only; never on stall.** Stall marks;
  the human decides — Retry is the kill. `ErrNoSession` is success.
- **D13 — No new authz permission for command exec.** Session-create +
  send-keys already grant arbitrary exec on the target; command-on-create adds
  zero new capability. The rejection being lifted was M10 scoping, not a
  security control.
- **D14 — Store instance token guards stale acks.** The agent seq counter
  resets on restart; a stale hub cursor could otherwise delete fresh reports.
  The agent mints a random instance ID at startup; drain responses carry it;
  an ack whose instance does not match deletes nothing.

## 3. Agent: report intake

`POST /orchestrator/report` — loopback only, mirroring `/hook` exactly:
mounted only when `hook_token` is configured; gated by the existing
`RequireLoopback` + `RequireHookAuth` middleware (same token).

- **Body** (≤ 8 KiB, `maxCreateBody` convention): `{repo, epic, stage, note,
  pr}` — the CLI's unauthenticated claims. Validation: `epic > 0`; `stage`
  must pass `shared.ReportableStage` (400 otherwise); `repo`/`note`/`pr`
  pass through (hub-side routing validates repo against registered projects
  and the trust boundary).
- **Session is stamped server-side.** The CLI sends `X-AgentMon-Pane`
  (`$TMUX_PANE`) and `X-AgentMon-Tmux` (`$TMUX`); the agent resolves socket →
  target via the existing `socketFromTmux`/`matchTarget` path, then resolves
  the pane's `#{session_name}` via a new tmux helper on that target's socket.
  Unresolvable pane/socket → 400 (a report must come from a live tmux pane on
  a configured target). `Ts` is stamped server-side too.
- **`?dry_run=1`**: full validation including session resolution; buffers
  nothing; returns 200 with the resolved session name. This is the doctor's
  connectivity probe.

## 4. Agent: buffered store + drain

Store: per-process monotonic `seq` counter; each accepted report is buffered
with its seq and resolved target. Cap 256 (mirrors hub `maxPendingReports`);
overflow drops oldest with a log line (D7). Instance ID minted at startup (D14).

`GET /orchestrator/reports?target=<label>&ack=<cursor>&instance=<id>` — hub
bearer auth (normal agent API middleware, not loopback):

1. If `instance` matches the store's, delete buffered reports for the
   resolved target with `seq ≤ ack`. Mismatch or absent → delete nothing.
2. Return every report still buffered for that target:
   `{"instance": "<id>", "cursor": <max seq in reports, 0 if empty>,
   "reports": [...]}`.

Crash semantics: hub crash before apply → no ack → redelivery next poll;
duplicates rejected by guarded transitions. Agent restart → buffer lost
(accepted, D7) and instance changes → stale acks are inert (D14).

The shipped `?drain=1` parameter dies: no fleet agent ever implemented the
endpoint, so there is no legacy to honor; the hub client changes in the same
release and deploys first.

Shared type (new, in `shared/orchestrator.go`):

```go
type OrchestratorReportBatch struct {
    Instance string               `json:"instance"`
    Cursor   uint64               `json:"cursor"`
    Reports  []OrchestratorReport `json:"reports"`
}
```

## 5. Agent: execute CreateSessionRequest.Command

- `CreateSessionHandler` accepts non-empty `Command` (body size cap is the
  length cap); `tmux.CreateSession` gains a command argument appended to
  `new-session` argv (tmux runs it via `sh -c`; empty → default shell, today's
  argv exactly). When the command exits the session dies — which is spec §7.7's
  normal end ("exit after `pr_open` is not stalled").
- Hub user-facing handler (`hubd/internal/api/sessions.go`): the early
  rejection is lifted and `Command` forwarded — enables board
  New-Session-with-command later. No new authz permission (D13).

## 6. Hub: drain client + KillSession wiring

- `registry.Client.DrainReports` gains `(instance string, ack uint64)` params
  and decodes the batch object; returns `(reports, instance, cursor, error)`.
  404 → treated as "no reports" (old agent; mixed-fleet safe) — preserved.
- Orchestrator keeps `map[server+"/"+target]{instance, cursor}` in memory,
  updated after each drain's reports are routed; hub restart → empty map →
  `ack=0` → full redelivery → duplicates rejected. The existing pending-queue
  (transient-DB-error stash) is unchanged — belt and braces on top of
  redelivery.
- `AgentAPI` interface gains `KillSession(ctx, srv db.Server, target, name
  string) error` — `registry.Client` already implements it (shipped with the
  sidebar kill feature); this is an interface addition + wiring.
- Wiring (best-effort: log on error, proceed): **Cancel** kills the epic's
  live session if `SessionName != ""`; **Retry** kills the predecessor session
  before spawning attempt N+1. `ErrNoSession` → success. Stall never kills
  (D12).

## 7. CLI: `agentmon report` / `agentmon doctor`

Both are `agentmon-agent` subcommands reached via the installed `agentmon`
symlink (D6). Both read `agent.toml` (default `/etc/agentmon/agent.toml`,
`--config` override) for `hook_token` + listen port, like `hook-test`.

**report** — `agentmon report --epic N --stage <stage> [--note s] [--pr N]
[--repo owner/name]`:

- `--repo` omitted → derived from `git config --get remote.origin.url` in the
  cwd, normalized to `owner/name` (skills never template the repo name);
  explicit flag wins.
- Stage validated client-side with `shared.ReportableStage` (fail fast).
- POSTs to `127.0.0.1:<port>/orchestrator/report` with the hook-token bearer
  and `X-AgentMon-Pane`/`X-AgentMon-Tmux` from the environment.
- Exit 0 on 2xx; non-zero + stderr message otherwise. Reporting failures are
  non-fatal to a pipeline except at escalation (see skill: fall back to
  `gh issue comment`, then still stop).

**doctor** — run in the project workdir, `[--base main] [--repo owner/name]`:

- `gh auth status` (authenticated, push access probe via `gh repo view`).
- `git fetch origin <base>` succeeds; workdir is a clone of the repo.
- Reporter connectivity: `dry_run` POST round-trip (requires running inside a
  tmux pane on a configured target — the doctor is dispatched/run as a
  session, same as real runners).
- Provider binaries present (`claude` and/or `codex`).
- Codex hosts (spec §12 facts): `~/.codex/config.toml` has the repo's `.git`
  under `[sandbox_workspace_write] writable_roots` and
  `network_access = true`.
- Human-readable pass/fail lines + nonzero exit on any failure. Hub dispatch
  and board display are sub-3.

## 8. Skill: `epic-pipeline` (Claude) — `agent/internal/runnerfiles/files/claude/epic-pipeline.md`

Installed to `~/.claude/commands/epic-pipeline.md`; invoked by the shipped
kickoff `IS_SANDBOX=1 claude --dangerously-skip-permissions "/epic-pipeline N"`.
Content is authored by Claude on the branch (D5); the load-bearing rules it
must encode (dossier §§1–4):

1. **Assess first, always** (D3): `gh issue view N` (title/body/labels), then
   look for artifacts — worktree/branch `epic/N-slug`, committed plan with
   ticked boxes, per-task commits. Found → resume at the first unticked step;
   none → fresh start.
2. **Worktree always** (D11): `git worktree add ../<repo>-epic-N`.
3. **Plan**: report `planning`; issue body is REQUIREMENTS, never a plan (if
   it carries one, validate/adapt). Write the plan against current code,
   writing-plans discipline scaled to the issue; embed **checkpoints as
   artifact content** (Global Constraints line + explicit `CHECKPOINT` steps
   at seams, dossier §2) with truthfully verified file dispositions. Commit
   the plan. `plan-gate` label → report `escalated --note "plan-gate: plan
   ready at <path>"` and STOP; the human reviews and Retries — always-assess
   resumes right after the plan (plan-gate needs no new mechanism).
4. **Implement**: report `implementing`; execute the plan task-by-task
   (dossier §1 rules verbatim: read plan in full first; strict order,
   single-agent; literal checkboxes including run-test-to-fail; full
   build+test green before every commit; exact commit messages; no trailers;
   never push mid-plan; scope discipline). **Rule 7 maps to escalate**: any
   plan↔repo mismatch → `agentmon report --stage escalated --note
   "<mismatch>"` and STOP (trivial mechanical fixes excepted). If the escalate
   POST fails → `gh issue comment` fallback, then still stop.
5. **Checkpoint reviews**: report `reviewing`; run `/multi-review --codex` on
   the segment diff in-session (lenses are subagents; only the consolidated
   report enters context). FIX → apply + commit (separate commits, D9), with
   regression tests per the test-with-fix policy. DISCUSS → escalate. NITPICK
   → note. **Recursion termination (dossier §3 verbatim)**: recurse only while
   the delta contains unreviewed judgment; hard cap one review-of-fixes per
   checkpoint; the final whole-branch review is the fixpoint.
6. **Finish**: rebase on base (conflict it cannot cleanly resolve → escalate,
   never force-push); final whole-branch `/multi-review --codex`; commit the
   consolidated report to the branch; write durable learnings into
   `CLAUDE.md`/`AGENTS.md`/docs in the same branch; push; `gh pr create` with
   the body ending in the **v1 verdict block populated from the final review
   only** (D8), `reviews:` satisfying the project's `required_reviews`;
   `agentmon report --stage pr_open --pr <num>`; then END TURN. (Authoring
   correction to the parent spec's "exit": the kickoff runs the provider CLI
   interactively, so the session IDLES after the final turn instead of dying
   — deliberately kept, because an attachable runner session is the product's
   whole point: humans join escalated/finished sessions to discuss or steer.
   pr_open has no stall timeout, and Cancel/Retry retire idle sessions.
   Verdict `reviews` convention: list each lens that ran PLUS the pseudo-entry
   `cross-model` when at least one reviewing model differs from the authoring
   model — projects should set `required_reviews: [cross-model]` so the
   requirement stays provider-agnostic for both runner types.)
7. **`pipeline:light`** (D10): skip plan artifact + checkpoints; implement
   directly; single pre-PR multi-review; full verdict block; all reporting/
   escalation/worktree/learnings rules unchanged.

## 9. Codex playbook — `agent/internal/runnerfiles/files/codex/epic-pipeline.md`

Installed to `~/.codex/prompts/epic-pipeline.md`; invoked by the shipped
kickoff `codex -a never "/epic-pipeline N"` (custom-prompt argument passing
empirically verified on codex-cli 0.144.1). Same pipeline with two provider
inversions: subagent-heavy steps become `codex exec` subprocesses (Codex has
no subagent primitive), and checkpoint/final reviews invert to **headless
`claude` as the cross-provider reviewer**. The invocation was VALIDATED live
on 2026-07-10 (toy repo, planted index-out-of-range + contract bug):

```
IS_SANDBOX=1 claude --dangerously-skip-permissions -p "/multi-review <base>..HEAD" > docs/reviews/epic-N-<cpK|final>.md
```

found both bugs, applied 3 fixes with a regression test, committed them
(`fix(review): …`), wrote the consolidated report to stdout, exit 0. The
reviewer commits fixes on the runner's branch; the playbook has Codex re-run
the full suite afterward and read the new commits.

## 10. Skill: `plan-epics` + import script

- `agent/internal/runnerfiles/files/claude/plan-epics.md` →
  `~/.claude/commands/plan-epics.md`.
  Interactive on the project host: brainstorm the PRD/phase with the human
  (wrapping superpowers:brainstorming); emit `docs/plan/epic-NN-<slug>.md`
  files — front-matter: title, labels (`agentmon:epic` + dials `pr-gate`,
  `plan-gate`, `agent:codex`/`agent:claude`, `pipeline:light`), `Blocked-by:`
  lines — body: scope, acceptance criteria, constraints, decisions-taken
  (requirements with decisions baked in, never implementation plans). Commit,
  run the import, then the go-live ritual: import while the project is
  paused → human reviews the board → Resume.
- **Import: `agentmon import-epics`** (Go subcommand shelling to `gh` — see
  D6): deterministic and idempotent. For each `docs/plan/epic-*.md` with no
  `issue:` front-matter key → `gh issue create` (title, labels, body) →
  **stamp the created issue number back into the file's front-matter** (the
  file is the birth certificate; re-runs skip stamped files). Second pass
  rewrites `Blocked-by: epic-NN` file references to `Blocked-by: #<issue>`
  lines in issue bodies (`gh issue edit`; the exact form the hub's
  `ParseBlockedBy` regex reads) once all issues exist. Front-matter parsing
  lives in `agent/internal/epicfile` — a deliberately strict key:value
  format, NOT YAML, so a typo'd dial fails the import instead of silently
  dropping. The skill invokes it; a human can run it standalone.

## 11. Installer

`install.sh.tmpl` additions (all idempotent, current-user install unchanged),
running on BOTH the fresh-install and update paths:

- Symlink `/usr/local/bin/agentmon → /usr/local/bin/agentmon-agent`.
- `runuser -u $RUN_USER -- agentmon-agent install-skills --home <run user's
  home>` — writes the binary-embedded skills into `~/.claude/commands/` and
  `~/.codex/prompts/` (unconditional; a file for an absent provider is
  harmless and becomes live the moment that provider is installed).

The owner's `agentmon_update.sh` fleet loop (which re-runs install.sh) now
distributes skill updates with agent updates — the skills ride the binary.

## 12. Security

- Intake trust boundary = `/hook`'s: loopback + shared hook token; threat
  model is other local processes on single-user hosts (accepted, as for
  hooks). The CLI's claims (repo/epic/stage/note/pr) are unauthenticated by
  design; the hub validates repo routing, the server+target trust boundary,
  the assigned-session check, and guarded transitions — the report can
  request, never command.
- Server-side session stamping means a report cannot claim a session its pane
  does not belong to; combined with attempt-suffixed names this is the D2
  provenance story.
- The exec surface (create-with-command) is hub-bearer-gated like every agent
  API; D13 documents why no new permission is needed.
- Drain is hub-bearer-gated; the instance/cursor protocol cannot be driven by
  local processes (loopback middleware is not on that route, bearer is).

## 13. Testing

- **Agent**: httptest handler tests mirroring the hooks suite — intake
  (loopback/token gating, validation, server-side stamping via fake tmux
  runner, dry_run), store (seq monotonicity, cap/drop-oldest, per-target ack
  deletion, instance mismatch → no deletion), drain handler (shape, ack).
  `tmux.CreateSession` fake-runner argv assertions (empty command → today's
  argv exactly).
- **Hub**: registry client against an httptest agent (batch decode, ack
  params, 404 tolerance); orchestrator KillSession wiring (Cancel/Retry
  retire sessions, ErrNoSession success, other errors best-effort + spawn
  proceeds); cursor-map flow across polls.
- **CLI**: report/doctor against a fake loopback server (`hooks_cli_test`
  pattern); import-epics against a fake `gh` runner (stamp-back + blocked-by
  rewrite + dry-run + idempotency); repo derivation table-tested;
  install-skills round-trip against the embedded FS.
- **Skills**: validated by the toy-repo acceptance run + Claude's hand-test of
  the headless reviewer invocation (§9). Not unit-testable.

## 14. Rollout

Hub rebuild first (DrainReports tolerates 404 from old agents → mixed fleet
safe), then agents via the fleet update loop (binary + symlink + skills +
import script). Orchestrator stays dormant until `github.token` is set.
Acceptance = the toy-repo ritual, now end-to-end: register → doctor →
3-epic run at `max_parallel=1` (parent spec §13).

## 15. Build process (sub-2 itself)

- One feature branch `feat/orchestrator-runner`. Claude authors + commits:
  spec, plan, the three skill/playbook files (early, so wiring tasks reference
  existing files). Codex implements all Go + installer + import script,
  task-by-task, exact commit messages, no trailers, never pushes.
- Plan Global Constraints cover the sub-2 surface: `agent/`, `shared/`,
  `hubd/`, `runner/`, `install.sh`. Codex sandbox on this host already covers
  them (repo `.git` writable, `network_access = true`, GOCACHE workaround)
  from the sub-1 live run.
- Checkpoints at seams (dossier §2 heuristic), roughly: after shared types +
  agent store/intake (data layer), after drain protocol + hub-side changes
  (integration), after CLI/doctor/installer. Exact placement in writing-plans.
- Review economics as validated (dossier §3): `/multi-review --codex` per
  checkpoint, Claude applies fixes, one review-of-fixes max per checkpoint,
  final whole-branch review as fixpoint, Claude gates the merge.
