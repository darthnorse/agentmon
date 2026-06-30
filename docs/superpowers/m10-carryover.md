# M10 → carry-over (new-session flow — Phase 4b)

M10 (create a tmux session from the UI — `POST /sessions` across agent → hub → web, with the §13.6 safety
rules) is complete, reviewed (ultracode workflow per-task adversarial verify + opus whole-branch +
`/multi-review --codex`), **SAFE-accepted on this host**, and merged to `main` (local merge only). M10 is the
**second of three Phase-4 sub-milestones** (M9 alerts+PWA+push → **M10 new-session** → M11 polish).

Branch: `phase-4-m10-new-session`, off `main@1b63dc1`. Spec:
`docs/superpowers/specs/2026-06-29-agentmon-m10-new-session-design.md`. Plan:
`docs/superpowers/plans/2026-06-29-agentmon-m10-new-session.md`.

## KEY DECISIONS (resolving design §18-Q6 for v1 single-user — annotated in agentmon-design.md)
- **Name:** required, one rule in `shared.ValidateSessionName` = `^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`
  (tmux-safe: excludes `.`/`:`/space/slash/leading-`-`), enforced at **both** the hub boundary and the agent
  exec boundary. Used verbatim as the tmux session name (§9.5).
- **Directory:** optional `cwd`, restricted to an agent `session_dirs` allow-list (TOML; default
  `os.UserHomeDir()` when unset); cleaned + `EvalSymlinks`-resolved + must be **within** an allowed root
  (separator-boundary prefix), so `..` traversal and symlink escape are blocked. The resolved path is what's
  passed to tmux. Omitted → the first allowed root.
- **Command:** custom commands are **NOT exposed** in v1 — a non-empty `command` is rejected (400) at both
  the hub and the agent. Sessions start the default shell (tmux default). The field exists for forward-compat.
- **Authz/audit:** new action `authz.SessionCreate` ("session.create", trivially allowed in v1). The hub
  authorizes **first** (so the decision is recorded), enforces CSRF (`RequireAuth`), and audits
  `session.create` with the name in `meta`. The agent stays bearer-gated.

## What shipped (7 SDD tasks via an ultracode Workflow: shared → agent ‖ hub ‖ web, TDD)
**shared:** `CreateSessionRequest{Name,Cwd?,Command?}`/`CreateSessionResponse{Name}` + `ValidateSessionName`.
**agent:** `tmux.CreateSession` (arg-array `new-session -d -s <name> -c <cwd>` via the existing `Runner` —
no shell) + `tmux.ValidateCwd` + `ErrSessionExists`; `api.CreateSessionHandler` (bearer-gated; validates
name/command/cwd, injects a `SessionCreator` mirroring the `Discoverer` DI); `config.SessionDirs`.
**hub:** `authz.SessionCreate`; `audit.Recorder.SessionCreate`; `registry.Client.CreateSession` (Bearer;
`ErrInvalidSession`/`ErrSessionExists` mapping); `api.ServerCreateSessionHandler` (authz-first, CSRF, re-list
after create + return the full `Session`, audit, error mapping) + route.
**web:** `api-client.createSession` (auto-CSRF, target-aware); `components/NewSessionForm` (live name
validation, name-defaults-to-cwd-basename, 409 inline error) + `routes/index.tsx` auto-open + invalidate.

## Contracts (additive)
- Agent (bearer-only REST): `POST /sessions?target= {name,cwd?,command?}` → `200 {name}` / 400 (bad name |
  cwd outside allow-list | non-empty command) / 409 (duplicate).
- Hub (cookie+CSRF): `POST /api/v1/servers/{id}/sessions {name,cwd?,command?}` → `201 <Session>` (the created
  session, seen-projected, with `windows[0].panes[0].id`) / 400 / 404 (unknown server) / 409 / 502 (agent
  unreachable). The hub forwards the **raw** target to the agent (empty → the agent resolves its first
  target, like the list path); `"default"` is used only for the audit resource + bare-session fallback.

## Invariants / security (don't regress) — verified empirically by opus + deep-scan
- **Single exec path, no shell:** `tmux new-session` has exactly one call site, built as a pure arg array via
  the `Runner` (`exec.CommandContext(ctx,"tmux",args...)`). `name`/`cwd` are positional args — never
  interpolated. `CreateSession`'s signature can't even accept a command (`req.Command` only feeds the reject).
- **RE2-safe name:** Go `$` = end-of-text (not before-`\n`), so a trailing newline/NUL is rejected.
- **cwd escape blocked:** `EvalSymlinks` resolves before the allow-list prefix check (boundary on a path
  separator, `TrimSuffix` so a `/` root still allows its subdirs); blocks `..`, symlink-escape, and
  prefix-collision (`/x/foo` vs `/x/foobar`).
