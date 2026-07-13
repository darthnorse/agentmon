# Launch flow + runner-session reaper — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reap runner sessions + their worktrees on epic merge, and fix three launch/board rough edges (Plan-epics vibe seed, board focus-trap, "CI gate"→"Require CI" relabel).

**Architecture:** #3 adds a new agent capability (`POST /worktrees/teardown`, git-based) called by the orchestrator at `finishMerged` alongside the existing session kill; the chain mirrors the existing kill-session request→handler→client→orchestrator path exactly. The three web items are self-contained UI/command changes.

**Tech Stack:** Go (go.work: `shared`/`agent`/`hubd`), React 18 + TS + Vitest (`web`), tmux, git.

**Spec:** `docs/superpowers/specs/2026-07-13-launch-flow-and-runner-session-reaper-design.md`

## Global Constraints

- **Commits:** conventional prefixes (`feat(...)`, `fix(...)`). **NEVER** add a `Co-Authored-By` / AI-attribution trailer.
- **Go gate:** `GOCACHE=/tmp/agentmon-go-cache go test ./shared/... ./agent/... ./hubd/...` (all pass).
- **Web gate:** `cd web && npm run typecheck && npm run test:run` (all pass).
- **No new web-facing contract:** the worktree-teardown types are agent-internal; do NOT touch `web/src/lib/contracts.ts`.
- **Safety (worktree teardown):** `git worktree remove` **without `--force`**; branch delete `-d` (safe), never `-D`; validate `workdir` against the agent's `session_dirs` roots before any git runs; git args passed positionally (no shell).
- **Reap is best-effort:** an already-gone session/worktree is success; a failure is logged and MUST NOT block the merge transition.
- **Branch:** `feat/launch-flow-and-reaper` (already created off `507e626`).

---

### Task 1: Agent worktree-teardown git logic (`agent/internal/worktree`)

The pure git logic behind teardown, in its own package with an injectable runner (mirrors the `tmux.Runner` seam) so the arg-array is unit-testable and the real behavior is integration-tested against a temp git repo.

**Files:**
- Create: `agent/internal/worktree/teardown.go`
- Test: `agent/internal/worktree/teardown_test.go`

**Interfaces:**
- Produces:
  - `type Runner func(ctx context.Context, dir string, args ...string) ([]byte, error)`
  - `var ExecRunner Runner` — runs `git -C <dir> <args...>` via `exec.CommandContext`.
  - `func Teardown(ctx context.Context, run Runner, workdir, branch string) error` — resolves the worktree for `branch` via `git worktree list --porcelain`, removes it (no `--force`), safe-deletes the branch (`-d`), prunes. Idempotent: a missing worktree/branch is success. Returns an error only on an unexpected git failure.

- [ ] **Step 1: Write the failing integration test (real git, skip if absent)**

