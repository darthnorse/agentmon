# Cost / usage tracking (feature #1) — design

- **Date:** 2026-07-14
- **Status:** approved after cross-model design review — ready for implementation plan
- **Feature:** #1 per-session → per-epic → per-project token + notional-$ tracking
- **Review provenance:** revised after a Codex (gpt-5.6-sol) cross-model design
  review found 3 confirmed BLOCKERs in the first draft (see §6). All findings
  were independently verified against the repo and live transcript/rollout data
  before folding in.

## 1. Purpose

This is the **instrument** for the pipeline-efficiency program. The whole
requirements-dogfood friction — "is the pipeline too slow / too token-heavy / is
the two-agent split (#4) even needed?" — was judged by gut. #1 turns it into
numbers: which epics, which pipeline **stages**, and which **models** burn the
tokens, and roughly what that is worth in notional dollars.

$ is **notional**: the runners are flat-rate subscriptions (Claude Max, Codex),
so the bill is fixed. Cost is a comparison proxy across models/stages/epics, not
an invoice. The usage-vs-limit "capacity meter" stays a later fast-follow.

## 2. Settled decisions (from brainstorming + review)

1. **Granularity:** per-stage (planning / implementing / reviewing, which
   **recur** across checkpoints) **and** per-attempt **and** per-(provider,model).
2. **Capture:** **multi-source aggregation, enrich-at-report trigger.** As each
   stage report passes the agent's loopback intake, the agent aggregates the
   attempt's cumulative token usage across **all** contributing sources in the
   worktree (parent transcript + child `codex exec` rollouts + `/multi-review`
   subagent transcripts), normalized per provider, and attaches a per-(provider,
   model) snapshot. A best-effort snapshot at session retire closes the tail.
3. **Rate card:** hardcoded Go map (`pricing.go`), per-token-class ($/Mtok for
   fresh-input / output / cache-read / cache-write). Unknown model → tokens
   shown, cost `$—`.
4. **Retry display:** per-attempt → per-stage. Each attempt is its own line with
   outcome + stage breakdown; the epic headline is the grand total.
5. **Storage:** a durable, self-describing `epic_usage` **ledger** of **per-
   boundary, per-(provider,model) cumulative snapshots** — **not** cascade-
   deleted, denormalized identifiers. Cost, per-stage deltas, and time are
   **derived at read**.
6. **Child-session scope:** **full** — the review stage's cost lives mostly in
   child sessions, so v1 aggregates them (that is the point of the feature).
7. **Stats page:** dedicated cross-project stats page is the **immediate fast-
   follow** (data model supports it now). **Per-project rollup is v1.**

## 3. Goals / non-goals

**v1 goals**

- Capture per-`(epic, attempt, stage-boundary, provider, model)` cumulative token
  usage, durably — aggregating parent + child sessions of both providers.
- Provider-correct token normalization (no double-counting).
- Per-epic display: compact total on the card; per-attempt→per-stage (with
  per-model) breakdown in the drawer; wall-clock time alongside tokens.
- Per-project rollup on the project page (totals + by-stage + by-model).
- Notional $ via a hardcoded rate card (cache-read priced at its own rate).
- Failed / canceled / escalated epics captured, including the wasted-cost tail.

**Non-goals (v1)** — deferred, data model must not preclude them:

- Dedicated cross-project stats page (immediate fast-follow).
- Usage-vs-limit capacity meter (later fast-follow).
- Editable rate card (hardcoded is fine — single-owner, notional $).
- Sub-second-accurate cross-stage slicing of a single child session (children
  are short and attributed wholesale to the interval they run in).

## 4. Architecture

Data path mirrors the `require_ci` / `requirements` traversal: **agent capture →
report drain → hub apply → DB ledger → aggregation → API → contract → UI.**

### 4.1 Multi-source capture model (the heart)

An **attempt** is one runner session in one git **worktree** (`p.Workdir` is the
shared base; the runner creates a per-epic worktree, so the worktree path is the
per-attempt key). Its token cost is spread across several files of possibly both
providers:

- **Parent transcript** — the pane's provider (Claude `~/.claude/projects/<enc>/
  <uuid>.jsonl` or Codex `~/.codex/sessions/.../rollout-*.jsonl`).
- **Child `codex exec` rollouts** — plan review + `/multi-review --codex` lens
  (`epic-pipeline.md:150,189`); separate Codex rollouts with `cwd` = worktree.
- **`/multi-review` subagent transcripts** — lens subagents write their own
  Claude transcripts (exact discovery confirmed by the capture-spike, §10).

**Session-safe binding (fixes review Finding 4).** The parent transcript is bound
to the pane's process, **not** "newest file in the cwd dir" — because a Claude
session launched in `p.Workdir` shares its project dir with every concurrent
attempt of the project. Bind via the tmux `pane_pid` → descendant runner process
→ its open transcript fd (`/proc/<pid>/fd`). Child sessions are attributed to the
attempt by `cwd == worktree` **and** activity within the attempt's time window.
(Under `max_parallel == 1` the cwd+window heuristic is already unambiguous; the
process-fd binding is required for correctness at `max_parallel > 1`.)

### 4.2 Provider normalization (fixes review Finding 3 — verified against live data)

Naive summing overcounts badly. Normalization is provider-specific and explicit:

- **Claude:** usage-bearing rows **repeat the same `message.id`** (observed up to
  7×). **Dedup by `message.id`** (one usage record per id) before summing. Map:
  `input = input_tokens`, `output = output_tokens`,
  `cache_read = cache_read_input_tokens`, `cache_write = cache_creation_input_tokens`
  (four disjoint buckets). `model = message.model`.
- **Codex:** `input_tokens` **includes** `cached_input_tokens`
  (`total_tokens = input_tokens + output_tokens`, verified). Store
  `cache_read = cached_input_tokens` and `input = input_tokens − cached_input_tokens`
  (fresh only) so buckets stay disjoint. `output = output_tokens` (includes
  reasoning). `cache_write = 0`. Use the rollout's **cumulative** total object
  (not the interleaved per-turn counter — exact field name pinned in §10).
- Tokens are stored per **(provider, model)** — an attempt legitimately spans
  both (e.g. a Claude runner whose review lens is Codex).

### 4.3 Durable ledger — `epic_usage`

New migration `0010_epic_usage.sql`. Append-only cumulative snapshots, **one row
per (boundary, provider, model)**:

```
epic_usage
  id            TEXT PRIMARY KEY
  project_id    TEXT NOT NULL          -- plain column, NOT a foreign key
  project_name  TEXT NOT NULL          -- denormalized: row stays attributable
  repo          TEXT NOT NULL          -- after DeleteProject
  issue_number  INTEGER NOT NULL
  attempt       INTEGER NOT NULL
  stage         TEXT NOT NULL          -- stage entered at this boundary
  captured_at   TEXT NOT NULL          -- boundary time (agent-stamped report Ts)
  provider      TEXT NOT NULL          -- claude | codex
  model         TEXT NOT NULL
  input_tokens        INTEGER NOT NULL -- CUMULATIVE for this (provider,model)
  output_tokens       INTEGER NOT NULL -- as of this boundary; disjoint buckets
  cache_read_tokens   INTEGER NOT NULL
  cache_write_tokens  INTEGER NOT NULL
  UNIQUE(project_id, issue_number, attempt, stage, captured_at, provider, model)
```

- **Per-boundary, not per-(attempt,stage) (fixes review Finding 1).** Stages
  **recur** (`machine.go:52` allows `reviewing→implementing`; pipeline Step 6
  loops `reviewing → implementing` per checkpoint). Keying by boundary
  (`stage, captured_at`) preserves chronology; the old `UNIQUE(attempt,stage)`
  would overwrite recurring boundaries. Boundaries are minutes apart, so
  second-resolution `captured_at` orders them; a rowid tie-break (as in
  `epic_events`) covers the negligible same-second case.
- **No foreign key**; `DeleteProject` does not touch it. Immutable history;
  denormalized `project_name`/`repo`/`model` keep every row self-describing.
- **Cumulative, not deltas** → idempotent upsert, robust to at-least-once
  redelivery. Per-stage deltas derived at read.

### 4.4 Capture triggers

1. **Enrich-at-report (per boundary).** `agent/internal/report/intake.go`
   `IntakeHandler` already resolves the reporting pane server-side. Add a
   best-effort `UsageCapturer(ctx, socket, pane) ([]shared.Usage, error)` DI seam
   (parallels `SessionResolver`) that runs §4.1–4.2 aggregation and returns one
   `Usage` per (provider, model). Attached to the report; rides the existing
   drain. **Best-effort: any capture error → no usage; the report is never a
   400** (the stage transition is authoritative, usage is narrative).
2. **Reap snapshot (tail) — required.** At session retire (`killEpicSession`,
   hit by merge / cancel / retry), the hub captures one final aggregate before
   the kill (a capture-before-kill in the retire path) and stores it as a
   terminal boundary, `stage` = the epic's stage at retire — this closes the
   active-stage **wasted-cost tail** for hub-driven cancel/stall, which emit no
   report. This is the one added agent operation vs the first draft; it is in
   v1 (owner-confirmed) because capturing failed/canceled wasted cost is a stated
   goal.

Wire type (add to `shared/orchestrator.go`):

```go
type Usage struct {
    Provider   string `json:"provider"`
    Model      string `json:"model"`
    Input      int64  `json:"input"`       // fresh input (cache excluded)
    Output     int64  `json:"output"`
    CacheRead  int64  `json:"cache_read"`
    CacheWrite int64  `json:"cache_write"`
}
// OrchestratorReport gains:
    Usage []Usage `json:"usage,omitempty"`   // one per (provider,model); nil-safe
```

`omitempty` + slice makes it **backward-additive**: old agent sends none, old hub
ignores it (verified: the hub decoder does not reject unknown fields). Mixed
fleet degrades gracefully; deploy order per CLAUDE.md — **rebuild hub first, then
agents.**

### 4.5 Hub apply — ledger upsert

`hubd/internal/orchestrator/orchestrator.go` `applyReport` already resolves
project + epic under `tickMu` (verified). Extend it: for each `Usage` in the
report, upsert one `epic_usage` row.

- **Attempt** from the epic row under the lock (stable); the session name also
  carries it (`SessionNameFor`: attempt 1 has no suffix, `>1` uses `-rN` —
  verified reversible) as a cross-check. Late old-attempt reports are already
  rejected by provenance, so mis-pinning cannot happen.
- `project_name`/`repo` denormalized from the resolved project.
- **Usage upsert is unconditional and best-effort (fixes review Finding 5):** it
  runs even when the stage transition is a no-op (a redelivered report re-writes
  the same idempotent row, **recovering** a prior transient failure), and a
  usage-write error is logged and swallowed — never blocks/reverses the
  transition. We make **no at-least-once guarantee** for usage: a persistent DB
  error loses that one boundary, which the UI shows as a gap, never a wrong
  number.

### 4.6 Pricing + derivation

- `pricing.go`: `map[string]Rate`, `Rate{In, Out, CacheRead, CacheWrite}` $/Mtok.
  `cost = (input·In + output·Out + cache_read·CacheRead + cache_write·CacheWrite)/1e6`.
  Unknown model → nil cost (UI `$—`); tokens always shown.
- **Derivation helper** (single source of truth for epic, project, stats page):
  - order an attempt's boundaries by `captured_at`; per **interval**
    `[bₙ, bₙ₊₁)` compute the cumulative **delta per (provider,model)** and
    attribute it to the stage entered at `bₙ`. Recurring stages sum across their
    intervals.
  - **baseline = 0 (fixes review Finding 6):** the first boundary's cumulative is
    attributed to the first reported stage (captures orientation/worktree
    startup) — the first interval is `[0, b₁]`, not skipped.
  - per-attempt total = its final (reap) cumulative; epic total = Σ attempts.
  - **lower-bound honesty:** an attempt still in-flight (no terminal boundary
    yet), or one whose reap capture failed best-effort, is flagged
    `is_lower_bound` and rendered with a `≥`. A cleanly-retired attempt always
    has its reap boundary and is exact.
  - per-stage / per-attempt **duration** from boundary `captured_at` deltas
    (same boundaries as tokens → correctly segmented per attempt); epic total
    wall-clock from the epic row's `started_at`/`merged_at`.
  - notional cost per stage/attempt/epic/model via the rate card.

Because both token and duration deltas come from **agent-stamped** boundaries and
**same-host** transcripts, there is no cross-clock misattribution.

### 4.7 API + contracts (DB → API → `contracts.ts` → UI)

- **Light rollup inline** on `epicDTO` and `projectDTO`: `{ tokens, cost|null,
  duration_ms }` — one grouped ledger query per project (verified acceptable for
  v1; revisit the unbounded historical scan when volume warrants).
- **`GET /orchestrator/epics/{id}/usage`** → full per-attempt→per-stage→per-model
  breakdown (`EpicUsage`), lazy-fetched on drawer open.
- **`GET /orchestrator/projects/{id}/usage`** → project by-stage + by-model
  summary.
- Fast-follow stats page reuses the derivation helper with no project filter.

`UsageDTO` family (Go + `contracts.ts`, hand-mirrored):

```ts
interface TokenTotals  { input: number; output: number; cache_read: number; cache_write: number; total: number; }
interface ModelUsage   { provider: string; model: string; tokens: TokenTotals; cost: number | null; }
interface UsageStage   { stage: string; duration_ms: number; tokens: TokenTotals; cost: number | null; by_model: ModelUsage[]; }
interface UsageAttempt { attempt: number; outcome: string; duration_ms: number; tokens: TokenTotals; cost: number | null;
                         is_lower_bound: boolean; stages: UsageStage[]; }
interface EpicUsage    { tokens: TokenTotals; cost: number | null; duration_ms: number; by_model: ModelUsage[]; attempts: UsageAttempt[]; }
interface UsageRollup  { tokens: number; cost: number | null; duration_ms: number; } // inline light form
```

### 4.8 Display

1. **Epic card** (`EpicCard.tsx`) — compact: `1.24M tok · ~$3.40 · 38m` from the
   inline rollup; absent usage → omit.
2. **Epic drawer** (`EpicDrawer.tsx`) — lazy `…/epics/{id}/usage`; per-attempt
   (outcome + total) → per-stage (tokens / cost / time) → per-model where an
   attempt spans providers. Failed attempts show wasted cost; `≥` on lower-bounds.
3. **Project page** — rollup: total tokens/$/time + by-stage and **by-model**
   summary from `…/projects/{id}/usage`.
4. **Stats page** — *fast-follow*, ledger + helper already support it.

Follow `dataviz` guidance for any bars/summaries.

## 5. Edge cases & fail-safe behavior

- **Failed / canceled / escalated:** captured; the reap snapshot closes the
  wasted-cost tail (escalated already self-reports a boundary).
- **Capture failure** (missing/unreadable/unknown transcript): silent best-effort
  — report never fails; UI shows `—`.
- **Unknown model:** tokens shown, cost `$—`.
- **Double-count traps:** Claude `message.id` dedup + Codex `cached⊂input` (§4.2)
  — the specific defects the review caught; both covered by parser unit tests.
- **Recurring stages:** boundary-keyed ledger + interval attribution (§4.3/4.6).
- **Parallel attempts:** process-fd binding, not cwd+mtime (§4.1).
- **Mixed fleet:** `usage` backward-additive; hub-first deploy.
- **Model switch within one provider session:** rows are per model, so a switch
  just yields two model rows — no assumption of one model per attempt.

## 6. Design review history (Codex, gpt-5.6-sol, 2026-07-14)

All verified against code/data before folding in. Full output archived by the
run; summary:

- **BLOCKER 1 — recurring stages break `UNIQUE(attempt,stage)`.** Verified
  (`machine.go:52`, pipeline Step 6). → boundary-keyed ledger + interval
  attribution (§4.3, §4.6).
- **BLOCKER 2 — one-transcript read misses child-session review cost.** Verified
  (`epic-pipeline.md:150,189`; 74 Claude + 12 Codex files in one project dir). →
  full multi-source aggregation (§4.1), per-(provider,model) rows.
- **BLOCKER 3 — naive parsers overcount.** Verified on live files (Claude
  `message.id` ×7; Codex `cached⊂input`, `in+out=total`). → provider
  normalization (§4.2).
- **SHOULD-FIX 4 — cwd+mtime not session-safe.** → process-fd binding (§4.1).
- **SHOULD-FIX 5 — swallowed usage write ≠ at-least-once.** → unconditional
  idempotent upsert, explicit best-effort (no guarantee) (§4.5).
- **SHOULD-FIX 6 — baseline/tail coverage.** → baseline 0 + reap snapshot +
  lower-bound labeling (§4.4, §4.6).
- **Verified sound as-is:** intake seam has pane+socket; `applyReport` has
  project+epic under `tickMu`; `SessionNameFor` reverses to attempt; `pr_open`
  correctly closes *final* reviewing; wire field backward-additive; orphan-row
  durability + uniqueness safe across re-import; one grouped query/project OK.

## 7. Testing strategy

- **Go (`make test`):**
  - Parser unit tests on real-shape fixtures: Claude **`message.id` dedup**;
    Codex **`cached⊂input`** + cumulative-vs-per-turn selection; model extraction;
    malformed-line tolerance.
  - Multi-source aggregation: parent + child rollups summed per (provider,model);
    child attributed to the interval it ran in.
  - `UsageCapturer` best-effort: unknown command / missing files → empty, never
    error into the 400 path.
  - Ledger upsert idempotency (redelivery rewrites one row) + unconditional
    recovery when the transition is a no-op.
  - Derivation: interval deltas across **recurring** stages, baseline-0, per-model
    split, rate-card cost (incl. cache-read), lower-bound flag, durations.
  - API: inline rollup + both usage endpoints.
- **Web (`typecheck && test:run`):** `UsageDTO` shapes; card compact + omission;
  drawer per-attempt→per-stage→per-model + `≥`; project by-model rollup.

## 8. Traversal checklist (per CLAUDE.md)

- [ ] Capture-spike (§10) — empirically pin child-transcript discovery + Codex
      cumulative field + fd-binding **before** building the parser.
- [ ] `0010_epic_usage.sql` + `db/usage.go` (upsert, list, aggregate).
- [ ] `shared.Usage` (slice) + `OrchestratorReport.Usage`.
- [ ] Agent multi-source aggregator + provider parsers + fd-binding + intake wiring.
- [ ] Reap snapshot in the retire path.
- [ ] Hub `applyReport` unconditional best-effort upsert.
- [ ] `pricing.go` + derivation helper (intervals, baseline-0, per-model, cost).
- [ ] API: inline rollup + `…/epics/{id}/usage` + `…/projects/{id}/usage`.
- [ ] `contracts.ts`: `UsageDTO` family + rollup fields.
- [ ] UI: EpicCard line, EpicDrawer breakdown, project-page rollup.

## 9. Phasing

- **v1 (this feature):** §8 checklist — full multi-source capture + ledger +
  pricing/derivation + API + epic card/drawer + project-page rollup.
- **Fast-follow (next):** dedicated cross-project `/stats` screen (no schema
  change).
- **Later fast-follow:** usage-vs-limit capacity meter.

## 10. Open empirical unknowns → capture-spike is Plan Task 1

The first-draft spike proved single-file capture but **under-scoped the
multi-source reality**. Before writing the parser, a small spike must pin down,
on live runner data:

1. **Where `/multi-review` subagent (lens) transcripts are written** and how to
   attribute them to the parent attempt (project dir + window + lineage), and
   confirm subagent usage is **not** also inlined in the parent transcript (no
   double-count). Initial probe found no `isSidechain:true` rows in parent files
   — must be confirmed.
2. **The exact Codex rollout cumulative field** (`total_token_usage` object vs
   per-turn `last_token_usage`) so we read the session total, not a turn.
3. **The pane-pid → open-transcript-fd binding** mechanics for the parent, and a
   fallback when `/proc` fd inspection is unavailable.

These are empirical, not design-open; the design above is stable regardless of
their answers.

### Capture-spike finding (2026-07-14)

No live `/multi-review` pipeline run was available to observe during this
spike. Instead, the Step-1 probe was run against the existing transcripts on
this host:

```
files 74 isSidechain rows 0
```

`.jsonl` files under `~/.claude/projects/*agentmon*/` contain **zero** rows
with `"isSidechain":true` — subagent usage is not inlined into the parent
transcript; lens subagents write their own separate files.

**RESOLVED during the cross-model review (2026-07-14):** subagent transcripts
land at `<project-dir>/<parent-session-uuid>/subagents/agent-*.jsonl` (verified
on disk). The first-draft flat glob missed them, so the capturer now also
enumerates the `subagents/` dir derived from each fd-bound parent transcript's
own path (binding subagents to that session); global `message.id` dedup keeps
any overlap safe.

**Why this is non-blocking regardless:** the aggregator's dedup in Task 6 is
**global by `message.id`**, not scoped to a single expected file. Any Claude
transcript in scope — parent or subagent, wherever it lands — has its usage
counted exactly once. So the aggregator's correctness does not depend on
knowing the subagent file's exact location or naming convention ahead of
time; it only depends on the file being *discoverable* within the attempt's
worktree/window scope, which is a separate (already-tracked) empirical
unknown (#3 above, the fd-binding / scope-discovery mechanics).