- **Defense in depth:** the name is validated at the hub AND re-validated at the agent; a direct agent call
  bypassing the hub is still safe (agent re-validates + is bearer-gated).
- **Authz-first + CSRF + audit** on the browser endpoint; **no path disclosure** (the agent's detailed cwd
  errors are mapped to a generic hub message).

## Review path
**Workflow** (7 tasks, shared→agent‖hub‖web, each implement pipelined into an adversarial verify): all impl
green, all verdicts pass, max severity minor. **Opus whole-branch**: **READY** (no Critical/Important; it ran
the regex + path-escape tables itself) — applied #1 authorize-first + #3 early hub command-reject; #2 TOCTOU /
#4 no-cap / #6 basename deferred. **`/multi-review --codex`** (feature-dev:code-reviewer + code-simplifier +
deep-scan + codex gpt-5.5): **all reviewers converged** on a target-handling divergence — my own
authz-fix had normalized empty target → `"default"` and forwarded *that* to the agent, which would 502 a
create on an agent whose sole target is labeled non-`"default"` while list worked. **Fixed** (forward the raw
target, `"default"` for audit only; +regression test), plus web target-threading (killing the dead `void
target` prop), the `ValidateCwd` `/`-root edge, bare-session dedup, target-query idiom, a named body cap, and
an unreachable cast. Deferred: session name carried in both the audit resource and the structured `meta`
(intentional — `meta` is queryable, §13.5).

## SAFE acceptance — DONE this session (2026-06-29, on this host)
No prod touch (memory [[dev-host-runs-hub-and-claude]]). **Full Go suite green (`-race`, vet, gofmt,
`CGO_ENABLED=0` build); web vitest 145/145; `tsc` clean.** **Real-tmux create confirmed**: the agent's
`tmux` integration test (`TestCreateSessionIntegration`) creates + duplicate-detects on a **throwaway
`-L agentmon-m10-itest` socket** (tmux 3.5a) — the security-critical exec path on real tmux. **Binaries
build** (agent + the SPA-embedded hub). **Hub-only loopback runtime probe** (real M10 hub binary on
`127.0.0.1:19391`, fresh DB): `POST /sessions` route wired — no-CSRF→403, bad-name→400, non-empty
command→400, valid-name-unknown-server→404. Post-test: prod hub container Up, prod agent (pid 1889386) alive,
default session 0 + `agentmon` demo socket intact, scratch hub/DB + the stale itest socket torn down, repo
clean.

**FLAGGED for the owner (not run here):** the full **multi-process live `POST /sessions` e2e** — real hub
binary + real agent binary on a throwaway tmux socket + agent enrollment, driving create → real tmux session
→ open. Best run with oversight (it stands up two binaries + a tmux socket + enrollment on this prod host; a
socket misconfiguration is the one way to touch the default socket, so I did not stand up a live agent
autonomously). The create path is covered by the real-tmux integration test + the httptest end-to-end (real
`registry.Client` ↔ a fake agent) + handler unit tests + the two reviews.

## Deferred (with rationale)
- **cwd TOCTOU** (deep-scan): a path component swapped to a symlink between `ValidateCwd` and tmux's chdir
  could escape — requires same-user local write on a single-user host; not exploitable in the posture. Note.
- **No session-create cap / rate-limit** (codex): an authed user could spawn unbounded sessions. YAGNI for
  single-user LAN (consistent with M7's deferred `/seen`+SSE caps); revisit with multi-user.
- **No DNS-resolving guard** is needed here (cwd is a local path, not a URL) — N/A; the §18-Q6 dir policy is
  the allow-list above.
- **Web name-autofill from a cwd basename can suggest an invalid name** (e.g. `my.proj`) — harmless, the live
  validation hint guides the user; left as-is.
- **§18-Q6 not-yet-done:** custom start commands / templates (rejected in v1); session **kill/delete** UI
  (the `session.kill` action exists in the namespace, no handler/UI); per-OS-user dir policy / multi-target
  creation (single-target `default`, single service account).

## Reminders for M11 (polish + M8-deferred)
- focus-next-blocked; per-user layout/prefs (incl. configurable terminal theme/font, a `done`-too alert
  toggle); the §18-Q12 desktop grid-vs-inbox decision; the §6.2 mobile sectioned inbox; fold in the M8
  deferred — server-dot REST fallback, terminal-WS `{t:state}` frame, and the hub `TouchLastSeen` poller gap.

## Verification at merge
Full Go suite green (`shared`+`agent`+`hubd`, `-race`, `CGO_ENABLED=0`); `go vet` + `gofmt` clean on
M10-touched files; web vitest **145/145**; `tsc --noEmit` clean. Real-tmux create integration test + a
hub-binary loopback route probe accepted on this host. **NOT pushed and NOT deployed** — local merge only;
the prod hub redeploy + an **agent redeploy** (M10 adds the agent `POST /sessions` handler + `session_dirs`
config) remain owner-only.
