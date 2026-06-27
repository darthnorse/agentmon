package tmux

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

// ExecRunner runs the real tmux binary and returns its stdout VERBATIM. On
// failure it folds tmux's stderr into the error so Discover can detect the benign
// "no server running" message.
//
// It deliberately does no normalisation: Discover (escape.go) is the single,
// faithful de-escaper. tmux 3.5a's -F escaping is field-dependent — the injected
// 0x1f delimiter renders as the literal token `\037`, name fields (session_name,
// window_name) are C-escaped (backslash -> \\, control byte -> \NNN) while
// command/path fields are emitted raw — so splitFields/unescapeName decode each
// field by position rather than blanket-normalising the whole stream (which could
// mis-split a record and silently drop a pane). See escape.go.
func ExecRunner(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "tmux", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("tmux %v: %w: %s", args, err, bytes.TrimSpace(stderr.Bytes()))
	}
	return stdout.Bytes(), nil
}
