package usage

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenTranscriptFDsFindsOpenJSONL(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sess.jsonl")
	f, _ := os.Create(p)
	defer f.Close()
	got := openTranscriptFDs(context.Background(), os.Getpid())
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

// TestDescendantsWalksFullSubtree spawns a real process tree two levels deep
// (test process -> sh -> two sleep grandchildren) and asserts descendants()
// finds the grandchildren, not just the direct child. This is a
// behavior-preserving regression test for the single-pass /proc refactor
// (the old doc comment claimed "one level ... is enough", but the code
// always walked full-depth via recursion — both the old and new
// implementation must find the grandchildren).
func TestDescendantsWalksFullSubtree(t *testing.T) {
	shPath, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not available")
	}
	sleepPath, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep not available")
	}
	cmd := exec.Command(shPath, "-c", sleepPath+" 30 & "+sleepPath+" 30 & wait")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	shPid := cmd.Process.Pid
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
	}()

	var got []int
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got = descendants(context.Background(), os.Getpid())
		if len(got) >= 3 { // sh + its two sleep grandchildren
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if len(got) < 3 {
		t.Fatalf("want sh + 2 grandchild sleeps in descendants(self), got %v", got)
	}
	foundSh := false
	for _, p := range got {
		if p == shPid {
			foundSh = true
		}
	}
	if !foundSh {
		t.Fatalf("want direct child sh pid %d in %v", shPid, got)
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
	got := enumerateChildRollouts(context.Background(), root, "/wt/epic7", time.Time{})
	if len(got) != 1 || got[0] != mine {
		t.Fatalf("want only %s, got %v", mine, got)
	}
}

// TestEnumerateChildRolloutsAbortsOnCancelledContext covers Fix 4's
// cancellation plumbing: the WalkDir callback returns ctx.Err() once ctx is
// done, aborting the walk — a cancelled capture must yield nothing rather
// than pay for a full filesystem walk.
func TestEnumerateChildRolloutsAbortsOnCancelledContext(t *testing.T) {
	root := t.TempDir()
	day := filepath.Join(root, "2026", "07", "14")
	os.MkdirAll(day, 0o755)
	os.WriteFile(filepath.Join(day, "rollout-a.jsonl"), []byte(`{"payload":{"cwd":"/wt/epic7"}}`+"\n"), 0o644)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := enumerateChildRollouts(ctx, root, "/wt/epic7", time.Time{})
	if len(got) != 0 {
		t.Fatalf("want no results once ctx is already cancelled, got %v", got)
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

	got := enumerateChildTranscripts(context.Background(), root, "/root/agentmon", time.Time{})
	if len(got) != 1 || got[0] != mine {
		t.Fatalf("want only %s, got %v", mine, got)
	}
}

func TestSubagentTranscriptsFindsNestedLensFiles(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(dir, "parent-uuid.jsonl")
	os.WriteFile(parent, []byte("{}\n"), 0o644)

	subDir := filepath.Join(dir, "parent-uuid", "subagents")
	os.MkdirAll(subDir, 0o755)
	sub := filepath.Join(subDir, "agent-x.jsonl")
	os.WriteFile(sub, []byte("{}\n"), 0o644)

	got := subagentTranscripts(parent)
	if len(got) != 1 || got[0] != sub {
		t.Fatalf("want only %s, got %v", sub, got)
	}
}

func TestSubagentTranscriptsNonJSONLParent(t *testing.T) {
	if got := subagentTranscripts("/some/dir/not-a-transcript"); got != nil {
		t.Fatalf("want nil for non-.jsonl parent, got %v", got)
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

	got := enumerateChildTranscripts(context.Background(), root, "/root/agentmon", time.Now())
	if len(got) != 0 {
		t.Fatalf("want stale transcript excluded, got %v", got)
	}
}
