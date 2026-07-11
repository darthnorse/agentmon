package runnerfiles

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallSkillsWritesAllThree(t *testing.T) {
	home := t.TempDir()
	written, err := InstallSkills(home)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join(home, ".claude", "commands", "epic-pipeline.md"),
		filepath.Join(home, ".claude", "commands", "plan-epics.md"),
		filepath.Join(home, ".codex", "prompts", "epic-pipeline.md"),
	}
	if len(written) != len(want) {
		t.Fatalf("written = %v", written)
	}
	for i, p := range want {
		if written[i] != p {
			t.Fatalf("written[%d] = %s, want %s", i, written[i], p)
		}
		disk, err := os.ReadFile(p)
		if err != nil || len(disk) == 0 {
			t.Fatalf("%s: %v (len %d)", p, err, len(disk))
		}
		embedded, err := fsys.ReadFile(installs[i].src)
		if err != nil || string(disk) != string(embedded) {
			t.Fatalf("%s does not match embedded content", p)
		}
	}
}

func TestInstallSkillsRequiresHome(t *testing.T) {
	if _, err := InstallSkills(""); err == nil {
		t.Fatal("empty home must error")
	}
}
