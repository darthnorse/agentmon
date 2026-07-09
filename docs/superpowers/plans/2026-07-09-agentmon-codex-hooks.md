# AgentMon Codex Lifecycle Hooks Implementation Plan

**Goal:** Add Codex as a first-class source of AgentMon live session state while preserving the existing Claude Code integration and all current installer behavior.

**Approach:** Reuse AgentMon's authenticated loopback `POST /hook` intake and state machine. Add a small provider abstraction around hook configuration generation, write Codex hooks to `~/.codex/hooks.json`, and extend onboarding to detect and configure Claude Code, Codex, or both. No hub or web changes are required.

**Current compatibility:** Codex lifecycle hooks provide the same JSON fields AgentMon consumes (`hook_event_name` and `session_id`) and support the six events needed by the current state model: `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `PermissionRequest`, and `Stop`.

## Scope Decisions

- Keep the existing `/hook` HTTP contract, token handling, tmux headers, state values, hub polling, UI colors, and alert behavior unchanged.
- Use Codex lifecycle hooks, not `notify`. `notify` only covers turn completion and cannot produce the `blocked` state.
- Install Codex hooks globally at `~/.codex/hooks.json`. Global hooks work across repositories; Codex may still require the user to review and approve hook trust once.
- Preserve existing user hooks when installing or uninstalling AgentMon entries.
- Keep bare installer `--hooks` as a legacy alias for Claude Code. Add `--hooks=claude`, `--hooks=codex`, and `--hooks=all`; automatic mode detects installed clients and offers only those.
- Do not install Codex `SubagentStart` or `SubagentStop` hooks. A subagent finishing must not mark the parent pane done.
- Do not install unsupported Claude-only Codex events (`Notification`, `SessionEnd`). Codex `PermissionRequest` and `Stop` cover the relevant blocked/done transitions.
- Do not rename the existing `ClaudeSessionID` wire field in this change. Renaming it would create mixed-version agent/hub compatibility work without affecting behavior. Record the provider-neutral naming cleanup as separate technical debt.
- Do not add a provider dimension to state keys. One tmux pane has one foreground coding client, and `(target, pane)` remains the correct correlation key.

## State Mapping

| Codex event | AgentMon state | Reason |
|---|---|---|
| `SessionStart` | `idle` | Codex is present and waiting for work. |
| `UserPromptSubmit` | `working` | A new turn has begun, including turns that use tools not covered by tool hooks. |
| `PreToolUse` | `working` | Tool execution is active. |
| `PermissionRequest` | `blocked` | Codex is waiting for human approval. |
| `PostToolUse` | `working` | Clears `blocked` after an approved tool finishes and the turn continues. |
| `Stop` | `done` | The main agent turn finished. |

Codex has no `SessionEnd` lifecycle event. After every successful tmux discovery, the agent reconciles the state machine against the discovered pane IDs and removes absent entries, using a discovery-start timestamp to preserve hooks that race the snapshot. If Codex exits back to a shell in the same pane, the last state may remain until another agent emits `SessionStart`; do not apply an age-based expiry to legitimate long-running `working` or `blocked` states without stronger lifecycle evidence.

## Task 1: Make Hook Generation Provider-Aware

**Files:**
- Modify: `agent/internal/hooks/install.go`
- Modify: `agent/internal/hooks/install_test.go`

**Implementation:**

- [ ] Add a `Provider` string type with `ProviderClaude` and `ProviderCodex` constants and a strict parser that returns a useful error for unknown values.
- [ ] Replace the package-global `events` slice with provider-specific immutable event lists:
  - Claude: retain the current list exactly.
  - Codex: `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `PermissionRequest`, `Stop`.
- [ ] Change `Snippet` and `Merge` to accept a provider and generate only that provider's supported events.
- [ ] Keep `Command`, `LoadSettings`, `SaveSettings`, and marker-based `Unmerge` provider-neutral.
- [ ] Keep the command silent and non-blocking: stdin is forwarded to curl, curl has the existing two-second limit, stdout/stderr are discarded, and failures resolve to exit zero.
- [ ] Update package comments from "Claude Code hook intake" to "coding-agent hook intake" where they describe shared behavior.

**Tests:**

- [ ] Assert the Claude event set is unchanged to prevent a regression for existing users.
- [ ] Assert the Codex snippet contains exactly the six supported state events and excludes `Notification`, `SessionEnd`, and subagent events.
- [ ] Run idempotent merge/unmerge tests for both providers, including preservation of unrelated top-level keys and user hook groups.
- [ ] Assert both provider snippets retain `$TMUX`, `$TMUX_PANE`, the loopback endpoint, auth token lookup, marker, and silent-success behavior.

