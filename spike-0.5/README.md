# AgentMon — Phase 0.5 mobile input-fidelity spike (THROWAWAY)

One make-or-break question: **can a browser terminal on my phone drive Claude
Code's interactive TUI at least as well as Termius does today?** This slice
exists only to answer that on a real iPhone (Safari) and a real Android (Chrome).
Pass → build Phase 1 clean and delete this. Fail → fix the input path first.

It is deliberately *not* the real architecture: one Go binary (hub+agent
collapsed), one hard-coded tmux pane, one static token, a single static HTML
page. No SQLite / RBAC / inbox / React.

## Run it

```bash
cd /root/agentmon/spike-0.5
./run.sh
```

`run.sh` will, idempotently:
1. generate a self-signed cert with `IP:192.168.1.51` in the SAN,
2. mint a stable token (`token.txt`),
3. create the dedicated `agentmon-spike` tmux session running its own `claude`
   in `./scratch` (never the pane this session runs in),
4. build and serve HTTPS on `192.168.1.51:8443`.

It prints **two** phone URLs:

```
http://192.168.1.51:8080/?token=<token>    # CORE input test — no cert needed
https://192.168.1.51:8443/?token=<token>   # FULL test (clipboard + PWA) — needs cert TRUSTED
```

### Why two URLs (the self-signed WebSocket trap — learned the hard way)
A self-signed cert that you only **click-through** ("proceed anyway") on the page
interstitial is **not** trusted for `wss://` WebSocket handshakes — browsers
(Chromium/WebKit) reject the WS even though the page itself loaded. Symptom: the
page renders but flips straight to "disconnected — reconnecting…", and the server
log shows `TLS handshake error … remote error: tls: unknown certificate` with no
matching "ws connected". So:

- **Core input fidelity** (the make-or-break bet: Esc, Ctrl-C, arrows, ⏎-newline,
  Tab, scrollback, sizing, reconnect, Lock) → use the **`http://…:8080`** URL.
  No cert, `ws://`, works immediately. Clipboard + PWA won't work here (plain HTTP
  is not a secure context) — but those aren't the bet (§20 blesses HTTP for this).
- **Clipboard Copy/Paste + Add-to-Home-Screen** → use the **`https://…:8443`** URL,
  but you must **actually trust the cert** (below), not just click through.

### Trusting the cert (only needed for the clipboard/PWA subset)
- **iOS:** serve `cert.pem` to the phone (e.g. open `https://…:8443/cert.pem`… or
  AirDrop it), install the profile (Settings → Profile Downloaded → Install), then
  **Settings → General → About → Certificate Trust Settings → enable full trust**
  for it. Then `wss://` works and `https://…:8443` is a secure context.
- **Android (Chrome):** Settings → Security → Encryption & credentials → Install a
  certificate → CA certificate → pick `cert.pem`.
- Easier alternative if you ever want it: a real cert via Tailscale/Caddy — then no
  trust dance at all.

