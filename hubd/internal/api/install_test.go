package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"agentmon/hubd/internal/agentbin"
)

// hookFunctionBlock returns the delimited hook-setup function block from a rendered
// install script, for harness tests that source those functions with stubs.
func hookFunctionBlock(t *testing.T, body string) string {
	t.Helper()
	const startMarker, endMarker = "# BEGIN hook setup functions", "# END hook setup functions."
	start := strings.Index(body, startMarker)
	end := strings.Index(body, endMarker)
	if start < 0 || end < start {
		t.Fatalf("could not locate hook function block (start=%d end=%d)", start, end)
	}
	return body[start : end+len(endMarker)]
}

func TestInstallScriptIsTemplated(t *testing.T) {
	d := InstallDeps{HubURL: "https://hub.example.lan"}
	r := httptest.NewRequest("GET", "/install.sh", nil)
	w := httptest.NewRecorder()
	d.ScriptHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("code %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "shellscript") {
		t.Fatalf("content-type: %s", ct)
	}
	body := w.Body.String()
	amd, _ := agentbin.SHA256Hex("amd64")
	arm, _ := agentbin.SHA256Hex("arm64")
	for _, want := range []string{"https://hub.example.lan", amd, arm, "/api/v1/enroll", "agent-linux-"} {
		if !strings.Contains(body, want) {
			t.Fatalf("install.sh missing %q", want)
		}
	}
	if strings.Contains(body, "{{") {
		t.Fatal("install.sh still contains an unrendered template directive")
	}
}

func TestInstallScriptChownsAgentTomlToRunUser(t *testing.T) {
	d := InstallDeps{HubURL: "https://hub.example.lan"}
	r := httptest.NewRequest("GET", "/install.sh", nil)
	w := httptest.NewRecorder()
	d.ScriptHandler()(w, r)
	body := w.Body.String()
	// agent.toml must be chowned to the service user, else the agent (User=RUN_USER) can't read its config.
	if !strings.Contains(body, `chown "$RUN_USER" /etc/agentmon/agent.toml`) {
		t.Fatal("install.sh must chown agent.toml to the run user (agent runs as that user and reads the config)")
	}
}

func TestInstallScriptDefaultsToDedicatedSocket(t *testing.T) {
	d := InstallDeps{HubURL: "https://hub.example.lan"}
	r := httptest.NewRequest("GET", "/install.sh", nil)
	w := httptest.NewRecorder()
	d.ScriptHandler()(w, r)
	// The agent must default to a dedicated 'agentmon' socket, never the run-user's
	// default socket (where unrelated/sensitive sessions live), unless --socket overrides.
	if !strings.Contains(w.Body.String(), `SOCKET="${SOCKET_OVERRIDE:-agentmon}"`) {
		t.Fatal("install.sh must default the agent socket to the dedicated 'agentmon' socket")
	}
}

