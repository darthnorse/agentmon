package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// doctorMain runs `agentmon doctor [--base main] [--repo o/r] [--config p]` in
// a project workdir: validates the spec-§12 host prerequisites (gh auth, repo
// access, base-branch fetch, reporter connectivity, provider binaries, skills,
// Codex sandbox config). Run it inside a monitored tmux session — the reporter
// probe needs a resolvable pane. Hub-dispatched doctors + board display are
// sub-project 3; this is the tool they will call.
func doctorMain(args []string, stdout io.Writer) error {
	return doctorRun(args, stdout, execRunner, exec.LookPath, os.UserHomeDir)
}

type doctorCheck struct {
	name string
	err  error
	skip string
}

func doctorRun(args []string, stdout io.Writer, run cmdRunner, look func(string) (string, error), home func() (string, error)) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stdout)
	cfgPath := fs.String("config", "/etc/agentmon/agent.toml", "path to agent.toml")
	base := fs.String("base", "main", "base branch to verify fetching")
	repo := fs.String("repo", "", "owner/name (default: derived from the cwd's git remote origin)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var checks []doctorCheck
	add := func(name string, err error) { checks = append(checks, doctorCheck{name: name, err: err}) }
	skip := func(name, why string) { checks = append(checks, doctorCheck{name: name, skip: why}) }

	r := *repo
	if r == "" {
		var err error
		r, err = repoFromGit(".", run)
		add("repo derivation (cwd is a clone)", err)
	}
	_, err := run(".", "gh", "auth", "status")
	add("gh auth", err)
	if r != "" {
		_, err = run(".", "gh", "repo", "view", r, "--json", "viewerPermission")
		add("gh repo access ("+r+")", err)
	}
	_, err = run(".", "git", "fetch", "origin", *base)
	add("git fetch origin "+*base, err)

	_, err = postReport(*cfgPath, map[string]any{
		"repo": "doctor/doctor", "epic": 1, "stage": "planning", "note": "doctor dry-run",
	}, true)
	add("reporter dry-run", err)

	claudeBin, codexBin := false, false
	if _, err := look("claude"); err == nil {
		claudeBin = true
		add("claude binary", nil)
	} else {
		skip("claude binary", "not detected")
	}
	if _, err := look("codex"); err == nil {
		codexBin = true
		add("codex binary", nil)
	} else {
		skip("codex binary", "not detected")
	}
	if !claudeBin && !codexBin {
		add("provider binaries", fmt.Errorf("neither claude nor codex on PATH"))
	}
	if h, herr := home(); herr != nil {
		add("home dir", herr)
	} else {
		if claudeBin {
			add("claude epic-pipeline skill", statFile(filepath.Join(h, ".claude", "commands", "epic-pipeline.md")))
		}
		if codexBin {
			add("codex epic-pipeline prompt", statFile(filepath.Join(h, ".codex", "prompts", "epic-pipeline.md")))
			add("codex sandbox config", checkCodexConfig(filepath.Join(h, ".codex", "config.toml"), run))
			if _, herr := os.Stat(filepath.Join(h, ".codex", "hooks.json")); herr == nil {
				add("codex hooks trust", checkCodexHooksTrust(filepath.Join(h, ".codex")))
			} else if !errors.Is(herr, os.ErrNotExist) {
				// EACCES/ENOTDIR ≠ "no hooks installed": surface it rather
				// than silently skipping a check that guards against hangs.
				add("codex hooks trust", herr)
			}
		}
	}

	failed := 0
	for _, c := range checks {
		switch {
		case c.skip != "":
			fmt.Fprintf(stdout, "– %s: %s\n", c.name, c.skip)
		case c.err != nil:
			failed++
			fmt.Fprintf(stdout, "✗ %s: %v\n", c.name, c.err)
		default:
			fmt.Fprintf(stdout, "✓ %s\n", c.name)
		}
	}
	if failed > 0 {
		return fmt.Errorf("doctor: %d check(s) failed", failed)
	}
	fmt.Fprintln(stdout, "doctor: all checks passed")
	return nil
}

