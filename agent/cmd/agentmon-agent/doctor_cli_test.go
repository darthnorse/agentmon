package main

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// doctorEnv fakes every doctor dependency: run succeeds for the listed command
// prefixes (git plumbing gets realistic outputs), look finds the listed
// binaries, home is a temp dir.
func doctorEnv(t *testing.T, bins []string, failPrefixes ...string) (cmdRunner, func(string) (string, error), func() (string, error), string) {
	t.Helper()
	wd := mustGetwd(t)
	run := func(_ string, name string, args ...string) (string, error) {
		full := name + " " + strings.Join(args, " ")
		for _, p := range failPrefixes {
			if strings.HasPrefix(full, p) {
				return "", errors.New("boom: " + full)
			}
		}
		if strings.HasPrefix(full, "git rev-parse") {
			return filepath.Join(wd, ".git") + "\n", nil
		}
		if strings.HasPrefix(full, "git config --get remote.origin.url") {
			return "git@github.com:o/r.git\n", nil
		}
		return "ok", nil
	}
	look := func(bin string) (string, error) {
		for _, b := range bins {
			if b == bin {
				return "/usr/bin/" + bin, nil
			}
		}
		return "", errors.New("not found")
	}
	h := t.TempDir()
	return run, look, func() (string, error) { return h, nil }, h
}

// seedSkills writes the files the doctor expects for the given providers.
func seedSkills(t *testing.T, home string, claude, codex bool) {
	t.Helper()
	if claude {
		p := filepath.Join(home, ".claude", "commands")
		_ = os.MkdirAll(p, 0o755)
		_ = os.WriteFile(filepath.Join(p, "epic-pipeline.md"), []byte("skill"), 0o644)
	}
	if codex {
		p := filepath.Join(home, ".codex", "prompts")
		_ = os.MkdirAll(p, 0o755)
		_ = os.WriteFile(filepath.Join(p, "epic-pipeline.md"), []byte("playbook"), 0o644)
		_ = os.WriteFile(filepath.Join(home, ".codex", "config.toml"),
			[]byte("[sandbox_workspace_write]\nwritable_roots = [\""+filepath.Join(mustGetwd(t), ".git")+"\"]\nnetwork_access = true\n"), 0o644)
	}
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return wd
}

