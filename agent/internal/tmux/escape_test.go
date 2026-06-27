package tmux

import "testing"

// These fixtures encode tmux 3.5a's empirically-probed -F behaviour (see the M1
// carry-over + the M2 probe): the injected 0x1f delimiter renders as the literal
// 4-char token `\037`; name fields (session_name/window_name) are C-escaped
// (backslash -> \\, control byte -> \NNN octal); command/path fields are raw.

func TestUnescapeName(t *testing.T) {
	cases := []struct {
		name string
		in   string // exactly what tmux emits for the field
		want string // the real underlying value
	}{
		{"plain", `main`, "main"},
		{"backslash_and_space", `proj a\\b`, `proj a\b`}, // one real backslash
		{"double_backslash", `x\\\\y`, `x\\y`},           // two real backslashes
		{"unit_separator_byte", `a\037b`, "a\x1fb"},       // a 0x1f byte inside a name
		{"newline_byte", `l1\012l2`, "l1\nl2"},
		{"trailing_backslash_literal", `a\z`, `a\z`}, // backslash not starting an escape stays literal
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := unescapeName(c.in); got != c.want {
				t.Fatalf("unescapeName(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestSplitFieldsOnDelimiterToken(t *testing.T) {
	// fields delimited by the rendered `\037` token, not a raw 0x1f byte.
	line := `@1\037` + `0\037` + `proj a\\b\037` + `1\037` + `%0\037` + `bash\037` + `/home/dev/a\b\037` + `1`
	got, err := splitFields(line, 8)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"@1", "0", `proj a\\b`, "1", "%0", "bash", `/home/dev/a\b`, "1"}
	if len(got) != len(want) {
		t.Fatalf("got %d fields %#v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("field %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSplitFieldsWrongCountIsError(t *testing.T) {
	// A path containing the literal 4-char text \037 mis-splits into too many
	// fields; that must be a hard error, never a silent drop.
	line := `@1\037` + `0\037` + `w\037` + `1\037` + `%0\037` + `bash\037` + `/weird\037path\037` + `1`
	if _, err := splitFields(line, 8); err == nil {
		t.Fatal("want error for record that splits into the wrong field count, got nil")
	}
}