func statFile(p string) error {
	if _, err := os.Stat(p); err != nil {
		return fmt.Errorf("missing %s (run: agentmon install-skills)", p)
	}
	return nil
}

// codexConfig is the subset of ~/.codex/config.toml the doctor validates
// (spec §12: without these, runner sessions cannot commit or pass test gates).
type codexConfig struct {
	SandboxMode           string `toml:"sandbox_mode"`
	SandboxWorkspaceWrite struct {
		WritableRoots []string `toml:"writable_roots"`
		NetworkAccess bool     `toml:"network_access"`
	} `toml:"sandbox_workspace_write"`
}

// checkCodexHooksTrust catches the never-trusted state: codex hooks are
// installed but ~/.codex/config.toml records no [hooks.state."<hooks.json>…"]
// trust entry for them. An untrusted hooks.json hangs EVERY codex runner
// session at codex's interactive "Hooks need review" prompt (`-a never`
// answers tool approvals, not trust prompts) until the stage timeout. Codex
// has no non-interactive trust command and the hash format is undocumented,
// so the fix is a one-time interactive trust per run user. A trust entry
// whose hash went STALE (hooks.json edited after trusting) re-prompts the
// same way but is indistinguishable from a valid entry here — this check
// catches the common fresh-host case, not that one.
func checkCodexHooksTrust(codexDir string) error {
	hooksPath := filepath.Join(codexDir, "hooks.json")
	notTrusted := fmt.Errorf("%s is installed but never trusted — run codex once interactively and trust the hooks, or runner sessions hang at its trust prompt", hooksPath)
	// Parse, don't substring-match: codex may emit the header form
	// ([hooks.state."<path>:…"]) or the parent-table form, and a
	// commented-out leftover entry must not read as trust.
	var c struct {
		Hooks struct {
			State map[string]toml.Primitive `toml:"state"`
		} `toml:"hooks"`
	}
	cfgPath := filepath.Join(codexDir, "config.toml")
	if _, err := toml.DecodeFile(cfgPath, &c); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return notTrusted // hooks installed, no config at all: never trusted
		}
		return fmt.Errorf("read %s: %w", cfgPath, err)
	}
	for key := range c.Hooks.State {
		if strings.HasPrefix(key, hooksPath+":") {
			return nil
		}
	}
	return notTrusted
}

func checkCodexConfig(path string, run cmdRunner) error {
	var c codexConfig
	if _, err := toml.DecodeFile(path, &c); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	// Unset defaults to workspace-write for interactive sessions (the kickoff
	// path). An explicit read-only sandbox passes the checks below yet cannot
	// commit or run test gates — the exact misconfig the doctor exists to catch.
	if c.SandboxMode != "" && c.SandboxMode != "workspace-write" && c.SandboxMode != "danger-full-access" {
		return fmt.Errorf("%s: sandbox_mode %q cannot write the workspace (runner sessions must commit)", path, c.SandboxMode)
	}
	// No sandbox at all: writes and network are unrestricted, so the
	// workspace-write table (often absent in this mode) must not be checked.
	if c.SandboxMode == "danger-full-access" {
		return nil
	}
	if !c.SandboxWorkspaceWrite.NetworkAccess {
		return fmt.Errorf("%s: [sandbox_workspace_write] network_access must be true (httptest loopback binds)", path)
	}
	// The real git dir: worktree-safe (--git-common-dir) and independent of a
	// subdirectory cwd. Codex keeps every writable root's TOP-LEVEL .git
	// read-only (verified live against the sandbox), so a writable repo root
	// is NOT sufficient — only an explicit .git entry lets runners commit.
	out, err := run(".", "git", "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return fmt.Errorf("%s: cannot resolve the clone's git dir (run the doctor from the project workdir): %w", path, err)
	}
	gitDir := filepath.Clean(strings.TrimSpace(out))
	for _, root := range c.SandboxWorkspaceWrite.WritableRoots {
		if filepath.Clean(root) == gitDir {
			return nil
		}
	}
	return fmt.Errorf("%s: writable_roots must include %s (else no branches/commits)", path, gitDir)
}
