# In-app Artifact Viewer (v1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the owner read a runner-produced `.md` artifact (plan, review evidence) rendered as markdown *in the AgentMon UI*, opened by clicking a path in an escalation note — no SCP/GitHub round-trip.

**Architecture:** Generalize the hub's existing plan proxy. A shared `fetchArtifact` helper (branch → base-branch fallback + error→status mapping) backs both the existing `/plan` route and a new allowlist-guarded `/artifact` route. The web gets a presentational `ArtifactPanel` (extracted from `PlanPanel`) reused by both the plan review and clickable review paths in `EpicDrawer`. The runner pushes its branch before every mid-pipeline escalation so committed artifacts are on GitHub when the UI needs them.

**Tech Stack:** Go (hubd `net/http`, `internal/github` Contents API), React 18 + TS (TanStack Query, `react-markdown` + `remark-gfm`), Vitest + Testing Library, Go stdlib `testing`. Runner "skill" files are prose markdown embedded via `go:embed`.

## Global Constraints

- **Go gate:** `make test` (all 3 modules). If `GOCACHE` is read-only, prefix `GOCACHE=/tmp/agentmon-go-cache`.
- **Web gate:** `cd web && npm run typecheck && npm run test:run`.
- **`web/src/lib/contracts.ts` hand-mirrors Go types** — a new response type is added there by hand (the hub response is a `map[string]string`, so there is no Go `shared` struct to change).
- **Commits:** conventional prefixes (`feat(hubd):`, `feat(web):`, `feat(agent):`). **NEVER** add a `Co-Authored-By:` / AI-attribution trailer.
- **Security boundary (spec §Security):** the `/artifact` path is user-settable; validation is fail-closed — reject `..`, leading `/`, non-`.md`, unsafe chars, and anything not under an allowlisted dir. The endpoint can only ever serve a doc under `docs/plans/` or `docs/reviews/`, via the GitHub Contents API — never the host filesystem.
- **Allowlist:** `var artifactDirs = []string{"docs/plans/", "docs/reviews/"}` (extensible).
- **Runner carrier trust (v1):** push-on-escalate must **never force-push** and must work **only** on the epic branch.

## Decisions resolving the spec's open questions

1. **OQ1 (reuse vs. reimplement plan handler):** extract one shared `fetchArtifact(ctx, w, repo, baseBranch, branch, path)` helper. `/plan` derives `path` server-side via `planDocPath` (web never needs to know the plan path → zero contract drift); `/artifact` validates a query `path`. Both gain the base-branch fallback.
2. **OQ2 (clickable affordance):** matched artifact paths in an event note render as inline `<button>` links; clicking opens `ArtifactPanel` in a drawer-bounded overlay (`<aside>`) with a back button, mirroring the existing `confirmCancel` overlay idiom.

## File Structure

- `hubd/internal/api/orchestrator.go` — add `artifactDirs`, `validateArtifactPath`, shared `fetchArtifact` helper; reimplement `OrchestratorEpicPlanHandler` on the helper; add `OrchestratorEpicArtifactHandler`.
- `hubd/internal/api/router.go` — register the `/artifact` GET route.
- `hubd/internal/api/orchestrator_test.go` — extend `fakeContents` (per-ref responses + call log); add plan base-branch-fallback test + full artifact-handler test.
- `web/src/lib/contracts.ts` — add `EpicArtifactResponse`.
- `web/src/lib/api-client.ts` — add `epicArtifactKey` + `getEpicArtifact`.
- `web/src/components/board/ArtifactPanel.tsx` (new) — presentational markdown viewer (loading/error/GitHub-fallback + optional footer children).
- `web/src/components/board/ArtifactPanel.test.tsx` (new) — renders markdown; error shows GitHub fallback.
- `web/src/components/board/PlanPanel.tsx` — thin caller of `ArtifactPanel` (keeps the "Plan review" heading + Approve button).
- `web/src/components/board/EpicDrawer.tsx` — `EventNote` clickable paths + `selectedArtifact` overlay rendering `ArtifactPanel`; Escape closes overlay first.
- `web/src/components/board/EpicDrawer.test.tsx` — clicking a `docs/reviews/…md` note path opens the panel.
- `agent/internal/runnerfiles/files/claude/epic-pipeline.md` + `.../codex/epic-pipeline.md` — push-on-escalate in the escalation protocol + quick-ref table.

---

### Task 1: Hub — shared `fetchArtifact` helper + plan handler on top of it (with base-branch fallback)

**Files:**
- Modify: `hubd/internal/api/orchestrator.go` (add helper near `planDocPath` ~`:728`; rewrite `OrchestratorEpicPlanHandler` `:733-776`)
- Modify (test scaffold): `hubd/internal/api/orchestrator_test.go` (`fakeContents` `:624-633`)
- Test: `hubd/internal/api/orchestrator_test.go` (new `TestEpicPlanHandlerBaseBranchFallback`)

**Interfaces:**
- Consumes: `d.Contents.GetContents(ctx, repo, path, ref)`, `github.ErrNotFound`, `github.ErrTooLarge`, `writeJSON`, `writeJSONError`.
- Produces: `func (d Deps) fetchArtifact(ctx context.Context, w http.ResponseWriter, repo, baseBranch, branch, path string)` — writes the `{path,ref,markdown}` JSON or the mapped error status. `fakeContents` gains `byRef map[string]refResp` (per-ref override) and `refsSeen []string` (call log); `type refResp struct { body []byte; err error }`.

- [ ] **Step 1: Extend the test fake to support per-ref responses (needed to test fallback)**

In `orchestrator_test.go`, replace the `fakeContents` type + method (`:624-633`) with:

