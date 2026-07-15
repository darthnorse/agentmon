package usage

import (
	"os"
	"testing"
)

func TestParseCodexLastCumulative(t *testing.T) {
	f, _ := os.Open("testdata/codex_rollout.jsonl")
	defer f.Close()
	got, ok, err := ParseCodex(f)
	if err != nil || !ok {
		t.Fatalf("want ok, got ok=%v err=%v", ok, err)
	}
	// cache_read=1000, input=1200-1000=200, output=30, cache_write=0
	if got.Model != "gpt-5.6-sol" || got.CacheRead != 1000 || got.Input != 200 || got.Output != 30 || got.CacheWrite != 0 {
		t.Fatalf("bad normalization: %+v", got)
	}
}

// TestParseCodexModelSnapshotAtTokenCount covers Fix 3: the returned Model
// must be whichever model was CURRENT at the moment the last token_count was
// emitted, not the last model seen anywhere in the rollout. The fixture's
// sequence is model-a -> token_count(total) -> model-b (no later
// token_count) — a later turn's context record must not relabel the earlier
// cumulative total.
func TestParseCodexModelSnapshotAtTokenCount(t *testing.T) {
	f, err := os.Open("testdata/codex_rollout_model_after.jsonl")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()
	got, ok, err := ParseCodex(f)
	if err != nil || !ok {
		t.Fatalf("want ok, got ok=%v err=%v", ok, err)
	}
	if got.Model != "model-a" {
		t.Fatalf("Model = %q, want %q (model current AT the token_count, not model-b seen only afterward)", got.Model, "model-a")
	}
}

// TestParseCodexNestedCollaborationModeModel covers Fix CM: when the ONLY
// model source in the rollout is the nested
// payload.collaboration_mode.settings.model (no top-level turn_context
// payload.model at all), ParseCodex must still pick it up rather than
// returning "" — the doc comment already promised this source is read.
func TestParseCodexNestedCollaborationModeModel(t *testing.T) {
	f, err := os.Open("testdata/codex_rollout_nested_model.jsonl")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()
	got, ok, err := ParseCodex(f)
	if err != nil || !ok {
		t.Fatalf("want ok, got ok=%v err=%v", ok, err)
	}
	if got.Model != "nested-only-model" {
		t.Fatalf("Model = %q, want %q (nested collaboration_mode.settings.model)", got.Model, "nested-only-model")
	}
}
