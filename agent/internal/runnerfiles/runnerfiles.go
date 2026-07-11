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

// installs maps each embedded skill to its destination under $HOME. Order is
// part of the contract (tests + install output).
var installs = []struct{ src, dstDir, dstName string }{
	{"files/claude/epic-pipeline.md", filepath.Join(".claude", "commands"), "epic-pipeline.md"},
	{"files/claude/plan-epics.md", filepath.Join(".claude", "commands"), "plan-epics.md"},
	{"files/codex/epic-pipeline.md", filepath.Join(".codex", "prompts"), "epic-pipeline.md"},
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
		dst := filepath.Join(dir, in.dstName)
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			return written, err
		}
		written = append(written, dst)
	}
	return written, nil
}