```go
type refResp struct {
	body []byte
	err  error
}

type fakeContents struct {
	body            []byte
	err             error
	repo, path, ref string
	byRef           map[string]refResp // optional per-ref override; nil → use flat body/err
	refsSeen        []string           // every ref GetContents was called with, in order
}

func (f *fakeContents) GetContents(_ context.Context, repo, path, ref string) ([]byte, error) {
	f.repo, f.path, f.ref = repo, path, ref
	f.refsSeen = append(f.refsSeen, ref)
	if f.byRef != nil {
		if r, ok := f.byRef[ref]; ok {
			return r.body, r.err
		}
	}
	return f.body, f.err
}
```

- [ ] **Step 2: Write the failing base-branch-fallback test**

Append to `orchestrator_test.go`:

```go
// A completed epic's branch is deleted post-merge, so the plan 404s on
// e.Branch but exists on the base branch — the handler must fall back.
func TestEpicPlanHandlerBaseBranchFallback(t *testing.T) {
	database := orchDB(t)
	ctx := context.Background()
	database.CreateProject(ctx, db.Project{ID: "p1", Name: "p", Repo: "o/r", ServerID: "h1", Workdir: "/w", BaseBranch: "main", Provider: "claude", MaxParallel: 1})
	e, _ := database.UpsertEpicIssue(ctx, db.Epic{ProjectID: "p1", IssueNumber: 7, IssueState: "open", QueuedAt: "t", StageUpdatedAt: "t"})
	if ok, err := database.SetEpicPR(ctx, e.ID, 0, "epic/7-x"); err != nil || !ok {
		t.Fatalf("SetEpicPR: ok=%v err=%v", ok, err)
	}

	fc := &fakeContents{byRef: map[string]refResp{
		"epic/7-x": {err: github.ErrNotFound},
		"main":     {body: []byte("# Merged Plan")},
	}}
	d := Deps{DB: database, Orch: &fakeOrch{}, Contents: fc}

	r, w := orchReq("GET", "/api/v1/orchestrator/projects/p1/epics/"+e.ID+"/plan", "")
	r.SetPathValue("id", "p1")
	r.SetPathValue("epicID", e.ID)
	d.OrchestratorEpicPlanHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("fallback = %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"markdown":"# Merged Plan"`) || !strings.Contains(w.Body.String(), `"ref":"main"`) {
		t.Fatalf("expected base-branch plan, got %s", w.Body.String())
	}
	if len(fc.refsSeen) != 2 || fc.refsSeen[0] != "epic/7-x" || fc.refsSeen[1] != "main" {
		t.Fatalf("expected branch-then-base fetch order, got %v", fc.refsSeen)
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./hubd/internal/api/ -run TestEpicPlanHandlerBaseBranchFallback -v`
Expected: FAIL — the current handler does not fall back (gets `"ref":"epic/7-x"` and a 404, and `refsSeen` has length 1).

- [ ] **Step 4: Add the shared `fetchArtifact` helper**

In `orchestrator.go`, insert immediately after `planDocPath` (after `:728`):

```go
// fetchArtifact resolves a repo-relative .md doc for an epic and writes the
// {path,ref,markdown} JSON response (or the mapped error status). It tries the
// epic branch first, then falls back to the project base branch on ErrNotFound:
// in-flight artifacts live on the branch; a merged epic's live on the base
// branch (the branch is often deleted post-merge). Callers must have validated
// `path`, confirmed branch != "" and d.Contents != nil.
func (d Deps) fetchArtifact(ctx context.Context, w http.ResponseWriter, repo, baseBranch, branch, path string) {
	b, err := d.Contents.GetContents(ctx, repo, path, branch)
	ref := branch
	if errors.Is(err, github.ErrNotFound) && baseBranch != "" && baseBranch != branch {
		if b2, err2 := d.Contents.GetContents(ctx, repo, path, baseBranch); err2 == nil {
			b, err, ref = b2, nil, baseBranch
		}
	}
	switch {
	case errors.Is(err, github.ErrNotFound):
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("artifact not available at %s (may not be pushed yet)", path))
	case errors.Is(err, github.ErrTooLarge):
		writeJSONError(w, http.StatusRequestEntityTooLarge, "artifact exceeds 256 KiB — open it on GitHub")
	case err != nil:
		log.Printf("api: epic artifact fetch: %v", err)
		writeJSONError(w, http.StatusBadGateway, "artifact fetch failed")
	default:
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]string{"path": path, "ref": ref, "markdown": string(b)})
	}
}
```

- [ ] **Step 5: Reimplement the plan handler on the helper**

Replace the body of `OrchestratorEpicPlanHandler` (`:733-776`) from the `p, err := d.DB.GetProject(...)` block through the end of the closure. The final resolution changes from the inline `GetContents` + `switch` to a single `fetchArtifact` call:

```go
func (d Deps) OrchestratorEpicPlanHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, ok := d.authorizeOr403(w, r, authz.OrchestratorView, "project:"+id); !ok {
			return
		}
		if d.Contents == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "orchestrator disabled")
			return
		}
		e, err := d.DB.GetEpic(r.Context(), r.PathValue("epicID"))
		switch {
		case errors.Is(err, sql.ErrNoRows) || (err == nil && e.ProjectID != id):
			writeJSONError(w, http.StatusNotFound, "epic not found in project")
			return
		case err != nil:
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if e.Branch == "" {
			writeJSONError(w, http.StatusConflict, "epic has no branch yet — the plan is committed once the runner starts")
			return
		}
		p, err := d.DB.GetProject(r.Context(), id)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		d.fetchArtifact(r.Context(), w, p.Repo, p.BaseBranch, e.Branch, planDocPath(e.Needs, e.IssueNumber))
	}
}
```

- [ ] **Step 6: Run the fallback test + the whole existing plan suite to verify green**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./hubd/internal/api/ -run 'TestEpicPlanHandler|TestPlanDocPath|TestEpicPlanHandlerBaseBranchFallback' -v`
Expected: PASS. (The existing `TestEpicPlanHandler` still passes: its 200 case makes one call with err=nil — no fallback; its 404/413/502 loop uses flat `body`/`err` so the fallback re-fetch returns the same error.)

