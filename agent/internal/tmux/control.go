package tmux

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os/exec"
	"regexp"
	"strings"
	"sync"
)

// ControlClient owns one `tmux -C attach` control-mode connection for a single
// session, streaming a target pane's output and injecting input via send-keys.
//
// All four mechanics below were verified empirically against tmux 3.5a (see the
// probes in the build session); this code just encodes those findings.
type ControlClient struct {
	socket  string
	session string
	pane    string // e.g. "%0"

	cmd   *exec.Cmd
	stdin io.WriteCloser

	mu     sync.Mutex // serialises writes to the control client's stdin
	closed bool

	// quit is closed by Close to unblock readLoop's send to Output if the
	// consumer has stopped draining — otherwise a full Output channel deadlocks
	// the parser goroutine (and leaks it + the tmux process).
	quit chan struct{}
	// Output delivers raw, un-escaped pty bytes for the target pane.
	Output chan []byte
	// Done closes when the control client exits (%exit or process death), after
	// the tmux process has been reaped.
	Done chan struct{}
	// attached closes when the attach-session handshake's reply block terminates
	// (first %end/%error): from that point on, every pane write is guaranteed to be
	// delivered as %output, so a snapshot taken AFTER this cannot leave a gap.
	// (Bytes written between attach and the snapshot appear in both — accepted
	// duplication trade-off, see the gate in api/ws.go.)
	attachOnce sync.Once
	attached   chan struct{}
}

// paneIDRe matches a tmux pane id ("%0", "%37"). The pane string is interpolated
// into the control-mode command stream (send-keys), so it MUST be validated to
// keep a caller-supplied value (e.g. a request-derived pane id in M2) from
// injecting a second tmux command via an embedded newline.
var paneIDRe = regexp.MustCompile(`^%[0-9]+$`)

// ValidatePaneID reports whether id is a syntactically valid tmux pane id ("%0",
// "%37"). Both the WS handler (before resolution) and NewControlClient (before any
// exec) gate on this — one source of the pattern for the two layers.
func ValidatePaneID(id string) bool { return paneIDRe.MatchString(id) }

// NewControlClient starts the control-mode client. The caller MUST keep the
// process alive by reading Output; a dead reader will block the parser.
//
// Critical gotcha (verified): if the control client's stdin hits EOF it exits
// immediately with %exit. We therefore hold the stdin pipe open for the whole
// session and use it for send-keys / refresh-client.
func NewControlClient(ctx context.Context, socket, session, pane string) (*ControlClient, error) {
	if !ValidatePaneID(pane) {
		return nil, fmt.Errorf("invalid pane id %q", pane)
	}
	args := with(socketArgs(socket), "-C", "attach-session", "-t", session)
	cmd := exec.CommandContext(ctx, "tmux", args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	c := &ControlClient{
		socket:   socket,
		session:  session,
		pane:     pane,
		cmd:      cmd,
		stdin:    stdin,
		quit:     make(chan struct{}),
		Output:   make(chan []byte, 256),
		Done:     make(chan struct{}),
		attached: make(chan struct{}),
	}
	go c.readLoop(stdout)
	return c, nil
}

func (c *ControlClient) readLoop(stdout io.Reader) {
	// Reap the tmux process when the loop ends (defers run LIFO, so Wait runs
	// before Done closes — Done signals "exited AND reaped").
	defer close(c.Done)
	defer func() { _ = c.cmd.Wait() }()
	r := bufio.NewReader(stdout)
	inBlock := false // inside a %begin/%end command-response block
	for {
		// tmux control mode is strictly line-oriented: raw newlines in pty data
		// are escaped to \012, so a literal '\n' only ever ends a notification.
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			line = trimNL(line)
			switch {
			case hasPrefix(line, "%output "):
				if inBlock {
					continue
				}
				pane, data := parseOutput(line)
				if pane == c.pane {
					select {
					case c.Output <- unescapeOutput(data):
					case <-c.quit:
						return
					}
				}
			case hasPrefix(line, "%begin"):
				inBlock = true
			case hasPrefix(line, "%end"), hasPrefix(line, "%error"):
				inBlock = false
				c.markAttached()
			case hasPrefix(line, "%exit"):
				return
				// %window-*, %layout-change, %session-changed etc: ignored for
				// the single-pane spike.
			}
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("control read: %v", err)
			}
			return
		}
	}
}

