package usage

import (
	"os"
	"path/filepath"
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

// TestAggregateCodexPathDedup reproduces a live rollout matching BOTH the
// /proc-fd scan and the cwd walk, landing in Sources.Codex twice. Codex rows
// have no per-record id to dedup on (unlike Claude), so without path dedup
// the cumulative total is summed twice (~2x tokens).
func TestAggregateCodexPathDedup(t *testing.T) {
	s := Sources{Codex: []string{"testdata/codex_rollout.jsonl", "testdata/codex_rollout.jsonl"}}
	got := Aggregate(s)
	if len(got) != 1 {
		t.Fatalf("want 1 codex usage entry, got %d: %+v", len(got), got)
	}
	if got[0].Input != 200 || got[0].CacheRead != 1000 || got[0].Output != 30 {
		t.Fatalf("codex path dedup wrong (want single-counted, not doubled): %+v", got[0])
	}
}

// TestAggregateClaudePathDedupProtectsIDlessRows covers the case
// message.id-only dedup can't: a Claude transcript row with no message.id
// (r.ID == "" always bypasses the seenID dedup in Aggregate). If the SAME
// path is discovered twice (fd scan + child-transcript enumeration), only
// path-level dedup stops the id-less row from being summed twice.
func TestAggregateClaudePathDedupProtectsIDlessRows(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "idless.jsonl")
	line := `{"type":"assistant","message":{"model":"claude-opus-4-8","usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}` + "\n"
	if err := os.WriteFile(p, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	got := Aggregate(Sources{Claude: []string{p, p}})
	if len(got) != 1 || got[0].Input != 10 || got[0].Output != 5 {
		t.Fatalf("want single-counted id-less row (not doubled), got %+v", got)
	}
}
