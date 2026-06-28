package tmux

import (
	"fmt"
	"strings"
)

// delimToken is how tmux 3.5a renders the 0x1f (Unit Separator) byte we inject as
// the -F field delimiter: the literal four-character text `\037`. tmux escapes the
// control byte in the format *template* this way, so list-* output never contains a
// raw 0x1f to split on — we split on this token instead. (Empirically verified; see
// the M2 probe and the M1 carry-over.)
const delimToken = `\037`

// splitFields splits one tmux -F output line into exactly want fields on the
// delimiter token. A record that does not split into exactly want fields is a
// hard error, never a silent drop: a raw command/path field that happens to
// contain the literal text `\037`, or a name field carrying a raw 0x1f byte,
// mis-splits, and we must surface that rather than silently lose the pane/window.
func splitFields(line string, want int) ([]string, error) {
	fields := strings.Split(line, delimToken)
	if len(fields) != want {
		return nil, fmt.Errorf("tmux -F record split into %d fields, want %d: %q", len(fields), want, line)
	}
	return fields, nil
}

// unescapeName reverses tmux's C-style escaping of name fields (session_name,
// window_name): a doubled backslash is one literal backslash, and a backslash
// followed by exactly three octal digits is that byte (tmux escapes every control
// byte this way, e.g. 0x1f -> \037, 0x0a -> \012). Any other backslash is left
// literal. Command/path fields are emitted raw by tmux and must NOT be passed
// through this.
func unescapeName(s string) string {
	if !strings.ContainsRune(s, '\\') {
		return s // fast path: nothing escaped
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		if s[i] == '\\' && i+1 < len(s) {
			if s[i+1] == '\\' {
				out = append(out, '\\')
				i += 2
				continue
			}
			if i+3 < len(s) && isOctal(s[i+1]) && isOctal(s[i+2]) && isOctal(s[i+3]) {
				out = append(out, (s[i+1]-'0')<<6|(s[i+2]-'0')<<3|(s[i+3]-'0'))
				i += 4
				continue
			}
		}
		out = append(out, s[i])
		i++
	}
	return string(out)
}
