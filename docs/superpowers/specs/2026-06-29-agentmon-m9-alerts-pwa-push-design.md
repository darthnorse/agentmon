# M9 — Attention alerts + PWA + Web-Push (Phase 4a): close the core supervision loop

## 1. Goal

Phase 3 made the state plane **live** (M6 agent → M7 hub → M8 web: SSE dots, blocked-first, per-principal
seen). M9 closes the remaining **must-have** supervision gap from §2 / §18-Q9 / acceptance #12: when a
*different* session goes `blocked`, the user is **alerted** even while inside another terminal — in-app
(toast + sound + vibrate) while AgentMon is foreground, and via **Web-Push** when it is backgrounded or the
phone is asleep. M9 also ships the **installable PWA** (manifest + standalone display + service worker) that
§2 lists as must-have and that iOS *requires* before Web-Push works at all.

M9 is the first of three Phase-4 sub-milestones: **M9 alerts+PWA+push → M10 new-session → M11 polish**.
This milestone is **web-heavy with a focused hub addition** (VAPID, a subscription store, three endpoints,
and a push dispatcher on the existing broadcaster). It re-uses the M7/M8 data path end-to-end; no change to
the agent, the poller's state logic, the SSE wire contract, or the terminal relay.

## 2. Locked decisions (from brainstorming, 2026-06-29, with owner)

- **Three-tier notification model, no double-alert** (see §4): Tier 1 in-app (foreground/visible), Tier 2
  page-driven OS `Notification` (alive but tab hidden), Tier 3 hub Web-Push (page dead / asleep). De-dup is
  **server-side**: the hub pushes (Tier 3) only when the principal has **no live SSE connection** — Tiers
  1/2 only fire while the page is alive, so the tiers never overlap. This also satisfies iOS's rule that
  every *delivered* push must show a user-visible notification (we never deliver a push we'd want to
  suppress).
- **`blocked` only.** Alerts and pushes fire on a transition **into `blocked`**. `done` still surfaces in
  the inbox/dots (M8) but raises no active alert. (A `done`-too pref is M11.)