### On the phone (general)
- iOS clipboard quirk: the first `Copy`/`Paste` tap may show a permission prompt.
- To test PWA install: open the **https** URL (trusted), **Add to Home Screen**.
- Your phone must reach the host. Note: during the first run the phone showed up on
  `192.168.80.2` (a different subnet from the host's `192.168.1.51`) but routing let
  the page through — just make sure it can reach `192.168.1.51:8080`.
- The `agentmon-spike` Claude starts in a fresh folder, so it may first ask to
  trust the directory — that's a fine first thing to drive (tap Enter).

## Go / No-Go checklist — **you** run this; only the on-device result decides

I cannot run the real test (no phone, no touch, no mobile Safari/Chrome). Do NOT
treat anything here as "passing" until you've ticked these on **both** an iPhone
(Safari) and an Android (Chrome), driving the real Claude session.

Input fidelity (the bet) — use the **`http://…:8080`** URL:
- [ ] `Esc` cancels a Claude prompt (clears the input box)
- [ ] `Ctrl` then `C` interrupts a running tool (Ctrl-C)
- [ ] `↑ ↓` move through Claude's prompt options; `← →` edit within the line
- [ ] `Tab` and `⇧Tab` work (completion / mode cycle)
- [ ] `⏎ nl` inserts a newline **without** submitting; plain `Enter` submits
- [ ] type a multi-line prompt with `⏎ nl` and submit it intact (no `\` trick)
- [ ] native keyboard paste (long-press → Paste) inserts a multi-line block intact
      (the `Paste`/`Copy` *buttons* need the clipboard API → test those over HTTPS)

Mobile terminal behaviour (§6.4):
- [ ] swipe scrolls the scrollback without selecting the page
- [ ] native page scroll does not fight terminal scroll
- [ ] the soft keyboard does not permanently cover the prompt
- [ ] rotate portrait ↔ landscape reflows cleanly
- [ ] phone sleep / app-switch reconnects and the Claude session survives
- [ ] `Lock` (🔓/🔒) toggles read-only; while locked, taps don't reach Claude

Clipboard + Add-to-home-screen — use the **`https://…:8443`** URL with the cert TRUSTED:
- [ ] `Copy` selection reaches the OS clipboard; `Paste` works (needs secure context)
- [ ] page loads as HTTPS with the cert trusted (not just click-through)
- [ ] Add-to-Home-Screen installs and launches standalone

**Overall verdict (your call): GO / NO-GO** — does this beat or match Termius for
driving Claude from your phone?

## What I already resolved server-side (so you don't re-discover it)

These were verified against this host's **tmux 3.5a** and **Claude Code v2.1.195**;
they're baked into the code:

- **Input path:** xterm.js `onData` bytes are forwarded verbatim via
  `send-keys -t <pane> -H <hex>` written to the control client's stdin. Verified
  byte-for-byte (`1b 5b 41 c3 a9 58` in → identical out), including a **lone ESC**
  as a single clean byte — `-H` injection bypasses tmux's ESC-timeout (§18.1).
- **Soft newline:** **LF (`0x0a`) inserts a newline without submitting; CR
  (`0x0d`) submits.** That's what the `⏎ nl` vs `Enter` buttons send.
- **Bracketed paste owned by exactly one layer:** the `Paste` button calls
  `term.paste()`, so xterm.js does the `ESC[200~…ESC[201~` framing; tmux never
  does (we never use `paste-buffer`). No double-wrap (§18.5/§20).
- **Buffer mode:** Claude v2.1.195 runs on the **normal buffer** (`alternate_on=0`),
  not the alt-screen — so swipe-scroll over real scrollback is meaningful (§6.3).
- **Sizing (§11.7):** under `window-size latest`, the passive control client
  adopts the viewer's size via `refresh-client -C <cols>x<rows>`; verified the
  window resized to the phone's reported size (e.g. 90x35).
- **`%output` un-escaping:** `\` + 3 octal digits → byte; everything else literal
  (high/UTF-8 bytes pass raw). Unit-tested in `control_test.go`.
- End-to-end over HTTPS: static page + manifest served, bad token → 401, WS
  delivers a scrollback snapshot, and a WS input frame reaches Claude's prompt.

Optional server-side self-check (no phone needed):
```bash
go build -buildvcs=false -o smoke ./cmd/smoke
./smoke -url wss://127.0.0.1:8443/ws -token "$(cat token.txt)" -probe HELLO
# then: tmux capture-pane -p -t agentmon-spike | grep HELLO
```

## Known rough edge to judge on-device (not a bug)
Touch **text selection** is xterm's own (canvas/DOM), not iOS's native long-press
handles, so it's rougher than Termius (§6.3). Swipe-scroll and the `Copy` button
are wired; judge whether selection is good enough or whether Phase 1 needs a
dedicated select/copy affordance.

## Throwing it away
It's self-contained under `spike-0.5/`. Delete the directory. Nothing else
depends on it; the only side effects are the `agentmon-spike` tmux session
(`tmux kill-session -t agentmon-spike`) and `cert.pem`/`key.pem`/`token.txt`.

## Files
- `main.go` — HTTPS server, static embed, `/ws` relay, scrollback bootstrap, resize
- `control.go` — tmux control-mode client (output parse + `send-keys -H` input)
- `control_test.go` — unit tests for the byte-fidelity functions
- `static/index.html` — xterm.js page, key bar, gestures, viewport/keyboard, PWA
- `static/manifest.json` — Add-to-Home-Screen manifest
- `cmd/smoke/` — optional server-side WS smoke client
- `run.sh` — cert + session + build + serve