**Verification:**

```bash
go test ./agent/internal/hooks -run 'Provider|Snippet|Merge|Unmerge|Command' -v
```

## Task 2: Extend the Agent Hook CLI

**Files:**
- Modify: `agent/cmd/agentmon-agent/hooks_cli.go`
- Modify: `agent/cmd/agentmon-agent/hooks_cli_test.go`

**CLI contract:**

```text
agentmon-agent hooks print     --provider claude|codex [--config PATH]
agentmon-agent hooks install   --provider claude|codex --settings PATH [--config PATH]
agentmon-agent hooks uninstall --provider claude|codex --settings PATH [--config PATH]
```

- [ ] Add `--provider`, defaulting to `claude` so every existing command remains behaviorally compatible.
- [ ] Thread the parsed provider through `Snippet` and `Merge`.
- [ ] Keep `--settings` explicit. The system installer already knows the monitored user's home, while a manual root invocation cannot safely infer which user's global configuration should be modified.
- [ ] Include the provider in install/uninstall success output.
- [ ] Reject unknown providers before reading or writing the settings file.
- [ ] Keep `hook-test` provider-neutral because both clients send the same intake payload.

**Tests:**

- [ ] Preserve all current default-provider tests.
- [ ] Add Codex print/install/uninstall round trips using a temporary `hooks.json`.
- [ ] Verify an existing Codex user hook survives install, reinstall, and uninstall.
- [ ] Verify an invalid provider returns an error and does not create or alter the settings file.

**Verification:**

```bash
go test ./agent/cmd/agentmon-agent -run 'HooksMain|HookTest' -v
```

## Task 3: Lock the Shared Codex Intake Behavior

**Files:**
- Modify: `agent/internal/hooks/hooks.go`
- Modify: `agent/internal/hooks/hooks_test.go`
- Modify: `agent/internal/state/state.go`
- Modify: `agent/internal/state/state_test.go`
- Modify: `agent/internal/api/sessions.go`
- Modify: `agent/internal/api/sessions_test.go`

This task should be mostly tests and terminology changes; the current implementation already accepts Codex payloads.

- [ ] Add a table-driven handler test using representative Codex payload fields: `session_id`, `turn_id`, `cwd`, `model`, `permission_mode`, `tool_name`, and extra nested fields.
- [ ] Drive the sequence `SessionStart -> UserPromptSubmit -> PermissionRequest -> PostToolUse -> Stop` and assert `idle -> working -> blocked -> working -> done` for one pane.
- [ ] Assert the Codex `session_id` continues through the existing informational snapshot field without changing the wire schema.
- [ ] Confirm unknown future fields remain ignored and malformed bodies remain soft failures returning HTTP 204.
- [ ] Reconcile state after successful tmux discovery, pruning absent panes while retaining hook events newer than the discovery-start cutoff.
- [ ] Never prune state when discovery fails.
- [ ] Mark discovery partial when malformed tmux records are skipped and never reconcile from a partial snapshot.
- [ ] Update Claude-specific comments only where the code is now genuinely shared. Do not churn historical design documents or JSON field names.

**Verification:**

```bash
go test ./agent/internal/hooks ./agent/internal/state -v
```

## Task 4: Add Codex to One-Command Onboarding

**Files:**
- Modify: `hubd/internal/api/install.sh.tmpl`
- Modify: `hubd/internal/api/install_test.go`

**Installer behavior:**

- [ ] Generalize `install_hooks_into` to accept a provider and destination:
  - Claude: `$home_dir/.claude/settings.json`
  - Codex: `$home_dir/.codex/hooks.json`
- [ ] Continue running directory creation and hook installation as `RUN_USER`; root must not follow or overwrite user-controlled paths directly.
- [ ] Detect Claude using the current `.claude` directory or `claude` executable check.
- [ ] Detect Codex using a `.codex` directory or `codex` executable check in the run user's environment.
- [ ] In automatic mode, build a list of detected clients whose config does not already contain `agentmon-hook`. If the list is empty, exit without prompting or provisioning a token.
- [ ] For an interactive terminal, prompt once and name the detected clients. On acceptance, provision one shared hook token and install every missing detected client.
- [ ] For piped/non-interactive installation, print exact rerun commands such as `--hooks=codex` or `--hooks=all`; do not attempt a prompt.
- [ ] Support explicit modes:
  - bare `--hooks` and `--hooks=claude`: force Claude only, preserving the legacy contract;
  - `--hooks=codex`: force Codex only;
  - `--hooks=all`: force both;
  - `--no-hooks`: skip both.