- [ ] **Step 7: Commit**

```bash
git add hubd/internal/api/orchestrator.go hubd/internal/api/orchestrator_test.go
git commit -m "feat(hubd): extract fetchArtifact helper with base-branch fallback"
```

---

### Task 2: Hub — allowlist-guarded `/artifact` endpoint

**Files:**
- Modify: `hubd/internal/api/orchestrator.go` (add `artifactDirs`, `validateArtifactPath`, `OrchestratorEpicArtifactHandler`)
- Modify: `hubd/internal/api/router.go` (`:61` — add the route after the plan route)
- Test: `hubd/internal/api/orchestrator_test.go` (new `TestValidateArtifactPath`, `TestEpicArtifactHandler`)

**Interfaces:**
- Consumes: `fetchArtifact` (Task 1), `fakeContents` with `byRef`/`refsSeen` (Task 1), `authz.OrchestratorView`, `d.authorizeOr403`.
- Produces: `func validateArtifactPath(p string) bool`; `func (d Deps) OrchestratorEpicArtifactHandler() http.HandlerFunc`; route `GET /api/v1/orchestrator/projects/{id}/epics/{epicID}/artifact`.

- [ ] **Step 1: Write the failing path-validation unit test**

Append to `orchestrator_test.go`:

```go
func TestValidateArtifactPath(t *testing.T) {
	for p, want := range map[string]bool{
		"docs/plans/epic-7.md":         true,
		"docs/reviews/epic-7-final.md": true,
		"docs/reviews/sub/r.md":        true,  // nested under an allowlisted dir is fine
		"docs/plans/epic-7":            false, // not .md
		"docs/reviews/../secrets.md":   false, // traversal
		"/etc/passwd":                  false, // leading slash (and not allowlisted)
		"/docs/reviews/x.md":           false, // leading slash
		"src/main.go":                  false, // not allowlisted, not .md
		"docs/other/x.md":              false, // .md but not an allowlisted dir
		"docs/reviews/a b.md":          false, // space fails the safe-char regex
		"":                             false, // empty
	} {
		if got := validateArtifactPath(p); got != want {
			t.Fatalf("validateArtifactPath(%q) = %v, want %v", p, got, want)
		}
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./hubd/internal/api/ -run TestValidateArtifactPath -v`
Expected: FAIL — `undefined: validateArtifactPath` (compile error).

- [ ] **Step 3: Add the allowlist + validator**

In `orchestrator.go`, insert after the `planDirPrefix` const (`:706`):

```go
// artifactDirs is the fail-closed allowlist for the generic artifact proxy.
// The endpoint can only ever serve a doc under one of these dirs, via the
// GitHub Contents API — never the host filesystem. Extensible.
var artifactDirs = []string{"docs/plans/", "docs/reviews/"}

// validateArtifactPath applies the plan-proxy's fail-closed rules to a
// user-supplied path: bounded length, no leading slash, no traversal, safe
// chars (planPathRe), .md only, and under an allowlisted artifact dir. This is
// the security boundary (spec §Security) — false → 400.
func validateArtifactPath(p string) bool {
	if p == "" || len(p) > 512 || strings.HasPrefix(p, "/") || strings.Contains(p, "..") ||
		!strings.HasSuffix(p, ".md") || !planPathRe.MatchString(p) {
		return false
	}
	for _, dir := range artifactDirs {
		if strings.HasPrefix(p, dir) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run the validator test to verify it passes**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./hubd/internal/api/ -run TestValidateArtifactPath -v`
Expected: PASS.

- [ ] **Step 5: Write the failing handler test**

Append to `orchestrator_test.go`:

