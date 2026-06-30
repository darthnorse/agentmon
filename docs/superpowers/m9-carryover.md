# M9 → carry-over (attention alerts + PWA + Web-Push — Phase 4a)

M9 (the **core supervision loop closure** — alert the user when a *different* session goes `blocked`, plus
the installable PWA + Web-Push the design lists as must-have) is complete, reviewed (ultracode workflow
per-task adversarial verify + opus whole-branch + `/multi-review --codex`), **SAFE-accepted on this host**,
and merged to `main` (local merge only). M9 is the **first of three Phase-4 sub-milestones: M9 alerts+PWA+push
→ M10 new-session → M11 polish.** This captures the contract, decisions, deferrals, and the M10 handoff.

Branch: `phase-4-m9-alerts-pwa-push`, off `main@6cea364`. Spec:
`docs/superpowers/specs/2026-06-29-agentmon-m9-alerts-pwa-push-design.md`. Plan:
`docs/superpowers/plans/2026-06-29-agentmon-m9-alerts-pwa-push.md`.

## The KEY DECISIONS (resolved with owner during brainstorm)
1. **Three-tier notification model, server-side presence de-dup.** Tier 1 in-app toast+sound+vibrate
   (foreground/visible, pure client off the M8 SSE store, tab-aware via `focusedKey`); Tier 2 page-driven
   `Notification` (alive but `document.hidden`); Tier 3 hub Web-Push (page dead/asleep). De-dup is
   server-side: the hub pushes (Tier 3) **only when the principal has no live SSE connection** — Tiers 1/2
   only fire while the page is alive, so the tiers never overlap, which also satisfies iOS's rule that every
   *delivered* push must show a notification.
2. **`blocked` only** raises alerts/pushes. `done` stays inbox-only.
3. **Read/write input lock DESCOPED from v1** (owner, 2026-06-29) — terminals are always `rw` (the hub
   already minted `rw` only, so this was a scope *removal*). The master `agentmon-design.md` acceptance #9 +
   §6.3 / §7.5 / §11.8 are annotated as descoped.
4. **PWA before push** (iOS push needs an installed standalone PWA); push is **feature-detected**, so
   unsupported browsers no-op to Tiers 1/2.

## What shipped (9 SDD tasks via an ultracode Workflow: hub Go chain ‖ web TS chain, TDD; 139 web tests)
**Hub:** `db/push.go` (push_subscriptions store + `LoadOrCreateVAPID`) + migration `0004_push.sql`;
`api/push.go` (`VapidHandler`/`SubscribeHandler`/`UnsubscribeHandler` + `isSafePushEndpoint` SSRF guard) +
`PushStore` on `Deps` + routes; `state/presence.go` (SSE connection ref-counter) wired into `EventsHandler`;
`state/push_dispatcher.go` (`RunPushDispatcher` + `blockedGate` transition de-dup + `NewWebPushSender` with
a 10s client timeout) + `main` lifecycle (VAPID load, presence, dispatcher goroutine) + config
`vapid_subject`; `webui/embed.go` registers the `.webmanifest` mime. Dep: `github.com/SherClockHolmes/
webpush-go v1.4.0` (pure-Go, `CGO_ENABLED=0` clean).
**Web:** `lib/alerts.ts` (`isAttentionTransition` pure gate + shared `blockedTitle`); `lib/push.ts`
(feature-detected `enablePush`/`disablePush` + `getActiveRegistration` — the `.ready`-hang guard);
`lib/audio-cue.ts` (synthesized WebAudio chirp — **no mp3 asset**); `hooks/useAttentionAlerts.ts` (Tier 1/2
driver) wired through `useStateStream`'s `onAttention`; `sw.ts` (vite-plugin-pwa `injectManifest`: precache
+ push handler + notificationclick); `components/EnableAlerts.tsx` (opt-in, user-gesture) + sign-out
`disablePush`; PWA manifest + placeholder icons; `index.html` iOS meta.

## Public contracts (all additive — no change to existing endpoints / SSE / terminal wire)
- `GET /api/v1/push/vapid` (auth) → `{ "publicKey": "<base64url VAPID public key>" }`. Public key is
  non-secret; **private key is DB-only, never served/logged**.
