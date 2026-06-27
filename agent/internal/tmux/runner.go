package tmux

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

// ExecRunner runs the real tmux binary. On failure it folds tmux's stderr into
// the error so Discover can detect the benign "no server running" message.
//
// On success it normalises tmux's list-* output so Discover's fieldSep split
// works: tmux 3.5a renders the 0x1f delimiter we inject into -F formats as the
// four literal characters \037, so we map \037 back to 0x1f.
//
// KNOWN LIMITATION (tracked for the M2 agent review gate): this is a heuristic,
// not a faithful decoder. tmux's -F escaping is field-dependent and NOT uniform
// — e.g. a backslash in a window name comes back as \\ (not octal \134), while a
// backslash in pane_current_path is returned raw. So a field value that itself
// contains the literal text \037 (backslash,0,3,7) or a literal backslash can be
// mis-normalised, splitting a record into the wrong field count and silently
// dropping that pane/window from the tree. Real single-user session/window names
// and paths don't contain these, so normal discovery is unaffected; the robust
// fix (an unambiguous delimiter strategy or a faithful per-field decoder) is
// deferred to M2.
func ExecRunner(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "tmux", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("tmux %v: %w: %s", args, err, bytes.TrimSpace(stderr.Bytes()))
	}
	out := bytes.ReplaceAll(stdout.Bytes(), []byte(`\037`), []byte(fieldSep))
	return out, nil
}
