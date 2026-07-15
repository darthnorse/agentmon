package usage

import (
	"testing"

	"agentmon/shared"
)

func TestAggregateDedupAndPerModel(t *testing.T) {
	// Two Claude files sharing msg_A must count it ONCE; Codex summed separately.
	s := Sources{
		Claude: []string{"testdata/claude_dup.jsonl", "testdata/claude_dup.jsonl"},
		Codex:  []string{"testdata/codex_rollout.jsonl"},
	}
	got := Aggregate(s)
	var claude, codex *shared.Usage
	for i := range got {
		switch got[i].Provider {
		case "claude":
			claude = &got[i]
		case "codex":
			codex = &got[i]
		}
	}
	if claude == nil || claude.Input != 107 || claude.Output != 53 || claude.CacheRead != 1000 || claude.CacheWrite != 10 {
		t.Fatalf("claude dedup wrong: %+v", claude) // msg_A once + msg_B, despite the file listed twice
	}
	if codex == nil || codex.Input != 200 || codex.CacheRead != 1000 {
		t.Fatalf("codex wrong: %+v", codex)
	}
}
