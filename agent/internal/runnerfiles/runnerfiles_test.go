package runnerfiles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallSkillsWritesAllFour(t *testing.T) {
	home := t.TempDir()
	written, err := InstallSkills(home)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join(home, ".claude", "commands", "epic-pipeline.md"),
		filepath.Join(home, ".claude", "commands", "plan-epics.md"),
		// Codex skills live at ~/.codex/skills/<name>/SKILL.md. ~/.codex/prompts
		// is NOT read by codex-cli (verified on 0.144.3: the TUI rejects
		// /epic-pipeline outright), so a file there is never loaded.
		filepath.Join(home, ".codex", "skills", "epic-pipeline", "SKILL.md"),
		filepath.Join(home, ".codex", "skills", "plan-epics", "SKILL.md"),
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

// The codex runner files shipped to ~/.codex/prompts for months before we
// learned codex-cli never reads that directory. Epics only ran because the
// model went hunting and found the file itself. Leaving the old copy behind
// is worse than never having written it: it would freeze at its last content
// while the real skill keeps updating, and a model that goes hunting would
// still find the stale one.
func TestInstallSkillsRemovesStaleCodexPrompts(t *testing.T) {
	home := t.TempDir()
	stale := filepath.Join(home, ".codex", "prompts")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"epic-pipeline.md", "plan-epics.md"} {
		if err := os.WriteFile(filepath.Join(stale, n), []byte("stale"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	keep := filepath.Join(stale, "someone-elses-prompt.md")
	if err := os.WriteFile(keep, []byte("not ours"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallSkills(home); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"epic-pipeline.md", "plan-epics.md"} {
		if _, err := os.Stat(filepath.Join(stale, n)); !os.IsNotExist(err) {
			t.Fatalf("stale %s survived: %v", n, err)
		}
	}
	if got, err := os.ReadFile(keep); err != nil || string(got) != "not ours" {
		t.Fatalf("removed a prompt that was not ours: %v %q", err, got)
	}
}

// Codex reads ONLY name+description to decide whether a skill triggers. A
// codex runner file without them cannot be loaded as a skill no matter where
// it is installed — which is how both of these shipped with no frontmatter.
func TestCodexSkillsHaveRequiredFrontmatter(t *testing.T) {
	for _, in := range installs {
		if !strings.Contains(in.dstDir, ".codex") {
			continue
		}
		b, err := fsys.ReadFile(in.src)
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		if !strings.HasPrefix(s, "---\n") {
			t.Fatalf("%s: codex skill needs YAML frontmatter", in.src)
		}
		end := strings.Index(s[4:], "\n---\n")
		if end < 0 {
			t.Fatalf("%s: unterminated frontmatter", in.src)
		}
		fm := s[4 : 4+end]
		for _, key := range []string{"name:", "description:"} {
			if !strings.Contains(fm, key) {
				t.Fatalf("%s: frontmatter missing %q (codex requires it)", in.src, key)
			}
		}
	}
}

func TestInstallSkillsReplacesSymlinkDestination(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".claude", "commands")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(home, "victim.txt")
	if err := os.WriteFile(victim, []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dir, "epic-pipeline.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallSkills(home); err != nil {
		t.Fatal(err)
	}
	// The rename replaces the symlink itself; the write never follows it.
	fi, err := os.Lstat(filepath.Join(dir, "epic-pipeline.md"))
	if err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("destination must be a regular file, got mode %v err=%v", fi.Mode(), err)
	}
	if got, _ := os.ReadFile(victim); string(got) != "precious" {
		t.Fatalf("symlink target was clobbered: %q", got)
	}
}