- **Read-only lock is OUT of Phase 4 entirely** (owner decision, 2026-06-29). Terminals are always `rw` —
  the hub already mints `rw` only, so this is a *removal* of previously-planned scope, not new work. The
  master design doc's acceptance #9 and §6.3 write-lock defaults are amended to "descoped" (separate
  doc-only edit). `authorize()`-on-every-endpoint (acceptance #11) is unaffected; it is already wired.
- **PWA before push.** The installable shell ships first within M9 because iOS Web-Push is only available
  from a Home-Screen-installed standalone PWA. Push subscription is **feature-detected** (`'PushManager' in
  window`), so iOS-non-installed and unsupported browsers simply fall back to Tiers 1/2 with no error path.
- **The store stays a pure reducer** (M8 invariant). Alert *detection* lives in the wiring/transport layer
  via a pure helper, never as a side-effect inside `applyDelta`.
- **One SSE stream** (M8 invariant). The alert layer reuses the single existing `StateStream`; it does not
  open a second `EventSource`.

## 3. Public contracts (all additive — no change to existing endpoints/wire shapes)

New hub endpoints (registered in `router.go` next to `POST /seen`; all behind `RequireAuth` → cookie auth
+ CSRF on the mutating ones, for free):

- `GET  /api/v1/push/vapid` → `{ "publicKey": "<base64url VAPID P-256 public key>" }`. The VAPID public key
  is **not secret**; gated behind auth only for tidiness. Read by the client before `pushManager.subscribe`.
- `POST /api/v1/push/subscribe` body `{ "endpoint": string, "keys": { "p256dh": string, "auth": string } }`
  → `204`. Upserts a subscription for the calling principal (PK = `endpoint`). Requires `X-CSRF-Token`.
- `POST /api/v1/push/unsubscribe` body `{ "endpoint": string }` → `204`. Deletes the subscription. Called on
  sign-out and when the user disables alerts. Requires `X-CSRF-Token`. Idempotent (deleting an absent
  endpoint is `204`).

Push message (hub → service worker, **encrypted** per the Web Push / VAPID protocol). Decrypted JSON:

```jsonc
{ "type": "blocked", "server": "<serverId>", "target": "<target>", "session": "<sessionName>", "ts": "<RFC3339>" }
```

The SW renders one notification per session (notification `tag` = the session key so repeated pushes for the
same session collapse), and `notificationclick` focuses an existing PWA window or opens `/` (the inbox,
where blocked-first already floats the session — see §4 Tier-3). Precise one-tap-to-pane deep-linking is
M11 polish; acceptance #12 only requires that the alert reaches the user.

## 4. Architecture — the three-tier alert model

A blocked transition reaches the user through exactly **one** tier, chosen by where the client is:

| Tier | Client state | Mechanism | Who fires it | Permission |
|---|---|---|---|---|
| 1 | Foreground & **visible** | toast + sound + vibrate | client (SSE delta) | none |
| 2 | Alive but **tab hidden** | page-driven `new Notification()` | client (SSE delta, `document.hidden`) | Notification |
| 3 | Page **dead** / phone asleep | Web-Push → SW `showNotification` | **hub** (broadcaster) | Push + (iOS) installed PWA |

**Tier 1 — in-app (the acceptance-#12 must-have, pure client, zero server).**
`useAttentionAlerts()` mounted in `AuthLayout` (beside `useStateStream`). On each SSE delta the wiring layer
computes, with a **pure** helper `isAttentionTransition(prevState, frame, focusedKey)` (in `lib/alerts.ts`),
whether the frame is a transition **into `blocked`** for a key that is **not** `focusedKey` (the
actively-viewed session — never alert what you're looking at) and was not already `blocked`. If so it drives:
toast (`sonner`, naming the project/session, with a "Go" action → navigate to `/`), a short sound cue
(`AudioCue`, primed on first user gesture to satisfy autoplay policy), and `navigator.vibrate(...)`
(Android; iOS Safari ignores it — acceptable).

**Tier 2 — alive-but-hidden.** Same detection; when `document.visibilityState === 'hidden'` but the page is
still running (desktop background tab, briefly-backgrounded mobile), fire a page-owned `new Notification(...)`
(reusing the granted Notification permission) instead of a toast the user can't see. No server involvement.

**Tier 3 — backgrounded / asleep (hub Web-Push).** A `state.PushDispatcher` subscribes to the existing
`state.Broadcaster` exactly like `EventsHandler` does. On a `Change` with `Global == blocked`, for each
principal that owns subscriptions **and has no live SSE connection** (presence check, see §6 hub), it sends
an encrypted Web-Push to every stored subscription. The SW's `push` handler decrypts and calls
`showNotification`. Because the hub only pushes when the page is dead, this never collides with Tier 1/2 and
always results in a shown notification (iOS-safe).

**De-dup invariant:** Tier 3 is gated by *server-observed SSE presence*; Tiers 1/2 require a live page. A
small race exists at the moment of backgrounding (page torn down but presence not yet decremented, or vice
versa) — bounded and harmless: at worst a stray push the SW shows, or a missed push the next blocked event
or app-resume corrects. Documented, not engineered away.

## 5. Data model (one new migration + VAPID persistence)

`hubd/internal/db/migrations/0004_push.sql` (mirrors the `principal_seen` upsert pattern):

```sql
push_subscriptions (
  principal_id TEXT NOT NULL,
  endpoint     TEXT NOT NULL,           -- the push service URL; natural unique key
  p256dh       TEXT NOT NULL,           -- client public key (base64url)
  auth         TEXT NOT NULL,           -- client auth secret (base64url)
  user_agent   TEXT,
  created_at   TEXT NOT NULL,
  PRIMARY KEY (endpoint)
);
CREATE INDEX idx_push_principal ON push_subscriptions(principal_id);

push_vapid (                            -- single-row; generated once on first boot
  id          INTEGER PRIMARY KEY CHECK (id = 1),
  public_key  TEXT NOT NULL,            -- base64url, served to clients
  private_key TEXT NOT NULL,            -- base64url, NEVER leaves the hub
  created_at  TEXT NOT NULL
);
```

VAPID keypair: generated with `webpush.GenerateVAPIDKeys()` on first boot if `push_vapid` is empty, then
persisted and loaded for the process lifetime. The private key is never logged and never served. The VAPID
**subject** (a `mailto:`/URL contact required by the protocol) comes from config `vapid_subject`
(default: the configured `external_origin`).

Pruning: a `410 Gone` / `404 Not Found` from the push service on send → delete that `endpoint` row (standard
Web-Push subscription expiry handling).

## 6. Components

### Web (the bulk)

- `lib/alerts.ts` (**new, pure**) — `isAttentionTransition(prev, frame, focusedKey) → boolean`; the
  blocked-transition + focus-suppression rule. Unit-tested in isolation. Mirrors the M8 `state.ts` purity.
- `lib/audio-cue.ts` (**new**) — `AudioCue`: lazy WebAudio/`<audio>` cue, `prime()` on first gesture,
  `play()` on alert; no-throw if blocked.
- `lib/push.ts` (**new**) — feature-detect (`serviceWorker`, `PushManager`, `Notification`); `subscribe()`
  (fetch VAPID key → `registration.pushManager.subscribe({userVisibleOnly:true, applicationServerKey})` →
  POST `/push/subscribe`), `unsubscribe()`, permission helpers. All guarded so unsupported browsers no-op.
- `hooks/useAttentionAlerts.ts` (**new**) — consumes deltas via the existing stream wiring; drives Tier
  1/2. Mounted in `AuthLayout`.
- `sw.ts` (**new service worker**, built via `vite-plugin-pwa` `injectManifest` so we own the SW source) —
  `push` handler (decrypt → `showNotification`), `notificationclick` (focus/open `/`), minimal precache of
  the app shell (Workbox-injected manifest). Authed data is never cached.
- `components/EnableAlerts.tsx` (**new**) — a small opt-in control (settings/inbox header) that requests
  Notification permission + push subscription from a user gesture; hidden when unsupported.
- `lib/api-client.ts` (extend) — `getVapidPublicKey()`, `subscribePush(sub)`, `unsubscribePush(endpoint)`.
- `lib/contracts.ts` (extend) — push request/response types.
- Wiring: `useStateStream`/`AuthLayout` pass deltas to `useAttentionAlerts`; `store/auth.ts` `signOut`
  best-effort `unsubscribePush`; SW registration in `main.tsx`/`AuthLayout`.
- `index.html` + `vite.config.ts` — `vite-plugin-pwa` (`injectManifest`): manifest (name, short_name,
  icons 192/512 + maskable, `display: standalone`, theme/background color, `start_url`, `scope`), iOS meta
  (`apple-mobile-web-app-capable`, status-bar-style, apple-touch-icon). `viewport-fit=cover` already set.
- **Asset gap (flagged):** PWA icons (192/512 + maskable) and a short notification sound. M9 ships
  functional placeholders; a designed icon/sound is an owner asset decision (note in carryover).

### Hub (focused)

- `db/push.go` (**new store**) — `UpsertSubscription`, `ListSubscriptionsForPrincipal`,
  `DeleteSubscription`, `PrincipalIDsWithSubscriptions`; `LoadOrCreateVAPID`. Mirrors `db/seen.go`.
- `api/push.go` (**new handlers**) — `VapidHandler`, `SubscribeHandler`, `UnsubscribeHandler`; decode with
  `MaxBytesReader`, `authorizeOr403`, persist, `204`. `PushStore` interface on `api.Deps` (like `SeenStore`).
- `state/presence.go` (**new**) — `Presence`: in-memory `map[principalID]int` connection counter;
  `Add(id)/Remove(id)/Online(id)`. `EventsHandler` increments on connect, decrements on disconnect (defer).
- `state/push_dispatcher.go` (**new**) — subscribes to `Broadcaster`; on `Change.Global == blocked`, for
  each principal in `PrincipalIDsWithSubscriptions` that is **not** `Presence.Online`, sends Web-Push
  (`webpush-go`) to each subscription in a bounded worker; prunes on 404/410. Started as a goroutine in
  `main` like the poller; clean shutdown.
- `cmd/agentmon-hubd/main.go` — wire `PushStore`, `Presence`, VAPID load, dispatcher lifecycle; config
  `vapid_subject`.
- Dependency: `github.com/SherClockHolmes/webpush-go` (pure-Go crypto — **verify `CGO_ENABLED=0` build**).

## 7. Data flow (end to end)

**Foreground (Tier 1):** agent hook → poller → `session_state_events` → projection → `Broadcaster.Publish` →
SSE delta → M8 store `applyDelta` → wiring computes `isAttentionTransition` → toast + sound + vibrate.
(Identical to M8's path; M9 only adds the terminal alert side-effect, off the pure store.)

**Backgrounded (Tier 3):** …→ `Broadcaster.Publish(blocked)` → `PushDispatcher` (parallel subscriber) sees
`Online(principal) == false` → `webpush.SendNotification` to each stored subscription → push service → SW
`push` event → `showNotification` → `notificationclick` → focus/open PWA at `/`.

**Subscribe:** install PWA → `EnableAlerts` (user gesture) → permission grant → fetch VAPID public key →
`pushManager.subscribe` → POST `/push/subscribe` → row in `push_subscriptions`. **Unsubscribe:** sign-out /
disable → POST `/push/unsubscribe` → row deleted.

## 8. Security

- New mutating endpoints inherit auth + CSRF from `RequireAuth` (the `POST /seen` pattern). The VAPID GET is
  cookie-authed; the public key is non-secret.
- VAPID **private** key: DB-only, never logged, never served. Generated with CSPRNG via the library.
- Push payload contains only `server/target/session/state/ts` — no secrets, no tokens, no keystrokes
  (consistent with §13.5 "no raw keystrokes"). The session name is already user-visible everywhere.
- `userVisibleOnly: true` on subscribe (required by Chrome; aligns with the iOS must-show rule).
- Subscriptions are per-principal; the dispatcher only sends to the owning principal's endpoints.
- SW caches the app shell only — never `/api` responses, the SSE stream, or terminal data.
- No new audit events (the alert path is read-only/notification; consistent with M7/M8 which added none).
  Push subscribe/unsubscribe are low-sensitivity; left un-audited for v1 (note for revisit if multi-user).

## 9. Testing — risk-tiered (no headless browser on this host; vitest + Go + loopback probe, as M5/M7/M8)

**Web (vitest):**
- HARD: `lib/alerts.ts` `isAttentionTransition` — blocked-transition true/false matrix, focus suppression,
  already-blocked no-refire, unknown/garbage state clamps (reuse M8 `normalizeState`).
- HARD: `lib/push.ts` — feature-detect no-ops on missing APIs; subscribe posts the right body; unsubscribe;
  permission-denied path; DI'd `serviceWorker`/`PushManager`/`fetch` (no real SW in jsdom).
- MEDIUM: `useAttentionAlerts` — fires toast/sound/vibrate on a mocked delta; Tier-1 vs Tier-2 branch on
  mocked `document.visibilityState`; does nothing for the focused key.
- LIGHT: `AudioCue` (prime/play no-throw), `EnableAlerts` (renders/hidden by feature-detect), api-client
  push methods, contracts.
- The SW itself (`sw.ts`) is not unit-testable in jsdom — covered by source review + the on-device check.

**Hub (Go, `-race`, `CGO_ENABLED=0`):**
- HARD: `PushDispatcher` — pushes only when `!Online`; one push per blocked Change; prunes on 404/410;
  never blocks the broadcaster (drop-oldest still holds); shutdown clean. Inject a fake push sender.
- HARD: `Presence` — concurrent Add/Remove/Online correctness; `EventsHandler` inc/dec on connect/disconnect.
- MEDIUM: `db/push.go` upsert/list/delete/`LoadOrCreateVAPID` (generate-once, idempotent reload); migration
  `0004` applies on a prod-shaped DB copy.
- MEDIUM: `api/push.go` handlers — auth/CSRF gates (403 without CSRF), body validation, 204 happy paths.

**Build:** `tsc --noEmit`, `vite build` (PWA plugin emits manifest + `sw.js` + precache), full Go suite,
`go vet`, `gofmt`, static build with the new dependency.

## 10. Acceptance (SAFE, per memory [[dev-host-runs-hub-and-claude]] + [[live-deployment]])

- Web suite green; `tsc` clean; `vite build` emits the PWA artifacts (manifest, `sw.js`).
- Go suite green; loopback **scratch hub on a fresh/copy DB** runtime contract probe: `0004` migrates;
  `GET /push/vapid` returns a key; `POST /push/subscribe`/`unsubscribe` gate CSRF (403/204); a synthesized
  blocked Change with a stored subscription + no SSE presence triggers a push send to a fake endpoint
  (assert payload); with SSE presence, no send. **No prod DB, no prod-agent, no tmux/session 0.**
- **Flagged for the owner (real device, no headless browser here):** install the PWA (iOS Safari "Add to
  Home Screen" + Android Chrome install), grant Notification permission, drive a real second session to
  `blocked`, and confirm: (1) in-app toast + sound + vibrate while foreground in another terminal; (2) an OS
  notification when the PWA is backgrounded / phone asleep; (3) tapping it opens AgentMon to the blocked
  session. This is acceptance #12 + the §6.4 cross-session-alert item, and the only part the host can't
  machine-test.

## 11. Scope boundaries — explicitly OUT of M9 (later Phase-4 or beyond)

- Read-only / write lock — **cut from Phase 4** (owner). Always `rw`.
- New-session flow — **M10**.
- focus-next-blocked, per-user prefs (incl. a `done`-too alert toggle), terminal theme/font *settings*,
  desktop grid-vs-inbox (§18-Q12), mobile §6.2 sectioned inbox, the M8-deferred server-dot REST fallback /
  terminal-WS `{t:state}` frame / hub `TouchLastSeen` poller gap — **M11**.
- Precise push/toast one-tap-to-pane deep-link (M9 opens the inbox) — M11 polish.
- Designed app icon + notification sound asset — owner asset decision.

## 12. Alternatives considered (and rejected)

- **Foreground-suppress de-dup** (SW checks for a focused client and drops the notification when foreground)
  — rejected: iOS penalizes/revokes subscriptions that receive a push without showing a notification.
  Server-side presence de-dup avoids ever delivering a suppressible push.
- **Client-only "push" via the page Notification API** (no server) — rejected: cannot wake a dead page /
  asleep phone; only Tiers 1/2. Insufficient for the real requirement. (We keep it *as* Tier 2 for the
  alive-but-hidden case, where it's exactly right.)
- **Hand-rolled manifest + SW** (no `vite-plugin-pwa`) — viable but we'd re-implement registration,
  precache, and dev/prod SW paths. `injectManifest` gives us those while we still own `sw.ts` for the push
  handler. Chosen.
- **`sonner` vs hand-rolled toast** — `sonner` is small, accessible, shadcn-idiomatic, and `tailwindcss-
  animate` is already present. Chosen over hand-rolling.
- **Per-principal push action enum in `authz`** — YAGNI for v1 single-user; subscribe/unsubscribe authorize
  under the existing `ServerView` seam (the chokepoint still runs). Revisit with multi-user.

## 13. Build sequence (work-list → writing-plans → Workflow)

Risk-tiered tasks for the implementation plan (parallel implement + pipelined adversarial verify, hard tier
on the dispatcher / presence / alert-detection / push-client, light on presentational + manifest):

1. **Hub data layer** — migration `0004`, `db/push.go` store, `LoadOrCreateVAPID`. (HARD: migrate-on-prod-
   shape; generate-once.)
2. **Hub endpoints** — `api/push.go` + `PushStore` on `Deps` + routes. (MEDIUM.)
3. **Hub presence** — `state/presence.go` + `EventsHandler` inc/dec. (HARD.)
4. **Hub dispatcher** — `state/push_dispatcher.go` + `webpush-go` + `main` lifecycle + config. (HARD.)
5. **Web pure core** — `lib/alerts.ts`, `lib/contracts.ts`, api-client push methods. (HARD on alerts.)
6. **Web audio + push client** — `lib/audio-cue.ts`, `lib/push.ts`. (HARD on push feature-detect.)
7. **Web alert wiring** — `hooks/useAttentionAlerts.ts`, `<Toaster/>` mount, `useStateStream`/`AuthLayout`
   integration, Tier-1/2 branch. (HARD.)
8. **PWA shell** — `vite-plugin-pwa` `injectManifest`, `sw.ts` (push + notificationclick + precache),
   manifest, iOS meta, icons/sound placeholders, SW registration. (MEDIUM; SW source-reviewed.)
9. **Enable-alerts UX + sign-out unsubscribe** — `components/EnableAlerts.tsx`, `store/auth.ts` wiring.
   (MEDIUM.)

Then: opus whole-branch review → `/multi-review --codex` (fix all but nitpicks) → SAFE acceptance → merge +
an M9 carryover.
