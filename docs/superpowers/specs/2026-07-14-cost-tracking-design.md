# Cost / usage tracking (feature #1) — design

- **Date:** 2026-07-14
- **Status:** approved (brainstorm complete) — ready for implementation plan
- **Feature:** #1 per-session → per-epic → per-project token + notional-$ tracking

## 1. Purpose

This is the **instrument** for the pipeline-efficiency program. The whole
requirements-dogfood friction — "is the pipeline too slow / too token-heavy / is
the two-agent split (#4) even needed?" — was judged by gut. #1 turns it into
numbers: which epics, which pipeline **stages**, and which **models** burn the
tokens, and roughly what that is worth in notional dollars.

$ is **notional**: the runners are flat-rate subscriptions (Claude Max, Codex),
so the bill is fixed. Cost is a comparison proxy across models/stages/epics, not
an invoice. The usage-vs-limit "capacity meter" (needs each sub's weekly limit +
cross-session weekly totals) stays a later fast-follow.

The feasibility spike (2026-07-14) already proved capture is tidy: both runners
write per-session token usage to machine-readable JSONL on disk.

## 2. Settled decisions (from brainstorming)

1. **Granularity:** per-stage (planning / implementing / reviewing) **and**
   per-attempt. Per-stage is the point — it answers "is review the token hog?".
2. **Capture mechanism:** **enrich-at-report**. The agent snapshots the
   session's cumulative token usage as each stage report passes through its
   loopback intake; the hub computes per-stage deltas. No LLM change, no report
   CLI change, no new RPC.
3. **Rate card:** hardcoded Go map (`pricing.go`). Unknown model → tokens shown,
   cost `$—`. Re-pricing is a redeploy, not a migration.
4. **Retry display:** per-attempt → per-stage. Each attempt (fresh session /
   transcript) is its own line with its outcome and stage breakdown; the epic
   headline is the grand total across attempts.
5. **Storage:** a durable, self-describing `epic_usage` **ledger** — cumulative
   snapshot rows, **not** cascade-deleted, denormalized identifiers. Cost,
   per-stage deltas, and wall-clock time are all **derived at read**, never
   stored.
6. **Stats page:** dedicated cross-project stats page is the **immediate
   fast-follow** (data model built to support it now, no schema change later).
   The **per-project rollup on the project page is v1**.

## 3. Goals / non-goals

**v1 goals**

- Capture per-`(epic, attempt, stage)` token usage + model, durably.
- Per-epic display: compact total on the card; per-attempt→per-stage breakdown
  in the drawer; wall-clock time alongside tokens.
- Per-project rollup on the project page (totals + by-stage + by-model summary).
- Notional $ via a hardcoded rate card.
- Failed / canceled / escalated epics captured for free (whatever stages
  reported before the end).

**Non-goals (v1)** — deferred, but the data model must not preclude them:

- Dedicated cross-project stats page (immediate fast-follow — pure query + screen).
- Usage-vs-limit capacity meter (later fast-follow — needs weekly sub limits).
- Editable rate card (Settings UI / config) — hardcoded is fine for a
  single-owner self-hosted tool where $ is notional.
- Live/in-flight cost (mid-run) — capture is at stage boundaries only.

## 4. Architecture

Data path mirrors the existing `require_ci` / `requirements` traversal:
**agent capture → report drain → hub apply → DB ledger → aggregation → API →
contract → UI.**

### 4.1 Durable ledger — `epic_usage`

New migration `0010_epic_usage.sql`. An append-only fact table of **cumulative**
token snapshots, one row per stage boundary reported:

```
epic_usage
  id            TEXT PRIMARY KEY
  project_id    TEXT NOT NULL          -- plain column, NOT a foreign key
  project_name  TEXT NOT NULL          -- denormalized: row stays attributable
  repo          TEXT NOT NULL          -- after DeleteProject
  issue_number  INTEGER NOT NULL
  attempt       INTEGER NOT NULL
  stage         TEXT NOT NULL          -- planning|implementing|reviewing|pr_open|escalated
  provider      TEXT NOT NULL          -- claude | codex
  model         TEXT NOT NULL          -- claude-opus-4-8 | gpt-5.6-sol | ...
  input_tokens        INTEGER NOT NULL -- CUMULATIVE session totals as of this
  output_tokens       INTEGER NOT NULL -- boundary (NOT per-stage deltas)
  cache_read_tokens   INTEGER NOT NULL
  cache_write_tokens  INTEGER NOT NULL
  captured_at   TEXT NOT NULL
  UNIQUE(project_id, issue_number, attempt, stage)
```

Design properties:

- **No foreign key** to `projects`/`epics`. This is an immutable historical fact
  table; `DeleteProject` deliberately does **not** touch it, so all-time
  aggregates survive project deletion. Denormalized `project_name`/`repo`/`model`
  keep every row self-describing (a stats screen shows a deleted project's spend).
- **Cumulative, not deltas.** Enrich-at-report naturally produces cumulative
  session totals; storing them makes the write an **idempotent upsert** (the
  at-least-once drain re-delivers; a duplicate carries identical values). Storing
  deltas would require both boundaries at write time and be fragile under
  re-drain / out-of-order. Per-stage deltas are derived at read.
- **Survives board pruning automatically** — the board's 50-epic view is only a
  query limit; `epic_usage` is never pruned.

### 4.2 Capture — enrich-at-report (agent)

Integration point: `agent/internal/report/intake.go` `IntakeHandler`. It already
resolves the reporting pane's session server-side; the same handler gains a
best-effort usage snapshot. The **pane** (from `X-AgentMon-Pane`) gives both cwd
and command directly, so no session→cwd indirection is needed.

New DI seam (parallels the existing `SessionResolver`):

```go
// UsageCapturer reads the cumulative token usage of the runner in `pane` on
// `socket`, best-effort. Returns (nil, nil) when usage cannot be determined —
// intake must never fail because usage was unreadable.
type UsageCapturer func(ctx context.Context, socket, pane string) (*shared.Usage, error)
```

Production implementation:

1. Read the pane's `cwd` + `command` via tmux (`display-message`, reusing the
   discovery helpers).
2. Pick a parser by command: contains `claude` → Claude; contains `codex` → Codex.
3. **Claude:** locate the transcript dir from cwd via Claude Code's project-dir
   encoding (`~/.claude/projects/<encoded-cwd>/`; exact encoding verified in the
   spike — confirm during implementation), take the newest `*.jsonl`, sum
   `.message.usage.{input_tokens,output_tokens,cache_read_input_tokens,cache_creation_input_tokens}`
   over assistant messages; `model` = latest `.message.model`.
4. **Codex:** find the rollout under `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl`
   whose recorded `cwd` matches (newest wins), take the **last** cumulative
   `total_tokens` line (+ `input/output/cached_input/reasoning_output_tokens`);
   `model` from the file.
5. On any error → `(nil, nil)`; log at debug. The report proceeds; the stage
   transition is authoritative, usage is narrative.

The captured snapshot is attached to the buffered `OrchestratorReport` and rides
the existing drain to the hub. **Host-local by construction:** the report passes
through the runner's own host agent, which reads that host's files — no
cross-host access, works identically on the DMZ host.

Wire type (add to `shared/orchestrator.go`):

```go
type Usage struct {
    Provider   string `json:"provider"`
    Model      string `json:"model"`
    Input      int64  `json:"input"`
    Output     int64  `json:"output"`
    CacheRead  int64  `json:"cache_read"`
    CacheWrite int64  `json:"cache_write"`
}
// OrchestratorReport gains:
    Usage *Usage `json:"usage,omitempty"`
```

`omitempty` + pointer makes it **backward-additive**: an old agent sends no
usage; an old hub ignores the field. Mixed fleet degrades gracefully (per the
deploy contract).

### 4.3 Hub apply — ledger upsert

`hubd/internal/orchestrator/orchestrator.go` `applyReport` already resolves the
epic (by repo+issue) and applies the stage transition. Extend it: when
`r.Usage != nil`, upsert one `epic_usage` row.

- **Attempt** is derived from `r.Session` via the inverse of `SessionNameFor`
  (the session name encodes project+issue+attempt), so a late report is pinned to
  the attempt that actually emitted it; fall back to the epic row's `Attempt` if
  unparseable.
- `project_name` / `repo` denormalized from the resolved project.
- Upsert on `UNIQUE(project_id, issue_number, attempt, stage)` — idempotent under
  at-least-once delivery.
- Usage upsert failure is logged and swallowed; it never blocks or reverses the
  stage transition (same "stage authoritative, usage narrative" stance the
  existing `AppendEpicEvent` swallow takes).

### 4.4 Pricing + derivation

- `hubd/internal/orchestrator/pricing.go` (or `db`): `map[string]Rate` where
  `Rate{In, Out, CacheRead, CacheWrite}` is $/Mtok. `Cost = Σ tokens×rate / 1e6`.
  Unknown model → nil cost (UI shows `$—`); tokens always shown.
- **Derivation helper** (single source of truth for epic, project, and the
  future stats page): given ledger rows for a scope, compute
  - per-stage tokens = consecutive cumulative deltas, stages ordered by pipeline
    order (planning→implementing→reviewing→pr_open→escalated); the `pr_open`
    snapshot closes `reviewing`.
  - per-attempt total = its final cumulative snapshot; epic total = Σ attempts.
  - notional cost per stage/attempt/epic via the rate card.
  - **per-stage / per-attempt duration** from the ledger's own `captured_at`
    (Δ between consecutive boundary snapshots **of the same attempt**) — the
    same boundaries as the token diffs, so durations are correctly segmented by
    attempt without having to untangle `epic_events`. Epic total wall-clock uses
    the epic row's `started_at`/`merged_at` (or last−first `captured_at` while
    live).
  - **per-attempt `outcome`**: the last attempt's outcome is the epic's current
    stage (merged/escalated/failed/…); every prior attempt ended in a retry, so
    it is labeled `retried`. No new storage — derived from the epic row + the set
    of attempts present in the ledger.

