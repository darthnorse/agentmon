#!/bin/sh
# AgentMon hub entrypoint — linuxserver.io / Unraid PUID:PGID convention.
#
# The container starts as root, takes ownership of /data as PUID:PGID, then drops
# to that user to run the hub. This means bind-mounted data is owned by your host
# user (set PUID/PGID to match — Unraid: PUID=99 PGID=100), named volumes are
# writable, and the hub process itself never runs as root. No host-side chown.
set -eu

PUID="${PUID:-1000}"
PGID="${PGID:-1000}"

# Take ownership of the data dir so the (non-root) hub can create its SQLite DB.
# Ignore failures on read-only mounts (e.g. a :ro-bound config file).
chown -R "${PUID}:${PGID}" /data 2>/dev/null || true

exec su-exec "${PUID}:${PGID}" /agentmon-hubd "$@"
