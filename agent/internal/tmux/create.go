package tmux

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrSessionExists is returned by CreateSession when tmux refuses to create a
// session because one with that name already exists on the target socket. The
// REST handler maps it to HTTP 409 (§12.2).
var ErrSessionExists = errors.New("session already exists")

// CreateSession starts a new detached tmux session named name with working
// directory cwd on the given socket, via the arg-array Runner seam (no shell —
// name and cwd are positional args, never interpolated; §13.6). It mirrors the
// proven `new-session -d -s … -c …` shape from discovery_integration_test.go.
//
// The caller MUST have already validated name (shared.ValidateSessionName) and
// cwd (ValidateCwd); CreateSession is the exec boundary, not the policy boundary.
// A tmux "duplicate session" failure is mapped to ErrSessionExists.
func CreateSession(ctx context.Context, run Runner, socket, name, cwd string) error {
	out, err := run(ctx, with(socketArgs(socket), "new-session", "-d", "-s", name, "-c", cwd)...)
	if err != nil {
		// tmux reports "duplicate session" on stderr. The production ExecRunner
		// returns nil stdout and folds stderr into the error string, so check
		// BOTH the returned output and the error text (a fake Runner may surface
		// the message either way).
		if isDuplicateSession(out) || isDuplicateSession([]byte(err.Error())) {
			return ErrSessionExists
		}
		return fmt.Errorf("tmux new-session: %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

func isDuplicateSession(b []byte) bool {
	return bytes.Contains(bytes.ToLower(b), []byte("duplicate session"))
}

// ValidateCwd resolves and authorises a requested working directory against an
// allow-list of roots (§13.6 directory policy). It returns the cleaned, symlink-
// resolved absolute path on success, or an error describing the rejection.
//
// Rules: the path must be absolute, exist, and be a directory; after cleaning and
// resolving symlinks it must lie within one of the allowed roots (equal to a root
// or under it on a path-separator boundary). An empty cwd defaults to the first
// allowed root. Symlinks and `..` traversal are resolved before the prefix check,
// so neither can escape the allow-list. With no allowed roots, every path is
// rejected.
func ValidateCwd(cwd string, allowed []string) (string, error) {
	if len(allowed) == 0 {
		return "", errors.New("no session_dirs configured")
	}
	if cwd == "" {
		cwd = allowed[0]
	}
	if !filepath.IsAbs(cwd) {
		return "", fmt.Errorf("cwd must be an absolute path")
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(cwd))
	if err != nil {
		return "", fmt.Errorf("cwd not found: %w", err)
	}
	if fi, err := os.Stat(resolved); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("cwd is not a directory")
	}
	for _, root := range allowed {
		r, err := filepath.EvalSymlinks(filepath.Clean(root))
		if err != nil {
			continue
		}
		if resolved == r || strings.HasPrefix(resolved, r+string(filepath.Separator)) {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("cwd %q is outside the allowed session_dirs", cwd)
}