```go
package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func requireGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

// git runs a git command in dir, failing the test on error.
func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// newMainClone makes a main clone with one commit on `main`, returns its path.
func newMainClone(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-m", "init")
	return dir
}

func TestTeardownRemovesMergedWorktreeAndBranch(t *testing.T) {
	requireGit(t)
	main := newMainClone(t)
	wt := main + "-epic-1"
	// Merged-branch scenario: branch points at a commit already on main (no new commits).
	gitCmd(t, main, "worktree", "add", wt, "-b", "epic/1-x", "main")

	if err := Teardown(context.Background(), ExecRunner, main, "epic/1-x"); err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree dir still present: %v", err)
	}
	// branch gone
	c := exec.Command("git", "-C", main, "rev-parse", "--verify", "epic/1-x")
	if err := c.Run(); err == nil {
		t.Fatal("branch epic/1-x still exists")
	}
}

func TestTeardownIdempotentWhenNothingToRemove(t *testing.T) {
	requireGit(t)
	main := newMainClone(t)
	if err := Teardown(context.Background(), ExecRunner, main, "epic/does-not-exist"); err != nil {
		t.Fatalf("Teardown on missing branch should be nil, got %v", err)
	}
}

func TestTeardownKeepsDirtyWorktree(t *testing.T) {
	requireGit(t)
	main := newMainClone(t)
	wt := main + "-epic-2"
	gitCmd(t, main, "worktree", "add", wt, "-b", "epic/2-x", "main")
	if err := os.WriteFile(filepath.Join(wt, "dirty"), []byte("uncommitted"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Non-forced remove refuses a dirty worktree; Teardown surfaces that as an error
	// (caller logs + swallows) and the worktree survives so no work is lost.
	if err := Teardown(context.Background(), ExecRunner, main, "epic/2-x"); err == nil {
		t.Fatal("expected error for dirty worktree, got nil")
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("dirty worktree should survive: %v", err)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./agent/internal/worktree/ -run TestTeardown -v`
Expected: FAIL — `Teardown`/`ExecRunner` undefined (package won't compile).

- [ ] **Step 3: Implement `teardown.go`**

```go
// Package worktree removes an epic's git worktree + branch on the agent host.
package worktree

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Runner runs `git -C <dir> <args...>` (arg-array; no shell). Injectable for tests.
type Runner func(ctx context.Context, dir string, args ...string) ([]byte, error)

// ExecRunner is the production Runner.
var ExecRunner Runner = func(ctx context.Context, dir string, args ...string) ([]byte, error) {
	c := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	return c.CombinedOutput()
}

// Teardown removes the worktree checked out at `branch` under `workdir`'s repo,
// then safe-deletes `branch`. Idempotent: a missing worktree or branch is not an
// error. A non-forced `worktree remove` on a DIRTY worktree fails — surfaced so
// the caller logs + swallows, never destroying uncommitted work.
func Teardown(ctx context.Context, run Runner, workdir, branch string) error {
	path, err := worktreePathForBranch(ctx, run, workdir, branch)
	if err != nil {
		return err
	}
	if path != "" {
		if out, err := run(ctx, workdir, "worktree", "remove", path); err != nil {
			return fmt.Errorf("worktree remove %q: %w: %s", path, err, bytes.TrimSpace(out))
		}
	}
	// Safe-delete the branch; ignore "not found" / "not fully merged" (idempotent,
	// never force). A leftover branch is harmless; force-deleting would lose commits.
	if out, err := run(ctx, workdir, "branch", "-d", branch); err != nil {
		low := strings.ToLower(string(out))
		if !strings.Contains(low, "not found") && !strings.Contains(low, "not fully merged") {
			return fmt.Errorf("branch -d %q: %w: %s", branch, err, bytes.TrimSpace(out))
		}
	}
	_, _ = run(ctx, workdir, "worktree", "prune")
	return nil
}

// worktreePathForBranch parses `git worktree list --porcelain` for the worktree
// whose checked-out branch is refs/heads/<branch>. "" if none.
func worktreePathForBranch(ctx context.Context, run Runner, workdir, branch string) (string, error) {
	out, err := run(ctx, workdir, "worktree", "list", "--porcelain")
	if err != nil {
		return "", fmt.Errorf("worktree list: %w: %s", err, bytes.TrimSpace(out))
	}
	want := "refs/heads/" + branch
	var curPath string
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "worktree "):
			curPath = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch "):
			if strings.TrimPrefix(line, "branch ") == want {
				return curPath, nil
			}
		case line == "":
			curPath = ""
		}
	}
	return "", nil
}
```

- [ ] **Step 4: Run to verify pass**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./agent/internal/worktree/ -v`
Expected: PASS (3 tests; skipped if git absent).

- [ ] **Step 5: Commit**

```bash
git add agent/internal/worktree/
git commit -m "feat(agent): git worktree+branch teardown helper"
```

---

### Task 2: Agent teardown endpoint (`POST /worktrees/teardown`)

Mirror `KillSessionHandler` exactly: decode body, validate `workdir` against the config roots, run `Teardown`, map errors.

**Files:**
- Modify: `shared/session.go` (add request type)
- Create: `agent/internal/api/worktrees.go`
- Modify: `agent/cmd/agentmon-agent/main.go:99` (wire the handler + closure)
- Test: `agent/internal/api/worktrees_test.go`

**Interfaces:**
- Consumes: `worktree.Teardown`, `tmux.ValidateCwd` (`tmux/create.go:125`).
- Produces:
  - `shared.WorktreeTeardownRequest{ Workdir string `json:"workdir"`; Branch string `json:"branch"` }`
  - `type WorktreeTeardowner func(ctx context.Context, workdir, branch string) error`
  - `func WorktreeTeardownHandler(cfg config.Config, teardown WorktreeTeardowner) http.HandlerFunc` — 400 on empty workdir/branch or workdir outside `session_dirs`; 200 on success (idempotent).

- [ ] **Step 1: Add the shared request type**

In `shared/session.go`, after `KillSessionRequest`:

```go
// WorktreeTeardownRequest is the body of POST /worktrees/teardown (agent) and
// the hub's teardown call. Workdir is the project's main clone; Branch is the
// epic's branch whose worktree to remove.
type WorktreeTeardownRequest struct {
	Workdir string `json:"workdir"`
	Branch  string `json:"branch"`
}
```

- [ ] **Step 2: Write the failing handler test**

```go
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"agentmon/agent/internal/config"
	"agentmon/shared"
)