func doctorReporterOK(t *testing.T) string {
	t.Helper()
	t.Setenv("TMUX_PANE", "%1")
	t.Setenv("TMUX", "/tmp/tmux-0/agentmon,1,0")
	cfgPath := reportTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("dry_run") != "1" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"session":"doctor"}`))
	})
	return cfgPath
}

func TestDoctorAllGreen(t *testing.T) {
	run, look, home, h := doctorEnv(t, []string{"claude"})
	seedSkills(t, h, true, false)
	cfgPath := doctorReporterOK(t)
	var out bytes.Buffer
	err := doctorRun([]string{"--config", cfgPath, "--repo", "o/r"}, &out, run, look, home)
	if err != nil {
		t.Fatalf("err=%v out:\n%s", err, out.String())
	}
	for _, want := range []string{"✓ gh auth", "✓ git fetch origin main", "✓ reporter dry-run", "✓ claude epic-pipeline skill", "– codex binary"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in:\n%s", want, out.String())
		}
	}
}

func TestDoctorFailsOnBrokenCheck(t *testing.T) {
	run, look, home, h := doctorEnv(t, []string{"claude"}, "git fetch")
	seedSkills(t, h, true, false)
	cfgPath := doctorReporterOK(t)
	var out bytes.Buffer
	err := doctorRun([]string{"--config", cfgPath, "--repo", "o/r"}, &out, run, look, home)
	if err == nil || !strings.Contains(out.String(), "✗ git fetch origin main") {
		t.Fatalf("err=%v out:\n%s", err, out.String())
	}
}

func TestDoctorNoProvidersFails(t *testing.T) {
	run, look, home, _ := doctorEnv(t, nil)
	cfgPath := doctorReporterOK(t)
	var out bytes.Buffer
	if err := doctorRun([]string{"--config", cfgPath, "--repo", "o/r"}, &out, run, look, home); err == nil {
		t.Fatal("no provider binaries must fail the doctor")
	}
}

func TestDoctorCodexConfigChecked(t *testing.T) {
	run, look, home, h := doctorEnv(t, []string{"codex"})
	seedSkills(t, h, false, true)
	// Break the config: network_access missing.
	_ = os.WriteFile(filepath.Join(h, ".codex", "config.toml"),
		[]byte("[sandbox_workspace_write]\nwritable_roots = []\n"), 0o644)
	cfgPath := doctorReporterOK(t)
	var out bytes.Buffer
	err := doctorRun([]string{"--config", cfgPath, "--repo", "o/r"}, &out, run, look, home)
	if err == nil || !strings.Contains(out.String(), "✗ codex sandbox config") {
		t.Fatalf("err=%v out:\n%s", err, out.String())
	}
}

// Installed-but-untrusted codex hooks hang every codex runner session at the
// interactive "Hooks need review" prompt (`-a never` answers tool approvals,
// not trust prompts). Codex offers no non-interactive trust path, so the best
// the doctor can do is catch the never-trusted state before a kickoff hits it.
func TestDoctorCodexHooksUntrustedFails(t *testing.T) {
	run, look, home, h := doctorEnv(t, []string{"codex"})
	seedSkills(t, h, false, true)
	_ = os.WriteFile(filepath.Join(h, ".codex", "hooks.json"), []byte(`{"hooks":{}}`), 0o644)
	cfgPath := doctorReporterOK(t)
	var out bytes.Buffer
	err := doctorRun([]string{"--config", cfgPath, "--repo", "o/r"}, &out, run, look, home)
	if err == nil || !strings.Contains(out.String(), "✗ codex hooks trust") {
		t.Fatalf("untrusted codex hooks must fail the doctor; err=%v out:\n%s", err, out.String())
	}
}

func TestDoctorCodexHooksTrustedPasses(t *testing.T) {
	// Both TOML spellings codex may emit: the header form and the
	// parent-table form. Substring matching only ever saw the first.
	for name, entry := range map[string]string{
		"header":       "\n[hooks.state.\"%s:session_start:0:0\"]\ntrusted_hash = \"sha256:x\"\n",
		"parent-table": "\n[hooks.state]\n\"%s:session_start:0:0\" = { trusted_hash = \"sha256:x\" }\n",
	} {
		t.Run(name, func(t *testing.T) {
			run, look, home, h := doctorEnv(t, []string{"codex"})
			seedSkills(t, h, false, true)
			hooksPath := filepath.Join(h, ".codex", "hooks.json")
			_ = os.WriteFile(hooksPath, []byte(`{"hooks":{}}`), 0o644)
			cfg, _ := os.ReadFile(filepath.Join(h, ".codex", "config.toml"))
			cfg = append(cfg, []byte(fmt.Sprintf(entry, hooksPath))...)
			_ = os.WriteFile(filepath.Join(h, ".codex", "config.toml"), cfg, 0o644)
			cfgPath := doctorReporterOK(t)
			var out bytes.Buffer
			err := doctorRun([]string{"--config", cfgPath, "--repo", "o/r"}, &out, run, look, home)
			if err != nil || !strings.Contains(out.String(), "✓ codex hooks trust") {
				t.Fatalf("trusted codex hooks must pass; err=%v out:\n%s", err, out.String())
			}
		})
	}
}

// A commented-out trust entry is NOT trust — the raw-substring approach
// false-passed this exact troubleshooting leftover.
func TestDoctorCodexHooksCommentedEntryStillUntrusted(t *testing.T) {
	run, look, home, h := doctorEnv(t, []string{"codex"})
	seedSkills(t, h, false, true)
	hooksPath := filepath.Join(h, ".codex", "hooks.json")
	_ = os.WriteFile(hooksPath, []byte(`{"hooks":{}}`), 0o644)
	cfg, _ := os.ReadFile(filepath.Join(h, ".codex", "config.toml"))
	cfg = append(cfg, []byte("\n# [hooks.state.\""+hooksPath+":session_start:0:0\"]\n# trusted_hash = \"sha256:x\"\n")...)
	_ = os.WriteFile(filepath.Join(h, ".codex", "config.toml"), cfg, 0o644)
	cfgPath := doctorReporterOK(t)
	var out bytes.Buffer
	err := doctorRun([]string{"--config", cfgPath, "--repo", "o/r"}, &out, run, look, home)
	if err == nil || !strings.Contains(out.String(), "✗ codex hooks trust") {
		t.Fatalf("commented-out trust entry must still fail; err=%v out:\n%s", err, out.String())
	}
}

// A stat error that is NOT "file absent" (here: ENOTDIR via a file where the
// .codex dir should be) must surface as a failed check, not silently skip it.
func TestDoctorCodexHooksStatErrorSurfaces(t *testing.T) {
	run, look, home, h := doctorEnv(t, []string{"codex"})
	_ = os.WriteFile(filepath.Join(h, ".codex"), []byte("not a dir"), 0o644)
	cfgPath := doctorReporterOK(t)
	var out bytes.Buffer
	err := doctorRun([]string{"--config", cfgPath, "--repo", "o/r"}, &out, run, look, home)
	if err == nil || !strings.Contains(out.String(), "✗ codex hooks trust") {
		t.Fatalf("hooks.json stat error must fail the trust check, not skip it; err=%v out:\n%s", err, out.String())
	}
}

func TestCheckCodexHooksTrustMissingConfigActionable(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "hooks.json"), []byte(`{"hooks":{}}`), 0o644)
	err := checkCodexHooksTrust(dir)
	if err == nil || !strings.Contains(err.Error(), "never trusted") {
		t.Fatalf("missing config.toml must yield the never-trusted guidance, got: %v", err)
	}
}

func TestDoctorDerivesRepoFromGit(t *testing.T) {
	run, look, home, h := doctorEnv(t, []string{"claude"})
	seedSkills(t, h, true, false)
	cfgPath := doctorReporterOK(t)
	var out bytes.Buffer
	err := doctorRun([]string{"--config", cfgPath}, &out, run, look, home)
	if err != nil {
		t.Fatalf("err=%v out:\n%s", err, out.String())
	}
	for _, want := range []string{"\u2713 repo derivation", "\u2713 gh repo access (o/r)"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in:\n%s", want, out.String())
		}
	}
}

func TestDoctorCodexDangerFullAccessPasses(t *testing.T) {
	run, look, home, h := doctorEnv(t, []string{"codex"})
	seedSkills(t, h, false, true)
	// No [sandbox_workspace_write] table at all: that mode has no sandbox, so
	// the workspace-write checks must not run.
	_ = os.WriteFile(filepath.Join(h, ".codex", "config.toml"),
		[]byte("sandbox_mode = \"danger-full-access\"\n"), 0o644)
	cfgPath := doctorReporterOK(t)
	var out bytes.Buffer
	if err := doctorRun([]string{"--config", cfgPath, "--repo", "o/r"}, &out, run, look, home); err != nil {
		t.Fatalf("danger-full-access must pass the sandbox check: %v\n%s", err, out.String())
	}
}

func TestDoctorCodexRepoRootWritableRootFails(t *testing.T) {
	run, look, home, h := doctorEnv(t, []string{"codex"})
	seedSkills(t, h, false, true)
	// The repo ROOT as a writable root is the fleet-validated false config:
	// codex keeps a writable root's top-level .git read-only, so this host
	// cannot commit. The doctor must fail it, not pass it.
	_ = os.WriteFile(filepath.Join(h, ".codex", "config.toml"),
		[]byte("[sandbox_workspace_write]\nwritable_roots = [\""+mustGetwd(t)+"\"]\nnetwork_access = true\n"), 0o644)
	cfgPath := doctorReporterOK(t)
	var out bytes.Buffer
	err := doctorRun([]string{"--config", cfgPath, "--repo", "o/r"}, &out, run, look, home)
	if err == nil || !strings.Contains(out.String(), "writable_roots must include") {
		t.Fatalf("repo-root-only writable_roots must fail: err=%v out:\n%s", err, out.String())
	}
}

func TestDoctorCodexUncleanWritableRootPasses(t *testing.T) {
	run, look, home, h := doctorEnv(t, []string{"codex"})
	seedSkills(t, h, false, true)
	// A trailing slash is still the same directory; exact string equality
	// used to false-fail this.
	_ = os.WriteFile(filepath.Join(h, ".codex", "config.toml"),
		[]byte("[sandbox_workspace_write]\nwritable_roots = [\""+filepath.Join(mustGetwd(t), ".git")+"/\"]\nnetwork_access = true\n"), 0o644)
	cfgPath := doctorReporterOK(t)
	var out bytes.Buffer
	if err := doctorRun([]string{"--config", cfgPath, "--repo", "o/r"}, &out, run, look, home); err != nil {
		t.Fatalf("trailing-slash writable root must pass: %v\n%s", err, out.String())
	}
}

func TestDoctorCodexReadOnlySandboxFails(t *testing.T) {
	run, look, home, h := doctorEnv(t, []string{"codex"})
	seedSkills(t, h, false, true)
	// writable_roots and network_access are valid — only the active sandbox
	// is explicitly read-only, which cannot commit or run test gates.
	_ = os.WriteFile(filepath.Join(h, ".codex", "config.toml"),
		[]byte("sandbox_mode = \"read-only\"\n[sandbox_workspace_write]\nwritable_roots = [\""+filepath.Join(mustGetwd(t), ".git")+"\"]\nnetwork_access = true\n"), 0o644)
	cfgPath := doctorReporterOK(t)
	var out bytes.Buffer
	err := doctorRun([]string{"--config", cfgPath, "--repo", "o/r"}, &out, run, look, home)
	if err == nil || !strings.Contains(out.String(), "✗ codex sandbox config") {
		t.Fatalf("err=%v out:\n%s", err, out.String())
	}
}
