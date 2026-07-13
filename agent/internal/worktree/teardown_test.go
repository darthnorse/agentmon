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

// gitCmd runs a git command in dir, failing the test on error.
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