func TestInstallScriptInstallsRunnerFiles(t *testing.T) {
	d := InstallDeps{HubURL: "https://hub.example.lan"}
	r := httptest.NewRequest("GET", "/install.sh", nil)
	w := httptest.NewRecorder()
	d.ScriptHandler()(w, r)
	body := w.Body.String()
	for _, want := range []string{
		`ln -sfnT /usr/local/bin/agentmon-agent /usr/local/bin/agentmon`,
		`install-skills --home`,
		// getent failure must not abort the installer under set -euo pipefail.
		`cut -d: -f6 || true`,
		// The update path must serve the ENROLLED user (config os_user), not
		// the invoking user — a root-shell fleet update would otherwise write
		// the skills to /root and skip the monitored user.
		`RUN_USER="$cfg_user"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("install.sh missing %q", want)
		}
	}
	// Both the update path and the fresh path must install runner files, so a
	// fleet UPDATE delivers new skills too (the whole point of binary-embedded
	// distribution). Two call sites of the same function.
	if strings.Count(body, "install_runner_files\n") < 2 {
		t.Fatal("install_runner_files must run on both the update and fresh paths")
	}
}

func TestInstallScriptUnitUsesKillModeProcess(t *testing.T) {
	d := InstallDeps{HubURL: "https://hub.example.lan"}
	r := httptest.NewRequest("GET", "/install.sh", nil)
	w := httptest.NewRecorder()
	d.ScriptHandler()(w, r)
	body := w.Body.String()
	// The agentmon tmux server runs inside the agent service's cgroup, so the installer
	// MUST set KillMode=process — otherwise the default control-group KillMode makes a
	// `systemctl stop`/`restart` (e.g. this installer's binary swap) kill the tmux server
	// and every monitored session. Match the DIRECTIVE on its own line (leading+trailing
	// newline) so a comment containing the substring can't make this pass vacuously.
	if !strings.Contains(body, "\nKillMode=process\n") {
		t.Fatal("install.sh must set KillMode=process so restarts/updates don't kill the tmux server + sessions")
	}
	// The fix must be CALLED, not just defined — assert the bare call on its own line
	// (`\nensure_killmode\n`), distinct from the `ensure_killmode() {` definition, so
	// the KillMode=process text sitting inside the function body can't pass vacuously.
	kmCall := strings.Index(body, "\nensure_killmode\n")
	if kmCall < 0 {
		t.Fatal("install.sh must CALL ensure_killmode (apply the KillMode drop-in), not just define it")
	}
	// ...and it must run BEFORE the update path's in-place binary swap + restart, so an
	// existing control-group host is protected on the very run that swaps the binary.
	// "Updating the agent binary in place" is unique to that real update path.
	swap := strings.Index(body, "Updating the agent binary in place")
	if swap < 0 || kmCall > swap {
		t.Fatalf("ensure_killmode must run before the in-place binary swap/restart (call=%d swap=%d)", kmCall, swap)
	}
}

func TestInstallScriptPathHarvestCannotHangTheInstall(t *testing.T) {
	d := InstallDeps{HubURL: "https://hub.example.lan"}
	r := httptest.NewRequest("GET", "/install.sh", nil)
	w := httptest.NewRecorder()
	d.ScriptHandler()(w, r)
	body := w.Body.String()
	// Harvesting the run-user's PATH must NOT use an interactive shell (`bash -i`).
	// Inside the installer's pipeline + command-substitution an interactive bash is
	// not the foreground process group; reaching for the terminal earns it SIGTTOU
	// and it STOPS — ignoring SIGTERM (so `timeout` can't reap it) and SIGINT (so
	// Ctrl-C can't), wedging the whole install until an out-of-band SIGKILL.
	if strings.Contains(body, "bash -ilc") || strings.Contains(body, "bash -i ") {
		t.Fatal("PATH harvest must not use an interactive shell (bash -i) — it can hang the installer under SIGTTOU")
	}
	// The timeout guarding the harvest must escalate to SIGKILL (-k): a pathological
	// login shell can ignore the default SIGTERM, defeating a bare `timeout`.
	if !strings.Contains(body, "timeout -k") {
		t.Fatal("PATH-harvest timeout must use -k (SIGKILL escalation) so a stuck shell can't wedge the install")
	}
}

func TestInstallScriptInstallsHooksWithoutAnInteractivePrompt(t *testing.T) {
	d := InstallDeps{HubURL: "https://hub.example.lan"}
	r := httptest.NewRequest("GET", "/install.sh", nil)
	w := httptest.NewRecorder()
	d.ScriptHandler()(w, r)
	body := w.Body.String()
	// Hooks install by default (auto), so there is no [y/N] prompt to mishandle
	// under `curl | sudo bash` (stdin is the pipe). Assert the interactive prompt and
	// its /dev/tty read are gone so they can't regress into a hang or a silent skip.
	if strings.Contains(body, "Install AgentMon hooks for live agent state?") {
		t.Fatal("hooks must install by default (auto), not via an interactive prompt")
	}
	if strings.Contains(body, "read -r -t 30 ans") {
		t.Fatal("hook install must not read an interactive answer from the terminal")
	}
}

func TestInstallScriptSupportsClaudeAndCodexHooks(t *testing.T) {
	d := InstallDeps{HubURL: "https://hub.example.lan"}
	r := httptest.NewRequest("GET", "/install.sh", nil)
	w := httptest.NewRecorder()
	d.ScriptHandler()(w, r)
	body := w.Body.String()
	for _, want := range []string{
		`echo "$home_dir/.claude/settings.json"`,
		`echo "$home_dir/.codex/hooks.json"`,
		`--provider "$provider"`,
		`provider_detected "$home_dir" claude`,
		`provider_detected "$home_dir" codex`,
		`sh -lc "command -v $provider" </dev/null >/dev/null 2>&1`,
		`[ "$token_ready" != "1" ]`,
		`--hooks=codex`,
		`--hooks=all`,
		`Claude Code and Codex`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("install.sh missing Codex/Claude hook behavior %q", want)
		}
	}
	// Both-provider installation must converge on one provisioning call before
	// the two conditional merges, so both clients use the same hook token.
	if got := strings.Count(body, "\n  provision_hook_token\n"); got != 1 {
		t.Fatalf("hook token provisioning calls = %d, want 1", got)
	}
	provision := strings.Index(body, "\n  provision_hook_token\n")
	claudeInstall := strings.Index(body, `install_hooks_into "$home_dir" claude`)
	codexInstall := strings.Index(body, `install_hooks_into "$home_dir" codex`)
	if provision < 0 || provision > claudeInstall || provision > codexInstall {
		t.Fatalf("one token must be provisioned before both installs (provision=%d claude=%d codex=%d)", provision, claudeInstall, codexInstall)
	}
}

func TestMaybeInstallHooksReturnsSuccessForEverySelection(t *testing.T) {
	d := InstallDeps{HubURL: "https://hub.example.lan"}
	r := httptest.NewRequest("GET", "/install.sh", nil)
	w := httptest.NewRecorder()
	d.ScriptHandler()(w, r)
	body := w.Body.String()
	functions := hookFunctionBlock(t, body)

	tests := []struct {
		mode    string
		want    []string
		notWant string
	}{
		{mode: "claude", want: []string{"token", "install:claude", "complete"}, notWant: "install:codex"},
		{mode: "codex", want: []string{"token", "install:codex", "complete"}, notWant: "install:claude"},
		{mode: "all", want: []string{"token", "install:claude", "install:codex", "complete"}},
	}
	for _, tc := range tests {
		t.Run(tc.mode, func(t *testing.T) {
			harness := "set -euo pipefail\n" + functions + "\n" +
				"HOOKS_MODE=" + tc.mode + "\nRUN_USER=root\n" +
				"provision_hook_token() { echo token; }\n" +
				"install_hooks_into() { echo install:\"$2\"; }\n" +
				"maybe_install_hooks\necho complete\n"
			cmd := exec.Command("bash")
			cmd.Stdin = strings.NewReader(harness)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("maybe_install_hooks exited nonzero: %v\n%s", err, out)
			}
			for _, want := range tc.want {
				if !strings.Contains(string(out), want) {
					t.Fatalf("output missing %q:\n%s", want, out)
				}
			}
			if tc.notWant != "" && strings.Contains(string(out), tc.notWant) {
				t.Fatalf("output unexpectedly contains %q:\n%s", tc.notWant, out)
			}
		})
	}
}

func TestMaybeInstallHooksAutoInstallsDetectedProvidersByDefault(t *testing.T) {
	d := InstallDeps{HubURL: "https://hub.example.lan"}
	r := httptest.NewRequest("GET", "/install.sh", nil)
	w := httptest.NewRecorder()
	d.ScriptHandler()(w, r)
	body := w.Body.String()
	functions := hookFunctionBlock(t, body)

	// Hooks are what make AgentMon work (live state + the orchestrator reporter), so
	// default (auto) mode must install them for every DETECTED provider — no prompt,
	// and no skip on a piped/non-interactive install (the fleet loop is piped). Stdin
	// here is a pipe, exactly the case that used to bail with a "re-run with --hooks"
	// hint instead of installing.
	harness := "set -euo pipefail\n" + functions + "\n" +
		"HOOKS_MODE=auto\nRUN_USER=root\n" +
		"run_user_home() { echo /home/test; }\n" +
		"hook_token_ready() { return 1; }\n" +
		"provider_detected() { return 0; }\n" + // both claude and codex present
		"provider_has_hooks() { return 1; }\n" + // not yet wired
		"provision_hook_token() { echo token; }\n" +
		"install_hooks_into() { echo install:\"$2\"; }\n" +
		"maybe_install_hooks\necho complete\n"
	cmd := exec.Command("bash")
	cmd.Stdin = strings.NewReader(harness)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("maybe_install_hooks exited nonzero: %v\n%s", err, out)
	}
	for _, want := range []string{"token", "install:claude", "install:codex", "complete"} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("auto mode must install detected providers by default; output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(string(out), "re-run with") {
		t.Fatalf("auto mode must not skip and point at a flag anymore — it installs by default:\n%s", out)
	}
}

func TestMaybeInstallHooksAutoInstallsOnlyDetectedProvider(t *testing.T) {
	d := InstallDeps{HubURL: "https://hub.example.lan"}
	r := httptest.NewRequest("GET", "/install.sh", nil)
	w := httptest.NewRecorder()
	d.ScriptHandler()(w, r)
	body := w.Body.String()
	functions := hookFunctionBlock(t, body)

	// Only-Claude-present: install claude, never codex. Guards against a blanket
	// "install everything" that would wire hooks for a client the host doesn't have.
	harness := "set -euo pipefail\n" + functions + "\n" +
		"HOOKS_MODE=auto\nRUN_USER=root\n" +
		"run_user_home() { echo /home/test; }\n" +
		"hook_token_ready() { return 1; }\n" +
		"provider_detected() { [ \"$2\" = claude ]; }\n" + // only claude present
		"provider_has_hooks() { return 1; }\n" +
		"provision_hook_token() { echo token; }\n" +
		"install_hooks_into() { echo install:\"$2\"; }\n" +
		"maybe_install_hooks\necho complete\n"
	cmd := exec.Command("bash")
	cmd.Stdin = strings.NewReader(harness)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("maybe_install_hooks exited nonzero: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "install:claude") {
		t.Fatalf("must install the detected provider (claude):\n%s", out)
	}
	if strings.Contains(string(out), "install:codex") {
		t.Fatalf("must NOT install an undetected provider (codex):\n%s", out)
	}
}

func TestInstallScriptHookModesDryRun(t *testing.T) {
	d := InstallDeps{HubURL: "https://hub.example.lan"}
	r := httptest.NewRequest("GET", "/install.sh", nil)
	w := httptest.NewRecorder()
	d.ScriptHandler()(w, r)
	path := filepath.Join(t.TempDir(), "install.sh")
	if err := os.WriteFile(path, w.Body.Bytes(), 0o700); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		arg  string
		want string
	}{
		{"legacy bare hooks", "--hooks", "install Claude Code hooks"},
		{"explicit claude", "--hooks=claude", "install Claude Code hooks"},
		{"codex", "--hooks=codex", "install Codex hooks"},
		{"all", "--hooks=all", "install Claude Code + Codex hooks"},
		{"none", "--no-hooks", "skip (--no-hooks)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command("bash", path, tc.arg, "--dry-run")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("dry-run failed: %v\n%s", err, out)
			}
			if !strings.Contains(string(out), "hooks:    "+tc.want) {
				t.Fatalf("dry-run output missing %q:\n%s", tc.want, out)
			}
		})
	}
}

func TestInstallScriptRejectsUnknownHookMode(t *testing.T) {
	d := InstallDeps{HubURL: "https://hub.example.lan"}
	r := httptest.NewRequest("GET", "/install.sh", nil)
	w := httptest.NewRecorder()
	d.ScriptHandler()(w, r)
	path := filepath.Join(t.TempDir(), "install.sh")
	if err := os.WriteFile(path, w.Body.Bytes(), 0o700); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("bash", path, "--hooks=other", "--dry-run").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "want claude, codex, or all") {
		t.Fatalf("unknown mode result err=%v output=%s", err, out)
	}
}

func TestInstallScriptRequiresTmux(t *testing.T) {
	d := InstallDeps{HubURL: "https://hub.example.lan"}
	r := httptest.NewRequest("GET", "/install.sh", nil)
	w := httptest.NewRecorder()
	d.ScriptHandler()(w, r)
	path := filepath.Join(t.TempDir(), "install.sh")
	if err := os.WriteFile(path, w.Body.Bytes(), 0o700); err != nil {
		t.Fatal(err)
	}
	// A host without tmux is the prism failure: the agent enrolls but every tmux
	// operation 500s and the host looks installed-but-broken. tmux must be a hard
	// prerequisite, refused up front like curl/systemd. Run with a PATH holding the
	// earlier preflight tools (systemctl, curl) but NO tmux, so the installer must
	// die at the tmux check before it touches anything.
	stub := t.TempDir()
	for _, name := range []string{"systemctl", "curl"} {
		if err := os.WriteFile(filepath.Join(stub, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var env []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "PATH=") {
			env = append(env, e)
		}
	}
	env = append(env, "PATH="+stub)
	cmd := exec.Command("bash", path, "--dry-run")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("install on a host without tmux must fail; output:\n%s", out)
	}
	if !strings.Contains(string(out), "tmux is required") {
		t.Fatalf("installer must die with a clear tmux-required message; got:\n%s", out)
	}
}

func TestGraftProviderDirsGraftsEachMissedProviderDir(t *testing.T) {
	d := InstallDeps{HubURL: "https://hub.example.lan"}
	r := httptest.NewRequest("GET", "/install.sh", nil)
	w := httptest.NewRecorder()
	d.ScriptHandler()(w, r)
	body := w.Body.String()
	sig := "graft_provider_dirs() {"
	start := strings.Index(body, sig)
	if start < 0 {
		t.Fatal("graft_provider_dirs helper not found in rendered script")
	}
	rel := strings.Index(body[start:], "\n}\n")
	if rel < 0 {
		t.Fatal("graft_provider_dirs end not found")
	}
	fn := body[start : start+rel+len("\n}\n")]

	home := t.TempDir()
	localBin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		t.Fatal(err)
	}
	// codex lives ONLY in ~/.local/bin; the input PATH already has other dirs (claude
	// resolvable elsewhere) but is missing ~/.local/bin. The graft must still append
	// it — gating on "a provider already resolves" would strand codex.
	if err := os.WriteFile(filepath.Join(localBin, "codex"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	runGraft := func(inPath string) string {
		harness := "set -euo pipefail\n" + fn + "\ngraft_provider_dirs '" + inPath + "' '" + home + "'\n"
		cmd := exec.Command("bash")
		cmd.Stdin = strings.NewReader(harness)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("graft_provider_dirs exited nonzero: %v\n%s", err, out)
		}
		return string(out)
	}
	if got := runGraft("/usr/local/bin:/usr/bin:/bin"); !strings.Contains(got, localBin) {
		t.Fatalf("graft must append %s (holds codex) even when the input PATH already has other dirs; got: %q", localBin, got)
	}
	// A dir already on the input PATH must not be duplicated.
	if got := runGraft(localBin + ":/usr/bin"); strings.Count(got, localBin) != 1 {
		t.Fatalf("graft must not duplicate an already-present dir; got: %q", got)
	}
}

func TestInstallScriptBashSyntax(t *testing.T) {
	d := InstallDeps{HubURL: "https://hub.example.lan"}
	r := httptest.NewRequest("GET", "/install.sh", nil)
	w := httptest.NewRecorder()
	d.ScriptHandler()(w, r)
	cmd := exec.Command("bash", "-n")
	cmd.Stdin = strings.NewReader(w.Body.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("rendered install.sh has invalid syntax: %v\n%s", err, out)
	}
}

func TestBinaryHandlerServesBytesAndChecksum(t *testing.T) {
	d := InstallDeps{HubURL: "https://hub.example.lan"}
	r := httptest.NewRequest("GET", "/dl/agent-linux-amd64", nil)
	r.SetPathValue("file", "agent-linux-amd64")
	w := httptest.NewRecorder()
	d.BinaryHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("code %d", w.Code)
	}
	want, _ := agentbin.Binary("amd64")
	if w.Body.Len() != len(want) {
		t.Fatalf("served %d bytes, want %d", w.Body.Len(), len(want))
	}
}

func TestBinaryHandlerRejectsUnknownFile(t *testing.T) {
	d := InstallDeps{HubURL: "x"}
	for _, f := range []string{"agent-linux-sparc", "../../etc/passwd", "install.sh"} {
		r := httptest.NewRequest("GET", "/dl/"+f, nil)
		r.SetPathValue("file", f)
		w := httptest.NewRecorder()
		d.BinaryHandler()(w, r)
		if w.Code != http.StatusNotFound {
			t.Fatalf("file %q: want 404, got %d", f, w.Code)
		}
	}
}