- `POST /api/v1/push/subscribe` `{endpoint, keys:{p256dh, auth}}` (+ `X-CSRF-Token`) → `204`. Rejects
  non-https / loopback / private / link-local / localhost endpoints → `400` (SSRF guard).
- `POST /api/v1/push/unsubscribe` `{endpoint}` (+ `X-CSRF-Token`) → `204`; **principal-scoped** delete.
- Push message (encrypted, hub→SW): `{type:"blocked", server, target, session, ts}` — only already-visible
  identifiers, no secrets/keystrokes. SW notification `tag` = `stateKey(server,target,session)` (``),
  shared with the Tier-2 in-app Notification so the tiers coalesce.

## Invariants the implementation upholds (don't regress)
- **Presence de-dup actually works:** `main.go` builds ONE `state.NewPresence()` and shares the instance
  with both `api.Deps.Presence` (EventsHandler inc/dec) and the dispatcher. `EventsHandler` pairs `Add`/
  `defer Remove` after every early-return, so no path leaks a count and permanently suppresses push.
- **One push per blocked episode:** `blockedGate` (a self-pruning set of currently-blocked session keys)
  fires only on a transition into `blocked`; suppresses the poller's republish of an already-blocked session.
- **Store reducer stays pure** (M8 invariant): alert detection is the pure `isAttentionTransition` called
  from the wiring layer, never a side-effect in `applyDelta`; `prev` is captured before `applyDelta`.
- **Never blocks the broadcaster drain:** the dispatcher offloads each fresh blocked Change to a goroutine;
  each send is bounded by a 10s HTTP timeout.
- **Security:** VAPID private never served/logged; CSRF on the new POSTs (via `RequireAuth`); `authorize()`
  on every push endpoint (incl. the VAPID GET); SW precaches only the app shell (never `/api`/SSE/terminal);
  session names render as text (no XSS).

## Review path
**Implement workflow** (ultracode): 9 tasks, hub‖web, each implement **pipelined into an adversarial
verify** (HARD on store/endpoints-no/presence/dispatcher/alert-core/push-client/wiring). All 9 impl green,
all verdicts pass, max severity minor. **Opus whole-branch review**: **READY-WITH-FIXES**, no Critical —
applied I1 (VapidHandler authorize), I2 (send timeout) + M1 (https-only), M2 (principal-scoped delete), M3
(bounded blockedGate), M5 (manifest mime), M6 (EnableAlerts `.ready` hang), M7 (drop dead mp3).
**`/multi-review --codex`** (feature-dev:code-reviewer + code-simplifier + deep-scan + codex gpt-5.5): **8
fixes applied** (`e71dbb5`) — SW tag→stateKey (3 reviewers), SSRF host validation, SW userVisibleOnly
fallback, response-body drain, shared `blockedTitle`/`getActiveRegistration`, blockedGate struct key,
single `getState()` per delta — each bug/security fix with a regression test. One fix (auth.ts) initially
broke the "throw never blocks logout" test; the best-effort guard was restored and re-verified.

## SAFE acceptance — DONE this session (2026-06-29, on this host)
No prod touch (memory [[dev-host-runs-hub-and-claude]] + [[live-deployment]]). **Go suite green (`-race`,
`vet`, `gofmt`, `CGO_ENABLED=0` build); web vitest 139/139; `tsc` clean; `vite build` emits the PWA
artifacts (`manifest.webmanifest` + `sw.js`, 13-entry precache).** Built a scratch embedded hub, ran it on a
**loopback port (127.0.0.1:19390) with a FRESH empty DB** (no prod DB, no prod-agent, no tmux/session 0);
**contract probe 9/9**: migration `0004` applied; SPA serves (`GET /` + hashed M9 bundle); `manifest.
webmanifest` served as `application/manifest+json`; `sw.js` 200; `/push/vapid` unauth→401; wrong-Origin
login→403, correct→cookie+csrf; VAPID generated-on-first-boot and **persisted** (same key on the 2nd call,
pub+priv non-empty in `push_vapid`); subscribe no-CSRF→403, **internal endpoint→400 (SSRF guard live)**,
valid https→204; unsubscribe→204; the subscription round-tripped then was deleted in `push_subscriptions`.
Post-test: prod hub container Up, prod agent (pid 1889386) alive, default session 0 intact, `deploy/data`
byte-identical, scratch hub + DB torn down, port released, repo clean.

