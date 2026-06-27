# AgentMon

Multi-server, mobile-first terminal dashboard for supervising AI coding agents.
See `agentmon-design.md` for the full design and `docs/superpowers/specs/` for phase specs.

## Layout
- `shared/` — wire contracts shared by hub and agent (Go module `agentmon/shared`)
- `agent/`  — per-server `agentmon-agent` (Go module `agentmon/agent`)
- `hubd/`   — central `agentmon-hubd` control plane (Go module `agentmon/hubd`)
- `web/`    — Vite + React SPA, embedded into `hubd`
- `deploy/` — Dockerfile, compose, Caddy, systemd unit
- `spike-0.5/` — throwaway validated input-fidelity spike (reference only)

## Dev quickstart
    make test          # Go unit tests across all modules
    make build         # build SPA + both Go binaries (CGO_ENABLED=0)
    cd web && npm run dev   # SPA dev server, proxies /api + /ws to a local hubd
