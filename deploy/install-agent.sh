#!/usr/bin/env bash
set -euo pipefail
# Minimal M0 installer: drop the binary + unit, leave config to the operator.
BIN_SRC="${1:-bin/agentmon-agent}"
install -m 0755 "$BIN_SRC" /usr/local/bin/agentmon-agent
install -d /etc/agentmon
install -m 0644 deploy/agentmon-agent.service /etc/systemd/system/agentmon-agent.service
echo "Installed. Create /etc/agentmon/agent.toml (see deploy/agent.example.toml), then:"
echo "  systemctl daemon-reload && systemctl enable --now agentmon-agent"
