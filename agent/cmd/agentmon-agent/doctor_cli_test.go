package main

import (
	"bytes"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// doctorEnv fakes every doctor dependency: run succeeds for the listed command
// prefixes, look finds the listed binaries, home is a temp dir.
func doctorEnv(t *testing.T, bins []string, failPrefixes ...string) (cmdRunner, func(string) (string, error), func() (string, error), string) {
	t.Helper()
	run := func(_ string, name string, args ...string) (string, error) {
		full := name + " " + strings.Join(args, " ")
		for _, p := range failPrefixes {
			if strings.HasPrefix(full, p) {
				return "", errors.New("boom: " + full)
			}
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
	_, cfgPath := reportTestServer(t, func(w http.ResponseWriter, r *http.Request) {
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
