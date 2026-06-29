# M5 → next carry-over (web SPA: login + session list + xterm.js terminal)

M5 (the browser half of the Phase-1 terminal spine — the Vite + React + TS SPA that logs in,
lists servers/sessions, and opens live xterm.js terminals through the M4 relay) is complete,
reviewed (per-task + opus whole-branch + `/multi-review --codex`), **live-accepted on this host**,
and merged. This captures the deferred items and the reminders for the next milestone.

Branch: `phase-1-m5-web-spa`, off `main@25ff1a7`. Spec:
`docs/superpowers/specs/2026-06-28-agentmon-m5-web-spa-design.md`. Plan:
`docs/superpowers/plans/2026-06-28-agentmon-m5-web-spa.md`. Ledger: `.superpowers/sdd/progress.md`.

## Locked decisions (brainstorming)
- **Desktop = live tiled grid, grid-first.** Each open session is a tile holding its OWN live WS; the
  grid shows them all at once; **expand is in-state (⤢ → `focus`), not a route change**, and the
  non-focused tiles stay MOUNTED (`display:none`) so their sockets + scrollback survive; ⊟ collapses.
  (Esc is never bound to collapse — it must reach the terminal.) Client soft cap `GRID_TILE_CAP = 6`.
- **Mobile = one full-screen terminal** + the §6.2 key bar **minus `[Lock]`** + a session switcher
  (list ↔ `/t/$serverId/$paneId` route, back button). No grid on mobile.
- **No read-only lock in M5** (product owner). The hub always mints `rw`; true `ro` + the "tap to
  unlock" posture wait for real authz (design §6.3/§7.5/§11.8).
- **Input fidelity inherited verbatim from the Phase 0.5 spike** (`spike-0.5/static/index.html`): LF
  `0x0a` soft-newline / CR `0x0d` Enter; lone Esc `0x1b`; Tab/⇧Tab `ESC[Z`; DECCKM-aware arrows;
  sticky Ctrl; paste via `term.paste()` (xterm owns bracketed-paste) with a multiline/>200-char confirm.
- **Transparent WS, client side:** every inbound binary frame is `term.write` (the first scrollback
  snapshot is NOT special-cased); the only control frame sent is JSON `{type:"resize",cols,rows}`.

## What shipped (10 SDD tasks, TDD throughout; 53 web tests)
- **`lib/` (pure, unit-tested):** `keybar` (key→byte encodings), `ws-terminal` (`TerminalSocket`
  transport + bounded reconnect, DI'd WebSocket/location), `api-client` (typed `/api/v1`,
  same-origin creds, `X-CSRF-Token` only on non-empty mutations), `contracts`, `query-client`,
  `use-media-query`, `utils.cn`.
- **`store/`:** `auth` (session + csrf; `signIn/signOut/bootstrap`; pushes csrf to the api-client;
  resets panes + query cache on clear), `panes` (open tiles, focus, soft cap — grid-first).
- **`components/`:** `XTerm` (the only xterm.js touchpoint — imperative handle, fit/web-links/lazy-webgl
  with fallback, touch-scroll, visualViewport sizing), `useTerminalSession` (wires socket↔xterm↔sticky-
  Ctrl + reconnect/resize), `TerminalView`, `MobileKeyBar`, `SessionList`, `Sidebar`, `GridView`,
  `DesktopShell`, `LoginForm`, shadcn primitives (`ui/{button,input,label,card}`).
- **`routes/`:** `/login`, `/` (responsive shell: desktop grid / mobile list via `useMediaQuery`),
  `/t/$serverId/$paneId` (mobile terminal) — all under a pathless `auth` layout route (guard runs
  `bootstrap()`, redirects to `/login`); a QueryCache `onError` recovers a 401 to `/login`.
- **Tooling:** Tailwind + shadcn, Vitest + Testing Library, the dev Vite `/api` WS-proxy fix
  (`ws:true`), `@`→`src` alias, and a CI web-test step (`npm run test:run` before `npm run build`).

## Review path
SDD per-task (spec + quality) reviews on all 10 (1 IMPORTANT caught + fixed: the ws-terminal
CONNECTING double-open — the `onVisibility` guard was `!connected`, fixed to `ws===null`). Then an
**opus whole-branch review** ("Ready to merge with fixes"): 0 Critical; verified the security posture,
transparent-WS protocol, reconnect guard, grid liveness invariant, auth guard, and the hub WS
path-encoding contract end-to-end. Then the **`/multi-review --codex`** 4-lens gate (feature-dev:code-
reviewer + code-simplifier + deep-scan + Codex gpt-5.5): no Critical/High; deep-scan independently
confirmed no XSS (all session/cwd/command render as React text), CSRF correct (token only on non-empty
mutations, never on the WS/URL), reconnect guard correct, paneId double-encoding correct.

### Fixes applied pre-merge (each test-locked unless noted)
- **Whole-branch IMPORTANT — grid-first:** `openPane` auto-set `focusedId` on every open, so opening a
  2nd session expanded it and hid the 1st — you'd never "see 4/6 at once". Fixed: `openPane` appends a
  tile without focusing; expand is the explicit ⤢ (`focus`). (Tests updated.)
- **Whole-branch IMPORTANT — 401 recovery:** an expired session degraded to a blank screen with no way
  back. Fixed: a global QueryCache `onError` clears auth + redirects to `/login` on `ApiError` 401;
  `ShellRoute` renders servers loading/error+retry.