func teardownReq(t *testing.T, body any) *http.Request {
	t.Helper()
	b, _ := json.Marshal(body)
	return httptest.NewRequest(http.MethodPost, "/worktrees/teardown", bytes.NewReader(b))
}

func TestWorktreeTeardownHandlerValid(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{Targets: []config.Target{{Label: "default", SessionDirs: []string{root}}}}
	var gotWorkdir, gotBranch string
	h := WorktreeTeardownHandler(cfg, func(_ context.Context, wd, br string) error {
		gotWorkdir, gotBranch = wd, br
		return nil
	})
	rr := httptest.NewRecorder()
	h(rr, teardownReq(t, shared.WorktreeTeardownRequest{Workdir: root, Branch: "epic/1-x"}))
	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", rr.Code, rr.Body)
	}
	if gotBranch != "epic/1-x" || gotWorkdir == "" {
		t.Fatalf("teardown called with %q/%q", gotWorkdir, gotBranch)
	}
}

func TestWorktreeTeardownHandlerWorkdirOutsideRoots400(t *testing.T) {
	cfg := config.Config{Targets: []config.Target{{Label: "default", SessionDirs: []string{t.TempDir()}}}}
	called := false
	h := WorktreeTeardownHandler(cfg, func(context.Context, string, string) error { called = true; return nil })
	rr := httptest.NewRecorder()
	h(rr, teardownReq(t, shared.WorktreeTeardownRequest{Workdir: "/etc", Branch: "epic/1-x"}))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code = %d", rr.Code)
	}
	if called {
		t.Fatal("teardown must not run for an out-of-roots workdir")
	}
}

func TestWorktreeTeardownHandlerEmptyBranch400(t *testing.T) {
	cfg := config.Config{Targets: []config.Target{{Label: "default", SessionDirs: []string{t.TempDir()}}}}
	h := WorktreeTeardownHandler(cfg, func(context.Context, string, string) error { return nil })
	rr := httptest.NewRecorder()
	h(rr, teardownReq(t, shared.WorktreeTeardownRequest{Workdir: t.TempDir(), Branch: ""}))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code = %d", rr.Code)
	}
}
```

*(Confirm the target/config field names — `config.Target`, `.SessionDirs`, target resolution — against `agent/internal/api/sessions.go`'s existing handlers and `tmux.ValidateCwd`'s signature before finalizing; mirror how `CreateSessionHandler` reads roots for the request's target.)*

- [ ] **Step 3: Run to verify it fails**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./agent/internal/api/ -run TestWorktreeTeardown -v`
Expected: FAIL — `WorktreeTeardownHandler` undefined.

- [ ] **Step 4: Implement `worktrees.go`**

```go
package api

import (
	"context"
	"encoding/json"
	"net/http"

	"agentmon/agent/internal/config"
	"agentmon/agent/internal/tmux"
	"agentmon/shared"
)

// WorktreeTeardowner removes the worktree+branch under workdir. Prod binds
// worktree.Teardown + worktree.ExecRunner; tests inject a fake.
type WorktreeTeardowner func(ctx context.Context, workdir, branch string) error

// WorktreeTeardownHandler serves POST /worktrees/teardown?target=<label>. It
// validates workdir against the target's session_dirs roots (same allow-list as
// session creation) before any git runs. Teardown is idempotent, so success is
// 200 whether or not a worktree existed.
func WorktreeTeardownHandler(cfg config.Config, teardown WorktreeTeardowner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxCreateBody)
		var req shared.WorktreeTeardownRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if req.Branch == "" {
			writeJSONError(w, http.StatusBadRequest, "branch required")
			return
		}
		roots, ok := cfg.SessionDirsForTarget(r.URL.Query().Get("target")) // match sessions.go's target resolution
		if !ok {
			writeJSONError(w, http.StatusNotFound, "unknown target")
			return
		}
		resolved, err := tmux.ValidateCwd(req.Workdir, roots)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := teardown(r.Context(), resolved, req.Branch); err != nil {
			// Best-effort: a dirty-worktree refusal or transient git error must not
			// masquerade as a hard failure that the hub might retry destructively.
			// Log-and-200 so the merge flow proceeds; teardown is idempotent.
			log.Printf("worktree teardown %q %q: %v", resolved, req.Branch, err)
		}
		w.WriteHeader(http.StatusOK)
	}
}
```

