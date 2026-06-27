#!/usr/bin/env bash
# Phase 0.5 spike launcher — THROWAWAY. Stands up everything needed to test
# mobile input fidelity against a real Claude Code pane, then serves it over
# HTTPS so a phone can drive it.
set -euo pipefail
cd "$(dirname "$0")"

HOST_IP="${HOST_IP:-192.168.1.51}"
PORT="${PORT:-8443}"
HTTP_PORT="${HTTP_PORT:-8080}"
SESSION="${SESSION:-agentmon-spike}"
SCRATCH="${SCRATCH:-$PWD/scratch}"

# 1) self-signed cert with the LAN IP in the SAN (so the phone can accept it
#    and get a *secure context* -> clipboard + add-to-home-screen work, §20).
if [[ ! -f cert.pem || ! -f key.pem ]]; then
  echo ">> generating self-signed cert for IP:$HOST_IP"
  openssl req -x509 -newkey rsa:2048 -nodes -days 365 \
    -keyout key.pem -out cert.pem \
    -subj "/CN=$HOST_IP" \
    -addext "subjectAltName=IP:$HOST_IP,DNS:$(hostname),IP:127.0.0.1" >/dev/null 2>&1
fi

# 2) stable shared token (persisted so the home-screen URL keeps working).
if [[ ! -f token.txt ]]; then openssl rand -hex 16 > token.txt; fi
TOKEN="$(cat token.txt)"

# 3) dedicated Claude Code session — NEVER the controlling pane. A real Claude
#    TUI is required (a shell hides the hard cases, §6.4).
if ! tmux has-session -t "$SESSION" 2>/dev/null; then
  mkdir -p "$SCRATCH"
  echo ">> creating tmux session '$SESSION' running claude in $SCRATCH"
  tmux new-session -d -s "$SESSION" -c "$SCRATCH" -x 100 -y 30 "claude"
  sleep 1
fi

# 4) build (no git repo here, so disable VCS stamping) and run.
echo ">> building"
go build -buildvcs=false -o spike .

echo
echo "================================================================"
echo " Phone — CORE input test (no cert needed, ws://):"
echo "   http://$HOST_IP:$HTTP_PORT/?token=$TOKEN"
echo " Phone — FULL test incl. clipboard/PWA (needs the cert TRUSTED,"
echo " not just click-through — see README 'Trusting the cert'):"
echo "   https://$HOST_IP:$PORT/?token=$TOKEN"
echo " Target Claude session: $SESSION   (scratch: $SCRATCH)"
echo " Go/no-go checklist: README.md  — only your on-device run decides."
echo "================================================================"
echo

exec ./spike -addr "$HOST_IP:$PORT" -http "$HOST_IP:$HTTP_PORT" \
  -session "$SESSION" -token "$TOKEN"
