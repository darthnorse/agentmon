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

func TestIsCodexPath(t *testing.T) {
	if !isCodexPath("/root/.codex/sessions/2026/07/14/rollout.jsonl", "/root/.codex/sessions") {
		t.Fatal("expected codex path to match")
	}
	if isCodexPath("/root/.claude/projects/-root-agentmon/sess.jsonl", "/root/.codex/sessions") {
		t.Fatal("claude path must not match codex root")
	}
}

func TestClaudeEncodeCwd(t *testing.T) {
	cases := map[string]string{
		"/root/agentmon":                   "-root-agentmon",
		"/root/agentmon/spike-0.5/scratch": "-root-agentmon-spike-0-5-scratch",
	}
	for in, want := range cases {
		if got := claudeEncodeCwd(in); got != want {
			t.Fatalf("claudeEncodeCwd(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEnumerateChildTranscriptsFiltersByCwdEncoding(t *testing.T) {
	root := t.TempDir()
	mineDir := filepath.Join(root, "-root-agentmon")
	os.MkdirAll(mineDir, 0o755)
	mine := filepath.Join(mineDir, "sess-a.jsonl")
	os.WriteFile(mine, []byte("{}\n"), 0o644)

	otherDir := filepath.Join(root, "-root-otherproj")
	os.MkdirAll(otherDir, 0o755)
	os.WriteFile(filepath.Join(otherDir, "sess-b.jsonl"), []byte("{}\n"), 0o644)

	got := enumerateChildTranscripts(root, "/root/agentmon", time.Time{})
	if len(got) != 1 || got[0] != mine {
		t.Fatalf("want only %s, got %v", mine, got)
	}
}

func TestEnumerateChildTranscriptsRespectsSince(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "-root-agentmon")
	os.MkdirAll(dir, 0o755)
	stale := filepath.Join(dir, "old.jsonl")
	os.WriteFile(stale, []byte("{}\n"), 0o644)
	old := time.Now().Add(-time.Hour)
	os.Chtimes(stale, old, old)

	got := enumerateChildTranscripts(root, "/root/agentmon", time.Now())
	if len(got) != 0 {
		t.Fatalf("want stale transcript excluded, got %v", got)
	}
}
