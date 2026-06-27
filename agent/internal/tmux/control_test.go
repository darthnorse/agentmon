package tmux

import (
	"reflect"
	"testing"
)

// Cases mirror bytes observed from real tmux 3.5a control-mode probes.
func TestUnescapeOutput(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []byte
	}{
		{"plain ascii", "root@aigallery", []byte("root@aigallery")},
		{"control bytes", `\015\012`, []byte{0x0d, 0x0a}},
		{"esc + csi", `\033[?2004l`, append([]byte{0x1b}, []byte("[?2004l")...)},
		{"escaped backslash", `A\134B`, []byte("A\\B")},
		{"tab", `\011`, []byte{0x09}},
		// high bytes (UTF-8 é = c3 a9) pass through raw, not octal-escaped
		{"utf8 raw", "C\xc3\xa9Z", []byte{'C', 0xc3, 0xa9, 'Z'}},
		// the full program-output line captured in the probe
		{"probe line", `\015\012\033[?2004l\015A\134B\011C` + "\xc3\xa9" + `Z\015\012`,
			[]byte{0x0d, 0x0a, 0x1b, '[', '?', '2', '0', '0', '4', 'l', 0x0d,
				'A', '\\', 'B', 0x09, 'C', 0xc3, 0xa9, 'Z', 0x0d, 0x0a}},
		{"trailing lone backslash", `x\`, []byte{'x', '\\'}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := unescapeOutput([]byte(c.in))
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("unescapeOutput(%q)\n got=%v\nwant=%v", c.in, got, c.want)
			}
		})
	}
}

func TestParseOutput(t *testing.T) {
	pane, data := parseOutput([]byte(`%output %0 \033[?2004hroot@x:~# `))
	if pane != "%0" {
		t.Fatalf("pane=%q want %%0", pane)
	}
	if string(data) != `\033[?2004hroot@x:~# ` {
		t.Fatalf("data=%q", data)
	}
}

func TestEncodeSendKeys(t *testing.T) {
	// the exact sequence verified to land byte-for-byte on the pty: ESC [ A é X
	got := encodeSendKeys("%0", []byte{0x1b, '[', 'A', 0xc3, 0xa9, 'X'})
	want := []string{"send-keys -t %0 -H 1b 5b 41 c3 a9 58"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestEncodeSendKeysChunks(t *testing.T) {
	in := make([]byte, sendKeysChunk+5) // forces a second command line
	cmds := encodeSendKeys("%1", in)
	if len(cmds) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(cmds))
	}
}

func TestEncodeSendKeysEmpty(t *testing.T) {
	if cmds := encodeSendKeys("%0", nil); cmds != nil {
		t.Fatalf("expected nil for empty input, got %v", cmds)
	}
}
