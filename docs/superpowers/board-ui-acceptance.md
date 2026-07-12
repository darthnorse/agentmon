# Orchestrator board UI acceptance — 2026-07-11

## Verdict

**Automated gates and isolated toy-stack API/runtime acceptance passed. Full visual acceptance remains manual.**

The code under test was `ad9768c638b31f2c996af820984bcfc20d3f53f4` on
`feat/board-ui`. No implementation defect was found. This environment had no
headless browser, so it would be inaccurate to claim direct observation of the
rendered board/timeline/drawer, a background Web-Push notification tap, or the
phone layout. Those items are listed explicitly below for the owner's final
browser pass.

## Isolated environment

- Prod was untouched. The existing `:8377` listener remained up throughout.
- The system service bus was unavailable, so the documented transient units
  could not be started. Branch-built binaries were run directly on the same toy
  endpoints: hub `127.0.0.1:8378`, agent `127.0.0.1:8379`, target socket
  `agentmon-toy`.
- The hub used a copy of the toy SQLite database under `/tmp`. The original
  `/root/agentmon-toy/hub/data/agentmon.sqlite` remained byte-identical to its
  pre-run backup.
- Hub binary SHA-256:
  `8fac3069dc02969b1936cd65cb4dd92fb03bc62d9d0bffab4472436890c38a4c`.
- Agent binary SHA-256:
  `2854e131ea4e2bee9846ff6bd88187d112c6c143564940a76163f7bfbd45eaf4`.
- Both toy processes were stopped after the pass; ports `8378` and `8379` were
  no longer listening.

The local hub build requires `make embed` after `web/npm run build` and before
`go build`: `npm run build` writes `web/dist`, while the Go binary embeds
`hubd/internal/webui/dist`. The first hub build intentionally exposed the
committed 176-byte placeholder until the documented embed target was run. The
tracked placeholder and ignored copied assets were restored/removed after the
acceptance binary was built.

## Directly observed evidence

### Stack, board data, and embedded SPA

- Authenticated board API: HTTP 200 with `orchestrator_enabled: true`, one toy
  project, and five historic epics.
- Dedicated agent sessions API: HTTP 200 against `toyhost`; the target list was
  initially empty and backed by the `agentmon-toy` tmux socket.
- Board history contained four merged epics and one canceled epic. Derived stat
  counts were merged `4`, done `5`, and working/needs/PR-open/queued all `0`.
  This pins the rule that canceled is shown in Done but is not counted as
  merged.
- Project board API returned all five epics and event histories for all five.
- Historic plan proxy returned HTTP 200 for epic #7:
  `docs/plans/epic-7.md` at `epic/7-add-whispered-greeting`, 4,885 markdown
  bytes. Its events include plan-gate escalation, user retry, resume, review,
  PR-open, and merge.
- After `make embed` and hub rebuild, `/`, `/projects`, and an epic deep link all
  returned the 968-byte SPA shell. `/manifest.webmanifest` was served as
  `application/manifest+json`; `/sw.js` was served as JavaScript.
- The production app bundle contained the Task 13–21 control labels, including
  New project, Plan epics, Approve plan, Open full session, Delete project, and
  Timeline. The service-worker bundle contained the epic notification and
  navigation-message markers.

### Real mutations against the disposable toy DB

- Header-backed actions all returned HTTP 200: max-parallel `1 → 2 → 1`, CI
  gate `false → true → false`, and pause/resume. Final project state matched the
  initial state.
- PATCH workdir typo/fix calls both returned HTTP 200 and restored
  `/root/agentmon-toy/repo`.
- A temporary registration using the registered `toyhost` returned HTTP 201.
  The server API exposes registrations without a connectivity/health field,
  matching the binding clarification: every registered host is selectable and
  doctor is the connectivity check.
- To test deletion without spawning a provider, the temporary project was
  paused on a deliberately nonexistent target and a queued epic fixture was
  inserted only into the disposable DB copy. DELETE returned the typed HTTP 409
  message, `project has 1 active epic — cancel or finish them first`. After the
  fixture was made terminal, DELETE returned HTTP 200 and removed the project
  and its epic.
- Existing session regression: creating `board-ui-acceptance-shell` returned
  HTTP 201 with pane `%0`; it appeared in both the sessions API and the
  `agentmon-toy` socket, then kill returned HTTP 200 and the session disappeared.

### Final gates

- `shared`: `go build ./...` and `go test ./...` passed.
- `agent`: `go build ./...` and `go test ./...` passed.
- `hubd`: `go build ./...` and `go test ./...` passed.
- Web typecheck passed.
- Web tests passed: **70 files, 420 tests**.
- Production app and service worker build passed: 528 app modules and 56 worker
  modules transformed; `dist/sw.js` generated.
- The existing jsdom canvas warnings and Vite chunk-size warning remained
  non-failing.

## Manual remainder before merge

The following Task 22 observations were not claimed:

- Render `/projects` and the narrowed project in a real browser; visually check
  five columns, stat strip, attention badge, ProjectHeader, and compact Done
  cards.
- Visually exercise Timeline range changes, dependency arrows, queued barless
  rows, and a live running edge.
- From the rendered drawer, drive preview/open-full typing, guidance, plan
  approval, review approval/merge, retry, and cancel. The retained history and
  APIs prove the data/contracts, not these clicks.
- Run `agentmon doctor` from the new-project UI and watch all ten checks. The
  previous toy acceptance did this end-to-end; this pass did not repeat it
  because a direct agent process lacks the runbook's mount namespace.
- Trigger a real background Web-Push delivery and tap, observe the in-app toast,
  and perform the narrow/phone layout and persisted layout-toggle pass.
- Visually smoke the existing home, grid, mobile tabs, and interactive terminal.
  Their full suites and the real session create/list/kill path passed here.

Checkpoint 4 is therefore a **reportable partial acceptance**, not an assertion
that the manual browser checklist passed. The owner should complete the items
above during the final whole-branch review before deciding to merge.
