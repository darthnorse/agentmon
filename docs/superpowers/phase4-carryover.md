# Phase 4 → carry-over. **PHASE 4 COMPLETE.**

Phase 4 (the UX + alerts layer on top of the Phase-3 state plane) is complete — three sub-milestones, each
brainstormed/spec'd/planned, implemented via an **ultracode Workflow** (parallel implement + pipelined
adversarial verify), and gated by **opus whole-branch review + `/multi-review --codex`** (fix-all-but-nitpicks),
SAFE-accepted on this host, and **locally merged to `main`**. **Not pushed, not deployed** (owner-only).

## The three sub-milestones (all merged to `main`)
- **M9 — Attention alerts + PWA + Web-Push** (Phase 4a, merge `1b63dc1`). The core supervision loop: a
  *different* session going `blocked` reaches the user via in-app toast/sound/vibrate (foreground), a
  page-driven `Notification` (alive-but-hidden), or hub **Web-Push** (backgrounded/asleep), de-duped
  server-side by SSE presence. Installable **PWA** (manifest + service worker). New hub `/api/v1/push/*`
  endpoints + VAPID + a subscription store + a broadcaster-driven dispatcher. Carryover: `m9-carryover.md`.
- **M10 — New-session flow** (Phase 4b, merge `d750257`). Create a tmux session from the UI: `POST /sessions`
  across agent (`tmux new-session` via the arg-array Runner, no shell) → hub (`session.create` authz + CSRF +
  audit + re-list) → web (a New-session form + auto-open). §13.6 safety: name charset (hub+agent), cwd
  allow-list (symlink/traversal blocked), custom commands rejected (shell-only v1). Carryover: `m10-carryover.md`.
- **M11 — UX polish + M8-deferred** (Phase 4c, merge `26e60de`). focus-next-blocked; per-user prefs
  (localStorage) + live terminal theme/font + an optional `done` alert; mobile §6.2 sectioned inbox;
  server-dot REST fallback; the terminal-WS `{t:state}` frame (focused-pane-only); the hub `TouchLastSeen`
  poller patch. Carryover: `m11-carryover.md`.

## KEY DECISIONS resolved with the owner / autonomously
- **Read/write input LOCK = DESCOPED from v1** (owner, 2026-06-29) — terminals are always `rw`. The master
  `agentmon-design.md` acceptance #9 + §6.3/§7.5/§11.8 are annotated descoped. (M10 was originally "lock +
  new-session"; the lock removal made it new-session-only.)
- **Notifications = three tiers + server-side presence de-dup; `blocked` only** (M9).
- **Session-creation policy (§18-Q6, M10):** single user creates; tmux-safe name charset; cwd allow-list
  (default `$HOME`, symlink/traversal-blocked); **no custom commands** (shell only).
- **§18-Q12 desktop layout = KEEP the grid** (M11, autonomous-conservative) — the inbox-pivot is **flagged
  for the owner**.
- **Prefs = localStorage** (per-device); a hub prefs endpoint is a later additive step.

## The review process earned its keep
Each milestone ran the full gate. The standout: in **M11**, the single-model **opus whole-branch review
returned READY**, but the **`/multi-review --codex` panel — codex and deep-scan INDEPENDENTLY converged** —
caught a real alert-suppression race (the new terminal-WS `{t:state}` store-write pre-empting the SSE alert
gate for open non-focused tiles, dropping the M9 toast ≈ coin-flip). Fixed + regression-tested. This is the
case for the multi-model panel on top of a single deep review.

## What the OWNER needs to do (nothing is pushed/deployed)
1. **Review the three specs** in `docs/superpowers/specs/` (M9/M10/M11) if you want to sanity-check the
   autonomous design calls — notably the **§18-Q12 grid-vs-inbox** decision (I kept the grid) and the
   **read/write-lock descope** (acceptance #9).
2. **On-device tests** (no headless browser here, so these are vitest/contract-probe-proxied only):
   - M9: install the PWA (iOS "Add to Home Screen" + Android), grant Notification permission, drive a 2nd
     session `blocked`, confirm Tier-1 toast/sound/vibrate foreground + a Tier-3 OS push when backgrounded/
     asleep + tap-to-open (acceptance #12 / §6.4).
   - M10: the full multi-process live `POST /sessions` e2e (real hub + real agent on a throwaway tmux socket
     + enrollment) — best with your oversight; the create path is unit/integration/contract-covered here.
   - M11: the visual polish (sectioned inbox, live theme/font, focus-next, session-less-server dot).
3. **Deploy** when ready — **Phase 4 needs BOTH a hub redeploy AND an agent redeploy** (M10 added the agent
   `POST /sessions` handler + the `session_dirs` config; M9/M11 hub changes need the hub rebuild). Hub:
   `docker compose up -d --build` from repo root (memory [[live-deployment]]); agent: redeploy the systemd
   binary. **Confirm before each prod deploy.** Optional new agent config: `session_dirs` (TOML allow-list
   for `POST /sessions`; defaults to `$HOME`). Optional hub config: `vapid_subject` (defaults to
   `external_origin`).

## v1 acceptance status (design §2)
All §2 v1-acceptance items are met EXCEPT #9 (read-only lock — owner-descoped). #12 (cross-session alert) is
implemented + unit/contract-tested here, pending the on-device confirmation above. The data plane (M1–M8) +
this UX/alerts layer (M9–M11) constitute the v1 target (§21), modulo the flagged on-device/owner items.

## Engineering notes
- Branches `phase-4-m9-alerts-pwa-push`, `phase-4-m10-new-session`, `phase-4-m11-polish` are merged (`--no-ff`)
  and can be deleted at will. New deps: `github.com/SherClockHolmes/webpush-go` (hub), `sonner` +
  `vite-plugin-pwa` (web) — all verified `CGO_ENABLED=0` / build-clean.
- Full suites at merge: Go (`shared`+`agent`+`hubd`, `-race`, `CGO_ENABLED=0`) green; web **195 vitest**;
  `tsc` clean; `vite build` emits the PWA bundle.
