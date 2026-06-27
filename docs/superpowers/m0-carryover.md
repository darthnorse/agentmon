# M0 → M1 carry-over (from the M0 whole-branch review)

These are findings from the M0 scaffold reviews that were intentionally deferred
(not merge-blockers). Fold the relevant ones into the M1 plan. The M0
merge-blocker (embed placeholder clobber) was **fixed** before merge
(commit `2bd7284`): `make build` now restores the tracked placeholder and CI
guards against committing a real-SPA embed.

## Must fix before the relevant feature lands

- **`resolveRef` secret hardening — before auth (M3).** Both config loaders'
  `default:` case returns the raw string, so a typo (`envFOO`, missing colon) or
  a deliberate `token_ref: "sk-plaintext"` is silently accepted as the secret.
  Also the loaders have drifted: the **hub** errors on an empty ref, the
  **agent** does not. Before M3 wires real auth/agent tokens: require an
  `env:`/`file:` scheme for secret fields (reject bare literals) and make both
  loaders symmetric. (`agent/internal/config/config.go`,
  `hubd/internal/config/config.go`.)
- **`migrate()` transactions — when a 2nd/non-idempotent migration lands.**
  Currently safe only because every migration uses `IF NOT EXISTS` (now
  documented in `hubd/internal/db/migrations.go`). A future `ALTER`-style
  migration MUST wrap its file in a transaction.

## Should fix opportunistically in M1

- **Test hygiene** (several files): `d, _ := Open(...)` drops the error →
  nil-deref panic on failure instead of a clean `t.Fatal` (`hubd/internal/db/repo_test.go`
  and others); `os.WriteFile` errors unchecked in `agent/internal/config/config_test.go`;
  `directive_test.go` discards the 2nd `CanonicalJSON` error and never asserts
  `UserID`; hub `health_test.go` asserts `"ok":true` but not `version`.
- **`agent` healthz** runs `exec.LookPath("tmux")` per request — fine now; move to
  handler-construction time if healthz gets hot.
- **`.dockerignore`** omits `hubd/internal/webui/dist/` — locally-built assets get
  shipped into the build context (then overwritten by the `--from=web` copy).
  Harmless; add it.
- **Dockerfile** has no `go mod download` cache layer (`COPY . /src` before build);
  perf-only. Note the suggested split must not reference a non-existent
  `go.work.sum`.

## Acceptable as-is (noted, no action needed)

- `SetMaxOpenConns(1)` serializes all DB access — correct for M0; revisit for
  concurrent reads under WAL when read traffic grows.
- `contracts.ts` `Window` interface shadows the DOM global `Window` — the name is
  mandated to match the Go DTO; consumers alias on import.
- `webui.Handler()` panics on `fs.Sub` failure — init-time programmer error; panic
  is acceptable.
- The M0 plan was committed in two doc-fix commits (`c5f18af`, `2278efe`) plus the
  per-task code; Task 1 landed in two commits. Cosmetic; squash on merge if desired.
