# Orchestrator toy-repo acceptance — run report (2026-07-11)

**Verdict: PASS, with one code fix shipped.** The full ritual — register →
doctor → epic runs at `max_parallel=1` — completed end-to-end against a hub +
agent built from `main` (`65a98ce`) on aigallery, with real Claude and Codex
runner sessions against a real GitHub repo (`darthnorse/agentmon-toy`,
private). All merge-gate paths behaved as designed across FOUR epics
(auto-merge / pr-gate / CI-red escalation / plan-gate + real-green-CI), plus
the wrong-target chaos pass. Finding A below was judged a real design gap by
the owner and fixed during the run (`ca33904` + review fixes `b8140c7`,
4-lens `/multi-review --codex`, all-confirmed validation), then re-validated
by replaying the original incident against the fixed hub. Findings B–C are
runbook/prerequisite items.

## Environment (hermetic — prod untouched)

The prod agent on this host (aigallery, `:8377`, socket `agentmon`) and the
prod hub were not touched. The test stack:

- **Hub**: `bin/agentmon-hubd` from `main`, transient unit `agentmon-toy-hub`,
  `127.0.0.1:8378`, fresh DB at `/root/agentmon-toy/hub/data`, `github.token`
  from `gh auth token`, `orchestrator.max_attempts: 4` (headroom for harness
  churn; default 2).
- **Agent**: `toyhost`, `127.0.0.1:8379`, target socket `agentmon-toy`,
  transient unit `agentmon-toy-agent` running in a **mount namespace**
  (systemd `BindPaths`) that overlays `/etc/agentmon` → toy config,
  `/root/.claude/settings.json` → hook URLs rewritten to `:8379`, and
  `/root/.codex` → a full toy copy of the codex home. Everything the agent's
  tmux server spawns (runner sessions, their shells, the report CLI, hooks)
  inherits the namespace; the host files stay prod.
- **Two hand-patches the harness needs** (documented product behavior, fine
  for the one-agent-per-host prod topology): enroll hardcodes the agent dial
  port to 8377 (`enroll.go` `agentPort`), so `servers.url` was updated to
  `:8379` in the hub DB; and `agentmon report`/hooks discover the agent via
  `/etc/agentmon/agent.toml` + baked hook URLs, hence the namespace.
- Toy repo: tiny Go module (`greet/`), plus `.github/workflows/doomed.yml` —
  a check that always fails for PRs touching `doomed/**` (deterministic
  CI-red fixture). PRs not touching it trigger zero check runs (= green).

Re-run: the whole stack lives under `/root/agentmon-toy/` (binaries, configs,
`cj.txt`/`csrf.txt` curl session, `watch_epic.sh`, `trust_codex.sh`);
`codex-home/` was deleted at teardown — recreate with rsync + the two file
swaps if a codex epic is needed.

## Ritual results

1. **Register** — `POST /api/v1/orchestrator/projects` (no board UI yet;
   curl + session cookie + CSRF). Project `toy`, `max_parallel=1`,
   `require_ci=false`. Paused before import per the go-live ritual.
2. **Doctor** — `agentmon doctor` inside an agent-spawned session: **all 10
   checks passed** (repo derivation, gh auth, repo access, fetch, reporter
   dry-run through the real intake, both provider binaries, both skills,
   codex sandbox config).
3. **Import** — `agentmon import-epics`: 3 files → issues #1–#3, `issue:`
   stamped back, `Blocked-by: #1` rewritten into #2's body. Dry-run exact.
   Board mirrored all three (queued, correct labels/deps) while paused.
4. **3-epic run** (resume → all behaved):

| Epic | Dials | Path proven | Outcome |
|---|---|---|---|
| #1 farewell | (none) — full pipeline, claude | plan artifact + cross-model plan review + checkpoint & final `/multi-review --codex` + verdict → gate auto-merge | **PR #5 squash-merged by the gate**, issue closed, `agentmon:merged` applied |
| #2 shout | `pr-gate, pipeline:light, agent:codex`, blocked-by #1 | dep held until #1 merged; codex kickoff (`ProviderFor`); headless-claude review; verdict honestly lists `cross-model` without a `codex` lens; gate → `escalated: "pr-gate label: human merges"` | **approve action → hub merged PR #6**, issue closed |
| #3 omen | `pipeline:light` (touches `doomed/`) | PR #4 → gatekeeper check fails → gate → `escalated: "CI checks failing"` | **cancel action** → session retired; PR/issue closed by hand |
| #7 whisper | `plan-gate` — full pipeline, claude | plan committed → **live cross-model plan review** (`codex exec` caught a real `Whisper("Wow!")` replace-the-wrong-`!` edge case; runner amended the plan with a pinning test) → runner-initiated `escalated: "plan-gate: plan ready"` → human **retry** → resumed from plan → PR #8 → **real `greet` check run pending → green** → gate auto-merge | **PR #8 merged**, issue closed |