Because deltas (tokens **and** durations) come from boundary snapshots (not
per-message timestamp bucketing), there is **no cross-clock misattribution** —
the stage boundary is the report event itself.

### 4.5 API + contracts

Traversal: **DB → API DTO → `contracts.ts` → UI.**

- **Light rollup inline** on `epicDTO` and `projectDTO`: `{ tokens, cost|null,
  duration_ms }`. Batch-computed once per project (one grouped ledger query),
  cheap enough for the board snapshot.
- **`GET /orchestrator/epics/{id}/usage`** → full per-attempt→per-stage
  breakdown (`UsageDTO`), fetched lazily when the drawer opens (mirrors how the
  epic plan is fetched on demand).
- **`GET /orchestrator/projects/{id}/usage`** → project by-stage + by-model
  summary for the project page.
- The **fast-follow stats page** reuses the derivation helper with no project
  filter — no schema or capture change.

`UsageDTO` (Go + `contracts.ts`, hand-mirrored per the repo contract):

```ts
interface TokenTotals { input: number; output: number; cache_read: number; cache_write: number; total: number; }
interface UsageStage   { stage: string; tokens: TokenTotals; cost: number | null; duration_ms: number; }
interface UsageAttempt { attempt: number; outcome: string; provider: string; model: string;
                         tokens: TokenTotals; cost: number | null; duration_ms: number; stages: UsageStage[]; }
interface EpicUsage    { tokens: TokenTotals; cost: number | null; duration_ms: number; attempts: UsageAttempt[]; }
interface UsageRollup  { tokens: number; cost: number | null; duration_ms: number; } // inline light form
```

