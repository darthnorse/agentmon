package usage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestNewCapturerIncludesSubagentTranscripts reproduces the live layout:
// /multi-review lens subagent transcripts live nested at
// <claudeRoot>/<encoded-cwd>/<parent-uuid>/subagents/agent-*.jsonl, alongside
// the fd-bound parent transcript <claudeRoot>/<encoded-cwd>/<parent-uuid>.jsonl.
// A flat glob of the project dir misses the nested file entirely.
func TestNewCapturerIncludesSubagentTranscripts(t *testing.T) {
	tempHome := t.TempDir()
	cwd := filepath.Join(tempHome, "work")
	projectDir := filepath.Join(tempHome, ".claude", "projects", claudeEncodeCwd(cwd))
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	parentPath := filepath.Join(projectDir, "parent-uuid.jsonl")
	writeUsageLine(t, parentPath, "msg_parent", "claude-opus-4-8", 7)

	subDir := filepath.Join(projectDir, "parent-uuid", "subagents")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	subPath := filepath.Join(subDir, "agent-x.jsonl")
	writeUsageLine(t, subPath, "msg_sub", "claude-haiku-4-8", 11)

	// Bind the parent transcript to this test process's open fds, mirroring
	// how openTranscriptFDs finds the real runner's parent transcript.
	f, err := os.Open(parentPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tempHome)
	defer os.Setenv("HOME", origHome)

	pid := os.Getpid()
	capture := NewCapturer(func(ctx context.Context, socket, pane string) (int, string, string, time.Time, error) {
		return pid, cwd, "claude", time.Time{}, nil
	})

	got := capture(context.Background(), "sock", "pane")

	var sawParent, sawSub bool
	for _, u := range got {
		switch u.Model {
		case "claude-opus-4-8":
			if u.Input == 7 {
				sawParent = true
			}
		case "claude-haiku-4-8":
			if u.Input == 11 {
				sawSub = true
			}
		}
	}
	if !sawParent {
		t.Fatalf("want parent transcript usage present, got %+v", got)
	}
	if !sawSub {
		t.Fatalf("want nested subagent transcript usage present (this is the fix under test), got %+v", got)
	}
}

func writeUsageLine(t *testing.T, path, id, model string, input int64) {
	t.Helper()
	line := struct {
		Type    string `json:"type"`
		Message struct {
			ID    string `json:"id"`
			Model string `json:"model"`
			Usage struct {
				Input      int64 `json:"input_tokens"`
				Output     int64 `json:"output_tokens"`
				CacheRead  int64 `json:"cache_read_input_tokens"`
				CacheWrite int64 `json:"cache_creation_input_tokens"`
			} `json:"usage"`
		} `json:"message"`
	}{Type: "assistant"}
	line.Message.ID = id
	line.Message.Model = model
	line.Message.Usage.Input = input
	line.Message.Usage.Output = 1
	b, err := json.Marshal(line)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}
