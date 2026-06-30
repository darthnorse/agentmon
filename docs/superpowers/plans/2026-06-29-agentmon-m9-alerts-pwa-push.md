# M9 — Attention alerts + PWA + Web-Push Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.
> This plan is executed via an **ultracode Workflow** (parallel implement + pipelined adversarial verify).

**Goal:** Alert the user when a *different* session goes `blocked` — in-app (toast + sound + vibrate) while
foreground, and via Web-Push when backgrounded/asleep — and make AgentMon an installable PWA.

**Architecture:** Three-tier notification model with **server-side presence de-dup**. Tier 1 (foreground/
visible) and Tier 2 (alive/hidden) are pure-client off the existing M8 SSE store; Tier 3 (page dead) is a
hub `PushDispatcher` subscribed to the existing `state.Broadcaster`, gated to principals with no live SSE
connection. PWA shell via `vite-plugin-pwa` (`injectManifest`) so we own the service-worker source.

**Tech Stack:** Go (`modernc.org/sqlite`, `net/http`, `github.com/SherClockHolmes/webpush-go`), TypeScript/
React (Vite, zustand, TanStack Router/Query, `vite-plugin-pwa`, `sonner`), vitest.

## Global Constraints

- **Additive only.** No change to existing endpoints, the SSE/terminal wire contracts, the agent, the
  poller's state logic, or the M8 store's pure reducer. New code only.
- **`CGO_ENABLED=0`** must keep building (verify the `webpush-go` dependency is pure-Go). `-race` clean.
- **Store stays a pure reducer** (M8 invariant): alert *detection* is a pure helper called from the wiring
  layer, never a side-effect inside `applyDelta`.
- **One `EventSource`** (M8 invariant): reuse the single `StateStream`; do not open a second SSE.
- **`blocked` only** raises alerts/pushes. `done` is inbox-only.
- **No prod deploy / no push to remote** during this milestone (owner-only). Local commits + local merge.
- **Safety for any live run:** loopback scratch hub + fresh/copy DB + throwaway tmux socket only.
- Match existing house patterns exactly: hub stores mirror `db/seen.go`; handlers mirror `api/seen.go`;
  routes mirror `router.go`'s `RequireAuth` wrapping; web modules mirror M8's `lib/state.ts`/`store/
  session-state.ts` purity + DI style.
