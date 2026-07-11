// Package runnerfiles embeds the runner skills (authored in this repo,
// reviewed like code — design doc D4/D5) and installs them into a user's
// provider directories. Distribution rides the agent binary: the fleet update
// loop re-runs install.sh, which re-runs `agentmon-agent install-skills`, so a
// skill edit reaches every host with the next agent update — the workflow
// lives in versioned markdown, never in protocol.
package runnerfiles

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed files/claude/epic-pipeline.md files/claude/plan-epics.md files/codex/epic-pipeline.md
var fsys embed.FS

// installs maps each embedded skill to its destination under $HOME; the file
// keeps its basename. Order is part of the contract (tests + install output).
var installs = []struct{ src, dstDir string }{
	{"files/claude/epic-pipeline.md", filepath.Join(".claude", "commands")},
	{"files/claude/plan-epics.md", filepath.Join(".claude", "commands")},
	{"files/codex/epic-pipeline.md", filepath.Join(".codex", "prompts")},
}

// InstallSkills writes every embedded skill under home (0755 dirs, 0644 files
// — they are prompts, not secrets) and returns the written paths.
// Unconditional for both providers: a file for an absent provider is harmless
// and becomes live the moment that provider is installed.
func InstallSkills(home string) ([]string, error) {
	if home == "" {
		return nil, fmt.Errorf("home directory is required")
	}
	var written []string
	for _, in := range installs {
		b, err := fsys.ReadFile(in.src)
		if err != nil {
			return written, fmt.Errorf("embedded %s: %w", in.src, err)
		}
		dir := filepath.Join(home, in.dstDir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return written, err
		}
		dst := filepath.Join(dir, filepath.Base(in.src))
		if err := writeAtomic(dst, b); err != nil {
			return written, err
		}
		written = append(written, dst)
	}
	return written, nil
}

// writeAtomic replaces dst via a same-directory temp file + rename: live
// runner sessions read these files, and an in-place truncate-write would
// expose empty/partial content mid-update. Rename also replaces (never
// follows) a symlink squatting at dst.
func writeAtomic(dst string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}