- [ ] Ensure update installs can add Codex hooks without re-enrollment, exactly as existing Claude hook updates work.
- [ ] Update dry-run output to report the selected/detected providers without creating either configuration directory.
- [ ] Print a restart instruction for the affected client sessions after installation.

**Tests:**

- [ ] Rendered script contains both destinations and passes `--provider codex` for Codex.
- [ ] Legacy bare `--hooks` still selects only Claude.
- [ ] Explicit Codex/all/no-hooks modes are parsed and represented in dry-run output.
- [ ] Automatic detection handles Claude-only, Codex-only, both, already-installed, and neither-installed cases.
- [ ] The non-interactive path never reads `/dev/tty` and gives an actionable provider-specific rerun command.
- [ ] Hook token provisioning happens once when both providers are installed.
- [ ] Existing symlink-safety ownership behavior remains intact.
- [ ] The rendered template passes `bash -n`; run `shellcheck` through the existing project check when available.

**Verification:**

```bash
go test ./hubd/internal/api -run 'InstallScript' -v
make test
```

## Task 5: Update Operator Documentation

**Files:**
- Modify: `README.md`

- [ ] Rename the live-state section from Claude-only wording to "Enable coding-agent state".
- [ ] Document automatic detection and the explicit installer modes.
- [ ] Add manual commands:

```bash
agentmon-agent hooks install --provider claude --settings ~/.claude/settings.json
agentmon-agent hooks install --provider codex  --settings ~/.codex/hooks.json
```

- [ ] Document that clients must be restarted after changing hook configuration.
- [ ] Document custom `CODEX_HOME` as a manual-install case.
- [ ] State the Codex event mapping and the absence of `SessionEnd` cleanup.
- [ ] Note that hook support is independent of the selected Codex model, including Sol.
- [ ] Avoid claims about model rollout or availability; they are unrelated to the integration and change independently.

## Task 6: Full Verification and Live Acceptance

**Automated:**

- [ ] Run formatting on changed Go files.
- [ ] Run all Go tests by module from the workspace root.
- [ ] Run web tests even though no web files should change, because alert transitions are part of the end-to-end behavior.
- [ ] Run static checks and inspect the final diff for accidental generated/binary changes.

```bash
gofmt -w agent/internal/hooks/*.go agent/cmd/agentmon-agent/hooks_cli*.go
go test ./shared/... ./agent/... ./hubd/...
go vet ./shared/... ./agent/... ./hubd/...
npm --prefix web test -- --run
git diff --check
git status --short
```

**Live tmux acceptance on a host with Codex:**

- [ ] Install Codex hooks for the monitored run user and restart Codex.
- [ ] Start Codex inside a monitored tmux pane and confirm `SessionStart` produces `idle`.
- [ ] Submit a prompt and confirm the pane/session changes to `working`.
- [ ] Trigger an action requiring approval and confirm `blocked` plus the existing AgentMon attention alert.
- [ ] Approve it and confirm a subsequent tool event returns the state to `working`.
- [ ] Let the turn finish and confirm `done`; verify the existing optional done alert still follows user preferences.
- [ ] Run a subagent and confirm its completion does not prematurely mark the parent pane done.
- [ ] Run Claude Code in a second monitored pane and confirm both providers update independently.
- [ ] Uninstall Codex hooks and confirm unrelated user hooks remain in `~/.codex/hooks.json`.

## Acceptance Criteria

- Codex sessions show `idle`, `working`, `blocked`, and `done` using the existing colors and alert pipeline.
- Claude Code behavior and existing CLI/install commands remain backward-compatible.
- Installing either provider is idempotent and preserves user configuration.
- Installing both providers provisions only one AgentMon hook token.
- Hook delivery failures never delay or fail a Codex turn beyond the existing two-second curl bound.
- No new network-exposed endpoint, database migration, hub API, frontend state, or browser preference is introduced.
- The full automated suite and the live tmux acceptance checklist pass.

## Expected Effort

- Provider abstraction, CLI, and tests: 3-4 hours.
- Installer behavior and tests: 3-5 hours.
- Documentation and live acceptance: 1-2 hours.
- Total: approximately one focused engineering day, with a second day reserved for installer edge cases or Codex hook-runtime differences found during live testing.

## References

- Codex hooks: <https://learn.chatgpt.com/docs/hooks>
- Codex advanced configuration: <https://learn.chatgpt.com/docs/config-file/config-advanced>
- Codex configuration reference: <https://learn.chatgpt.com/docs/config-file/config-reference>
