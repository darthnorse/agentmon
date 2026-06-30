# M11 → carry-over (UX polish + M8-deferred — Phase 4c). **PHASE 4 COMPLETE.**

M11 (the Phase-4 polish grab-bag + the M8 deferrals) is complete, reviewed (ultracode workflow per-task
adversarial verify + opus whole-branch + `/multi-review --codex`), **SAFE-accepted on this host**, and merged
to `main` (local merge only). M11 is the **third and FINAL Phase-4 sub-milestone** (M9 → M10 → M11). **With
M11 merged, Phase 4 — and the v1 acceptance set (minus the owner-descoped read/write lock) — is complete.**

Branch: `phase-4-m11-polish`, off `main@d750257`. Spec:
`docs/superpowers/specs/2026-06-29-agentmon-m11-polish-design.md`. Plan:
`docs/superpowers/plans/2026-06-29-agentmon-m11-polish.md`.

## KEY DECISIONS (conservative, made autonomously while the owner was away)
- **§18-Q12 desktop layout = KEEP the M5 grid + sidebar.** A pivot to inbox+single-terminal is a major UX
  change not appropriate to do unattended; M11 polished *within* the grid. **FLAGGED for the owner** to make
  the pivot call.
- **Prefs = `localStorage`** (zustand `persist`, key `agentmon-prefs`), per-device. v1 is single-user, one
  device at a time (§11.7); a hub prefs table/endpoint (anticipated by §5.3) is a later enhancement.
- **`done`-too alerts** are an opt-in (`prefs.alertOnDone`) extending the M9 blocked-only foreground alert;
  **Web-Push stays blocked-only** (a `done` push would be noisy).

## What shipped (6 SDD tasks via an ultracode Workflow: Phase A [1-4] ‖, Phase B [5-6])
1. **Hub poller `TouchLastSeen`** — the every-3s `/state` poll (and the degraded `/sessions` fallback) now
   refresh `servers.last_seen_at`, so an actively-polled server's last-seen isn't stale (the carried M7/M8
   gap). `ServerLister` gained `TouchLastSeen`; best-effort, gated to successful/reachable polls only.
2. **Prefs + themes + live terminal font/theme** — `store/prefs.ts` (fontSizeDesktop 13 / fontSizeMobile 10 /
   terminalTheme dark / alertOnDone false); `lib/terminal-themes.ts` (dark/light/highContrast `ITheme`); XTerm
   applies font+theme **live** (effect keyed on `[fontSize, theme]`, refit) — the owner's mobile-10 default is
   now configurable.
3. **Mobile §6.2 sectioned inbox** — `SessionList` groups into **Needs attention / Done / Working / Idle**
   headers (empty sections omitted, search still applies).
4. **terminal-WS `{t:state}` frame** — the web consumes the hub's `{t:state}` text frame for the **focused
   pane only** (see the review fix below).
5. **`done`-too alerts** — `isAlertTransition` generalizes the M9 gate; `useStateStream` fires on →done when
   `alertOnDone`; `isAttentionTransition`/`blockedTitle` kept for the M9 suite.
6. **Shell integration** — a `SettingsPanel` (gear: font/theme/alert-on-done), **focus-next-blocked**
   (`lib/focus-next.ts` + a "Next blocked" button + the `n` shortcut, cap-aware), and the **server-dot REST
   fallback** (session-less servers now render in the sidebar with `ServerSummary.state ?? unknown`).

## Review path — the cross-model catch
**Workflow** (6 tasks, each implement pipelined into an adversarial verify): all impl green, all verdicts
pass, max severity minor (folded 3 fixes pre-commit: focus-next tile-cap toast, non-sticky section headers,
swipe-scroll cell from the live font size). **Opus whole-branch**: **READY** — traced all 9 risk areas,
confirmed store-purity + the M9 alert path + poller concurrency, **but MISSED** the alert-suppression race.
**`/multi-review --codex`** (feature-dev:code-reviewer + code-simplifier + deep-scan + codex gpt-5.5):
**codex (HIGH) and deep-scan (MEDIUM) INDEPENDENTLY converged** on a real regression the single-model opus
review missed — the terminal-WS `{t:state}` consumer (task 4) wrote the shared `session-state` store, and the
SSE alert gate reads the *prior* state from that same store; for an **open but non-focused** desktop tile a
terminal-WS frame landing before the SSE delta pre-set `blocked`/`done`, so the gate saw no transition and
**silently dropped the M9 attention toast/sound (≈ coin-flip)**. **Fixed**: only the **focused** pane consumes
the `{t:state}` frame (its actual purpose — SSE already covers non-focused tiles at the same in-process
latency), so non-focused tiles are never pre-empted; + a `useTerminalSession` regression test. Plus simplifier
cleanups (export `paneKey` + reuse; reuse the precomputed `nextBlockedRow`; `groups0`→`visibleGroups`; correct
a stale comment). Specialist: **0 findings**. **This is the multi-model multi-review earning its keep.**

## SAFE acceptance — DONE this session (2026-06-29, on this host)
No prod touch (memory [[dev-host-runs-hub-and-claude]]). **Full Go suite green (`-race`, vet, gofmt,
`CGO_ENABLED=0` build); web vitest 195/195 (31 files); `tsc` clean; `vite build` still emits the PWA
artifacts.** The hub poller `TouchLastSeen` is internal (no endpoint) and is covered by its unit test +
`-race`; no live agent/loopback probe needed (per spec §7). **FLAGGED for the owner (on-device, no headless
browser here):** the visual polish — sectioned-inbox layout, live theme/font, focus-next navigation,
session-less-server dot — and the **§18-Q12 desktop grid-vs-inbox decision** (the grid was kept).

## Deferred (with rationale)
- **§18-Q12 grid→inbox pivot** — kept the grid; a deliberate product decision left to the owner.
- **Hub-persisted prefs** — localStorage only (per-device); a prefs table/endpoint is the additive next step.
- **`done` Web-Push** — foreground `done` alert is opt-in; push stays blocked-only.
- **The XTerm live-apply effect has no direct unit test** (jsdom can't run xterm) — threading is tsc-checked
  + smoke-tested, consistent with the existing XTerm approach.
- **Nitpicks:** the `dark` theme adds a `selectionBackground` (tiny visual delta, additive); a redundant
  mount-time fit; `SettingsPanel` clamp bounds (8–24) not asserted by its test.

## Verification at merge
Full Go suite green (`shared`+`agent`+`hubd`, `-race`, `CGO_ENABLED=0`); `go vet` + `gofmt` clean; web vitest
**195/195**; `tsc --noEmit` clean; `vite build` emits the PWA bundle. **NOT pushed and NOT deployed** — local
merge only. **Deploying Phase 4 needs BOTH a hub redeploy (`docker compose up -d --build`) AND an agent
redeploy** (M10 added the agent `POST /sessions` handler + `session_dirs` config); both remain owner-only.

## Phase 4 — complete
M9 (alerts + PWA + Web-Push) → M10 (new-session flow) → M11 (polish + M8-deferred), all reviewed three ways
(workflow verify + opus whole-branch + `/multi-review --codex`), SAFE-accepted, and locally merged to `main`.
A Phase-4 summary + the milestone-memory update follow in `docs/superpowers/phase4-carryover.md`.
