package tmux

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// recordRunner returns a Runner that records the args it was called with and
// replays a canned (out, err). It mirrors the fake-Runner pattern used by the
// discovery unit tests, but additionally captures the exact arg array so the
// no-shell, positional-args contract (§13.6) can be asserted byte-for-byte.
func recordRunner(out []byte, err error, got *[]string) Runner {
	return func(ctx context.Context, args ...string) ([]byte, error) {
		*got = append([]string(nil), args...)
		return out, err
	}
}

func TestCreateSessionArgArray(t *testing.T) {
	var got []string
	run := recordRunner(nil, nil, &got)

	if err := CreateSession(context.Background(), run, "mysock", "proj", "/tmp"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	want := []string{"-L", "mysock", "new-session", "-d", "-s", "proj", "-c", "/tmp"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestCreateSessionDefaultSocket(t *testing.T) {
	var got []string
	run := recordRunner(nil, nil, &got)

	if err := CreateSession(context.Background(), run, "", "proj", "/tmp"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	// Empty socket → no -L flag (socketArgs returns nil).
	want := []string{"new-session", "-d", "-s", "proj", "-c", "/tmp"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestCreateSessionDuplicate(t *testing.T) {
	var got []string
	run := recordRunner([]byte("duplicate session: proj"), errors.New("exit status 1"), &got)

	err := CreateSession(context.Background(), run, "mysock", "proj", "/tmp")
	if !errors.Is(err, ErrSessionExists) {
		t.Fatalf("err = %v, want ErrSessionExists", err)
	}
}

func TestCreateSessionDuplicateMixedCase(t *testing.T) {
	// tmux output casing must not defeat detection.
	var got []string
	run := recordRunner([]byte("Duplicate Session: proj\n"), errors.New("exit status 1"), &got)

	err := CreateSession(context.Background(), run, "mysock", "proj", "/tmp")
	if !errors.Is(err, ErrSessionExists) {
		t.Fatalf("err = %v, want ErrSessionExists", err)
	}
}

func TestCreateSessionOtherError(t *testing.T) {
	var got []string
	run := recordRunner([]byte("some other failure"), errors.New("exit status 1"), &got)

	err := CreateSession(context.Background(), run, "mysock", "proj", "/tmp")
	if err == nil {
		t.Fatal("want error")
	}
	if errors.Is(err, ErrSessionExists) {
		t.Fatalf("must not classify as ErrSessionExists: %v", err)
	}
}

func TestValidateCwd(t *testing.T) {
	root := t.TempDir()
	// EvalSymlinks the root itself so comparisons are against the resolved form
	// (macOS /tmp → /private/tmp etc.); the function resolves both sides anyway.
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	allowed := []string{root}

	t.Run("subdir ok", func(t *testing.T) {
		got, err := ValidateCwd(sub, allowed)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		wantResolved, _ := filepath.EvalSymlinks(sub)
		if got != wantResolved {
			t.Fatalf("resolved = %q, want %q", got, wantResolved)
		}
	})

	t.Run("root itself ok", func(t *testing.T) {
		got, err := ValidateCwd(root, allowed)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		wantResolved, _ := filepath.EvalSymlinks(root)
		if got != wantResolved {
			t.Fatalf("resolved = %q, want %q", got, wantResolved)
		}
	})

	t.Run("empty defaults to first allowed", func(t *testing.T) {
		got, err := ValidateCwd("", allowed)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		wantResolved, _ := filepath.EvalSymlinks(root)
		if got != wantResolved {
			t.Fatalf("resolved = %q, want %q", got, wantResolved)
		}
	})

	t.Run("outside root rejected", func(t *testing.T) {
		if _, err := ValidateCwd("/etc", allowed); err == nil {
			t.Fatal("want error for /etc outside allowed root")
		}
	})

	t.Run("relative rejected", func(t *testing.T) {
		if _, err := ValidateCwd("sub", allowed); err == nil {
			t.Fatal("want error for relative path")
		}
	})

	t.Run("nonexistent rejected", func(t *testing.T) {
		if _, err := ValidateCwd(filepath.Join(root, "nope"), allowed); err == nil {
			t.Fatal("want error for missing path")
		}
	})

	t.Run("file (not dir) rejected", func(t *testing.T) {
		if _, err := ValidateCwd(file, allowed); err == nil {
			t.Fatal("want error for a regular file")
		}
	})

	t.Run("dot-dot traversal escaping root rejected", func(t *testing.T) {
		escape := filepath.Join(root, "..", "..", "etc")
		if _, err := ValidateCwd(escape, allowed); err == nil {
			t.Fatalf("want error for traversal %q", escape)
		}
	})

	t.Run("sibling prefix-collision rejected", func(t *testing.T) {
		// A directory whose path shares the string prefix of root but is NOT
		// under it (root="/x/foo", sibling="/x/foobar") must be rejected — the
		// allow-list check uses a separator boundary, not a raw string prefix.
		parent := t.TempDir()
		base := filepath.Join(parent, "foo")
		sibling := filepath.Join(parent, "foobar")
		if err := os.Mkdir(base, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(sibling, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := ValidateCwd(sibling, []string{base}); err == nil {
			t.Fatalf("want error: %q is not under %q", sibling, base)
		}
	})

	t.Run("symlink escape rejected", func(t *testing.T) {
		// A symlink inside the allowed root pointing OUTSIDE it must be rejected
		// because validation resolves symlinks before the prefix check.
		outside := t.TempDir()
		link := filepath.Join(root, "esc")
		if err := os.Symlink(outside, link); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
		if _, err := ValidateCwd(link, allowed); err == nil {
			t.Fatalf("want error: symlink %q escapes %q", link, root)
		}
	})

	t.Run("no allowed roots rejected", func(t *testing.T) {
		if _, err := ValidateCwd(sub, nil); err == nil {
			t.Fatal("want error when no session_dirs configured")
		}
	})
}

const itestSocket = "agentmon-m10-itest"

func killM10ITestServer() { _ = exec.Command("tmux", "-L", itestSocket, "kill-server").Run() }

func TestCreateSessionIntegration(t *testing.T) {
	requireTmux(t)
	killM10ITestServer()
	t.Cleanup(killM10ITestServer)

	dir := t.TempDir()
	if err := CreateSession(context.Background(), ExecRunner, itestSocket, "m10proj", dir); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	out, err := exec.Command("tmux", "-L", itestSocket, "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		t.Fatalf("list-sessions: %v", err)
	}
	if string(trimNL(out)) != "m10proj" {
		t.Fatalf("sessions = %q, want m10proj", out)
	}

	// Re-creating the same name must surface ErrSessionExists from real tmux.
	err = CreateSession(context.Background(), ExecRunner, itestSocket, "m10proj", dir)
	if !errors.Is(err, ErrSessionExists) {
		t.Fatalf("duplicate err = %v, want ErrSessionExists", err)
	}
}