- **Whole-branch + multi-review:** clear panes + query cache on sign-out (stale tiles reopened sockets
  after re-login); `GridView` guards a stale `focusedId` (avoid an unrecoverable blank grid).
- **Multi-review (Codex, medium):** `ws-terminal.open()` guarded only `disposed`, not `ws!==null` — a
  caller could open a 2nd live socket; added the guard (+test). **(deep-scan):** `dispose()` now nulls
  `onopen` too; the webgl dynamic-import guards `termRef.current` before `loadAddon`. **(specialist):**
  `signOut()` now swallows the logout error so it always resolves and the call site always redirects
  (+test). **(simplifier):** auth DRY via `(set,get)` + `setSession`/`clear`; dropped an unused field.

## LIVE acceptance — DONE this session (2026-06-29, on this host)
SAFETY (memory [[dev-host-runs-hub-and-claude]]): the relay is LIVE and mints `rw`. Tested against the
`aigallery` agent's **`agentmon`-socket demo panes ONLY** (`demo-web=%0`, `demo-db=%1`) — NEVER the
default socket. Built the M5 hub (embedded SPA) to scratch, ran it on a loopback port against a COPY of
the live SQLite (real DB + container untouched), set `patrik`'s password in the COPY only.
No browser/headless-Chromium on this host, so the React UI itself was not headlessly driven; everything
the SPA depends on was validated instead:
- the hub serves the embedded built SPA (`GET /` → built index + `/assets/` chunk loads);
- the API contract: wrong-Origin login → **403**; correct-Origin login → **200** + HttpOnly cookie +
  `csrfToken`; `/me`, `/servers` (aigallery), `/servers/{id}/sessions` (demo-web `%0` + demo-db `%1`);
- the relay WS via a node `ws` probe mimicking the SPA's `ws-terminal` exactly (double-encoded
  `%250`/`%251` path, Cookie+Origin headers, `arraybuffer`, snapshot-first, resize JSON, binary I/O):
  `%0` 101 upgrade + **98,698-byte scrollback snapshot** relayed + rw input reached the pane; **`%1`
  full rw round-trip** — `echo` executed in the pane and output relayed back (capture confirmed).
Post-test: real DB untouched, container up, **Claude's default-socket session `0` still alive**, demo
panes intact, scratch + DB copy removed, repo tree clean.

**Not yet exercised live:** the browser/device UI itself — React render, xterm.js terminal, grid/expand,
mobile key bar — needs a real browser or device. The on-device iOS/Android §6.4 checklist remains the
separate manual Phase-1 acceptance.

## Deferred / carried (non-blocking; reviewers + I triaged as carry)
- **xterm bundle is a >500 KB chunk** — the webgl addon already code-splits to its own ~101 KB chunk;
  lazy-loading the xterm core is a Phase-5 build optimization (`build.rollupOptions.manualChunks`).
- **No server-side per-principal relay concurrency cap** (carried from M4) — now more relevant since the
  grid opens N live WS; the client soft cap (6) is the Phase-1 bound; add the 429 cap when hardening.
- **Per-server session 502 ("agent unavailable") inline retry** — `ShellRoute` renders a top-level
  loading/error+retry for the servers query; per-server session-query errors are not yet surfaced inline.
- **Mobile next/prev session switcher** within a loaded set (design §6.2) — M5 ships back-to-list; the
  in-terminal prev/next affordance is a Phase-4 polish.
- **Pure nitpicks left per "fix all but nitpicks":** the index.tsx loading/error/desktop/mobile ternary
  chain; a GridView wrapper `div`; `MobileKeyBar` template-literal vs `cn()`; the `getCsrfToken` symmetric
  export; an XTerm `FONT_SIZE` constant; the LoginForm 403 `external_origin` hint (intentional admin
  dev-setup aid; the design's threat model is LAN single-admin); `queryClient.clear()` fire-and-forget
  on sign-out (negligible single-user window); a handful of test-coverage gaps the code already satisfies.
- Small per-task minors (e.g. `@ts-ignore` comment wording, an unguarded `panes.focus(id)`) — Phase-5.

## Reminders for the next milestone
- **State / the agent inbox (Phase 3)** is the natural next milestone: `shared.Session` has no `State`
  field yet, so M5's list is intentionally flat (no blocked/done dots, no blocked-first sorting). When
  hooks land, the SessionList/Sidebar gain state dots + sorting, and the desktop "focus next blocked"
  and mobile attention alerts (§9, §14) become real. The SPA's data layer (TanStack Query over
  `/servers` + `/sessions`) and the grid/list are ready to render state once the field exists.
- **Read-only lock** (spec §6.3/§11.8): when real authz arrives, honor a browser-requested `ro` and
  authorize `terminal.read`; the mobile key bar's `[Lock]` button + the safe-default posture slot back in.
- Fold the carried minors above into Phase-4/5 where that work touches them.

## Verification at merge
Full web suite green (13 files / 53 tests, pristine); `tsc --noEmit` clean; `vite build` emits `dist/`;
CI runs `npm run test:run` before `npm run build`. Go side untouched (M5 changed no hub code). Live
two-pane relay (read snapshot + rw round-trip) accepted on this host against the `agentmon` demo panes.
