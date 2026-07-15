package usage

import (
	"os"
	"testing"
)

func TestParseClaudeRawRows(t *testing.T) {
	f, err := os.Open("testdata/claude_dup.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, err := ParseClaude(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 usage rows (2 dup + 1), got %d", len(got))
	}
	if got[0].ID != "msg_A" || got[0].Model != "claude-opus-4-8" || got[0].CacheRead != 1000 || got[0].CacheWrite != 10 {
		t.Fatalf("bad first row: %+v", got[0])
	}
}