### 4.6 Display

1. **Epic card** (`web/src/components/board/EpicCard.tsx`) — compact one-liner:
   `1.24M tok · ~$3.40 · 38m` from the inline light rollup. Absent usage → omit.
2. **Epic drawer** (`EpicDrawer.tsx`) — lazy-fetch `…/epics/{id}/usage`; render
   per-attempt (outcome + total) → per-stage (tokens / cost / time), matching the
   approved layout. Failed attempts show their wasted cost.
3. **Project page** — project rollup: total tokens/$/time + a small by-stage and
   by-model summary from `…/projects/{id}/usage`.
4. **Stats page** — *fast-follow, not built in v1*; the ledger + derivation
   helper already support it.

Follow `dataviz` guidance for any bars/summary visuals.

## 5. Edge cases & fail-safe behavior

- **Failed / canceled / escalated epics:** captured for free — you get whatever
  stages reported before the end (the wasted-cost signal).
- **Capture failure** (missing/unreadable/unknown-format transcript): silent,
  best-effort — a report never fails; UI shows `—`, never a wrong number.
- **Unknown model:** tokens shown, cost `$—`.
- **Mixed fleet during rollout:** old agents omit `usage`; old hub ignores it.
  Deploy order per CLAUDE.md: **rebuild hub first, then update agents.**