- Commit message footer on every commit: `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.

## File Structure

**Hub (Go):**
- `hubd/internal/db/migrations/0004_push.sql` — new tables `push_subscriptions`, `push_vapid`.
- `hubd/internal/db/push.go` — subscription store + `LoadOrCreateVAPID`. (mirror `db/seen.go`)
- `hubd/internal/db/push_test.go`
- `hubd/internal/api/push.go` — `VapidHandler`, `SubscribeHandler`, `UnsubscribeHandler`; `PushStore` iface
  on `Deps`. (mirror `api/seen.go`)
- `hubd/internal/api/push_test.go`
- `hubd/internal/state/presence.go` — `Presence` connection counter.
- `hubd/internal/state/presence_test.go`
- `hubd/internal/state/push_dispatcher.go` — broadcaster subscriber → Web-Push sender (DI'd sender).
- `hubd/internal/state/push_dispatcher_test.go`
- `hubd/internal/api/events.go` (modify) — `Presence` inc/dec around the stream loop.
- `hubd/internal/api/router.go` (modify) — register the three push routes.
- `hubd/internal/api/servers.go` (modify) — add `Push PushStore` + `Presence *state.Presence` to `Deps`.
- `hubd/cmd/agentmon-hubd/main.go` (modify) — VAPID load, presence, dispatcher lifecycle, config.
- `hubd/internal/config/config.go` (modify) — `vapid_subject`.

**Web (TS):**
- `web/src/lib/alerts.ts` — pure `isAttentionTransition`. + `alerts.test.ts`
- `web/src/lib/audio-cue.ts` — `AudioCue`. + `audio-cue.test.ts`
- `web/src/lib/push.ts` — feature-detected subscribe/unsubscribe. + `push.test.ts`
- `web/src/lib/api-client.ts` (modify) — `getVapidPublicKey`, `subscribePush`, `unsubscribePush`.
- `web/src/lib/contracts.ts` (modify) — push types.
- `web/src/hooks/useAttentionAlerts.ts` — Tier 1/2 driver. + `useAttentionAlerts.test.tsx`
- `web/src/hooks/useStateStream.ts` (modify) — pass deltas to an `onAttention` callback.
- `web/src/components/AuthLayout.tsx` (modify) — mount `useAttentionAlerts`.
- `web/src/components/EnableAlerts.tsx` — opt-in control. + `EnableAlerts.test.tsx`
- `web/src/components/SessionList.tsx` / `routes/index.tsx` (modify) — mount `<Toaster/>` + `EnableAlerts`.
- `web/src/main.tsx` (modify) — `<Toaster/>` at root; SW registration.
- `web/src/store/auth.ts` (modify) — best-effort `unsubscribePush` on sign-out.
- `web/src/sw.ts` — service worker (push + notificationclick + precache).
- `web/vite.config.ts` (modify) — `vite-plugin-pwa` `injectManifest`.
- `web/index.html` (modify) — iOS meta tags.
- `web/public/` (new) — `icon-192.png`, `icon-512.png`, `icon-maskable-512.png`, `alert.mp3` (placeholders).

## Parallelization map (for the workflow)

- **Phase A (hub foundation, parallel):** T1 (db), T3 (presence). T1 ⟶ T2 (endpoints) ⟶ T4 (dispatcher,
  also needs T3). 
- **Phase B (web pure core, parallel with Phase A):** T5 (alerts+contracts+api-client), T6 (audio+push).
- **Phase C (web wiring, after T5/T6):** T7 (alert wiring), T9 (enable-alerts + sign-out).
- **Phase D (PWA shell, after T6):** T8 (vite-plugin-pwa + sw.ts + manifest + registration).
- Each implement task is **pipelined into an adversarial verify**; HARD tier on T1/T3/T4/T5/T6/T7, MEDIUM
  on T2/T8/T9.

---

### Task 1: Hub push store + VAPID persistence (HARD)

**Files:**
- Create: `hubd/internal/db/migrations/0004_push.sql`, `hubd/internal/db/push.go`,
  `hubd/internal/db/push_test.go`
- Read first: `hubd/internal/db/seen.go` (receiver, handle name, upsert + scan style),
  `hubd/internal/db/migrations/0001_init.sql` (DDL style), `hubd/internal/db/migrations.go`.

**Interfaces:**
- Produces:
  - `type PushSubscription struct { PrincipalID, Endpoint, P256dh, Auth, UserAgent, CreatedAt string }`
  - `(d *DB) UpsertSubscription(ctx, PushSubscription) error`
  - `(d *DB) ListSubscriptionsForPrincipal(ctx, principalID string) ([]PushSubscription, error)`
  - `(d *DB) DeleteSubscription(ctx, endpoint string) error`
  - `(d *DB) PrincipalIDsWithSubscriptions(ctx) ([]string, error)`
  - `type VAPIDKeys struct { Public, Private string }`
  - `(d *DB) LoadOrCreateVAPID(ctx, gen func() (priv, pub string, err error), now string) (VAPIDKeys, error)`

**Migration `0004_push.sql`:**
```sql
CREATE TABLE push_subscriptions (
  principal_id TEXT NOT NULL,
  endpoint     TEXT NOT NULL,
  p256dh       TEXT NOT NULL,
  auth         TEXT NOT NULL,
  user_agent   TEXT,
  created_at   TEXT NOT NULL,
  PRIMARY KEY (endpoint)
);
CREATE INDEX idx_push_principal ON push_subscriptions(principal_id);

