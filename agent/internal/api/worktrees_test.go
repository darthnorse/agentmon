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
	cfg := config.Config{SessionDirs: []string{root}}
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
	cfg := config.Config{SessionDirs: []string{t.TempDir()}}
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
	cfg := config.Config{SessionDirs: []string{t.TempDir()}}
	h := WorktreeTeardownHandler(cfg, func(context.Context, string, string) error { return nil })
	rr := httptest.NewRecorder()
	h(rr, teardownReq(t, shared.WorktreeTeardownRequest{Workdir: t.TempDir(), Branch: ""}))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code = %d", rr.Code)
	}
}

func TestWorktreeTeardownHandlerEmptyWorkdir400(t *testing.T) {
	cfg := config.Config{SessionDirs: []string{t.TempDir()}}
	called := false
	h := WorktreeTeardownHandler(cfg, func(context.Context, string, string) error { called = true; return nil })
	rr := httptest.NewRecorder()
	h(rr, teardownReq(t, shared.WorktreeTeardownRequest{Workdir: "", Branch: "epic/1-x"}))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code = %d", rr.Code)
	}
	if called {
		t.Fatal("teardown must not run for an empty workdir")
	}
}
