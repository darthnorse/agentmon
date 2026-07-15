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
