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

// ErrNoSession is returned by RenameSession when the source session `from` does
// not exist on the target socket. The REST handler maps it to HTTP 404.
var ErrNoSession = errors.New("no such session")

func isNoSession(b []byte) bool {
	low := bytes.ToLower(b)
	return bytes.Contains(low, []byte("can't find session")) ||
		bytes.Contains(low, []byte("session not found")) ||
		bytes.Contains(low, []byte("no such session"))
}

// RenameSession renames the existing tmux session `from` to `to` on the socket,
// via the arg-array Runner (no shell — both names are positional args, never
// interpolated). The caller MUST have validated `to` (shared.ValidateSessionName);
// `from` is an existing tmux name passed as a positional -t arg. A target name
// that already exists → ErrSessionExists (409); an unknown `from` → ErrNoSession (404).
func RenameSession(ctx context.Context, run Runner, socket, from, to string) error {
	out, err := run(ctx, with(socketArgs(socket), "rename-session", "-t", from, to)...)
	if err != nil {
		// ExecRunner folds stderr into the error string; a fake Runner may surface
		// the message via either channel — check both.
		errb := []byte(err.Error())
		if isDuplicateSession(out) || isDuplicateSession(errb) {
			return ErrSessionExists
		}
		if isNoSession(out) || isNoSession(errb) {
			return ErrNoSession
		}
		return fmt.Errorf("tmux rename-session: %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

// KillSession terminates the tmux session `name` on the socket via the arg-array
// Runner (no shell — the name is a positional -t arg). The socket is the agent's
// own configured socket, never client input, so this cannot target another socket.
// An unknown session → ErrNoSession (404). Kills the whole session (all windows).
func KillSession(ctx context.Context, run Runner, socket, name string) error {
	out, err := run(ctx, with(socketArgs(socket), "kill-session", "-t", name)...)
	if err != nil {
		errb := []byte(err.Error())
		if isNoSession(out) || isNoSession(errb) {
			return ErrNoSession
		}
		return fmt.Errorf("tmux kill-session: %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
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
	sep := string(filepath.Separator)
	for _, root := range allowed {
		r, err := filepath.EvalSymlinks(filepath.Clean(root))
		if err != nil {
			continue
		}
		// TrimSuffix handles a root that resolves to "/" (boundary "/"), where a plain
		// r+sep would be "//" and reject every subdirectory.
		if resolved == r || strings.HasPrefix(resolved, strings.TrimSuffix(r, sep)+sep) {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("cwd %q is outside the allowed session_dirs", cwd)
}