Addendum passes: **wrong-target chaos test** — a project registered with
`target: "bogus"` produced the intended loud per-tick error (`agent toyhost
reports: unknown target "bogus" — check the project's target label`, the
final-review DrainReports 404-sniff fix live-firing) instead of silent empty
drains; **import idempotency** re-demonstrated (epics 1–3 skipped as stamped
while #4 imported); GitHub **sync backoff** observed on a nonexistent repo.

Also live-validated by the run (some via a harness incident, see Findings A):
stall detection + capacity release; **retry** with attempt-suffixed sessions
(`epic-toy-1-r2`); **artifact resume** twice — epic #1 resumed from the
killed attempt's committed plan, epic #3 resumed from an already-open PR and
cleanly resolved a real `CLAUDE.md` add/add conflict against just-merged
main; `max_parallel=1` serialization; report provenance (session stamping +
socket match, stage-guard drops of a zombie session's reports); drain/ack
across hub restarts; audit log (register/pause/resume/retry/approve/cancel
with principal+IP, orchestrator's own `epic.merge`); epic event timelines;
verdict parsing for both providers' review sets; runners writing durable
learnings to the repo's `CLAUDE.md`.

## Findings

**A. Hooks were load-bearing for orchestrator liveness — FIXED in code.**
The stall detector read the hub's live-state projection, which is fed *only*
by provider lifecycle hooks (agent `/state` ← hook intake; tmux discovery was
not consulted). A host where hooks were missing, mis-pointed, or rejected
stalled **every** epic ~45s in (2 ticks + grace) while the runner worked on
obliviously — reproduced live by the harness, and judged by the owner a
design gap, not a runbook item. **Fix (`ca33904`)**: `checkStalls` now
queries the agent's REAL tmux session list (`AgentAPI.Sessions`, the same
dial the drain uses), target-scoped — which also closes the old cross-target
name-scan gap; agent unreachable = liveness unknown = no stall verdict (fail
safe, stage timeouts still apply). **Review fixes (`b8140c7`)**: per-Tick
liveness cache shared across co-hosted projects (failures cached too), 3s
per-call deadline bounding the tickMu hold, `o.server` helper reuse, and the
Partial-snapshot/charset invariant documented + the raw-target contract
pinned by tests. Hook state is now what it was designed to be: a display
surface. A doctor hooks round-trip check (`hook-test` exists) is still worth
adding for live-view quality, no longer for liveness.

**B. Codex hook trust is an interactive, per-user, one-time gate.** On a host
where `~/.codex/hooks.json` was installed but never trusted, the first codex
runner session hangs at codex's "Hooks need review" TUI prompt (`-a never`
does not answer trust prompts) and every retry re-hangs. (With fix A the epic
no longer false-stalls — but the runner still sits at the prompt until its
stage timeout.) Owner already knew: trusting codex hooks right after agent
install is their standing practice — this is a runbook line so it never gets
skipped on a new host, not a discovery. Hashes live in `~/.codex/config.toml`
`[hooks.state]` and are not reproducible externally.

**C. Kickoff PATH prerequisite.** The kickoff command resolves `claude` /
`codex` via the **agent process's** environment (tmux server inherits it; the
command runs through `sh -c`, no login profile). On aigallery the provider
binaries live in `/root/.local/bin`, which the stock systemd unit PATH does
not include — a prod kickoff on this host would die instantly today. The toy
unit set an explicit PATH. Deploy runbook: give the agent unit a PATH drop-in
(or symlink providers into `/usr/local/bin`) on every runner host. Note the
doctor's provider check only catches this when doctor runs inside an
**agent-spawned** session (it inherits the same env); running doctor from a
human login shell would pass while kickoffs fail. Related: the toy runners
also found `go` absent from the session PATH and self-recovered — worth a
line in the project-host prereqs.

**D. Minor observations.** (1) Merged epics leave their runner sessions
running (attachable by design); on a busy project idle TUIs will accumulate —
consider auto-retire-on-merge later. (2) Enroll's hardcoded `:8377` dial port
and the reporter's fixed config path are fine for prod but preclude
two-agents-one-host without the namespace tricks above — acceptable,
documented here. (3) Codex sandbox for worktree-based epics needs the repo
**parent** dir writable (worktrees are siblings) in addition to the exact
`.git` entry the doctor checks — the toy config added both;
school-platform's codex host config should too.

## Incident replay (regression proof for fix A)

After rebuilding the toy hub at `b8140c7`, the original incident was replayed
exactly: the test agent restarted WITHOUT hook redirection (runner hooks
misdirect to the prod agent and are rejected; the hub's hook-fed view of the
runner stays permanently empty; stage reports still flow), then probe epic #9
(`pipeline:light`, trivial doc) spawned. Old hub: stalled at ~80s. Fixed hub:
**4.7 minutes observed, hook-fed pane count 0 on every sample, stages
progressing `implementing → reviewing` via reports, zero stall events in the
hub journal** — the epic ran to auto-merge on a hub that never saw a single
hook from it.

## State after teardown

Toy hub + agent units stopped; toy tmux server killed; `/usr/local/bin/agentmon`
symlink removed (the installer recreates it properly at deploy);
`~/.codex/config.toml` writable_roots restored (toy entries removed).
Kept: `/root/agentmon-toy/` (minus codex-home) for re-runs, and the private
`darthnorse/agentmon-toy` repo — issues/PRs closed, `main` green with the
merged epics (#1 farewell, #2 shout, #7 whisper, #9 contributing note).

**Next step: deploy** — hub rebuild first (now REQUIRED ≥ `b8140c7` for the
liveness fix; DrainReports tolerates 404 from old agents), then all agents
via the fleet update loop, with runbook items B–C folded in before enabling
any project: (B) trust codex hooks once per runner host — owner's standing
practice; (C) agent unit PATH must resolve `claude`/`codex` (drop-in or
symlinks; on aigallery they live in `/root/.local/bin`).
