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

	"agentmon/agent/internal/fsatomic"
)

//go:embed files/claude/epic-pipeline.md files/claude/plan-epics.md files/codex/epic-pipeline.md files/codex/plan-epics.md
var fsys embed.FS

// installs maps each embedded skill to its destination under $HOME. dstName
// overrides the basename where the host demands a fixed filename. Order is
// part of the contract (tests + install output).
//
// The two hosts use different mechanisms, and they are not interchangeable:
//   - Claude Code reads slash commands from ~/.claude/commands/<name>.md.
//   - Codex reads SKILLS from ~/.codex/skills/<name>/SKILL.md. It does NOT read
//     ~/.codex/prompts — verified on codex-cli 0.144.3, where the TUI rejects
//     /epic-pipeline outright ("Unrecognized command"). These files shipped to
//     that directory for months; Codex epics ran anyway ONLY because the model
//     went hunting through the filesystem, found the file, and chose to follow
//     it. That is not a mechanism. A model that guessed instead would run an
//     epic having never read the plan-gate, the checkpoint reviews, or the
//     escalation protocol — silently, with no human watching.
var installs = []struct{ src, dstDir, dstName string }{
	{"files/claude/epic-pipeline.md", filepath.Join(".claude", "commands"), ""},
	{"files/claude/plan-epics.md", filepath.Join(".claude", "commands"), ""},
	{"files/codex/epic-pipeline.md", filepath.Join(".codex", "skills", "epic-pipeline"), "SKILL.md"},
	{"files/codex/plan-epics.md", filepath.Join(".codex", "skills", "plan-epics"), "SKILL.md"},
}

// staleCodexPrompts are the dead pre-skills destinations. Removing them is not
// tidiness: left behind they would freeze at their last-installed content while
// the real skill keeps updating, and the model that has been finding them would
// keep finding the stale copy. Two copies, and the wrong one wins.
var staleCodexPrompts = []string{
	filepath.Join(".codex", "prompts", "epic-pipeline.md"),
	filepath.Join(".codex", "prompts", "plan-epics.md"),
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
		name := in.dstName
		if name == "" {
			name = filepath.Base(in.src)
		}
		dst := filepath.Join(dir, name)
		// Atomic replace: live runner sessions read these files, and an
		// in-place truncate-write would expose empty/partial content mid-update.
		if err := fsatomic.WriteFile(dst, b, 0o644); err != nil {
			return written, err
		}
		written = append(written, dst)
	}
	// Remove the dead pre-skills copies. Only our two basenames, never the
	// directory or anything else in it — a user's own prompts may live there.
	// A removal failure is not fatal: the skills above are already installed
	// and correct, and refusing to install over an unremovable leftover would
	// be worse than the leftover.
	for _, rel := range staleCodexPrompts {
		if err := os.Remove(filepath.Join(home, rel)); err != nil && !os.IsNotExist(err) {
			continue
		}
	}
	return written, nil
}
