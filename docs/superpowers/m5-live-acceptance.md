# M5 live acceptance runbook (safety-critical)

SAFETY (memory dev-host-runs-hub-and-claude): this host runs the hub AND Claude's
own tmux on the DEFAULT socket. The relay is LIVE. Test ONLY against the `aigallery`
agent's `agentmon`-socket demo panes (demo-web=%0, demo-db=%1). NEVER the default socket.

1. Build the SPA into the hub:
   - `make embed` (copies web/dist → hubd/internal/webui/dist), then build the hub
     binary (`make build-hub`) OR run the hub from source with the embedded dist.
2. Run the hub on a LOOPBACK TEST PORT against a COPY of the live DB:
   - copy `deploy/data` to a throwaway dir; point a test config at it.
   - set the test hub `external_origin` to the loopback origin you browse to
     (e.g. `http://127.0.0.1:8388`); `trust_forwarded_proto: false`.
3. Browse to the loopback origin; log in as `patrik`.
4. Verify: servers list shows `aigallery`.
5. Desktop grid: open BOTH demo panes (%0, %1) as two live tiles; confirm both stream.
6. Expand demo-web; run `echo AGENTMON_M5_OK`; confirm it runs; collapse → the other tile is still live.
7. Mobile path: narrow the viewport (or device-emulate < 1024px) → list → open demo-web →
   key bar drives it (Esc, Ctrl-C, arrows, ⏎ nl vs Enter); force a WS drop → it reconnects
   with a fresh snapshot; the tmux session survives.
8. Tear down: stop the test hub; delete the DB copy + the embedded dist
   (`make clean` restores the tracked placeholder). Confirm Claude's default-socket
   session and the demo panes are intact.

Full on-device iOS Safari / Android Chrome §6.4 checklist is run separately as Phase-1 acceptance.