// markAttached signals AttachedChan exactly once, on the first %end/%error — the
// reply terminator of the implicit attach-session command. nil-safe: hand-built
// test clients may not populate the channel.
func (c *ControlClient) markAttached() {
	if c.attached == nil {
		return
	}
	c.attachOnce.Do(func() { close(c.attached) })
}

// AttachedChan closes once the control-mode attach handshake has completed. Callers
// should also watch DoneChan — a client that dies pre-attach never signals this.
func (c *ControlClient) AttachedChan() <-chan struct{} { return c.attached }

// SendInput injects raw bytes into the pane as keystrokes via `send-keys -H`.
// Verified: this delivers exact bytes (incl. a lone 0x1b and UTF-8) to the pty,
// bypassing tmux's terminal ESC-timeout. We chunk so command lines stay short.
func (c *ControlClient) SendInput(b []byte) error {
	for _, cmd := range encodeSendKeys(c.pane, b) {
		if err := c.writeCmd(cmd); err != nil {
			return err
		}
	}
	return nil
}

// Resize makes the passive control client adopt the viewer's size; under
// `window-size latest` this resizes the window. Verified on 3.5a.
func (c *ControlClient) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	return c.writeCmd(fmt.Sprintf("refresh-client -C %dx%d", cols, rows))
}

func (c *ControlClient) writeCmd(cmd string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return io.ErrClosedPipe
	}
	_, err := io.WriteString(c.stdin, cmd+"\n")
	return err
}

func (c *ControlClient) Close() {
	c.mu.Lock()
	if !c.closed {
		c.closed = true
		close(c.quit)
		_ = c.stdin.Close()
	}
	c.mu.Unlock()
	_ = c.cmd.Process.Kill()
}

// ---- pure helpers (unit-tested in control_test.go) ----

// parseOutput splits a "%output %<pane> <data>" line into pane id and the still-
// escaped data bytes. Data may contain literal spaces, so we split only twice.
func parseOutput(line []byte) (pane string, data []byte) {
	// line starts with "%output "
	rest := line[len("%output "):]
	sp := indexByte(rest, ' ')
	if sp < 0 {
		return string(rest), nil
	}
	return string(rest[:sp]), rest[sp+1:]
}

// unescapeOutput reverses tmux control-mode escaping. Verified rule on 3.5a:
// a backslash followed by exactly 3 octal digits encodes one byte (used for all
// bytes < 0x20 and for backslash itself, \134); every other byte is literal,
// including raw high bytes (UTF-8 passes through untouched).
func unescapeOutput(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); {
		if b[i] == '\\' && i+3 < len(b) &&
			isOctal(b[i+1]) && isOctal(b[i+2]) && isOctal(b[i+3]) {
			v := (b[i+1]-'0')<<6 | (b[i+2]-'0')<<3 | (b[i+3] - '0')
			out = append(out, v)
			i += 4
			continue
		}
		out = append(out, b[i])
		i++
	}
	return out
}

// encodeSendKeys turns raw input bytes into one or more `send-keys -t <pane> -H`
// command lines (hex tokens), chunked so no single command line gets too long.
const sendKeysChunk = 200

func encodeSendKeys(pane string, b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	var cmds []string
	for off := 0; off < len(b); off += sendKeysChunk {
		end := off + sendKeysChunk
		if end > len(b) {
			end = len(b)
		}
		var sb strings.Builder
		sb.WriteString("send-keys -t ")
		sb.WriteString(pane)
		sb.WriteString(" -H")
		for _, by := range b[off:end] {
			fmt.Fprintf(&sb, " %02x", by)
		}
		cmds = append(cmds, sb.String())
	}
	return cmds
}

// OutputChan / DoneChan expose the client's channels behind read-only types so it
// satisfies api.PaneConn without the api package importing concrete fields.
func (c *ControlClient) OutputChan() <-chan []byte { return c.Output }
func (c *ControlClient) DoneChan() <-chan struct{} { return c.Done }

func isOctal(c byte) bool { return c >= '0' && c <= '7' }

func trimNL(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

func hasPrefix(b []byte, s string) bool {
	return len(b) >= len(s) && string(b[:len(s)]) == s
}

func indexByte(b []byte, c byte) int {
	for i := range b {
		if b[i] == c {
			return i
		}
	}
	return -1
}
