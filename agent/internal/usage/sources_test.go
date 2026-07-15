package usage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenTranscriptFDsFindsOpenJSONL(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sess.jsonl")
	f, _ := os.Create(p)
	defer f.Close()
	got := openTranscriptFDs(os.Getpid())
	found := false
	for _, g := range got {
		if g == p {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected to find open %s in %v", p, got)
	}
}

func TestEnumerateChildRolloutsFiltersByCwd(t *testing.T) {
	root := t.TempDir()
	day := filepath.Join(root, "2026", "07", "14")
	os.MkdirAll(day, 0o755)
	mine := filepath.Join(day, "rollout-a.jsonl")
	os.WriteFile(mine, []byte(`{"payload":{"cwd":"/wt/epic7"}}`+"\n"), 0o644)
	other := filepath.Join(day, "rollout-b.jsonl")
	os.WriteFile(other, []byte(`{"payload":{"cwd":"/wt/epic9"}}`+"\n"), 0o644)
	got := enumerateChildRollouts(root, "/wt/epic7", time.Time{})
	if len(got) != 1 || got[0] != mine {
		t.Fatalf("want only %s, got %v", mine, got)
	}
}
