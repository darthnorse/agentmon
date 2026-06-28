# ---- Stage 1: build the SPA ----
FROM node:22-alpine AS web
WORKDIR /web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# ---- Stage 1b: cross-compile both agent binaries (static, pure-Go) ----
FROM golang:1.26-alpine AS agentbin
WORKDIR /src
COPY . /src
WORKDIR /src/agent
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" \
    -o /agents/agent-linux-amd64 ./cmd/agentmon-agent
RUN CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" \
    -o /agents/agent-linux-arm64 ./cmd/agentmon-agent

# ---- Stage 2: build hubd with the SPA + agent binaries embedded ----
FROM golang:1.26-alpine AS hubd
WORKDIR /src
COPY . /src
COPY --from=web /web/dist /src/hubd/internal/webui/dist
COPY --from=agentbin /agents/agent-linux-amd64 /src/hubd/internal/agentbin/bin/agent-linux-amd64
COPY --from=agentbin /agents/agent-linux-arm64 /src/hubd/internal/agentbin/bin/agent-linux-arm64
WORKDIR /src/hubd
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/agentmon-hubd ./cmd/agentmon-hubd

# ---- Stage 3: minimal runtime ----
FROM gcr.io/distroless/static-debian12
COPY --from=hubd /out/agentmon-hubd /agentmon-hubd
VOLUME ["/data"]
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/agentmon-hubd"]
CMD ["--config", "/data/config.yaml"]