**FLAGGED for the owner (not run here):**
1. The full **hook-driven `blocked`→Web-Push live replay** (agent hook → poller → broadcaster → dispatcher →
   real `webpush.SendNotification`). Best run with oversight (needs a scratch agent on a throwaway tmux
   socket on this prod host); covered here by the dispatcher unit tests + the endpoint/VAPID contract probe.
2. The on-device **PWA install + push + §6.4 iOS/Android checklist** (install to Home Screen, grant
   Notification permission, drive a second session blocked, confirm Tier-1 toast/sound/vibrate foreground +
   a Tier-3 OS notification when backgrounded/asleep + tap-to-open). No headless browser on this host, so
   the React render + SW + push delivery were never machine-tested (vitest + contract probe are the proxy).

## Deferred (with rationale — surfaced by reviewers, triaged consistently with the project's single-user posture)
- **Per-device vs per-principal presence de-dup** (deep-scan, the most product-relevant): Tier-3 push is
  suppressed for ALL of a principal's subscriptions whenever ANY of their SSE streams is live. So
  desktop-open (SSE up) + phone-tab-closed → the phone gets no push for that episode. Matches the documented
  "single active surface" design (§11.7 one-device-at-a-time), but **if cross-device alerting is wanted,
  scope presence/de-dup per subscription endpoint.** Owner decision.
- **Per-principal subscription cap** (codex) + **bounded dispatch worker pool** (codex) + **dispatch N+1
  read** (simplifier): YAGNI for a single-user LAN tool (consistent with M7's deferred `/seen` + SSE caps);
  the dispatcher is bounded in practice by the few concurrently-blocked single-user sessions + the 10s send
  timeout. Revisit with multi-user.
- **Restart re-push:** on hub restart the gate is empty, so currently-blocked sessions re-push to offline
  subscribers. Deliberate (re-surfacing blocked work to an away user is reasonable; bounded small).
- **No DNS-resolving SSRF guard:** `isSafePushEndpoint` rejects internal IP literals + localhost but not a
  hostname that resolves to an internal IP. Moot under single-principal (the caller already owns the host).
- **Push/toast one-tap-to-pane deep-link:** notificationclick / toast "Open" go to `/` (blocked-first
  inbox), not the exact pane (no paneId in the payload). M11 polish.
- **Nitpicks left:** push `ts` uses RFC3339 (not `state.HubTS`); `autoUpdate` SW can reload a controlled tab
  on a mid-session deploy. **Asset gap:** PWA icons are functional placeholders (red "A" on `#0b0b0b`) — a
  designed icon is an owner decision. VAPID private key stored plaintext in `push_vapid` (consistent with
  the existing plaintext server Bearer/SigningKey + LAN posture).

## Reminders for M10 (new-session flow)
- `POST /api/v1/servers/{id}/sessions` is **greenfield on both hub AND agent** (the agent's tmux package
  only reads/attaches today). Needs: agent `tmux new-session` (arg-array, no shell interpolation, §13.6),
  a hub endpoint authorized via `authorize(session.create)` + audited, and a web "new session" flow
  (prompt name, suggest cwd basename) → `api.createSession` → invalidate `["sessions",serverId]` →
  `usePanes.openPane`. Sanitize the session name to a safe charset; restrict allowed dirs.
- The `POST` handler/CSRF/auth pattern to copy is `api/push.go` / `api/seen.go`; the agent REST shape is in
  design §12.2 (`POST /sessions?target= body {name, cwd?, command?}`).

## Verification at merge
Full Go suite green (`shared`+`agent`+`hubd`, `-race`, `CGO_ENABLED=0`); `go vet` + `gofmt` clean on
M9-touched files; web vitest **139/139**; `tsc --noEmit` clean; `vite build` emits `dist/` with the PWA
manifest + service worker. Runtime web↔hub contract probe (9/9) accepted on this host against a loopback
scratch hub + fresh DB. **NOT pushed and NOT deployed** — local merge only; the prod hub redeploy
(`docker compose up -d --build`) remains owner-only.
