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
// tmux 3.x escapes non-printable bytes in list-* output using C-style octal
// notation (e.g. byte 0x1f → the four characters \037). We normalise that back
// so Discover's fieldSep (\x1f) split works on real tmux output.
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
