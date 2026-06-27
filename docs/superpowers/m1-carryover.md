# M1 → M2 carry-over (from the M1 whole-branch review)

Findings from the M1 (agent: discovery + REST) reviews that were intentionally
deferred — not merge-blockers. Fold the relevant ones into the M2 plan. The M1
multi-review gate (incl. Codex) for the **agent** runs after M2 (agent = M1+M2),
per Phase-1 design §2.6; the items below feed it.

M1 merge state: branch `phase-1-m1`, final HEAD `0ddbc9d`. The final whole-branch
review (opus) returned "Ready to merge: With fixes" — the cheap fixes were applied
in `0ddbc9d`; the one substantive item (tmux `-F` de-escaping, below) is deferred
here consciously.

## Post-merge multi-review pass (with Codex `gpt-5.5`)

After merge, a 4-lens `/multi-review --codex` (feature-dev:code-reviewer +
code-simplifier + deep-scan + codex) ran on the M1 code. It **fixed** (on a
follow-up branch): a goroutine + tmux-process leak in `ControlClient.readLoop`
(dead `case <-c.Done` escape hatch → now a real `quit` channel; process now
reaped via `cmd.Wait`), pane-id command-injection hardening (`NewControlClient`
now rejects ids not matching `^%[0-9]+$` before any exec), a bearer length-leak
(now compares fixed-size SHA-256 digests so token length can't be timed), and a
small `writeJSONError` reuse in `bearer.go`. Two regression tests added
(quit-unblocks-full-Output; pane-id rejection). **Codex independently re-flagged
the tmux `-F` de-escaping below** — cross-model confirmation that it is real;
still deferred to M2.

## Must fix at the M2 agent review gate

- **Robust tmux `-F` delimiter / de-escaping — the M1 normalization is a heuristic.**
  Discovery delimits `-F` fields with `0x1f` (ASCII Unit Separator) and `ExecRunner`
  normalizes tmux's rendering of it (`\037`) back to `0x1f`. The **opus final review
  empirically probed tmux 3.5a** and refuted the original safety premise: tmux's
  `-F` escaping is **field-dependent and not uniform**:
  - `#{window_name}` containing a backslash → `\\` (NOT octal `\134`).
  - `#{pane_current_path}` containing a backslash → returned **raw** (not escaped).
  - the injected `0x1f` delimiter → `\037`.
  Consequence: a field value that itself contains a literal backslash or the literal
  text `\037` (backslash,0,3,7) can be mis-normalized by the blunt `\037`→`0x1f`
  `bytes.ReplaceAll`, splitting a record into the wrong field count so that
  `len(f) != 8` and **that pane/window is silently dropped from the tree**. The
  template separator and an in-value `0x1f` both render as `\037`, so post-hoc
  splitting cannot be made fully robust.
  **Why deferred:** likelihood is low on a single-user box (operator's own
  session/window names + cwds are escape-free), there is no trust boundary, and
  normal discovery + the done-when smoke are unaffected.
  **Robust fix options for M2:** (a) a delimiter strategy that is unambiguous with
  escaped data, or (b) a faithful per-field decoder that reverses tmux's actual
  `-F` escaping (and a unit test feeding *escaped* fixtures so the munging is
  covered in CI, not only the dev-box integration test). The current code marks
  this with a `KNOWN LIMITATION` comment in `agent/internal/tmux/runner.go`.

## Should fix opportunistically in M2

- **`discovery.go` active-pane fallback** fires only when **both** `sessCwd==""` AND
  `sessCommand==""`; a `foundActive bool` set alongside the active-pane assignment
  is cleaner. (Latent only — real tmux won't emit a blank `pane_current_command`
  for a running shell.)
- **`discovery_test.go` coverage gaps:** no test for `discoverPanes` error
  propagation (a `list-panes` failure that isn't "no server running");
  `TestDiscoverGroupsPanesIntoWindowsInOrder` doesn't assert session-level
  `Cwd`/`Command`; `TestDiscoverSessionCwdCommandFromActivePane` discards the
  `Discover` error (`got, _ :=`). Add a de-escaping unit test here when the robust
  fix lands.
- **`sessions_test.go`** opts assertion doesn't check `SocketName` (trivially `""`
  today) — a non-empty-socket case would catch a passthrough regression.
- **`bearer.go`** scheme short-circuit (`!ok ||` skips the constant-time compare for
  a wrong/absent scheme): leaks only the public `Bearer ` scheme, never a token
  byte. Optional one-line comment noting it is intentional.
- **`runner.go`** error wraps `*exec.ExitError` via `%w` but appends stderr as `%s`;
  typed unwrapping wouldn't see stderr. Fine for the current string-match
  `isNoServer`; revisit only if a caller needs typed unwrapping.
- **Stdlib-reuse nitpicks in the ported `control.go`** (multi-review, low): `indexByte`
  reimplements `bytes.IndexByte`; `trimNL` reimplements `bytes.TrimRight(b, "\r\n")`;
  `discovery.go`'s `cut2` is a one-call passthrough of `strings.Cut`. Skipped in the
  M1 fix pass (nitpicks, two in verbatim-ported code) — fold in if/when M2 touches them.

## Project-wide hardening (not M1/M2-specific; backlog)

- **No `http.Server` timeouts** on the agent (`http.ListenAndServe`) — Slowloris
  exposure. Mitigated by LAN-only deployment; consistent with the M0 hub scaffold.
  Add Read/Write/Idle timeouts project-wide when hardening.
- **Discovery has no request timeout** — a hung `tmux` ties up a `/sessions` request
  until the client gives up. Consider deriving `context.WithTimeout` from
  `r.Context()` (pairs with the server-timeout item).
- **`/sessions` spawns 1 + N tmux processes with no caching** — fine for M1 session
  counts; revisit if the hub polls aggressively.

## Still-open M0 carry-overs (not addressed in M1)

These remain from `docs/superpowers/m0-carryover.md`; M1 only folded in the
agent-local cleanups (config-test `WriteFile` error checks; `health.go` `LookPath`
moved to construction time):

- **`resolveRef` secret hardening — before auth (M3).** Require an `env:`/`file:`
  scheme for secret fields (reject bare literals); make the hub + agent loaders
  symmetric. (`agent/internal/config/config.go`, `hubd/internal/config/config.go`.)
- **`migrate()` transactions** — when a non-idempotent (`ALTER`-style) migration
  lands, wrap it in a transaction. (`hubd/internal/db/migrations.go`.)
- **`.dockerignore`** omits `hubd/internal/webui/dist/`; **Dockerfile** lacks a
  `go mod download` cache layer — fold into the hub build work (M3/M4).
- **hubd test hygiene** (`repo_test.go` `d, _ := Open`, `directive_test.go`
  discarded error / unasserted `UserID`, `health_test.go` no `version` assert) —
  fix with the hub milestones.

## Acceptable as-is (noted, no action needed)

- **Plaintext bearer token on the agent** — correct per design §13.1: agents are
  LAN-only with no public exposure; TLS is terminated by Caddy in front of the
  **hub**, not the agent. The HMAC directive (M2) adds the second factor.
- **`isNoServer` substring-matches `"no server running"`** — tmux isn't localized
  and exposes no distinct exit code for "no server", so substring match is the
  pragmatic choice.
