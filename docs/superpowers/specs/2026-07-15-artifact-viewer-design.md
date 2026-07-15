# In-app Artifact Viewer — Design (v1)

**Status:** design approved (brainstorm) 2026-07-15. Ready for writing-plans.
**Branch:** `feat/artifact-viewer` (spec + plan + code live here).

## Goal

Let the owner read a runner-produced `.md` artifact (plan, review, escalation
evidence) **in the AgentMon UI** — clickable from an escalation note, rendered
as real markdown — instead of having to `git checkout`/SCP the file off the
runner host or open github.com.

## Scope (v1 = Tier 1 only)

- **IN:** read any **checked-in artifact that is on GitHub** — on the epic's
  branch (in-flight) or the base branch (merged/history). Delivered by
  generalizing the hub's existing **plan proxy** to any allowlisted artifact
  dir, plus a small runner change so artifacts are **pushed** when you'd want
  to read them.
- **OUT (deferred):** reading **uncommitted / un-pushed** worktree files live
  (that would need a new agent file-read endpoint — "Tier 2"). Deferred because
  the runner **commits everything** by design (plan `:145`, review evidence
  `:207`, report `:231`, per-task `:174`; "the commits are the truth"), so the
  only real gap is *push timing*, which §2 closes. Revisit Tier 2 only if a
  genuinely-uncommitted artifact turns out to matter in practice.

## Background (why this shape)

The runner commits all its artifacts but **pushes only twice** — at plan-gate
(`epic-pipeline.md:155-159`) and at PR-open (`:255`). Between those, committed
artifacts sit **unpushed** on the branch (epic-1 was "7 commits ahead of origin,
no PR" — committed, not uncommitted). That's why a review was unreadable via
GitHub at an escalation. The hub already serves the *plan* this way (parse path
from escalation note → fail-closed validate → `GetContents(repo, path, ref)` →
web renders with react-markdown in `PlanPanel`). v1 generalizes that pipe.

## Architecture

Reuse + widen the existing plan proxy. Five small changes, no new subsystem:
hub artifact endpoint (§1), runner push-on-escalate (§2), web clickable paths +
generalized panel (§3).

## 1. Hub — generic artifact endpoint

New route `GET /projects/{id}/epics/{epicID}/artifact?path=<repo-relative>`,
built from the guts of `OrchestratorEpicPlanHandler` (`hubd/internal/api/orchestrator.go:733`):
same `authz.OrchestratorView`, same epic-in-project check, same 256 KiB cap and
GitHub-fallback error shape, same `{path, ref, markdown}` JSON response.

Two changes from the plan handler:

- **Path is a query param** (the web sends what the user clicked), not derived
  from `e.Needs`. Validate it with the *same* fail-closed rules as `planDocPath`
  (`:712-728`): `len ≤ 512`, no leading `/`, no `..`, `planPathRe` safe-char
  regex (`^[A-Za-z0-9._/-]+$`), must end `.md` — but against an **allowlist set**
  instead of the single `docs/plans/` prefix.
  - `var artifactDirs = []string{"docs/plans/", "docs/reviews/"}` (extensible).
  - Reject (400) any path that fails validation or matches no allowlisted prefix.
    **This is the security boundary** — see §Security.
- **Ref = `e.Branch`, falling back to `p.BaseBranch` on `github.ErrNotFound`.**
  In-flight artifacts live on the branch; merged ones live on the base branch
  (branch often deleted post-merge). The fallback is what makes the viewer work
  for **completed** epics (the majority) instead of silently 404-ing. `p.BaseBranch`
  is already on the project row — no GitHub round-trip to discover it.

Keep the existing `e.Branch == ""` → 409 guard ("no branch yet"). The existing
`OrchestratorEpicPlanHandler` stays (or is reimplemented on top of the generic
handler with `path = planDocPath(...)`); do not break the plan-review flow.

## 2. Runner — push branch on escalate (both variants)

`epic-pipeline.md` already pushes at plan-gate (`:159`:
`git push -u origin "$b"` then `agentmon report --stage escalated …`). Extend
that so **every** `agentmon report --stage escalated` is preceded by a branch
push, so committed artifacts are on GitHub at `e.Branch` when the escalation
surfaces. Apply to **both** `claude/` and `codex/` variants. Constraints
(existing): work only on the epic branch, **never force-push**, report only if
the push succeeds (so pushed and reported refs cannot diverge).

## 3. Web — clickable paths + generalized panel

- **Clickable paths (`EpicDrawer.tsx`):** the drawer already renders escalation
  notes (`ev.note`, `:240`). Parse each note for recognized artifact paths
  (`/docs\/(plans|reviews)\/[\w./-]+\.md/`) and render each as a clickable
  button/link instead of plain text.
- **`ArtifactPanel`:** refactor `PlanPanel.tsx` to take a `path` prop and call
  the generic artifact endpoint (new `getEpicArtifact(projectId, epicId, path)`
  + query key in `api-client.ts`). Reuse the exact `.markdown` styling,
  `ReactMarkdown + remarkGfm`, the loading/error states, and the "View on GitHub"
  fallback link it already has. `PlanPanel` becomes a thin caller of
  `ArtifactPanel` with the plan path (keep the "Approve plan" button on the plan
  path only).
- Contract: add the artifact response type to `contracts.ts` (mirror the plan
  response `{path, ref, markdown}`).

## Data flow

Escalation note (`ev.note`) → web parses artifact path → click → `GET
/projects/{id}/epics/{epicID}/artifact?path=…` → hub validates fail-closed +
resolves ref (`e.Branch` → `p.BaseBranch`) → `Contents.GetContents` (GitHub API,
PAT never leaves the hub) → `{path, ref, markdown}` → `ArtifactPanel` renders.

## Security (the crux)

The path becomes **user-settable** — exactly the future code path the plan
proxy's own comment anticipated (`orchestrator.go:721`). The fail-closed
validation IS the boundary: reject `..`, leading `/`, anything not under an
allowlisted prefix, non-`.md`, non-safe-chars. Consequence: the endpoint can
**only ever** serve a doc under `docs/plans/` or `docs/reviews/`, from a repo the
hub already fully controls, **via the GitHub Contents API — never the host
filesystem**. No new attack surface beyond "read two specific `.md` dirs." Authz
is the existing `OrchestratorView` on the project.

**Known v1 limitation (accepted — symlinks):** GitHub's Contents API follows a
committed symlink whose target is a normal file *in the same repo*, returning the
target's bytes. So an allowlisted `docs/reviews/x.md` that is actually a symlink
to a repo file *outside* the allowlisted dirs would be served — a lexical bypass
of the dir boundary. **Accepted for v1** because it is bounded by the same trust
model this feature rests on: planting such a symlink requires repo-write access,
which already grants full read of the hub-controlled repo, so it yields an
attacker nothing they couldn't already read. Hardening (reject `120000`
symlink / non-blob entries via the Git tree API before serving) is a deferred v2
follow-up.