CREATE TABLE push_vapid (
  id          INTEGER PRIMARY KEY CHECK (id = 1),
  public_key  TEXT NOT NULL,
  private_key TEXT NOT NULL,
  created_at  TEXT NOT NULL
);
```

- [ ] **Step 1: Write failing tests** (`push_test.go`) — open a temp DB (copy the helper used in
  `seen_test.go`), then assert:
  - `UpsertSubscription` then `ListSubscriptionsForPrincipal` returns the row; a second upsert on the same
    `endpoint` with a different `p256dh` updates (not duplicates) — list length stays 1.
  - `DeleteSubscription(endpoint)` removes it; deleting an absent endpoint is a no-op (no error).
  - `PrincipalIDsWithSubscriptions` returns distinct principals.
  - `LoadOrCreateVAPID` with a stub `gen` returning `("priv","pub",nil)` creates the row and returns the
    keys; a **second** call returns the **same** persisted keys and does **not** call `gen` again (use a
    counter in the stub; assert it stayed 1).
- [ ] **Step 2: Run — verify fail** (`go test ./hubd/internal/db/ -run Push -v`) → FAIL (undefined).
- [ ] **Step 3: Implement `push.go`** — mirror `seen.go`'s receiver/handle. `UpsertSubscription` uses
  `INSERT ... ON CONFLICT(endpoint) DO UPDATE SET principal_id=excluded..., p256dh=excluded..., auth=
  excluded..., user_agent=excluded...`. `LoadOrCreateVAPID` does `SELECT ... WHERE id=1`; on `sql.ErrNoRows`
  call `gen()`, `INSERT ... (1, pub, priv, now)`, return; on other error propagate. `List` uses
  `COALESCE(user_agent,'')`.
- [ ] **Step 4: Run — verify pass.**
- [ ] **Step 5: Verify migration applies** — `go test ./hubd/internal/db/ -run Migrat -v` (the existing
  migration test should pick up `0004` automatically; if there's a count assertion, bump it).
- [ ] **Step 6: Commit** — `feat(hub): push subscription store + VAPID persistence (M9 T1)`.

---

### Task 2: Hub push HTTP endpoints (MEDIUM)

**Files:**
- Create: `hubd/internal/api/push.go`, `hubd/internal/api/push_test.go`
- Modify: `hubd/internal/api/servers.go` (add to `Deps`), `hubd/internal/api/router.go` (routes)
- Read first: `hubd/internal/api/seen.go` (full handler pattern), `hubd/internal/api/servers.go`
  (`Deps`, `SeenStore` iface, `authorizeOr403`), `hubd/internal/api/router.go` (`RequireAuth` wrapping).

**Interfaces:**
- Consumes: `db.PushSubscription`, the `db` store methods from T1; `VAPIDKeys` (the public key is injected
  into `Deps`, see below).
- Produces:
  - `type PushStore interface { UpsertSubscription(...); DeleteSubscription(...); ListSubscriptionsForPrincipal(...); PrincipalIDsWithSubscriptions(...) }` (whatever the handlers + dispatcher need — keep it the union used by api + dispatcher, or split; a single `PushStore` is fine).
  - `Deps.Push PushStore`, `Deps.VAPIDPublic string`.
  - `(d Deps) VapidHandler() http.HandlerFunc` → `200 {"publicKey": d.VAPIDPublic}`.
  - `(d Deps) SubscribeHandler() http.HandlerFunc` → decode `{endpoint, keys:{p256dh,auth}}`, validate
    non-empty, `authorizeOr403(authz.ServerView, "server:*")`, `UpsertSubscription`, `204`.
  - `(d Deps) UnsubscribeHandler() http.HandlerFunc` → decode `{endpoint}`, authorize, `DeleteSubscription`,
    `204`.

- [ ] **Step 1: Write failing tests** (`push_test.go`, httptest, mirror `seen_test.go` harness with a fake
  `PushStore`):
  - `GET /push/vapid` (authed) → 200, body `{"publicKey":"<the injected key>"}`.
  - `POST /push/subscribe` with valid body + CSRF header → 204 and the fake store recorded the upsert with
    the principal from the auth context; **without** CSRF → 403 (the `RequireAuth`/csrf path — assert via
    the same harness `seen_test.go` uses for its CSRF case).
  - `POST /push/subscribe` with empty `endpoint` → 400.
  - `POST /push/unsubscribe` with `{endpoint}` + CSRF → 204 and store recorded the delete.
- [ ] **Step 2: Run — verify fail.**
- [ ] **Step 3: Implement** — `push.go` handlers (decode with `http.MaxBytesReader` like `seen.go`),
  `PushStore` interface + `Push`/`VAPIDPublic` on `Deps` (servers.go), routes in `router.go`:
  ```go
  mux.Handle("GET /api/v1/push/vapid", rd.Auth.RequireAuth(rd.API.VapidHandler()))
  mux.Handle("POST /api/v1/push/subscribe", rd.Auth.RequireAuth(rd.API.SubscribeHandler()))
  mux.Handle("POST /api/v1/push/unsubscribe", rd.Auth.RequireAuth(rd.API.UnsubscribeHandler()))
  ```
- [ ] **Step 4: Run — verify pass.**
- [ ] **Step 5: Commit** — `feat(hub): push subscribe/unsubscribe/vapid endpoints (M9 T2)`.

---

### Task 3: Hub SSE presence counter (HARD)

**Files:**
- Create: `hubd/internal/state/presence.go`, `hubd/internal/state/presence_test.go`
- Modify: `hubd/internal/api/events.go` (inc on connect / dec on disconnect)

**Interfaces:**
- Produces:
  - `type Presence struct{ ... }` with `NewPresence() *Presence`, `(p *Presence) Add(id string)`,
    `(p *Presence) Remove(id string)`, `(p *Presence) Online(id string) bool` (true iff count > 0).
- Consumes (events.go): `Deps.Presence *state.Presence` (nil-safe — guard so existing tests without it pass).

- [ ] **Step 1: Write failing tests** (`presence_test.go`):
  - `Add("u")` → `Online("u")==true`; `Add("u")` again then one `Remove("u")` → still online; second
    `Remove("u")` → offline. `Online("x")` for unknown → false. `Remove` below zero never goes negative /
    never panics (delete the key at 0).
  - Concurrency: 100 goroutines `Add` then 100 `Remove` on the same id → ends offline; run under `-race`.
- [ ] **Step 2: Run — verify fail.**
- [ ] **Step 3: Implement `presence.go`** — `sync.Mutex` + `map[string]int`; `Add` increments; `Remove`
  decrements and `delete`s at 0; `Online` returns `count>0`.
- [ ] **Step 4: Run — verify pass** (`go test ./hubd/internal/state/ -run Presence -race -v`).
- [ ] **Step 5: Wire into `events.go`** — after a successful principal resolve, if `d.Presence != nil`:
  `d.Presence.Add(p.ID); defer d.Presence.Remove(p.ID)` placed so the defer fires on stream exit
  (context-done or error). Keep it nil-guarded so existing `events_test.go` (no Presence) is unaffected.
- [ ] **Step 6: Run the events tests** (`go test ./hubd/internal/api/ -run Events -v`) → still green.
- [ ] **Step 7: Commit** — `feat(hub): SSE presence counter for push de-dup (M9 T3)`.

---

### Task 4: Hub push dispatcher (HARD)

**Files:**
- Create: `hubd/internal/state/push_dispatcher.go`, `hubd/internal/state/push_dispatcher_test.go`
- Modify: `hubd/cmd/agentmon-hubd/main.go` (lifecycle + VAPID load + config), `hubd/internal/config/config.go`
  (`vapid_subject`)
- Read first: `hubd/internal/state/broadcaster.go` (`Subscribe`/`Change`), `hubd/internal/api/events.go`
  (how a subscriber drains `ch`), `hubd/cmd/agentmon-hubd/main.go` (poller goroutine + graceful shutdown
  pattern), `hubd/internal/state/poller.go` finalize→Publish.

**Dependency:** add `github.com/SherClockHolmes/webpush-go` (`go get`); **verify** `CGO_ENABLED=0 go build
./...` still works (it is pure-Go).

**Interfaces:**
- Consumes: `*Broadcaster`, `Presence`, the push store (`ListSubscriptionsForPrincipal`,
  `PrincipalIDsWithSubscriptions`, `DeleteSubscription`), VAPID keys + subject.
- Produces:
  - `type PushSender func(ctx, sub db.PushSubscription, payload []byte) (status int, err error)` — DI seam;
    production impl wraps `webpush.SendNotification`.
  - `type DispatcherDeps struct { Bcast *Broadcaster; Presence *Presence; Store PushDispatchStore; Send PushSender; NowRFC3339 func() string }`
  - `type PushDispatchStore interface { PrincipalIDsWithSubscriptions(ctx)([]string,error); ListSubscriptionsForPrincipal(ctx,string)([]db.PushSubscription,error); DeleteSubscription(ctx,string) error }`
  - `func RunPushDispatcher(ctx context.Context, d DispatcherDeps)` — subscribes, loops on `ch` until
    `ctx.Done()`, calls `dispatch(c)` for each `Change` where `c.Global == shared.Blocked`.
  - the production `PushSender` (e.g. `NewWebPushSender(keys VAPIDKeys, subject string) PushSender`).

**Dispatch logic** (the behavior the tests pin):
```go
// NOTE: the state constant is shared.StateBlocked (== State("blocked")), confirmed in shared/session.go.
func dispatch(ctx, d, c Change) {
    if c.Global != shared.StateBlocked { return }
    ids, _ := d.Store.PrincipalIDsWithSubscriptions(ctx)
    payload := mustJSON(pushMsg{Type:"blocked", Server:c.ServerID, Target:c.Target, Session:c.Session, Ts:d.NowRFC3339()})
    for _, id := range ids {
        if d.Presence.Online(id) { continue }            // server-side de-dup
        subs, _ := d.Store.ListSubscriptionsForPrincipal(ctx, id)
        for _, s := range subs {
            status, err := d.Send(ctx, s, payload)
            if err == nil && (status == 404 || status == 410) {
                _ = d.Store.DeleteSubscription(ctx, s.Endpoint)   // prune expired
            }
        }
    }
}
```
Run `dispatch` either inline or in a bounded goroutine; it must **never block** the broadcaster drain (drain
`ch` promptly — if doing network sends inline, that's acceptable at single-user scale, but prefer launching
the per-Change `dispatch` in its own goroutine with a `sync.WaitGroup` honoring `ctx`). Keep it simple:
drain in the loop, `go dispatch(...)` per blocked Change.

- [ ] **Step 1: Write failing tests** (`push_dispatcher_test.go`) with a fake `PushSender` (records calls,
  returns programmable status) + fake store + a real `Broadcaster` + a `Presence`:
  - Publish a `blocked` Change for principal with 2 subscriptions, `Online==false` → sender called twice
    with a payload whose JSON decodes to `{type:"blocked",server,target,session}` matching the Change.
  - Same, but `Presence.Add(principal)` first (online) → sender **not** called (de-dup).
  - Publish a `working`/`done` Change → sender not called (blocked-only).
  - Sender returns `410` → `DeleteSubscription(endpoint)` called for that endpoint.
  - `ctx` cancel stops the loop (no goroutine leak — the test returns).
  Use a sync mechanism (channel/WaitGroup) to await the async dispatch before asserting.
- [ ] **Step 2: Run — verify fail.**
- [ ] **Step 3: Implement `push_dispatcher.go`** — `RunPushDispatcher` subscribes via `Bcast.Subscribe()`,
  `defer cancel()`, `for { select { case <-ctx.Done(): return; case c, ok := <-ch: if !ok {return}; go
  dispatch(ctx,d,c) } }`. Guard the blocked check with `shared.StateBlocked`. `pushMsg` struct with json tags. Production `NewWebPushSender` wraps
  `webpush.SendNotification(payload, &webpush.Subscription{Endpoint:s.Endpoint, Keys:webpush.Keys{P256dh:
  s.P256dh, Auth:s.Auth}}, &webpush.Options{Subscriber:subject, VAPIDPublicKey:keys.Public,
  VAPIDPrivateKey:keys.Private, TTL:60})`, returns `resp.StatusCode`. Close `resp.Body`.
- [ ] **Step 4: Run — verify pass** (`-race`).
- [ ] **Step 5: Wire `main.go`** — after DB open: `vapid, _ := database.LoadOrCreateVAPID(ctx,
  webpush.GenerateVAPIDKeys, state.HubTS(time.Now()))`; build `Presence`; pass `Push`/`VAPIDPublic`/
  `Presence` into `api.Deps`; start `go RunPushDispatcher(ctx, DispatcherDeps{...})` in the same lifecycle
  block as the poller; ensure ctx-cancel on shutdown. Add `vapid_subject` to config (default
  `cfg.ExternalOrigin`, fall back to `"mailto:admin@localhost"` if empty — webpush requires a non-empty
  subscriber).
- [ ] **Step 6: Build** — `CGO_ENABLED=0 go build ./...` OK; full `go test ./...` green.
- [ ] **Step 7: Commit** — `feat(hub): web-push dispatcher with presence de-dup + pruning (M9 T4)`.

---

### Task 5: Web pure alert core + contracts + api-client (HARD)

**Files:**
- Create: `web/src/lib/alerts.ts`, `web/src/lib/alerts.test.ts`
- Modify: `web/src/lib/contracts.ts`, `web/src/lib/api-client.ts`, `web/src/lib/api-client.test.ts`
- Read first: `web/src/lib/state.ts` (`stateKey`, `normalizeState`, `STATE_META`), `web/src/store/session-state.ts`
  (`SessionState`, `stateKey` usage), `web/src/lib/api-client.ts` (`request<T>`, CSRF on mutating).

**Interfaces:**
- Produces:
  - `lib/alerts.ts`: `export function isAttentionTransition(prev: SessionState | undefined, next: SessionState, focusedKey: string | null, key: string): boolean` — returns true iff `next === "blocked"` AND `prev !== "blocked"` AND `key !== focusedKey`. (`prev`/`next` are the normalized states; pass the key the caller already computed via `stateKey`.)
  - `contracts.ts`: `export interface PushSubscriptionJSON { endpoint: string; keys: { p256dh: string; auth: string } }`; `export interface VapidKeyResponse { publicKey: string }`.
  - `api-client.ts`: `getVapidPublicKey(): Promise<VapidKeyResponse>` (GET), `subscribePush(sub: PushSubscriptionJSON): Promise<void>` (POST, auto-CSRF), `unsubscribePush(endpoint: string): Promise<void>` (POST).

- [ ] **Step 1: Write failing tests** (`alerts.test.ts`): true when prev=`working`/`done`/`undefined`/
  `idle`, next=`blocked`, key≠focused; false when next≠`blocked`; false when prev already `blocked`; false
  when `key===focusedKey` even if next=`blocked`. (`api-client.test.ts`): `subscribePush` issues a POST to
  `/api/v1/push/subscribe` with the body and the `X-CSRF-Token` header (mirror the existing `postSeen`
  test); `getVapidPublicKey` GETs `/api/v1/push/vapid`.
- [ ] **Step 2: Run — verify fail** (`npx vitest run src/lib/alerts.test.ts src/lib/api-client.test.ts`).
- [ ] **Step 3: Implement** the pure function + contracts + api-client methods (use the existing `request`
  helper; CSRF is automatic on POST per `api-client.ts`).
- [ ] **Step 4: Run — verify pass.**
- [ ] **Step 5: Commit** — `feat(web): pure alert-transition helper + push contracts/api-client (M9 T5)`.

---

### Task 6: Web audio cue + push client (HARD)

**Files:**
- Create: `web/src/lib/audio-cue.ts`, `web/src/lib/audio-cue.test.ts`, `web/src/lib/push.ts`,
  `web/src/lib/push.test.ts`

**Interfaces:**
- Produces:
  - `audio-cue.ts`: `export const audioCue = { prime(): void; play(): void }` — `prime` lazily constructs/
    resumes an `AudioContext` (or preloads an `<audio>`); `play` triggers the cue; both no-throw if blocked
    or unsupported.
  - `push.ts`:
    - `export function pushSupported(): boolean` — `'serviceWorker' in navigator && 'PushManager' in window && 'Notification' in window`.
    - `export async function enablePush(reg: ServiceWorkerRegistration): Promise<boolean>` — request
      `Notification.requestPermission()`; if granted, fetch VAPID key, `reg.pushManager.subscribe({userVisibleOnly:true, applicationServerKey: urlBase64ToUint8Array(key)})`, POST it via `api.subscribePush`; return success.
    - `export async function disablePush(reg: ServiceWorkerRegistration): Promise<void>` — get the existing
      subscription, `api.unsubscribePush(endpoint)`, `sub.unsubscribe()`.
    - `urlBase64ToUint8Array(base64: string): Uint8Array` (the standard VAPID key conversion).

- [ ] **Step 1: Write failing tests**:
  - `push.test.ts`: `pushSupported()` false when APIs absent (delete from a mocked global), true when
    present; `enablePush` with a mock `reg` + mocked `Notification.requestPermission→'granted'` + mocked
    `api.getVapidPublicKey` calls `reg.pushManager.subscribe` and `api.subscribePush` with the sub JSON;
    returns false (no throw) when permission denied; `urlBase64ToUint8Array` round-trips a known vector.
  - `audio-cue.test.ts`: `prime()`/`play()` do not throw when `AudioContext` is undefined/mocked.
- [ ] **Step 2: Run — verify fail.**
- [ ] **Step 3: Implement** both modules; guard every Web API in try/catch or feature-checks (jsdom lacks
  `AudioContext`, `PushManager`).
- [ ] **Step 4: Run — verify pass.**
- [ ] **Step 5: Commit** — `feat(web): audio cue + feature-detected push client (M9 T6)`.

---

### Task 7: Web alert wiring — Tier 1/2 driver (HARD)

**Files:**
- Create: `web/src/hooks/useAttentionAlerts.ts`, `web/src/hooks/useAttentionAlerts.test.tsx`
- Modify: `web/src/hooks/useStateStream.ts` (emit deltas to an attention callback), `web/src/components/AuthLayout.tsx`
  (mount the hook + pass the callback)
- Read first: `web/src/hooks/useStateStream.ts`, `web/src/store/session-state.ts` (`useSessionState.getState()`,
  `applyDelta`, `stateKey`), `web/src/components/AuthLayout.tsx`, `sonner` docs (`toast`).

**Design:** keep the store reducer pure. In `useStateStream`, before calling `applyDelta(frame)`, capture
`const prev = useSessionState.getState().live.get(key)`; after applying, if an `onAttention` callback is
provided and `isAttentionTransition(prev?.state, normalizeState(frame.state), getState().focusedKey, key)`,
invoke `onAttention(frame)`. `useAttentionAlerts` provides that callback: it shows a `sonner` toast (title
`🔴 {session} needs input`, description `{server}`, action "Open" → `navigate({to:'/'})`), calls
`audioCue.play()` and `navigator.vibrate?.([120,60,120])`; if `document.visibilityState==='hidden'` and
`Notification.permission==='granted'`, also `new Notification('🔴 '+session+' needs input', {body:server, tag:key})` (Tier 2) instead of relying on the unseen toast.

**Interfaces:**
- Produces: `useAttentionAlerts(): (frame: StateEventFrame) => void` (the onAttention handler); mounted in
  `AuthLayout`, wired into `useStateStream({ onAttention })`.
- Consumes: `isAttentionTransition` (T5), `audioCue` (T6), `sonner` `toast`, router `useNavigate`.

- [ ] **Step 1: Write failing test** (`useAttentionAlerts.test.tsx`): render a harness that mounts the hook,
  spy on `toast`, `audioCue.play`, `navigator.vibrate`; push a `blocked` delta for a non-focused key → all
  three fire once; push a `working` delta → none fire; set `focusedKey` to the delta's key → none fire;
  with `document.visibilityState='hidden'` + permission granted, assert a `Notification` is constructed
  (mock the global). (Mock `sonner`, `navigator.vibrate`, `Notification`.)
- [ ] **Step 2: Run — verify fail.**
- [ ] **Step 3: Implement** `useAttentionAlerts` + extend `useStateStream` with the optional `onAttention`
  param + prev-capture, and mount in `AuthLayout`. Do **not** mutate the store reducer.
- [ ] **Step 4: Run — verify pass**; run the existing `useStateStream`/`session-state` tests → still green.
- [ ] **Step 5: Commit** — `feat(web): in-app attention alerts (toast/sound/vibrate, tab-aware) (M9 T7)`.

---

### Task 8: PWA shell — manifest + service worker (MEDIUM)

**Files:**
- Create: `web/src/sw.ts`, `web/public/icon-192.png`, `web/public/icon-512.png`,
  `web/public/icon-maskable-512.png`, `web/public/alert.mp3` (placeholders — see note)
- Modify: `web/vite.config.ts` (`vite-plugin-pwa`), `web/index.html` (iOS meta), `web/src/main.tsx` (register),
  `web/package.json` (`vite-plugin-pwa` + `sonner` deps)
- Read first: `web/vite.config.ts`, `web/index.html`, `web/src/main.tsx`.

**Note on assets:** generate simple placeholder PNG icons (a solid color block / the app initial) and a
short ~0.3s `alert.mp3` tone so the build is complete and functional. Flag in the carryover that a designed
icon + sound is an owner asset decision. Generate the PNGs programmatically (e.g. a tiny script or a
committed minimal PNG); do not block on design.

- [ ] **Step 1: Add deps** — `npm i vite-plugin-pwa sonner` (and `-D` types as needed); confirm
  `package.json` updated.
- [ ] **Step 2: Configure `vite.config.ts`** — add `VitePWA({ strategies:'injectManifest', srcDir:'src',
  filename:'sw.ts', registerType:'autoUpdate', manifest:{ name:'AgentMon', short_name:'AgentMon',
  display:'standalone', start_url:'/', scope:'/', theme_color:'#0b0b0b', background_color:'#0b0b0b',
  icons:[{src:'/icon-192.png',sizes:'192x192',type:'image/png'},{src:'/icon-512.png',sizes:'512x512',type:'image/png'},{src:'/icon-maskable-512.png',sizes:'512x512',type:'image/png',purpose:'maskable'}] }, injectManifest:{ /* defaults */ } })`.
- [ ] **Step 3: Write `sw.ts`** — `/// <reference lib="webworker" />`; Workbox precache
  (`precacheAndRoute(self.__WB_MANIFEST)`); `self.addEventListener('push', e => { const d = e.data?.json();
  e.waitUntil(self.registration.showNotification('🔴 '+d.session+' needs input', {body:d.server, tag:
  d.server+''+d.target+''+d.session, data:d})) })`; `self.addEventListener('notificationclick',
  e => { e.notification.close(); e.waitUntil(clients.matchAll({type:'window'}).then(cs => { for (const c of
  cs){ if('focus' in c) return c.focus(); } return clients.openWindow('/'); })) })`.