*(Use the SAME target→roots resolution helper the existing session handlers use; the `SessionDirsForTarget` name above is a placeholder for whatever `sessions.go` already calls. Add the `log` import.)*

- [ ] **Step 5: Wire it in `main.go`**

After the `killSession` closure (`main.go:83-85`):

```go
	teardownWorktree := func(ctx context.Context, workdir, branch string) error {
		return worktree.Teardown(ctx, worktree.ExecRunner, workdir, branch)
	}
```

After the kill route (`main.go:99`):

```go
	mux.Handle("POST /worktrees/teardown", api.RequireBearer(cfg.HubToken, api.WorktreeTeardownHandler(cfg, teardownWorktree)))
```

Add the `agentmon/agent/internal/worktree` import.

- [ ] **Step 6: Run to verify pass**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./agent/... ./shared/... -v -run 'Teardown|Worktree'`
Expected: PASS. Then `GOCACHE=/tmp/agentmon-go-cache go build ./agent/...` (compiles).

- [ ] **Step 7: Commit**

```bash
git add shared/session.go agent/internal/api/worktrees.go agent/cmd/agentmon-agent/main.go
git commit -m "feat(agent): POST /worktrees/teardown endpoint"
```

---

### Task 3: Hub registry client `TeardownWorktree`

Mirror `registry.Client.KillSession` (`registry/client.go:183`): POST to `/worktrees/teardown`, swallow 404 (old agent) as success.

**Files:**
- Modify: `hubd/internal/registry/client.go`
- Modify: `hubd/internal/orchestrator/orchestrator.go` (interface: add to the `Agents` interface, ~`:37`)
- Test: `hubd/internal/registry/client_test.go` (mirror the existing KillSession client test)

**Interfaces:**
- Produces: `func (c *Client) TeardownWorktree(ctx context.Context, srv db.Server, target, workdir, branch string) error` — 200/204 → nil; **404 → nil** (agent predates the endpoint; degrade gracefully); other non-2xx → error.

- [ ] **Step 1: Write the failing client test**

```go
func TestTeardownWorktreePostsAndSwallows404(t *testing.T) {
	var gotPath, gotBranch string
	srvHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var req shared.WorktreeTeardownRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotBranch = req.Branch
		w.WriteHeader(http.StatusOK)
	}))
	defer srvHTTP.Close()
	c := &Client{HTTP: srvHTTP.Client()}
	srv := db.Server{ID: "s", URL: srvHTTP.URL, Bearer: "b"}
	if err := c.TeardownWorktree(context.Background(), srv, "default", "/w", "epic/1-x"); err != nil {
		t.Fatalf("TeardownWorktree: %v", err)
	}
	if gotPath != "/worktrees/teardown" || gotBranch != "epic/1-x" {
		t.Fatalf("path=%q branch=%q", gotPath, gotBranch)
	}

	old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }))
	defer old.Close()
	if err := c.TeardownWorktree(context.Background(), db.Server{URL: old.URL}, "", "/w", "epic/1-x"); err != nil {
		t.Fatalf("404 must be swallowed, got %v", err)
	}
}
```

- [ ] **Step 2: Run to verify fail**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./hubd/internal/registry/ -run TestTeardownWorktree -v`
Expected: FAIL — `TeardownWorktree` undefined.

- [ ] **Step 3: Implement (mirror KillSession)**

```go
// TeardownWorktree removes the epic's worktree+branch on the agent. A 404 means
// the agent predates the endpoint (mixed fleet) — swallowed as success so the
// merge flow is never blocked; full teardown lands once agents update.
func (c *Client) TeardownWorktree(ctx context.Context, srv db.Server, target, workdir, branch string) error {
	u := srv.URL + "/worktrees/teardown"
	if target != "" {
		u += "?target=" + url.QueryEscape(target)
	}
	body, err := json.Marshal(shared.WorktreeTeardownRequest{Workdir: workdir, Branch: branch})
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Authorization", "Bearer "+srv.Bearer)
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return fmt.Errorf("dial agent %s: %w", srv.ID, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent, http.StatusNotFound:
		return nil
	default:
		return fmt.Errorf("agent %s worktree-teardown returned %d", srv.ID, resp.StatusCode)
	}
}
```

Add to the orchestrator's `Agents` interface (near `KillSession`, `orchestrator.go:37`):
```go
	TeardownWorktree(ctx context.Context, srv db.Server, target, workdir, branch string) error
```
Update any test fake/mock of `Agents` in `hubd/internal/orchestrator` to implement it (return nil).