## Error handling

- Path fails validation / not allowlisted → **400** (fail-closed).
- Not found on either ref (not pushed yet, or dropped in a squash) → **404**,
  message "artifact not available (may not be pushed yet)" + the web shows the
  existing "View on GitHub" branch link.
- `> 256 KiB` → **413** "open it on GitHub" (existing `ErrTooLarge` path).
- GitHub fetch error → **502** (existing).
- `e.Branch == ""` → **409** (existing).

## Testing

- **Hub** (`orchestrator_test.go` / a new artifact test): extend the plan-proxy
  coverage — allowlist **accepts** `docs/reviews/…md`, **rejects** traversal
  (`../`), leading `/`, a non-allowlisted dir (`src/…`), and non-`.md`; the ref
  fallback (`e.Branch` 404 → refetch `p.BaseBranch`); 404/413 shapes. Use the
  existing fake `ContentsFetcher`.
- **Web:** `ArtifactPanel` renders markdown from a fake fetch and shows the
  GitHub fallback on error; `EpicDrawer` note-parsing turns a `docs/reviews/…md`
  path into a clickable control.
- **Runner:** the push-on-escalate is a skill (prose) change — no unit test;
  verify on a real escalation (call this out in the plan).

## Gate (must pass)

- Go: `make test` (all 3 modules; `GOCACHE=/tmp/agentmon-go-cache` if needed).
- Web: `cd web && npm run typecheck && npm run test:run`.
- `contracts.ts` hand-mirrors the Go `shared` types — if a field is added,
  traverse DB→API→contract→UI.

## Out of scope / deferred

- **Tier 2** (agent live worktree read of uncommitted files) — deferred; only
  build if a genuinely-uncommitted artifact proves necessary.
- No new artifact *storage* (files stay in git; the hub proxies, never caches).
- No directory listing / browse — v1 opens a *known* path from an escalation
  note. (A future "list artifacts" view could enumerate `docs/reviews/` via the
  Contents API, but not v1.)

## Open questions for writing-plans

1. Reuse-vs-reimplement for `OrchestratorEpicPlanHandler`: cleanest is to keep
   the plan route and have both routes call one shared `fetchArtifact(e, p,
   path)` helper. Confirm during planning.
2. Exact web control affordance for a clickable artifact path (inline link in
   the note vs. a small "View" button beside it) — a UI nicety, decide in the
   plan.