- [ ] **Step 4: `index.html`** — add `<meta name="apple-mobile-web-app-capable" content="yes">`,
  `<meta name="apple-mobile-web-app-status-bar-style" content="black-translucent">`,
  `<link rel="apple-touch-icon" href="/icon-192.png">`, `<meta name="theme-color" content="#0b0b0b">`.
- [ ] **Step 5: Register in `main.tsx`** — the plugin's virtual `registerSW` (`import { registerSW } from
  'virtual:pwa-register'; registerSW({ immediate:true })`) guarded so dev/test don't error; add
  `vite-plugin-pwa/client` to tsconfig types if needed.
- [ ] **Step 6: Build** — `tsc --noEmit` (add SW lib types / `virtual:pwa-register` module decl so it
  compiles) and `vite build`; confirm `dist/manifest.webmanifest` + `dist/sw.js` emitted.
- [ ] **Step 7: Commit** — `feat(web): installable PWA shell + service worker (push + precache) (M9 T8)`.

---

### Task 9: Enable-alerts opt-in + sign-out unsubscribe (MEDIUM)

**Files:**
- Create: `web/src/components/EnableAlerts.tsx`, `web/src/components/EnableAlerts.test.tsx`
- Modify: `web/src/routes/index.tsx` (or `SessionList`/header) to render `<Toaster/>` + `<EnableAlerts/>`,
  `web/src/store/auth.ts` (best-effort `disablePush` on sign-out), `web/src/main.tsx` (mount `<Toaster/>`
  at root if not already)
- Read first: `web/src/store/auth.ts` (`signOut`/`clear`), `web/src/routes/index.tsx`.

**Interfaces:**
- Consumes: `pushSupported`, `enablePush`, `disablePush` (T6), `audioCue.prime` (T6), `sonner` `<Toaster/>`.
- Produces: `<EnableAlerts/>` — renders nothing when `!pushSupported()`; otherwise a button "Enable alerts"
  that (on click, a user gesture) calls `audioCue.prime()` + `enablePush(await navigator.serviceWorker.ready)`;
  reflects granted/denied state.

- [ ] **Step 1: Write failing test** (`EnableAlerts.test.tsx`): renders null when `pushSupported()` is
  mocked false; renders the button when true; clicking calls `enablePush` + `audioCue.prime`. (`auth.test.ts`):
  `signOut` calls `disablePush` best-effort (mock it; assert called, and that a throw doesn't break sign-out).
- [ ] **Step 2: Run — verify fail.**
- [ ] **Step 3: Implement** `EnableAlerts`, mount `<Toaster/>` (root) + `<EnableAlerts/>` (inbox header),
  add the best-effort `disablePush` to `auth.ts` `signOut` (wrapped so a failure never blocks logout).
- [ ] **Step 4: Run — verify pass**; full `npx vitest run` green; `tsc --noEmit` clean.
- [ ] **Step 5: Commit** — `feat(web): enable-alerts opt-in + sign-out unsubscribe (M9 T9)`.

---

## Self-Review (plan vs spec)

- **Spec coverage:** Tier 1 → T5+T7; Tier 2 → T7; Tier 3 → T3+T4+T8(sw push handler); PWA shell → T8;
  subscribe/unsubscribe endpoints → T2; store+VAPID → T1; presence de-dup → T3+T4; enable-alerts UX +
  sign-out → T9; blocked-only → T4(dispatch guard)+T5(`isAttentionTransition`); contracts → T2+T5. All
  spec §3/§4/§5/§6 items mapped.
- **Placeholder scan:** asset placeholders (icons/sound) are explicitly flagged as intentional functional
  placeholders, not TBDs; every code step has concrete code or an exact existing-pattern reference.
- **Type consistency:** `isAttentionTransition` signature is consistent T5→T7; `PushStore`/`PushDispatchStore`
  methods match T1's store; `db.PushSubscription` fields match T1; `pushMsg` JSON shape matches the SW
  decode in T8 and the dispatcher in T4; `stateKey` `` delimiter reused in the SW `tag`.
- **Gaps:** none blocking. The on-device PWA/push/iOS verification is acceptance-flagged (no headless
  browser on this host), consistent with M8.

## Execution

Executed via an **ultracode Workflow**: implement agents per task, each pipelined into an adversarial verify
(HARD tier on T1/T3/T4/T5/T6/T7). Phases A/B parallel, then C/D. After the workflow: opus whole-branch
review → `/multi-review --codex` (fix all but nitpicks) → SAFE acceptance (vitest + tsc + build + Go suite +
loopback probe) → local merge + `m9-carryover.md`.