- [ ] **Step 4: Run to verify pass**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./hubd/internal/registry/ ./hubd/internal/orchestrator/ -v -run 'Teardown'`
Expected: PASS; orchestrator package still compiles (fake updated).

- [ ] **Step 5: Commit**

```bash
git add hubd/internal/registry/client.go hubd/internal/orchestrator/orchestrator.go
git commit -m "feat(hub): registry client TeardownWorktree (404-tolerant)"
```

---

### Task 4: Reap on merge in `finishMerged`

Call session-kill + worktree-teardown when an epic reaches `EpicMerged`, and fix the two stale "exit-after-pr_open" comments.

**Files:**
- Modify: `hubd/internal/orchestrator/orchestrator.go` (`finishMerged` `:721`; comments at `:840` and `agent/internal/tmux/create.go:24`)
- Test: `hubd/internal/orchestrator/orchestrator_test.go` (or the file holding merge tests)

**Interfaces:**
- Consumes: `killEpicSession` (`:843`), `o.d.Agents.TeardownWorktree` (Task 3).

- [ ] **Step 1: Write the failing test**

Mirror the package's existing merge/gate test setup (fake `Agents` recording calls). Assert that driving an epic to `EpicMerged` (via the same path existing tests use — e.g. a gate `Merge` or a poll observing `pr.Merged`) results in **both** a `KillSession` and a `TeardownWorktree` call for the epic's `SessionName`/`Branch`, and that a `TeardownWorktree` error does NOT prevent the `EpicMerged` transition.

```go
func TestFinishMergedReapsSessionAndWorktree(t *testing.T) {
	// ... arrange an epic in pr_open with SessionName + Branch set, gate returns Merge ...
	// fake Agents records KillSession + TeardownWorktree calls.
	// act: run the tick that merges.
	// assert:
	//   fakeAgents.killed  contains e.SessionName
	//   fakeAgents.tornDown contains {workdir: p.Workdir, branch: e.Branch}
	//   epic stage == EpicMerged
}

func TestFinishMergedProceedsWhenTeardownFails(t *testing.T) {
	// TeardownWorktree returns an error; assert epic still reaches EpicMerged.
}
```

- [ ] **Step 2: Run to verify fail**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./hubd/internal/orchestrator/ -run FinishMerged -v`
Expected: FAIL (no reap yet).

- [ ] **Step 3: Implement in `finishMerged`**

Inside `finishMerged`, after the successful `EpicMerged` transition (`:722`) and before/after the label+comment side-effects, add:

```go
	// Reap the finished runner: kill the idle session, then tear down its worktree
	// + branch. Best-effort — killEpicSession swallows an unreachable agent, and
	// teardown failure is logged, never blocking the (already-committed) merge.
	o.killEpicSession(ctx, e, "merged")
	if srv, ok := o.server(ctx, p, "merged-teardown"); ok && e.Branch != "" {
		if err := o.d.Agents.TeardownWorktree(ctx, srv, p.Target, p.Workdir, e.Branch); err != nil {
			log.Printf("orchestrator[%s]: merged worktree teardown (branch %q): %v", p.Name, e.Branch, err)
		}
	}
```

Confirm `finishMerged` has (or can cheaply fetch) `p` — it already takes `p db.Project` (`:721`). `killEpicSession` re-fetches the project internally, which is fine; keep it.

Then fix the stale comments:
- `orchestrator.go:840` — replace "e.g. the normal exit-after-pr_open path" with a note that runner sessions are reaped on merge (and Cancel/Retry), NOT by self-exit.
- `agent/internal/tmux/create.go:24` — replace "the session ENDS when it exits — which is exactly the runner contract's normal exit (report pr_open, then quit)" with the accurate behavior: a command-backed session ends when the command exits, but the interactive runner stays alive after pr_open and is reaped by the hub on merge/Cancel/Retry.

- [ ] **Step 4: Run to verify pass**

Run: `GOCACHE=/tmp/agentmon-go-cache go test ./hubd/... -v -run 'FinishMerged|Merge|Gate'`
Expected: PASS (new + existing merge tests green).

- [ ] **Step 5: Full Go gate + commit**

```bash
GOCACHE=/tmp/agentmon-go-cache go test ./shared/... ./agent/... ./hubd/...
git add hubd/internal/orchestrator/orchestrator.go agent/internal/tmux/create.go
git commit -m "feat(hub): reap runner session + worktree on epic merge"
```

---

### Task 5: Plan-epics vibe input (web)

A modal seeds the vibe as `$ARGUMENTS`; the vibe is shell-safe quoted into the command.