```go
func TestEpicArtifactHandler(t *testing.T) {
	database := orchDB(t)
	ctx := context.Background()
	database.CreateProject(ctx, db.Project{ID: "p1", Name: "p", Repo: "o/r", ServerID: "h1", Workdir: "/w", BaseBranch: "main", Provider: "claude", MaxParallel: 1})
	e, _ := database.UpsertEpicIssue(ctx, db.Epic{ProjectID: "p1", IssueNumber: 7, IssueState: "open", QueuedAt: "t", StageUpdatedAt: "t"})

	fc := &fakeContents{body: []byte("# Review")}
	d := Deps{DB: database, Orch: &fakeOrch{}, Contents: fc}

	call := func(project, path string) *httptest.ResponseRecorder {
		r, w := orchReq("GET", "/api/v1/orchestrator/projects/"+project+"/epics/"+e.ID+"/artifact?path="+url.QueryEscape(path), "")
		r.SetPathValue("id", project)
		r.SetPathValue("epicID", e.ID)
		d.OrchestratorEpicArtifactHandler()(w, r)
		return w
	}

	// No branch yet → 409 (guard runs before the fetch).
	if w := call("p1", "docs/reviews/epic-7-final.md"); w.Code != 409 {
		t.Fatalf("branchless = %d %s", w.Code, w.Body.String())
	}
	if ok, err := database.SetEpicPR(ctx, e.ID, 0, "epic/7-x"); err != nil || !ok {
		t.Fatalf("SetEpicPR: ok=%v err=%v", ok, err)
	}

	// Allowlisted docs/reviews path → 200, fetched off the branch.
	w := call("p1", "docs/reviews/epic-7-final.md")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"markdown":"# Review"`) {
		t.Fatalf("review = %d %s", w.Code, w.Body.String())
	}
	if fc.path != "docs/reviews/epic-7-final.md" || fc.ref != "epic/7-x" {
		t.Fatalf("fetch args %+v", fc)
	}

	// Fail-closed rejects → 400, and never touch GitHub.
	for _, bad := range []string{"docs/reviews/../secrets.md", "/etc/passwd", "src/main.go", "docs/plans/epic-7"} {
		fc.path = "" // reset the recorder
		if w := call("p1", bad); w.Code != 400 {
			t.Fatalf("bad path %q = %d, want 400", bad, w.Code)
		}
		if fc.path != "" {
			t.Fatalf("bad path %q reached GitHub (fetched %q)", bad, fc.path)
		}
	}

	// Missing path param → 400.
	if w := call("p1", ""); w.Code != 400 {
		t.Fatalf("empty path = %d, want 400", w.Code)
	}

	// Wrong project → 404 (cross-project guard).
	if w := call("p2", "docs/reviews/epic-7-final.md"); w.Code != 404 {
		t.Fatalf("cross-project = %d", w.Code)
	}

	// Not on branch, present on base → 404 falls back to 200.
	fc.byRef = map[string]refResp{"epic/7-x": {err: github.ErrNotFound}, "main": {body: []byte("# Base Review")}}
	w = call("p1", "docs/reviews/epic-7-final.md")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"ref":"main"`) {
		t.Fatalf("fallback = %d %s", w.Code, w.Body.String())
	}

	// GitHub error shapes: not-found on both refs → 404; too-large → 413.
	fc.byRef = nil
	fc.err = github.ErrNotFound
	if w := call("p1", "docs/reviews/epic-7-final.md"); w.Code != 404 {
		t.Fatalf("not-found = %d", w.Code)
	}
	fc.err = github.ErrTooLarge
	if w := call("p1", "docs/reviews/epic-7-final.md"); w.Code != 413 {
		t.Fatalf("too-large = %d", w.Code)
	}

	// Contents unset (dormant) → 503.
	d.Contents = nil
	if w := call("p1", "docs/reviews/epic-7-final.md"); w.Code != 503 {
		t.Fatalf("dormant = %d", w.Code)
	}
}
```

Then ensure `net/url` is imported in the test file (add `"net/url"` to the test import block if not already present).

- [ ] **Step 6: Run it to verify it fails**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./hubd/internal/api/ -run TestEpicArtifactHandler -v`
Expected: FAIL — `undefined: OrchestratorEpicArtifactHandler` (compile error).

- [ ] **Step 7: Add the artifact handler**

In `orchestrator.go`, insert after `OrchestratorEpicPlanHandler` (after `:776`):

```go
// OrchestratorEpicArtifactHandler proxies any allowlisted committed .md
// artifact (plan or review evidence) off the epic branch — or the base branch
// for merged epics (spec §1). The `path` query param is user-settable, so it is
// validated fail-closed against artifactDirs BEFORE any GitHub access: the
// endpoint can only ever read docs/plans/ or docs/reviews/, never the host FS.
func (d Deps) OrchestratorEpicArtifactHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, ok := d.authorizeOr403(w, r, authz.OrchestratorView, "project:"+id); !ok {
			return
		}
		if d.Contents == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "orchestrator disabled")
			return
		}
		path := r.URL.Query().Get("path")
		if !validateArtifactPath(path) {
			writeJSONError(w, http.StatusBadRequest, "invalid or disallowed artifact path")
			return
		}
		e, err := d.DB.GetEpic(r.Context(), r.PathValue("epicID"))
		switch {
		case errors.Is(err, sql.ErrNoRows) || (err == nil && e.ProjectID != id):
			writeJSONError(w, http.StatusNotFound, "epic not found in project")
			return
		case err != nil:
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if e.Branch == "" {
			writeJSONError(w, http.StatusConflict, "epic has no branch yet")
			return
		}
		p, err := d.DB.GetProject(r.Context(), id)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		d.fetchArtifact(r.Context(), w, p.Repo, p.BaseBranch, e.Branch, path)
	}
}
```

Note: the fail-closed `validateArtifactPath` guard runs before `GetEpic`; the `e.Branch == ""` 409 guard runs before the fetch — the test relies on both orderings.

- [ ] **Step 8: Register the route**

In `router.go`, after `:61` (the plan route), add:

```go
	mux.Handle("GET /api/v1/orchestrator/projects/{id}/epics/{epicID}/artifact", rd.Auth.RequireAuth(rd.API.OrchestratorEpicArtifactHandler()))