- **Tail after `pr_open`:** tokens spent while idle-awaiting-merge aren't
  attributed to a stage (near-zero); known gap, not chased in v1.
- **Model switch within a session:** assumed single model per attempt (runner
  sessions are); `model` = latest observed. Acceptable for a notional instrument.
- **Agent user vs transcript owner:** capture assumes the agent can read the
  runner's `~/.claude` / `~/.codex` (same host user). Verify during
  implementation; on failure it degrades to the silent best-effort path.

## 6. Testing strategy

- **Go (`make test`):**
  - Parser unit tests against small Claude-transcript and Codex-rollout fixtures
    (cumulative sums, model extraction, malformed-line tolerance).
  - `UsageCapturer` best-effort: unknown command / missing dir → `(nil, nil)`.
  - Ledger upsert idempotency (re-drain delivers identical row once).
  - Attempt-from-session-name parsing + fallback.
  - Derivation: per-stage deltas, per-attempt/epic totals, rate-card cost,
    unknown-model → nil cost, per-stage/per-attempt duration from `captured_at`,
    per-attempt `outcome` (last = epic stage, priors = retried).
  - API handlers: inline rollup + the two usage endpoints.
- **Web (`npm run typecheck && npm run test:run`):** `UsageDTO` contract shape;
  card compact render + absent-usage omission; drawer per-attempt→per-stage
  render; project rollup render.

## 7. Traversal checklist (per CLAUDE.md)

A new field must traverse DB → API → contract → UI:

- [ ] `0010_epic_usage.sql` migration + `db/usage.go` (upsert, list, aggregate).
- [ ] `shared.Usage` + `OrchestratorReport.Usage` (agent + hub share it).
- [ ] Agent `UsageCapturer` + provider parsers + intake wiring.
- [ ] Hub `applyReport` ledger upsert (+ attempt-from-session parse).
- [ ] `pricing.go` rate card + derivation helper.
- [ ] API: inline rollup on `epicDTO`/`projectDTO` + `…/epics/{id}/usage` +
      `…/projects/{id}/usage`.
- [ ] `contracts.ts`: `UsageDTO` family + rollup fields.
- [ ] UI: EpicCard line, EpicDrawer breakdown, project-page rollup.

## 8. Phasing

- **v1 (this feature):** §7 checklist — capture + ledger + pricing/derivation +
  API + epic card/drawer + project-page rollup.
- **Fast-follow (next slice):** dedicated cross-project `/stats` screen — query +
  screen, no schema change.
- **Later fast-follow:** usage-vs-limit capacity meter (weekly sub limits).