**Files:**
- Create: `web/src/components/board/PlanEpicsModal.tsx`
- Create: `web/src/lib/shell-quote.ts` (+ test)
- Modify: `web/src/components/board/ProjectHeader.tsx` (button opens modal; build command from vibe)
- Test: `web/src/lib/shell-quote.test.ts`, `web/src/components/board/ProjectHeader.test.tsx`

**Interfaces:**
- Produces:
  - `shSingleQuote(s: string): string` — POSIX single-quote wrap, `'\''`-escaping embedded quotes. `shSingleQuote("")` → `''`.
  - `planCommand(provider: "claude"|"codex", vibe: string): string` — vibe trimmed; empty → today's bare `/plan-epics`; non-empty → `/plan-epics <vibe>` with the whole slash-command single-quoted.

- [ ] **Step 1: Write failing shell-quote + planCommand tests**

```ts
import { describe, expect, it } from "vitest";
import { shSingleQuote, planCommand } from "@/lib/shell-quote";

describe("shSingleQuote", () => {
  it("wraps plain text", () => expect(shSingleQuote("hi there")).toBe("'hi there'"));
  it("escapes single quotes", () => expect(shSingleQuote("it's")).toBe("'it'\\''s'"));
  it("neutralises $ and backticks", () => expect(shSingleQuote("$PATH `x`")).toBe("'$PATH `x`'"));
  it("empty → ''", () => expect(shSingleQuote("")).toBe("''"));
});

describe("planCommand", () => {
  it("empty vibe → bare slash command (claude)", () =>
    expect(planCommand("claude", "  ")).toBe(`IS_SANDBOX=1 claude --dangerously-skip-permissions "/plan-epics"`));
  it("seeds the vibe as $ARGUMENTS (claude)", () =>
    expect(planCommand("claude", "add dark mode")).toBe(
      `IS_SANDBOX=1 claude --dangerously-skip-permissions '/plan-epics add dark mode'`));
  it("codex variant seeds too", () =>
    expect(planCommand("codex", "add dark mode")).toBe(`codex -a never '/plan-epics add dark mode'`));
  it("a vibe with a quote stays shell-safe", () =>
    expect(planCommand("codex", "it's x")).toBe(`codex -a never '/plan-epics it'\\''s x'`));
});
```

*(Note the empty-vibe case keeps the EXISTING double-quoted `"/plan-epics"` form byte-for-byte so nothing about today's behavior changes; only the seeded case switches to single-quote wrapping.)*

- [ ] **Step 2: Run to verify fail**

Run: `cd web && npx vitest run src/lib/shell-quote.test.ts`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement `shell-quote.ts`**

```ts
// POSIX single-quote a string so it survives `sh -c` intact (the agent runs the
// session command via tmux → sh -c). Nothing expands inside single quotes; an
// embedded ' is closed, escaped as \', and reopened.
export function shSingleQuote(s: string): string {
  return `'${s.replace(/'/g, `'\\''`)}'`;
}

// Build the /plan-epics launch command. Empty vibe keeps today's bare form
// (unchanged behavior); a vibe is seeded as $ARGUMENTS, shell-safe quoted.
export function planCommand(provider: "claude" | "codex", vibe: string): string {
  const v = vibe.trim();
  if (provider === "codex") {
    return v
      ? `codex -a never ${shSingleQuote(`/plan-epics ${v}`)}`
      : `codex -a never "/plan-epics"`;
  }
  return v
    ? `IS_SANDBOX=1 claude --dangerously-skip-permissions ${shSingleQuote(`/plan-epics ${v}`)}`
    : `IS_SANDBOX=1 claude --dangerously-skip-permissions "/plan-epics"`;
}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd web && npx vitest run src/lib/shell-quote.test.ts`
Expected: PASS.

- [ ] **Step 5: Build `PlanEpicsModal.tsx`** (mirror `KillSessionModal.tsx`: backdrop, `role="dialog"`, Escape closes, autofocus the textarea)

```tsx
import * as React from "react";
import { Button } from "@/components/ui/button";

interface Props {
  project: string;
  onSubmit(vibe: string): void;
  onClose(): void;
}

// Captures an optional one-line vibe to seed /plan-epics ($ARGUMENTS). Submitting
// empty is allowed — it launches the interactive brainstorm-from-scratch.
export function PlanEpicsModal({ project, onSubmit, onClose }: Props) {
  const [vibe, setVibe] = React.useState("");
  const ref = React.useRef<HTMLTextAreaElement>(null);
  React.useEffect(() => { ref.current?.focus(); }, []);
  React.useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") onClose(); };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      onClick={onClose}>
      <div className="w-full max-w-md rounded-lg border border-border bg-background p-4 shadow-lg"
        role="dialog" aria-modal="true" aria-labelledby="plan-epics-title"
        onClick={(e) => e.stopPropagation()}>
        <h2 id="plan-epics-title" className="text-base font-semibold">Plan epics — {project}</h2>
        <p className="mt-1 text-sm text-muted-foreground">
          A one-line vibe seeds the session. Leave blank to brainstorm from scratch.
        </p>
        <textarea ref={ref} value={vibe} onChange={(e) => setVibe(e.target.value)}
          rows={3} placeholder="e.g. per-project enforceable requirements injected into plan/build/review"
          className="mt-3 w-full rounded-md border border-input bg-background p-2 text-sm" />
        <div className="mt-3 flex justify-end gap-2">
          <Button variant="outline" size="sm" onClick={onClose}>Cancel</Button>
          <Button size="sm" onClick={() => onSubmit(vibe)}>Launch</Button>
        </div>
      </div>
    </div>
  );
}
```

- [ ] **Step 6: Wire into `ProjectHeader.tsx`**

Replace the `planCommand` local (`:40-44`) with an import from `@/lib/shell-quote`. Add `const [showPlan, setShowPlan] = React.useState(false);`. Change the "Plan epics…" button's `onClick` to `() => setShowPlan(true)`. Render `{showPlan && <PlanEpicsModal project={project.name} onClose={() => setShowPlan(false)} onSubmit={(vibe) => { setShowPlan(false); void openOrFocusSession({ serverId: project.server_id, serverName: project.name, target: project.target, name: sessionSlug("plan", project.name), cwd: project.workdir, command: planCommand(project.provider === "codex" ? "codex" : "claude", vibe) }, isDesktop, navigate); }} />}`.

- [ ] **Step 7: Update `ProjectHeader.test.tsx`**

Add a test: clicking "Plan epics…" opens the modal; submitting a vibe calls `openOrFocusSession` with a command containing the seeded, quoted vibe; submitting empty uses the bare command. (Mirror existing ProjectHeader test harness/mocks.)

- [ ] **Step 8: Web gate + commit**

```bash
cd web && npm run typecheck && npx vitest run src/lib/shell-quote.test.ts src/components/board/ProjectHeader.test.tsx
git add web/src/lib/shell-quote.ts web/src/lib/shell-quote.test.ts web/src/components/board/PlanEpicsModal.tsx web/src/components/board/ProjectHeader.tsx web/src/components/board/ProjectHeader.test.tsx
git commit -m "feat(web): seed a vibe into the Plan-epics launch"
```

---

### Task 6: Board focus-trap fix (web)

Buttons open into the grid (no forced expand); opening a new pane while expanded collapses to grid so it's never hidden.

**Files:**
- Modify: `web/src/components/board/open-session.ts` (`openPaneTail` — don't focus on the button path)
- Modify: `web/src/store/panes.ts` (`openPane` collapses when a NEW pane is added while a tile is expanded)
- Test: `web/src/store/panes.test.ts` (if present; else create), `web/src/components/board/open-session.test.ts`

**Interfaces:**
- `openPane` gains the behavior: when it appends a genuinely new pane and `focusedId != null`, set `focusedId = null` (collapse to grid) so the new tile is visible. Re-opening an existing pane stays a no-op (no collapse).
- `openPaneTail` desktop path: **stop calling `focus()`** — just `openPane` and return `opened`/`cap`. (This removes the button-open force-expand; the pane appears in the grid.)

- [ ] **Step 1: Write failing store test**

```ts
import { describe, expect, it, beforeEach } from "vitest";
import { usePanes, paneKey } from "@/store/panes";

const base = (session: string) => ({ serverId: "h1", paneId: "p-" + session, target: "default", session, serverName: "host" });

describe("openPane while expanded", () => {
  beforeEach(() => usePanes.setState({ panes: [], focusedId: null }));

  it("collapses to grid when a NEW pane is opened while a tile is expanded", () => {
    usePanes.getState().openPane(base("a"));
    usePanes.getState().focus(paneKey("h1", "default", "a", "p-a")); // expand a
    usePanes.getState().openPane(base("b"));                          // open new b
    expect(usePanes.getState().focusedId).toBeNull();                // collapsed → b visible
    expect(usePanes.getState().panes).toHaveLength(2);
  });

  it("re-opening an already-open pane does NOT collapse", () => {
    usePanes.getState().openPane(base("a"));
    usePanes.getState().focus(paneKey("h1", "default", "a", "p-a"));
    usePanes.getState().openPane(base("a"));                          // same pane again
    expect(usePanes.getState().focusedId).toBe(paneKey("h1", "default", "a", "p-a"));
  });
});
```

- [ ] **Step 2: Run to verify fail**

Run: `cd web && npx vitest run src/store/panes.test.ts`
Expected: FAIL (currently focusedId stays set).

- [ ] **Step 3: Implement `openPane` collapse-on-new**

In `panes.ts` `openPane`, in the branch that appends a new pane (currently `set((s) => ({ panes: [...s.panes, { ...p, id }] }))`), also clear focus so the new tile isn't hidden behind an expanded one:

```ts
    if (get().panes.length >= GRID_TILE_CAP) return { ok: false, reason: "cap" };
    // A new pane must be visible immediately. If a tile is currently expanded, the
    // new one would be display:none behind it — collapse to the grid instead.
    set((s) => ({ panes: [...s.panes, { ...p, id }], focusedId: null }));
    return { ok: true };
```

(The existing-pane branch above returns early WITHOUT touching focusedId — unchanged, so re-opening never collapses.)

- [ ] **Step 4: Update `openPaneTail` (open-session.ts) — no forced expand**

Change the desktop branch so it no longer calls `focus()`:

```ts
  const res = usePanes.getState().openPane({
    serverId: args.serverId, paneId: args.paneId, target: args.target,
    session: args.session, serverName: args.serverName, state: args.state,
  });
  if (!res.ok && res.reason === "cap") return "cap";
  return "opened"; // grid-first: the pane shows in the grid; no forced expand/focus-trap
```

Update `open-session.test.ts`: the cases asserting `focusedId` is set after a desktop open (e.g. "opens a supplied existing session", "desktop opens and focuses the tile") must change to assert the pane is OPEN in `panes` (not focused). Keep the cap and mobile-navigate assertions.

- [ ] **Step 5: Run to verify pass**

Run: `cd web && npx vitest run src/store/panes.test.ts src/components/board/open-session.test.ts`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/src/store/panes.ts web/src/store/panes.test.ts web/src/components/board/open-session.ts web/src/components/board/open-session.test.ts
git commit -m "fix(web): don't trap focus when opening a session while a tile is expanded"
```

---

### Task 7: Rename "CI gate" → "Require CI" (web)

**Files:**
- Modify: `web/src/components/board/ProjectHeader.tsx:87-97` (button label + tooltip)
- Test: `web/src/components/board/ProjectHeader.test.tsx` (any assertion on the old text)

- [ ] **Step 1: Update the label + tooltip**

In `ProjectHeader.tsx`, the require-CI button:
- Button text: `CI gate: {on|off}` → `Require CI: {on|off}`.
- `title`: → `"Wait for CI checks before the merge gate lets an epic through (no effect on repos without CI; failing checks always block)."`
- Toast strings: `"CI gate off"/"CI gate on"` → `"Require CI off"/"Require CI on"`.

- [ ] **Step 2: Update any test asserting the old copy**

Grep `web/src` for `CI gate` and update matching test expectations to `Require CI`.

Run: `cd web && grep -rn "CI gate" src || echo "none left"`
Expected: none left.

- [ ] **Step 3: Web gate + commit**

```bash
cd web && npm run typecheck && npm run test:run
git add web/src/components/board/ProjectHeader.tsx web/src/components/board/ProjectHeader.test.tsx
git commit -m "feat(web): rename 'CI gate' toggle to 'Require CI'"
```

---

## Final verification

- [ ] `GOCACHE=/tmp/agentmon-go-cache go test ./shared/... ./agent/... ./hubd/...` — all pass.
- [ ] `cd web && npm run typecheck && npm run test:run` — all pass.
- [ ] `shellcheck` unaffected (untouched here; the SC2016 fix is on `fix/ci-shellcheck-installer`).

## Self-review notes (author)

- **Spec coverage:** #1→Task 5; #2→Task 6; #3→Tasks 1–4; rename→Task 7. Stale-comment fix folded into Task 4. All spec sections mapped.
- **Verify-before-code hooks:** Tasks 2 & 4 explicitly say to confirm the agent config target→roots helper name and the orchestrator merge-test harness against the actual repo before finalizing — these are the two spots the plan asserts a name/shape it hasn't pinned to a line.
- **Deploy:** hub rebuild then agents (Task 2/3/4 are the hub+agent surface); Tasks 5–7 are web-only.