```

- [ ] **Step 9: Run the artifact suite + the full api package to verify green**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./hubd/internal/api/ -run 'Artifact|Plan' -v && GOCACHE=/tmp/agentmon-go-cache go test ./hubd/...`
Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add hubd/internal/api/orchestrator.go hubd/internal/api/router.go hubd/internal/api/orchestrator_test.go
git commit -m "feat(hubd): add allowlist-guarded epic artifact proxy endpoint"
```

---

### Task 3: Web — presentational `ArtifactPanel` + `PlanPanel` as a thin caller

**Files:**
- Modify: `web/src/lib/contracts.ts` (`:74` region — add `EpicArtifactResponse`)
- Create: `web/src/components/board/ArtifactPanel.tsx`
- Create: `web/src/components/board/ArtifactPanel.test.tsx`
- Modify: `web/src/components/board/PlanPanel.tsx` (`:1-58` — refactor to use `ArtifactPanel`)
- Test: existing `web/src/components/board/PlanPanel.test.tsx` must still pass unchanged.

**Interfaces:**
- Consumes: `useQuery`, `ReactMarkdown`, `remarkGfm`, `ApiError`, `epicPlanKey`, `getEpicPlan`.
- Produces: `interface EpicArtifactResponse { path: string; ref: string; markdown: string; }`; `function ArtifactPanel(props: { queryKey: readonly unknown[]; queryFn: () => Promise<EpicArtifactResponse>; branchUrl: string; children?: ReactNode }): JSX.Element`.

- [ ] **Step 1: Add the contract type**

In `contracts.ts`, directly below `EpicPlanResponse` (`:74`), add:

```ts
export interface EpicArtifactResponse { path: string; ref: string; markdown: string; }
```

- [ ] **Step 2: Write the failing ArtifactPanel test**

Create `web/src/components/board/ArtifactPanel.test.tsx`:

```tsx
import { render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";

import { ArtifactPanel } from "@/components/board/ArtifactPanel";
import { ApiError } from "@/lib/api-client";

const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
const wrapper = ({ children }: { children: ReactNode }) => (
  <QueryClientProvider client={qc}>{children}</QueryClientProvider>
);

describe("ArtifactPanel", () => {
  it("renders the fetched markdown with path/ref and any footer children", async () => {
    const queryFn = vi.fn().mockResolvedValue({ path: "docs/reviews/r.md", ref: "epic/7-x", markdown: "# Review\n\n- one" });
    render(
      <ArtifactPanel queryKey={["t", "ok"]} queryFn={queryFn} branchUrl="https://github.com/o/r/tree/epic/7-x">
        <button>Approve</button>
      </ArtifactPanel>, { wrapper },
    );
    await waitFor(() => expect(screen.getByRole("heading", { name: "Review" })).toBeInTheDocument());
    expect(screen.getByText(/docs\/reviews\/r.md @ epic\/7-x/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Approve" })).toBeInTheDocument();
  });

  it("shows the error message and a GitHub fallback link on failure", async () => {
    const queryFn = vi.fn().mockRejectedValue(new ApiError(404, "artifact not available at docs/reviews/r.md (may not be pushed yet)"));
    render(
      <ArtifactPanel queryKey={["t", "err"]} queryFn={queryFn} branchUrl="https://github.com/o/r/tree/epic/7-x" />, { wrapper },
    );
    await waitFor(() => expect(screen.getByText(/artifact not available/)).toBeInTheDocument());
    expect(screen.getByRole("link", { name: /View the branch on GitHub/ })).toHaveAttribute("href", "https://github.com/o/r/tree/epic/7-x");
  });
});
```

- [ ] **Step 3: Run it to verify it fails**

Run: `cd web && npx vitest run src/components/board/ArtifactPanel.test.tsx`
Expected: FAIL — cannot resolve `@/components/board/ArtifactPanel`.

- [ ] **Step 4: Create `ArtifactPanel.tsx`**

```tsx
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import type { ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { ApiError } from "@/lib/api-client";
import type { EpicArtifactResponse } from "@/lib/contracts";

// Presentational markdown viewer for a runner artifact (plan or review).
// The caller injects the query (key + fn returning {path,ref,markdown}) and the
// GitHub fallback URL; ArtifactPanel owns the loading/error/render states and an
// optional footer (e.g. the plan's "Approve plan" button). Reviewing from a
// phone is the whole point — real markdown, not a <pre>.
export function ArtifactPanel({ queryKey, queryFn, branchUrl, children }: {
  queryKey: readonly unknown[];
  queryFn: () => Promise<EpicArtifactResponse>;
  branchUrl: string;
  children?: ReactNode;
}) {
  const q = useQuery({ queryKey, queryFn, staleTime: 30_000, retry: false });

  if (q.isLoading) return <div className="text-xs text-muted-foreground">Loading…</div>;
  if (q.isError) {
    return (
      <div className="rounded-md border border-border bg-card p-3 text-xs text-muted-foreground">
        <div>{q.error instanceof ApiError ? q.error.message : "Couldn't load the artifact."}</div>
        <a href={branchUrl} target="_blank" rel="noreferrer" className="mt-1 inline-block text-primary underline">
          View the branch on GitHub ↗
        </a>
      </div>
    );
  }
  if (!q.data) return null;
  return (
    <>
      <div className="font-mono text-[11px] text-muted-foreground">{q.data.path} @ {q.data.ref}</div>
      <div className="markdown max-h-[50vh] overflow-y-auto rounded-md border border-border bg-background p-3">
        <ReactMarkdown remarkPlugins={[remarkGfm]}>{q.data.markdown}</ReactMarkdown>
      </div>
      {children}
    </>
  );
}
```

- [ ] **Step 5: Run the ArtifactPanel test to verify it passes**

Run: `cd web && npx vitest run src/components/board/ArtifactPanel.test.tsx`
Expected: PASS (both cases).

- [ ] **Step 6: Refactor `PlanPanel.tsx` to a thin `ArtifactPanel` caller**

Rewrite the whole file (the old `useQuery`/`ReactMarkdown`/`remarkGfm`/`ApiError` imports and inline render are all dropped — `ArtifactPanel` owns them now). `web/src/components/board/PlanPanel.tsx` becomes:

```tsx
import { ConfirmButton } from "@/components/board/ConfirmButton";
import { ArtifactPanel } from "@/components/board/ArtifactPanel";
import { useEpicActions } from "@/hooks/useEpicActions";
import { epicPlanKey, getEpicPlan } from "@/lib/api-client";
import type { EpicDTO, ProjectDTO } from "@/lib/contracts";

// Plan review "plan mode" (spec §8.2): render the plan committed on the epic
// branch via the shared ArtifactPanel, plus the approve control.
//
// APPROVAL MECHANISM (verified against the runner skill + Orchestrator):
// a plan-gate epic is `escalated` with NO PR, so `Approve()` — which requires
// PRNumber>0 and merges — returns "no PR to merge" (orchestrator.go). The
// runner's epic-pipeline skill resumes past a plan gate on RETRY: a fresh
// session's assess-artifacts step finds the committed plan and continues.
// So "Approve plan" fires the RETRY action, not approve.
export function PlanPanel({ epic, project }: { epic: EpicDTO; project: ProjectDTO }) {
  const { act, busy } = useEpicActions(epic.project_id);
  const branchUrl = epic.branch
    ? `https://github.com/${project.repo}/tree/${epic.branch}`
    : `https://github.com/${project.repo}`;

  return (
    <section className="flex flex-col gap-2">
      <div className="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">Plan review</div>
      <ArtifactPanel
        queryKey={epicPlanKey(epic.project_id, epic.id)}
        queryFn={() => getEpicPlan(epic.project_id, epic.id)}
        branchUrl={branchUrl}
      >
        <ConfirmButton label="Approve plan" confirmLabel="Approve — runner resumes?" variant="default"
          className="self-start" disabled={busy !== null}
          onConfirm={() => void act({ action: "retry", epic_id: epic.id }, `Plan approved — #${epic.issue} resumes`)} />
      </ArtifactPanel>
    </section>
  );
}
```

(`getEpicPlan` returns `EpicPlanResponse`, structurally identical to `EpicArtifactResponse`, so it satisfies `ArtifactPanel`'s `queryFn` prop.)

- [ ] **Step 7: Run the existing PlanPanel test + ArtifactPanel test + typecheck**

Run: `cd web && npx vitest run src/components/board/PlanPanel.test.tsx src/components/board/ArtifactPanel.test.tsx && npm run typecheck`
Expected: PASS — the existing `PlanPanel.test.tsx` (heading, `path @ ref`, "Approve plan" button, 404 message + GitHub link) still passes because `ArtifactPanel` renders the injected query's result and the Approve button as its footer.

- [ ] **Step 8: Commit**

```bash
git add web/src/lib/contracts.ts web/src/components/board/ArtifactPanel.tsx web/src/components/board/ArtifactPanel.test.tsx web/src/components/board/PlanPanel.tsx
git commit -m "feat(web): extract ArtifactPanel, make PlanPanel a thin caller"
```

---

### Task 4: Web — clickable artifact paths in `EpicDrawer` + drawer overlay

**Files:**
- Modify: `web/src/lib/api-client.ts` (`:135` region — add `epicArtifactKey`; `:147` region — add `getEpicArtifact`; `:4` import add `EpicArtifactResponse`)
- Modify: `web/src/components/board/EpicDrawer.tsx` (add `EventNote`, `selectedArtifact` state + overlay, note rendering `:240`, Escape handler `:125-129`)
- Test: `web/src/components/board/EpicDrawer.test.tsx` (add the mock + a new test)

**Interfaces:**
- Consumes: `ArtifactPanel` (Task 3), `EpicArtifactResponse` (Task 3), `request`.
- Produces: `epicArtifactKey(projectId, epicId, path) => readonly ["epic-artifact", string, string, string]`; `getEpicArtifact(projectId, epicId, path) => Promise<EpicArtifactResponse>`; `function EpicNote`-style `EventNote({ note, onOpen }: { note: string; onOpen(path: string): void })` (local to EpicDrawer).

- [ ] **Step 1: Add the api-client key + fetcher**

In `api-client.ts`, add `EpicArtifactResponse` to the type import block (`:4`), e.g. append it to the existing `EpicPlanResponse, ...` line. Then add the key beside `epicPlanKey` (`:135`):

```ts
export const epicArtifactKey = (projectId: string, epicId: string, path: string) =>
  ["epic-artifact", projectId, epicId, path] as const;
```

And the fetcher beside `getEpicPlan` (`:147-151`):

```ts
export const getEpicArtifact = (projectId: string, epicId: string, path: string) =>
  request<EpicArtifactResponse>(
    "GET",
    `/orchestrator/projects/${encodeURIComponent(projectId)}/epics/${encodeURIComponent(epicId)}/artifact?path=${encodeURIComponent(path)}`,
  );
```

- [ ] **Step 2: Write the failing EpicDrawer test (clickable review path opens the panel)**

In `EpicDrawer.test.tsx`, add `getEpicArtifact: vi.fn()` to the hoisted `h` object (`:6-14`), add `getEpicArtifact: h.getEpicArtifact` to the `@/lib/api-client` mock return (`:16-20`), and reset it in `beforeEach` (add `h.getEpicArtifact.mockReset();`). Then append this test inside the `describe`:

```tsx
it("opens a clickable review artifact from an event note", async () => {
  h.getProjectBoard.mockResolvedValue({
    project, epics: [],
    events: { e1: [{ from: "reviewing", to: "escalated", source: "report",
      note: "DISCUSS unresolved — see docs/reviews/epic-15-review.md", ts: "2026-07-11T09:00:00Z" }] },
  });
  h.getEpicArtifact.mockResolvedValue({ path: "docs/reviews/epic-15-review.md", ref: "epic/15-x", markdown: "# Review\n\n- finding one" });

  render(<EpicDrawer epic={epic({})} project={project} onClose={() => {}} />, { wrapper });

  const link = await screen.findByRole("button", { name: "docs/reviews/epic-15-review.md" });
  fireEvent.click(link);

  await waitFor(() => expect(h.getEpicArtifact).toHaveBeenCalledWith("p1", "e1", "docs/reviews/epic-15-review.md"));
  await waitFor(() => expect(screen.getByRole("heading", { name: "Review" })).toBeInTheDocument());
});
```

- [ ] **Step 3: Run it to verify it fails**

Run: `cd web && npx vitest run src/components/board/EpicDrawer.test.tsx -t "clickable review artifact"`
Expected: FAIL — no button with that name (the note renders as plain text).

- [ ] **Step 4: Add the `EventNote` component + imports in `EpicDrawer.tsx`**

Add to the imports at the top of `EpicDrawer.tsx`: extend the `@/lib/api-client` import (`:12-14`) with `epicArtifactKey, getEpicArtifact`, and add `import { ArtifactPanel } from "@/components/board/ArtifactPanel";` near the other board imports (`:7`).

Add this component above `EpicDrawer` (e.g. after `UsageBreakdown`, `:81`):

```tsx
// Recognized runner artifacts committed under docs/plans|reviews. Matches the
// hub allowlist (artifactDirs) so a clicked path always validates server-side.
const ARTIFACT_PATH_RE = /docs\/(?:plans|reviews)\/[\w./-]+\.md/g;

// Renders an escalation note, turning any recognized artifact path into a
// clickable control that opens the in-app viewer (spec §3).
function EventNote({ note, onOpen }: { note: string; onOpen(path: string): void }) {
  const parts: React.ReactNode[] = [];
  let last = 0;
  for (const m of note.matchAll(ARTIFACT_PATH_RE)) {
    const start = m.index ?? 0;
    if (start > last) parts.push(note.slice(last, start));
    parts.push(
      <button key={start} type="button" onClick={() => onOpen(m[0])}
        className="text-primary underline underline-offset-2 hover:no-underline">
        {m[0]}
      </button>,
    );
    last = start + m[0].length;
  }
  if (last < note.length) parts.push(note.slice(last));
  return <span className="text-muted-foreground">· {parts}</span>;
}
```

- [ ] **Step 5: Wire `selectedArtifact` state, the note rendering, and the overlay**

In `EpicDrawer`, add the state near the other `useState`s (`:94-95`):

```tsx
  const [selectedArtifact, setSelectedArtifact] = React.useState<string | null>(null);
```

Update the Escape handler (`:125-129`) so the overlay closes first:

```tsx
  React.useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      if (selectedArtifact) setSelectedArtifact(null);
      else onClose();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose, selectedArtifact]);
```

Replace the event-note render (`:240`) — change:

```tsx
                    {ev.note && <span className="truncate text-muted-foreground" title={ev.note}>· {ev.note}</span>}
```

to:

```tsx
                    {ev.note && <EventNote note={ev.note} onOpen={setSelectedArtifact} />}
```

Add the overlay as a sibling of the main `<aside>`, immediately after its closing `</aside>` (`:265`) and before the `confirmCancel` block:

```tsx
      {selectedArtifact && (
        <aside className="absolute inset-y-0 right-0 z-20 flex w-full flex-col border-l border-border bg-background sm:max-w-[560px] lg:max-w-[50vw]"
          role="dialog" aria-modal="true" aria-label={`Artifact ${selectedArtifact}`}>
          <div className="flex items-center gap-2 border-b border-border p-4">
            <Button variant="ghost" size="sm" className="-ml-2 flex-none" onClick={() => setSelectedArtifact(null)} aria-label="back">←</Button>
            <span className="truncate font-mono text-xs text-muted-foreground">{selectedArtifact}</span>
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto p-4">
            <section className="flex flex-col gap-2">
              <ArtifactPanel
                queryKey={epicArtifactKey(epic.project_id, epic.id, selectedArtifact)}
                queryFn={() => getEpicArtifact(epic.project_id, epic.id, selectedArtifact)}
                branchUrl={epic.branch ? `https://github.com/${project.repo}/tree/${epic.branch}` : `https://github.com/${project.repo}`}
              />
            </section>
          </div>
        </aside>
      )}
```

- [ ] **Step 6: Run the new test + the full drawer suite to verify green**

Run: `cd web && npx vitest run src/components/board/EpicDrawer.test.tsx`
Expected: PASS — including the existing tests (the truncate-span replacement keeps the note text; the new `EventNote` still renders `· <note>`).

- [ ] **Step 7: Full web gate**

Run: `cd web && npm run typecheck && npm run test:run`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add web/src/lib/api-client.ts web/src/components/board/EpicDrawer.tsx web/src/components/board/EpicDrawer.test.tsx
git commit -m "feat(web): clickable artifact paths in EpicDrawer with in-app viewer"
```

---

### Task 5: Runner — push branch before every mid-pipeline escalation (both variants)

**Files:**
- Modify: `agent/internal/runnerfiles/files/claude/epic-pipeline.md` (escalation protocol `:35-45`; quick-ref table `:302`)
- Modify: `agent/internal/runnerfiles/files/codex/epic-pipeline.md` (escalation protocol `:42-52`; quick-ref table `:312`)

**Interfaces:**
- Consumes: nothing (prose). The push makes committed artifacts readable at `epic.branch` (already recorded on the epic) — so **no `--branch` change to the report command** is needed, and no orchestrator behavior changes.
- Produces: an escalation protocol whose step 1 pushes the epic branch first. Manual verification only (no unit test — the runnerfiles test asserts presence/non-emptiness, not content).

- [ ] **Step 1: Add the push step to the claude escalation protocol**

In `agent/internal/runnerfiles/files/claude/epic-pipeline.md`, replace the escalation protocol list (`:37-45`, currently starting `1. \`agentmon report --epic $ARGUMENTS --stage escalated ...`) with:

```markdown
1. **If you are on an epic branch, push it first (never force-push)** so the
   artifacts you already committed (plan, review evidence) are readable from
   the AgentMon UI: `b=$(git branch --show-current)`; if `$b` names an epic
   branch (not the repo's base branch), run `git push -u origin "$b"`. The
   runner only auto-pushes at plan-gate and PR-open, so without this a
   mid-pipeline escalation leaves your committed artifacts stranded on the
   branch. Do not proceed until the push succeeds; if you are not on an epic
   branch yet (e.g. escalating during Orient), skip this step.
2. `agentmon report --epic $ARGUMENTS --stage escalated --note "<one-line problem + what you need>"`
3. If (and only if) that command FAILS: `gh issue comment $ARGUMENTS --body "ESCALATED: <same note>"`
4. Commit any clean work-in-progress (never commit a broken tree — stash-level
   mess can simply be discarded with a note in the plan file).
5. State the blocker plainly in your final message, then END YOUR TURN and
   wait. The session stays attachable — a human may join this conversation
   and resolve it (then continue where you stopped), or fix things elsewhere
   and hit Retry (which kills this session; a fresh one resumes from your
   artifacts). Both are normal.
```

- [ ] **Step 2: Update the claude quick-ref table row**

In the same file, change the `blocked / DISCUSS` table row (`:302`) from:

```markdown
| blocked / DISCUSS | `agentmon report --epic N --stage escalated --note "…"` |
```

to:

```markdown
| blocked / DISCUSS | push the epic branch first, then `agentmon report --epic N --stage escalated --note "…"` |
```

- [ ] **Step 3: Add the push step to the codex escalation protocol**

In `agent/internal/runnerfiles/files/codex/epic-pipeline.md`, replace the escalation protocol list (`:44-52`) with the same content, adapted to codex wording (`N` for the epic arg, "STOP and wait" instead of "END YOUR TURN"):

```markdown
1. **If you are on an epic branch, push it first (never force-push)** so the
   artifacts you already committed (plan, review evidence) are readable from
   the AgentMon UI: `b=$(git branch --show-current)`; if `$b` names an epic
   branch (not the repo's base branch), run `git push -u origin "$b"`. The
   runner only auto-pushes at plan-gate and PR-open, so without this a
   mid-pipeline escalation leaves your committed artifacts stranded on the
   branch. Do not proceed until the push succeeds; if you are not on an epic
   branch yet (e.g. escalating during Orient), skip this step.
2. `agentmon report --epic N --stage escalated --note "<one-line problem + what you need>"`
3. If (and only if) that command FAILS: `gh issue comment N --body "ESCALATED: <same note>"`
4. Commit any clean work-in-progress (never commit a broken tree).
5. State the blocker plainly in your final message, then STOP and wait. The
   session stays attachable — a human may join and resolve it (then continue
   where you stopped), or fix things elsewhere and hit Retry (which kills
   this session; a fresh one resumes from your artifacts). Both are normal.
```

- [ ] **Step 4: Update the codex quick-ref table row**

Change the codex `blocked / DISCUSS` row (`:312`) the same way:

```markdown
| blocked / DISCUSS | push the epic branch first, then `agentmon report --epic N --stage escalated --note "…"` |
```

- [ ] **Step 5: Confirm the runnerfiles still embed and the agent module builds**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./agent/...`
Expected: PASS (the embed test checks the files are written and non-empty, not their content).

- [ ] **Step 6: Commit**

```bash
git add agent/internal/runnerfiles/files/claude/epic-pipeline.md agent/internal/runnerfiles/files/codex/epic-pipeline.md
git commit -m "feat(agent): push epic branch before mid-pipeline escalation"
```

- [ ] **Step 7: Record the manual verification owed (no unit test for a prose change)**

Note in the PR / handoff: verify on a real escalation that the branch is pushed and the review artifact opens in the drawer. This is the spec's called-out manual check (spec §Testing "Runner"). It also validates the full data flow end-to-end (push → `epic.branch` on GitHub → `/artifact` → `ArtifactPanel`).

---

## Final gate (run before opening the PR)

- [ ] Go: `GOCACHE=/tmp/agentmon-go-cache make test` (all 3 modules green).
- [ ] Web: `cd web && npm run typecheck && npm run test:run` (green).
- [ ] `git log --oneline` shows 5 conventional commits, none with a `Co-Authored-By:` trailer.

## Self-review (spec coverage)

- **§1 Hub generic artifact endpoint** → Task 2 (`OrchestratorEpicArtifactHandler`, query-param path, allowlist, `{path,ref,markdown}`, 256 KiB cap inherited via `github.ErrTooLarge`).
- **§1 Path validation (allowlist, `..`, leading `/`, non-`.md`, safe-char regex)** → Task 2 (`validateArtifactPath` + `TestValidateArtifactPath`).
- **§1 Ref = branch → base-branch fallback** → Task 1 (`fetchArtifact`), covered by Task 1 + Task 2 tests.
- **§1 Keep the plan route working (OQ1: shared helper)** → Task 1 (plan handler reimplemented on `fetchArtifact`, existing suite green).
- **§2 Runner push-on-escalate, both variants, never force-push, epic-branch-only** → Task 5.
- **§3 Clickable paths in `EpicDrawer` (OQ2)** → Task 4 (`EventNote` + overlay).
- **§3 `ArtifactPanel` refactor + `getEpicArtifact` + query key + contract** → Tasks 3 & 4.
- **§Security (fail-closed boundary, existing `OrchestratorView` authz)** → Task 2 (guard order: validate before `GetEpic`; authz unchanged).
- **§Error handling (400/404/413/502/409/503)** → Task 2 handler + tests (409/503 guards; 400 validation; 404/413/502 via `fetchArtifact`).
- **§Testing (hub allowlist accept/reject, ref fallback, error shapes; web ArtifactPanel + EpicDrawer parse)** → Tasks 1–4 tests. Runner = manual verify (Task 5 Step 7), per spec.
