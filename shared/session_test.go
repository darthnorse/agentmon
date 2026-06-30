package shared

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateSessionName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		ok   bool
	}{
		// accept
		{"simple", "dockmon", true},
		{"hyphenated", "streammon-api", true},
		{"single char", "a", true},
		{"single digit", "9", true},
		{"mixed charset", "A_b-9", true},
		{"underscore start", "0_a", true},
		{"max length 64", strings.Repeat("a", 64), true},

		// reject
		{"empty", "", false},
		{"leading hyphen", "-leading", false},
		{"leading underscore", "_x", false},
		{"leading dot", ".dot", false},
		{"has space", "has space", false},
		{"slash", "a/b", false},
		{"colon", "a:b", false},
		{"dot", "a.b", false},
		{"too long 65", strings.Repeat("a", 65), false},
		{"bang", "foo!", false},
		{"unicode", "café", false},
		{"tab", "a\tb", false},
		{"newline", "a\nb", false},
		{"trailing newline", "ab\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateSessionName(c.in)
			if c.ok && err != nil {
				t.Fatalf("ValidateSessionName(%q) = %v, want nil", c.in, err)
			}
			if !c.ok && err == nil {
				t.Fatalf("ValidateSessionName(%q) = nil, want error", c.in)
			}
		})
	}
}

// TestCreateSessionWireTags locks the JSON field names — they are the additive
// wire contract consumed verbatim by the agent (encode) and hub (decode) and the
// web mirror; a typo would compile but break the cross-layer round-trip silently.
func TestCreateSessionWireTags(t *testing.T) {
	b, err := json.Marshal(CreateSessionRequest{Name: "proj", Cwd: "/a", Command: "x"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"name":"proj"`, `"cwd":"/a"`, `"command":"x"`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("CreateSessionRequest JSON %s missing %s", b, want)
		}
	}
	// cwd/command are omitempty.
	if b, _ := json.Marshal(CreateSessionRequest{Name: "p"}); strings.Contains(string(b), "cwd") || strings.Contains(string(b), "command") {
		t.Fatalf("empty cwd/command must be omitted: %s", b)
	}
	var req CreateSessionRequest
	if err := json.Unmarshal([]byte(`{"name":"x","cwd":"/d","command":""}`), &req); err != nil || req.Name != "x" || req.Cwd != "/d" {
		t.Fatalf("unmarshal: err=%v req=%+v", err, req)
	}
	if b, _ := json.Marshal(CreateSessionResponse{Name: "y"}); string(b) != `{"name":"y"}` {
		t.Fatalf("CreateSessionResponse JSON = %s, want {\"name\":\"y\"}", b)
	}
}

func TestRollUpPriority(t *testing.T) {
	cases := []struct {
		name string
		in   []State
		want State
	}{
		{"empty", nil, StateUnknown},
		{"single idle", []State{StateIdle}, StateIdle},
		{"blocked beats all", []State{StateIdle, StateWorking, StateDone, StateBlocked}, StateBlocked},
		{"done beats working", []State{StateWorking, StateDone, StateIdle}, StateDone},
		{"working beats idle", []State{StateIdle, StateWorking}, StateWorking},
		{"idle beats unknown", []State{StateUnknown, StateIdle}, StateIdle},
		{"all unknown", []State{StateUnknown, StateUnknown}, StateUnknown},
		{"unrecognized is unknown", []State{"weird"}, StateUnknown},
		{"unrecognized with idle", []State{"weird", StateIdle}, StateIdle},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RollUp(c.in...); got != c.want {
				t.Fatalf("RollUp(%v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
